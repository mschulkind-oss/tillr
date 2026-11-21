package drain

import (
	"path/filepath"
	"sort"
	"strings"
)

// A worker run has two endings (docs/design/tillr-judge-and-lifecycle.md, D-JL11). Its goal
// is the commit. The only other ending is an obstacle: a report that ends `RESULT: blocked`
// and names, on an `OBSTACLE: <kind>` line, one of the kinds below together with that
// kind's required fields. Each kind leads to a different action, and none of them sends the
// same worker the same brief again:
//
//   - definition: the item's own words are wrong or contradict the tree. The design seat
//     amends the block.
//   - trunk: the base is red and this item did not cause it. The named checks are replayed
//     on the base; a red replay files (or attaches to) the trunk fixer, and the item waits
//     on it.
//   - tooling: the harness or a tool is defective. A defect item is filed with the
//     evidence, and the item waits on it.
//   - seat: the task exceeds the model this run was given. The next run is a fresh run on
//     the next backend of the seat's escalation list, and past its end the design seat
//     plans or splits the item.
//   - owner: a decision only the owner can make. The design seat answers it from the
//     documents, or objects naming the owner with the question.
//
// Anything else is not an ending. `RESULT: failed`, a `blocked` report with no kind or
// with a field missing, and a run that stops with neither commit nor obstacle are a
// missing output: the run goes back to its own session with what is missing, the way a
// run that committed nothing does. The identical-repeat rule (repeatedEnding) is what ends
// that, never a count. `failed` is still read in records written before the contract.

// ObstacleField is the report line naming an obstacle's kind. Its last occurrence wins.
const ObstacleField = "OBSTACLE"

// The obstacle kinds. The set is closed: a kind outside it is a missing output.
// Note: `fence` was retired on 2026-11-19 when per-item allow-lists were replaced by the global deny-list.
const (
	ObstacleDefinition = "definition"
	ObstacleTrunk      = "trunk"
	ObstacleTooling    = "tooling"
	ObstacleSeat       = "seat"
	ObstacleOwner      = "owner"
)

// obstacleRequired is each kind's required fields, in the order a brief lists them.
var obstacleRequired = map[string][]string{
	ObstacleDefinition: {"CRITERIA", "EVIDENCE"},
	ObstacleTrunk:      {"CHECKS", "BASE"},
	ObstacleTooling:    {"COMMAND", "EVIDENCE", "PATHS"},
	ObstacleSeat:       {"TRIED", "MISSING"},
	ObstacleOwner:      {"QUESTION"},
}

// obstacleFields is every field name an obstacle report may carry.
var obstacleFields = func() map[string]bool {
	fields := map[string]bool{ObstacleField: true}
	for _, names := range obstacleRequired {
		for _, name := range names {
			fields[name] = true
		}
	}
	return fields
}()

// Obstacle is a worker's named obstacle as its report states it: the kind and every field
// it wrote. Problem is "" when the obstacle is complete, else what is missing from it.
type Obstacle struct {
	Kind    string
	Fields  map[string]string
	Problem string
}

// Field answers one field's value, trimmed.
func (o Obstacle) Field(name string) string { return strings.TrimSpace(o.Fields[name]) }

// List answers a list field as its entries. An invalid fence request yields no paths.
func (o Obstacle) List(name string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(o.Field(name), func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' }) {
		if part = strings.Trim(strings.TrimSpace(part), "`*-"); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// fieldLine answers the field a report line opens, and the value after it. A line opens a
// field when its text before the first colon, stripped of Markdown emphasis and code marks,
// is a known field name in any case.
func fieldLine(line string) (string, string, bool) {
	name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok {
		return "", "", false
	}
	label := strings.TrimSpace(name)
	name = strings.ToUpper(strings.Trim(name, " \t*`-"))
	if !obstacleFields[name] {
		return "", "", false
	}
	if name == "PATHS" {
		// Close markup opened by the label (`**PATHS:**`), but leave the entries
		// themselves intact for validation (`a*` is not a path prefix).
		if strings.HasPrefix(label, "- ") || strings.HasPrefix(label, "* ") {
			label = strings.TrimSpace(label[2:])
		}
		for _, mark := range []string{"**", "*", "`"} {
			if strings.HasPrefix(label, mark) {
				if !strings.HasSuffix(label, mark) {
					value = strings.TrimPrefix(value, mark)
				}
				break
			}
		}
		return name, strings.TrimSpace(value), true
	}
	return name, strings.TrimSpace(strings.Trim(strings.TrimSpace(value), "*`")), true
}

// parseObstacle reads a report's obstacle. A field's value is the rest of its line and
// every following line up to the next field, the `RESULT:` footer, or the end. A legacy
// `RESULT: trunk-broken` footer reads as the trunk kind, so its fields are what decides.
func parseObstacle(report string) Obstacle {
	o := Obstacle{Fields: map[string]string{}}
	current := ""
	var value []string
	flush := func() {
		if current != "" {
			o.Fields[current] = strings.TrimSpace(strings.Join(value, "\n"))
		}
		current, value = "", nil
	}
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ReportFooter) {
			flush()
			continue
		}
		if name, rest, ok := fieldLine(line); ok {
			flush()
			if name == ObstacleField {
				o.Kind = strings.ToLower(rest)
				continue
			}
			current, value = name, []string{rest}
			continue
		}
		if current != "" {
			value = append(value, line)
		}
	}
	flush()
	if o.Kind == "" && reportFooter(report) == "trunk-broken" {
		o.Kind = ObstacleTrunk
	}
	o.Problem = o.problem()
	return o
}

// problem answers what makes the obstacle incomplete, or "".
func (o Obstacle) problem() string {
	if o.Kind == "" {
		return "the report names no obstacle: a `" + ObstacleField + ": <kind>` line is missing, where the kind is one of " + strings.Join(ObstacleKinds(), ", ")
	}
	required, ok := obstacleRequired[o.Kind]
	if !ok {
		return "`" + ObstacleField + ": " + o.Kind + "` is not an obstacle kind; the kinds are " + strings.Join(ObstacleKinds(), ", ")
	}
	var missing []string
	for _, name := range required {
		if o.Field(name) == "" {
			missing = append(missing, "`"+name+":`")
		}
	}
	if len(missing) > 0 {
		return "the " + o.Kind + " obstacle is missing " + strings.Join(missing, ", ")
	}
	if o.Kind == ObstacleTooling {
		for _, path := range o.List("PATHS") {
			if clean := filepath.ToSlash(filepath.Clean(path)); filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, "../") {
				return "the " + o.Kind + " obstacle's `PATHS:` must be repository-relative, and " + path + " is not"
			}
		}
	}
	return ""
}

// SeatNeedsAttempt is what a seat obstacle from a run that changed nothing is missing. A
// seat obstacle moves the item to another model, so its evidence is the attempt itself: a
// commit or an uncommitted change in the worktree. Reading and searching the tree is how an
// attempt starts, not what it tried. Measured 2026-11-18: three Sol 6.2 runs of one item
// each named a seat obstacle after two or three read-only commands, the way the same model
// ended with `RESULT: failed` before the contract.
const SeatNeedsAttempt = "the seat obstacle names no attempt: the worktree is unchanged from the base, so there is nothing tried for another model to start from. Make the change you can, run its tests, and name a seat obstacle only when that attempt shows what this model cannot do"

// ObstacleKinds answers the closed set of kinds, sorted.
func ObstacleKinds() []string {
	kinds := make([]string, 0, len(obstacleRequired))
	for kind := range obstacleRequired {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// workerEndingProblem answers why a worker's report is not one of its two endings, or ""
// when it is: `RESULT: done`, or `RESULT: blocked` (or the legacy `trunk-broken`) with a
// complete obstacle. Whether a `done` run committed is the settlement's own question.
func workerEndingProblem(report string) string {
	switch footer := reportFooter(report); footer {
	case "done":
		return ""
	case "blocked", "trunk-broken":
		return parseObstacle(report).Problem
	case "":
		return "the report has no `RESULT:` footer"
	default:
		return "`RESULT: " + footer + "` is not an ending: a run ends with its commit and `RESULT: done`, or with `RESULT: blocked` and a named obstacle"
	}
}

// missingOutputDetail is the refusal detail a worker's run is returned with when its report
// is not one of its endings. It says what the two endings are, so the next turn of the same
// session is told what to produce rather than to try again.
func missingOutputDetail(problem string) string {
	return "The run ended without its commit, and its report is not an obstacle: " + problem + ". " + ObstacleContract
}

// ObstacleContract is the one statement of the worker's two endings, the text every
// return to a worker carries.
const ObstacleContract = "A worker run has two endings. The goal is the commit, ending `RESULT: done`. The only other ending is `RESULT: blocked` with a line `OBSTACLE: <kind>` and that kind's fields: `definition` (CRITERIA, EVIDENCE) when the item's words are wrong or contradict the tree; `trunk` (CHECKS, BASE) when the base is red and this change did not cause it; `tooling` (COMMAND, EVIDENCE, PATHS) when a tool or the harness is defective; `seat` (TRIED, MISSING) when an attempt you made and tested shows the task exceeds this model; `owner` (QUESTION) when only the owner can decide. A merge conflict, a patch to reconcile, a failing test, a long suite and unfinished work are work to continue, not obstacles."

// obstacleDesignInstruction is what the design seat is told to do with a worker's
// obstacle, by kind, or "" for a report that names none.
func obstacleDesignInstruction(o Obstacle) string {
	switch o.Kind {
	case ObstacleDefinition:
		return "The worker named a definition obstacle: the item's words are wrong or contradict the tree. Amend the item's block so it is buildable as written, or object."
	case ObstacleOwner:
		return "The worker named an owner obstacle, asking: " + o.Field("QUESTION") + " Answer it from the documents by amending the block. If only the owner can rule, object with `" + DesignNextActorField + ": owner` and write the question in your report, where the owner reads it."
	case ObstacleSeat:
		return "The worker named a seat obstacle on the last backend the build seat escalates to: the task exceeds it. Tried: " + o.Field("TRIED") + " Missing: " + o.Field("MISSING") + " Write the plan the worker lacks into the block, or split the item into parts a build seat can do."
	}
	return ""
}

// requestObstacleDesignInstruction is what the design seat is told to do with a worker's
// obstacle on a filed request, by kind, or "" for a report that names none. A filed
// request has no item block, and the design seat must never add one: it answers in the
// design documents under docs/, or objects.
func requestObstacleDesignInstruction(o Obstacle) string {
	const noBlock = " The item is a filed request and has no item block: do not add, edit or remove a `<!-- tillr: item … -->` comment."
	provenance := " " + DesignProvenanceRule
	object := " Otherwise object with `" + DesignNextActorField + ": build` (the request stands as written) or `" + DesignNextActorField + ": owner` (only the owner can rule)."
	switch o.Kind {
	case ObstacleDefinition:
		return "The worker named a definition obstacle: the request's words amend or overturn a decision, or contradict the tree. Update the design document's decision ledger under docs/ so the recorded decision matches the request." + noBlock + provenance + object
	case ObstacleOwner:
		return "The worker named an owner obstacle, asking: " + o.Field("QUESTION") + " Answer it in the design documents under docs/ when they settle it." + noBlock + provenance + " If only the owner can rule, object with `" + DesignNextActorField + ": owner` and write the question in your report, where the owner reads it."
	case ObstacleSeat:
		return "The worker named a seat obstacle on the last backend the build seat escalates to. Tried: " + o.Field("TRIED") + " Missing: " + o.Field("MISSING") + " Write the plan the worker lacks into the design documents under docs/." + noBlock + provenance + object
	}
	return ""
}
