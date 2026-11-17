package drain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The lane directory's layout, as the brief template's own comment fixes it and the adapters
// read it. Staging is the one writer of these names: an adapter resolves them relative to
// the brief, and `run exec` makes them reachable by running the adapter with its working
// directory at the worktree inside the lane directory.
const (
	// WorktreeDir is the named target: a git worktree of the drained repository, and the
	// adapter's working directory. A run's change lands here and nowhere else. The name is
	// run's, because a request's working_dir defaults to it and run derives the request.
	WorktreeDir = run.WorktreeDir
	// BriefFile is the rendered brief.
	BriefFile = "brief.md"
	// FenceFile is the run's fence, one repo-relative prefix per line.
	FenceFile = "fence.txt"
	// BaseSHAFile is the sha the run was staged from, pinned before launch.
	BaseSHAFile = "base_sha"
	// ItemBaseSHAFile and ItemFenceFile retain the whole-item boundary while
	// base_sha and fence.txt describe the currently staged node.
	ItemBaseSHAFile = "item_base_sha"
	ItemFenceFile   = "item_fence.txt"
	// TargetFile is the worktree the run's change lands in when it is not the lane
	// worktree: a fork-series item's checkout under `.forks/lanes/<lane>/<fork>`. The
	// lane worktree is still created and is still the boundary target, but the candidate
	// is the fork checkout's HEAD and the gate runs there.
	TargetFile = "target"
	// PatchRefFile is the branch a fork-series item's delivery moves, refs/heads/patch/<fork>.
	// It is absent on an item that names no fork checkout.
	PatchRefFile = "patch_ref"
)

// SessionIDFile (session.go) is the harness session this run is launched under. It is staged
// beside the brief because every adapter resolves its inputs relative to the brief, and
// `run-pi` reads exactly this name there.

// The repo-owned companions a change to a tillr module is obliged to touch. They are named
// once, here, because a fence that omits one refuses the work after it is done: each is a
// file the module's own contract runs through, so a lane that could not write it would have
// to land a change the gate's documentation suite then fails.
const (
	// tillrSkillPath is the `tillr-operating` skill: the loop's home, and the document that
	// says what each module owns and when a face refuses.
	tillrSkillPath = "yolo-packs/tillr/skills/tillr-operating/SKILL.md"
	// tillrDocsTest is the suite that holds every module README to the Known Gaps callout
	// and runs every face a document names against the binary.
	tillrDocsTest = "tests/tillr-docs.test.py"
	// forkCheckTest is the suite for `tools/fork-check.py`, the gate's own dispatcher.
	forkCheckTest = "tests/fork-check.test.py"
	// museBackendTest is the suite for the adapters under `tools/tillr-run/backends/`.
	museBackendTest = "tests/tillr-muse-backend.test.py"
)

// fencePaths answers the fence a run is held to: the item's own allowed_paths, plus the
// repo-owned companions a change to that path is obliged to touch.
//
// # The pack copy of a tillr module
//
// `tools/tillr*` is delivered twice — as the source tree, and as the pack's copy under
// `yolo-packs/tillr/files/local/share/tillr/<module>/`, which `just pack-sync` rewrites from
// the source and the gate's `pack-sync` check compares. A fence that names the source and not
// the copy makes every compliant change a `boundary-scope-escape` before the gate ever runs.
//
// Measured 2026-11-14, and it cost an item: a brief that told the lane to run `just pack-sync`
// produced a candidate that `fenceViolation` refused on
// `yolo-packs/tillr/files/local/share/tillr/tillr-view/web/README.md`, so `item-obs-archive`
// was failed on a fence the brief itself had made mandatory.
//
// # The gate companions
//
// The mirrors are not the only derived entry, and deriving only them reproduces the same
// defect one layer out: a change to a tillr module is obliged to touch that module's README
// (the documentation suite checks it), the operating skill and the documentation suite
// itself, and nothing in an item's `allowed_paths` names them. So the fence derives them from
// the module the item did name:
//
//   - `tools/<module>/README.md` when the fence names a sub-path *inside* the module. A fence
//     that names the module's own directory already covers the README.
//   - `tillrSkillPath` and `tillrDocsTest` for every tillr module: both are the contract the
//     module's documentation is read under, and both are repo-owned.
//   - `tools/tillr-loop/` as a whole whenever the fence reaches `tillr-loop`. That module is a
//     program and its README and `test.py` sit beside it, so a fence naming
//     `tools/tillr-loop/tillr-loop` barred a lane from the two files the change is judged by.
//     The drain's embedded copy and its pack mirror also travel with an adapter change.
//   - `forkCheckTest` for `tools/fork-check.py`, and `museBackendTest` for
//     `tools/tillr-run/backends/`: each is the suite for exactly the path it is derived from.
//
// The fence is derived here rather than declared per item because forgetting it is silent,
// and this is the one place staging, the planner, the escape check and a resume all read.
// Every consumer goes through this function or `fenceText`; none of them repeats a rule.
func fencePaths(allowed []string) []string {
	seen := make(map[string]bool, len(allowed)*2)
	out := make([]string, 0, len(allowed)*2)
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range allowed {
		add(p)
	}
	for _, p := range allowed {
		// A fence spells a directory with or without its trailing slash and means the same
		// path either way, so the rules below match on the path and not on the spelling.
		named := strings.TrimSuffix(p, "/")
		if module, ok := tillrModule(named); ok {
			add("yolo-packs/tillr/files/local/share/tillr/" + module + "/")
			if named != "tools/"+module {
				add("tools/" + module + "/README.md")
			}
			add(tillrSkillPath)
			add(tillrDocsTest)
			if module == "tillr-loop" {
				add("tools/tillr-loop/")
				add("tools/tillr-drain/drain/tillr-loop.py")
				add("yolo-packs/tillr/files/local/share/tillr/tillr-drain/drain/tillr-loop.py")
			}
		}
		if named == "tools/fork-check.py" {
			add(forkCheckTest)
		}
		if named == "tools/tillr-run/backends" || strings.HasPrefix(named, "tools/tillr-run/backends/") {
			add(museBackendTest)
		}
	}
	return out
}

// fenceText is the fence as staging writes it to the lane's FenceFile: the derived fence
// (fencePaths), one repo-relative path per line, newline-terminated. Staging writes this
// text and the planner parses the same text with fenceEntries, so a plan and the escape
// check that convicts a run cannot disagree about where a lane may write.
func fenceText(allowed []string) string {
	return strings.Join(fencePaths(allowed), "\n") + "\n"
}

// tillrModule answers the module a fence path names when it names a tillr tool's source tree:
// `tools/tillr-view/` is `tillr-view`, and `tools/tillr-drain/drain/plan.go` is `tillr-drain`.
// A path outside `tools/` names no module, and neither does a tool whose name is not one.
func tillrModule(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, "tools/")
	if !ok {
		return "", false
	}
	module, _, _ := strings.Cut(rest, "/")
	if !strings.HasPrefix(module, "tillr") {
		return "", false
	}
	return module, true
}

// ForkTarget is the checkout a fork-series item's fence names and the ref its delivery
// moves. A fork item is one whose allowed_paths names `.forks/lanes/<lane>/<fork>`: the
// checkout is a fork's own git repository under the workspace's `.forks/`, so a run's
// commit lives there and is exported to the patch branch rather than landed on the trunk.
type ForkTarget struct {
	// Checkout is the absolute path of the fork's own checkout.
	Checkout string
	// Ref is the branch the delivery moves: refs/heads/patch/<fork>.
	Ref string
	// Path is the repo-relative path the fence names.
	Path string
	// Fork is the fork's name.
	Fork string
}

// forkTarget answers the fork checkout an item's fence names for this lane, or ok=false
// when it names none. It refuses more than one: an item is one fork, and two checkouts in
// one fence would make the candidate's base sha ambiguous.
func forkTarget(root, laneID string, allowed []string) (ForkTarget, bool, error) {
	prefix := ".forks/lanes/" + laneID + "/"
	found := make([]ForkTarget, 0, 1)
	seen := make(map[string]bool, 1)
	for _, raw := range allowed {
		_, fork, _, declared := item.ForkCheckoutPath(raw)
		if !declared {
			continue
		}
		// A fence may name a subdirectory of the checkout; the checkout is always the
		// fork root, because that is where the fork's HEAD and its patch branch live.
		checkout := prefix + fork
		if seen[checkout] {
			continue
		}
		seen[checkout] = true
		found = append(found, ForkTarget{
			Checkout: filepath.Join(root, filepath.FromSlash(checkout)),
			Ref:      "refs/heads/patch/" + fork,
			Path:     checkout,
			Fork:     fork,
		})
	}
	if len(found) > 1 {
		names := make([]string, len(found))
		for i, target := range found {
			names[i] = target.Path
		}
		return ForkTarget{}, false, Usagef("the item's fence names %d fork checkouts (%s): one item is one fork, so name one", len(found), strings.Join(names, ", "))
	}
	if len(found) == 0 {
		return ForkTarget{}, false, nil
	}
	return found[0], true, nil
}

// OutOfFence is the fixed half of the brief's fence section: the state outside the fence
// that is never a run's to touch, whatever command it chose. It is stated in paths because a
// fence is paths, and the same list holds for every item, so it is not a per-item value.
//
// The one path outside the fence that is a run's own is its scratch directory, and it is a
// bullet here because this is the only place a worker is told where scratch belongs: a
// scratch directory a worker cannot find is a scratch directory it puts in the repository,
// which is the fence violation the per-run durable directory exists to prevent. The path is
// the one `run exec` exports as YOLO_DURABLE_DIR, so it is a value and not a constant.
func OutOfFence(durableDir string) string {
	return "Nothing outside those paths. That includes:\n\n" +
		"- the store, under the repository's .tillr directory;\n" +
		"- any other lane directory under the lane root;\n" +
		"- the main checkout, unless this brief names it as the target;\n" +
		"- git refs, which no run moves: a commit belongs to the lane's own branch;\n" +
		"- your scratch directory, " + durableDir + ": it is outside the fence because it is not part of the change, and it is the one path outside the fence that is yours to write."
}

// StageSpec is one dispatch as the stager reads it: the request's identity, the values the
// brief's header carries, and the commit the worktree is pinned to.
type StageSpec struct {
	Continue       bool
	Node           *WorkflowStage
	InputPaths     map[string]string
	PreviousReport string
	BlockConflict  *item.Conflict
	LaneID         string
	ItemID         string
	RunID          string
	Backend        string
	BaseSHA        string
	WorkingDir     string
	TimeoutMS      int
	// SessionID pins the harness session this dispatch runs under. Empty mints a UUID: a
	// lane is reused by unrelated runs, so the id beside the brief describes the last run
	// there and reading it back would hand one item's transcript to the next (sessionIDFor).
	// Only a continuation of a run names an id, and a resume is the caller that does.
	SessionID string
	// Conflict names the squash conflict a conflict-fix run answers. It is nil for an
	// ordinary dispatch; a non-nil value is what makes the brief state the conflicting
	// paths and the three-way markers rather than the item's ordinary task alone.
	Conflict *ConflictBrief
}

// ConflictBrief is the conflict a conflict-fix run is asked to resolve: the paths git
// reported, the CONFLICT lines that named them, the three-way markers the merge produced,
// and the two commits the merge was between. The brief carries all four because a model
// that has only the paths has to repeat the merge to see what conflicts, and a model that
// has only the markers cannot tell which side of the merge is the candidate.
type ConflictBrief struct {
	Paths         []string
	Reports       []string
	Markers       string
	HeadSHA       string
	TrunkSHA      string
	OriginalRunID string
	// PreparedHead is the pinned head when the loop retired the candidate's item block on
	// top of HeadSHA. The brief tells the worker not to re-apply that loop-owned commit.
	PreparedHead string
}

// Stager produces the lane directory one run needs: its worktree, its brief, its fence and
// the sha it was staged from. It is an interface because the loop's tests drive a run
// without a template, and because a second lane layout would be a second implementation of
// this contract rather than a flag on one.
type Stager interface {
	// Stage prepares one lane directory, and answers an error when it cannot. Every error
	// it answers is infrastructure-class: nothing it refuses has learned anything about the
	// item's work.
	Stage(ctx context.Context, spec StageSpec) error
}

// LaneStager is the real stager: a lane directory under the lane root, holding a git
// worktree of the drained repository, a brief rendered from the tracked template, the fence
// and the base sha.
//
// It stages from a base sha the caller pinned — the trunk's tip, read before the tick — and
// never from a ref it resolves itself, so a trunk that moves mid-tick cannot become a run's
// base (design §1: what a step reads is resolved once and handed to it explicitly).
type LaneStager struct {
	// Root is the repository the lane worktree is a worktree of.
	Root string
	// LaneRoot is where the lane directories live.
	LaneRoot string
	// Store is the store the item is read from.
	Store string
	// Template is the tracked brief template, tools/tillr-run/backends/brief-template.md.
	Template string
	// Now is the injected clock the brief's date is stamped from.
	Now func() time.Time
}

// Stage writes one lane directory. The order is deliberate: the item is read and the brief
// rendered before anything is created or moved, so a brief that cannot be filled refuses
// without leaving a worktree behind, and a lane whose tree is somebody's unlanded work is
// refused before the brief is written over the evidence.
func (s *LaneStager) Stage(ctx context.Context, spec StageSpec) error {
	if spec.BaseSHA == "" {
		return Usagef("staging %s needs a base sha: a run is staged from a sha pinned before launch, never from whatever the trunk says at launch time", spec.RunID)
	}
	if err := checkRunID(spec.RunID); err != nil {
		return err
	}
	if spec.WorkingDir != WorktreeDir {
		return Usagef("the request's working_dir is %q, want %q: the lane layout the brief template fixes puts the run in <lane-dir>/%s, and a request naming another directory would run the adapter outside the worktree staging prepared",
			spec.WorkingDir, WorktreeDir, WorktreeDir)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	laneDir := filepath.Join(s.LaneRoot, spec.LaneID)
	worktree := filepath.Join(laneDir, WorktreeDir)

	recorded, err := s.readItem(ctx, spec.ItemID)
	if err != nil {
		return err
	}
	if spec.BlockConflict != nil {
		file, _, _ := strings.Cut(spec.BlockConflict.BodyRef, "#")
		recorded.AllowedPaths = []string{file}
	}
	fork, isFork, err := forkTarget(s.Root, spec.LaneID, recorded.AllowedPaths)
	if err != nil {
		return err
	}
	target, targetKind := worktree, "lane worktree"
	baseSHA := spec.BaseSHA
	if isFork {
		for i, raw := range recorded.AllowedPaths {
			if _, name, suffix, ok := item.ForkCheckoutPath(raw); ok {
				recorded.AllowedPaths[i] = ".forks/lanes/" + spec.LaneID + "/" + name + suffix
			}
		}
		if err := s.ensureForkCheckout(ctx, spec, fork); err != nil {
			return err
		}
		if !spec.Continue {
			if err := s.refreshForkCheckout(ctx, fork); err != nil {
				return err
			}
		}
		// The candidate's base is the fork's own HEAD, not the trunk: the patch branch is
		// the delivery the item moves, and the lane worktree the boundary watches is still
		// cut from the trunk below.
		head, err := s.git(ctx, fork.Checkout, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		baseSHA = strings.TrimSpace(head)
		if spec.Continue {
			baseSHA = spec.BaseSHA
		}
		target, targetKind = fork.Checkout, "fork checkout"
	}
	// The brief names the same scratch directory `run exec` exports, so the two have to
	// agree on the spelling: exec resolves the lane root to an absolute path, and a brief
	// naming a relative one would point the worker at a directory that does not exist.
	itemBaseSHA := baseSHA
	itemAllowed := append([]string(nil), recorded.AllowedPaths...)
	if spec.Node != nil {
		if spec.Continue {
			raw, err := os.ReadFile(filepath.Join(laneDir, ItemBaseSHAFile))
			if os.IsNotExist(err) {
				// Before per-node scopes, base_sha retained the original item
				// base across nodes. Migrate that recorded legacy boundary;
				// never substitute the current trunk or target head.
				raw, err = os.ReadFile(filepath.Join(laneDir, BaseSHAFile))
			}
			if err != nil {
				return Externalf("reading the item's base: %v", err)
			}
			itemBaseSHA = strings.TrimSpace(string(raw))
			head, err := s.git(ctx, target, "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			baseSHA = strings.TrimSpace(head)
		}
		scope, err := spec.Node.scopePaths(itemAllowed)
		if err != nil {
			return err
		}
		for i, raw := range scope {
			if _, name, suffix, ok := item.ForkCheckoutPath(raw); ok {
				scope[i] = ".forks/lanes/" + spec.LaneID + "/" + name + suffix
			}
		}
		recorded.AllowedPaths = scope
	}
	durableDir, err := filepath.Abs(run.DurableDir(laneDir, spec.RunID))
	if err != nil {
		return Internalf("resolving the run's scratch directory under %s: %v", laneDir, err)
	}
	values, err := briefValuesFor(recorded, spec, target, targetKind, baseSHA, durableDir, now().UTC())
	if err != nil {
		return err
	}
	if item.IsCommentItem(spec.ItemID) {
		task, err := s.commentTask(ctx, spec, durableDir)
		if err != nil {
			return err
		}
		for i := range values {
			if values[i].placeholder == "TASK" {
				values[i].text = task
			}
		}
	}
	earlier, err := preservedFacts(ctx, s.Store, spec)
	if err != nil {
		return err
	}
	for i := range values {
		if values[i].placeholder == "FACTS" && earlier != "" {
			if strings.HasPrefix(values[i].text, "None carried:") {
				values[i].text = strings.TrimSpace(earlier)
			} else {
				values[i].text += earlier
			}
		}
	}
	brief, err := renderBrief(s.Template, recorded.ItemID, values)
	if err != nil {
		return err
	}
	if isFork {
		brief = strings.Replace(brief, "## Method\n", "## Method\n\nCommit the change in the named fork checkout, on its own branch. Do not run `bin/pi-forks export`: the lander alone moves the patch branch after the candidate build gate passes and refreshes the shared cache. Do not edit the shared cache or another lane's checkout.\n", 1)
	}
	if item.IsCommentItem(spec.ItemID) {
		brief = strings.Replace(brief, "When the work is done, **commit it in the target worktree**", "For a reply-only answer, the task's no-commit exception overrides the ordinary commit requirement below.\n\nWhen the work is done, **commit it in the target worktree**", 1)
	}
	if !spec.Continue {
		if err := s.ensureWorktree(ctx, spec, laneDir, worktree); err != nil {
			return err
		}
	}
	if spec.Node != nil {
		brief += "\n## Workflow node\n\nRun only node " + spec.Node.ID + " on the " + spec.Node.Seat + " seat; access: " + spec.Node.Access + ".\n"
		if spec.Node.Access == "read_only" {
			brief += "Read only: do not change files in the target or commit. Write your findings in the report and finish with RESULT: done. The item criteria describe the whole workflow, not this node.\n"
		}
		dir := nodeOutputsDir(s.Root, spec.RunID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		brief += "Write declared outputs only in your output directory: " + dir + ". These files are outside the target and are not part of the candidate.\n"
		for _, name := range sortedNames(spec.Node.Outputs) {
			brief += "Output " + name + " (" + spec.Node.Outputs[name] + "): " + filepath.Join(dir, name) + "\n"
		}
		for _, name := range sortedNames(spec.InputPaths) {
			brief += "Input " + name + ": " + spec.InputPaths[name] + "\n"
		}
		// Preserve report-only workflows; explicit inputs receive only their wired files.
		if len(spec.Node.Inputs) == 0 && spec.PreviousReport != "" {
			brief += "Read the previous node report: " + spec.PreviousReport + "\n"
		}
	}
	// The session id is resolved before the lane is written: it is the handle a killed run
	// is resumed from, and a lane with no session id is a run whose transcript nothing can
	// name. A dispatch that names none gets a session of its own (sessionIDFor), because the
	// id already in the lane belongs to whatever ran there last.
	sessionID, err := sessionIDFor(spec.SessionID)
	if err != nil {
		return err
	}
	type stagedFile struct {
		name string
		body string
	}
	files := []stagedFile{
		{BriefFile, brief},
		{FenceFile, fenceText(recorded.AllowedPaths)},
		{BaseSHAFile, baseSHA + "\n"},
		{ItemBaseSHAFile, itemBaseSHA + "\n"},
		{ItemFenceFile, fenceText(itemAllowed)},
		{SessionIDFile, sessionID + "\n"},
	}
	if spec.BlockConflict != nil {
		files = append(files, stagedFile{"conflict.json", mustJSON(spec.BlockConflict) + "\n"})
	}
	if isFork {
		files = append(files,
			stagedFile{TargetFile, fork.Checkout + "\n"},
			stagedFile{PatchRefFile, fork.Ref + "\n"},
		)
	}
	// The lane directory is one per lane and the lane is reused, so the three files there
	// describe the last run on it and nothing else. Each run gets its own copy under
	// <root>/.tillr/loop/<run-id>/ in the same breath, which is what makes the timeline's
	// "item 1 ran under item 2's brief" impossible: a reader asks the run directory, not the
	// lane's.
	if spec.Conflict != nil && strings.TrimSpace(spec.Conflict.Markers) != "" {
		// Keep source evidence out of the prompt: a whole conflicted blob may contain
		// placeholder-shaped code and can exceed both argv and model-context limits.
		if err := os.MkdirAll(durableDir, 0o755); err != nil {
			return Externalf("creating conflict evidence directory %s: %v", durableDir, err)
		}
		markerPath := filepath.Join(durableDir, "conflict-markers.txt")
		if err := os.WriteFile(markerPath, []byte(spec.Conflict.Markers), 0o644); err != nil {
			return Externalf("writing conflict evidence %s: %v", markerPath, err)
		}
	}
	runDir := RunDir(s.Root, spec.RunID)
	for _, dir := range []string{laneDir, runDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Externalf("creating the run's directory %s: %v", dir, err)
		}
		for _, file := range files {
			path := filepath.Join(dir, file.name)
			if err := os.WriteFile(path, []byte(file.body), 0o644); err != nil {
				return Externalf("writing %s: %v", path, err)
			}
		}
	}
	return nil
}

// checkRunID refuses a run id that cannot name a run's record directory, before anything is
// staged. The run id staging keys the per-run copy on is the id the store records for the run
// (`runs.run_id`, written by `run exec` from the same request), never a stamp taken here: a
// second clock in staging would file one run's brief under a name no run record carries. Since
// it is also a path component, an empty id or one that walks out of <root>/.tillr/loop is
// refused rather than written to.
func checkRunID(runID string) error {
	if runID == "" {
		return Usagef("staging needs a run id: it names the directory the run's brief, fence and base sha are kept in")
	}
	if runID == "." || runID == ".." || strings.ContainsAny(runID, `/\`) {
		return Usagef("the run id %q cannot name a directory: a run's records live at <root>/.tillr/loop/<run-id>", runID)
	}
	return nil
}

// stagedItem is what staging reads of an item: the fields the brief is rendered from. It is
// the item record's own JSON, read through the item package's show face so the columns and
// their meanings stay in one place.
type stagedItem struct {
	ItemID       string   `json:"item_id"`
	Title        string   `json:"title"`
	BodyRef      string   `json:"body_ref"`
	Seat         string   `json:"seat"`
	Workflow     string   `json:"workflow"`
	AllowedPaths []string `json:"allowed_paths"`
	Criteria     []struct {
		CriterionID string `json:"criterion_id"`
		Text        string `json:"text"`
	} `json:"criteria"`
}

// readItem answers one item's staged fields. The store is opened read-only: staging reads
// the board and writes only the lane directory, so it holds no write lock a run would wait
// on.
func (s *LaneStager) readItem(ctx context.Context, itemID string) (stagedItem, error) {
	return readStagedItem(ctx, s.Store, s.Root, itemID)
}

// readStagedItem answers one item's staged fields from one store. It is package-level because
// staging is not the only reader: a resume re-derives the item's fence before it continues a
// run, and the two readers must agree on what the item says.
func readStagedItem(ctx context.Context, storePath, root, itemID string) (stagedItem, error) {
	if itemID == "" {
		return stagedItem{}, Usagef("staging needs an item: a brief is about one item")
	}
	r, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return stagedItem{}, Incompatiblef("opening the store %s: %v", storePath, err)
	}
	defer r.Close()
	var env record.Envelope
	if err := r.ReadTx(ctx, func(reader *store.Reader) error {
		found, err := item.Show(reader, itemID, root)
		env = found
		return err
	}); err != nil {
		return stagedItem{}, Externalf("reading item %s: %v", itemID, err)
	}
	var recorded stagedItem
	if err := json.Unmarshal(env.Payload, &recorded); err != nil {
		return stagedItem{}, Internalf("reading item %s's payload: %v", itemID, err)
	}
	return recorded, nil
}

// ensureForkCheckout makes sure a fork-series item's checkout is there. A checkout the
// writer already materialized is left alone; one that is missing is materialized from the
// shared cache by the same driver a person uses, with PI_FORKS_ROOT naming this repository
// so a linked-worktree copy of the driver cannot put a second .forks inside the lane.
// Materializing is not the stage's design of the checkout: it is the driver's, and a
// refusal here names the command to run when the driver is not available.
func (s *LaneStager) ensureForkCheckout(ctx context.Context, spec StageSpec, fork ForkTarget) error {
	if info, err := os.Stat(fork.Checkout); err == nil && info.IsDir() {
		return nil
	}
	driver := filepath.Join(s.Root, "bin", "pi-forks")
	if info, err := os.Stat(driver); err != nil || info.IsDir() {
		return Externalf("the fork checkout %s is not there and %s is not a driver this repository has: run `bin/pi-forks setup --lane %s --fork %s` before staging this item",
			fork.Checkout, driver, spec.LaneID, fork.Fork)
	}
	materialize := func(args ...string) error {
		cmd := exec.CommandContext(ctx, driver, args...)
		cmd.Dir = s.Root
		cmd.Env = append(os.Environ(), "PI_FORKS_ROOT="+s.Root)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return Externalf("materializing the fork checkout %s: %v: %s %s",
				fork.Checkout, err, strings.TrimSpace(stderr.String()), strings.TrimSpace(string(out)))
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.Root, ".forks", fork.Fork)); os.IsNotExist(err) {
		if err := materialize("setup", "--fork", fork.Fork); err != nil {
			return err
		}
	}
	if err := materialize("setup", "--lane", spec.LaneID, "--fork", fork.Fork); err != nil {
		return err
	}
	if info, err := os.Stat(fork.Checkout); err != nil || !info.IsDir() {
		return Externalf("the fork driver answered success but %s is not a directory: a lane staged there cannot run", fork.Checkout)
	}
	return nil
}

// ensureWorktree makes the lane's worktree the run is confined to, from the base sha.
//
// A lane is reused across ticks, so this is a reset and not a create, and a lane can hold
// any of four things: a working tree at the base (a reset is a no-op), a commit nobody
// landed (refused by name, because a candidate is a commit and resetting a lane deletes the
// only copy of work the loop has not judged), files no commit carries (cleared, because a
// file is not a candidate), or a registration whose directory is gone (pruned, because a
// registration is not work at all).
//
// Clearing before adding is what makes the second and later ticks possible at all.
// `git worktree add` refuses a directory that already exists, so a leftover untracked file
// or a crashed run's registration would fail every tick after it, or leak its files into
// the next run's tree. It also refuses a branch a live worktree has checked out, which the
// branch scoping leaves as this lane's own directory moved aside; that one is refused by
// name rather than left to git's `fatal:` line (refuseBranchHeld).
func (s *LaneStager) ensureWorktree(ctx context.Context, spec StageSpec, laneDir, worktree string) error {
	if err := os.MkdirAll(laneDir, 0o755); err != nil {
		return Externalf("creating the lane directory %s: %v", laneDir, err)
	}
	if err := s.refuseUnlandedCommit(ctx, spec, worktree); err != nil {
		return err
	}
	// The branch is scoped by the lane root, so the holder of it is never a lane in another
	// root — but it can be this lane's own directory moved aside, and `worktree add -B`
	// answers that with a `fatal:` line that names no lane. The check runs before the
	// clearing so a refusal leaves the lane's previous worktree alone; one held at this
	// lane's own path is the registration `clearWorktree` is about to remove.
	branch := LaneBranch(s.LaneRoot, spec.LaneID)
	holder, err := branchHolder(ctx, s.Root, branch, worktree)
	if err != nil {
		return err
	}
	if holder != "" {
		return refuseBranchHeld(spec.LaneID, s.LaneRoot, branch, holder)
	}
	if err := s.clearWorktree(ctx, worktree); err != nil {
		return err
	}
	if _, err := s.git(ctx, s.Root, "worktree", "add", "--force", "-B", branch, worktree, spec.BaseSHA); err != nil {
		return err
	}
	return nil
}

// refuseUnlandedCommit refuses a lane that holds a commit neither the base nor trunk contains. It reads
// the worktree's HEAD where the worktree can be read and the lane's own branch always: a lane
// whose directory was deleted still holds its commit on that branch, and that commit is the
// only copy of work nobody has judged.
//
// The selected base may be older than the idle lane's HEAD when lane_base is green.
// A commit contained in trunk is safe to reset: its work is already retained there,
// even when the green pointer has not caught up. Unlanded commits remain refused.
func (s *LaneStager) refuseUnlandedCommit(ctx context.Context, spec StageSpec, worktree string) error {
	candidates := []string{}
	if head, err := s.git(ctx, worktree, "rev-parse", "HEAD"); err == nil {
		candidates = append(candidates, strings.TrimSpace(head))
	}
	if branch, err := s.git(ctx, s.Root, "rev-parse", "--verify", "--quiet", "refs/heads/"+LaneBranch(s.LaneRoot, spec.LaneID)); err == nil {
		candidates = append(candidates, strings.TrimSpace(branch))
	}
	for _, sha := range candidates {
		if sha == "" || sha == spec.BaseSHA {
			continue
		}
		contained, err := containsCommit(ctx, s.Root, sha, spec.BaseSHA)
		if err != nil {
			return err
		}
		if !contained {
			contained, err = containsCommit(ctx, s.Root, sha, TrunkRef)
			if err != nil {
				return err
			}
		}
		if contained {
			continue
		}
		return unlandedLaneRefusal(spec.LaneID, sha, spec.BaseSHA)
	}
	return nil
}

// clearWorktree removes a lane's previous worktree and its registration, so the `worktree
// add` that follows creates one instead of colliding with it.
//
// Nothing here is a failure to report: a directory that is not a registered worktree answers
// `worktree remove` with "not a working tree", and that is exactly the state this call is
// for. What the caller has already refused is the one thing this must not touch — a commit
// nobody landed — so what is left to delete is a registration, a clean tree, and files no
// commit carries.
func (s *LaneStager) clearWorktree(ctx context.Context, worktree string) error {
	// A worktree that is not registered answers "not a working tree" and is removed below; a
	// registered one that git refuses to remove has its directory removed here and its
	// registration pruned after.
	_, _ = s.git(ctx, s.Root, "worktree", "remove", "--force", worktree)
	if err := os.RemoveAll(worktree); err != nil {
		return Externalf("clearing the lane's worktree %s: %v", worktree, err)
	}
	if _, err := s.git(ctx, s.Root, "worktree", "prune"); err != nil {
		return err
	}
	return nil
}

// LaneBranch is the branch a lane's worktree commits onto, so a candidate is a named ref
// rather than an anonymous detached head. The loop's reset and the stager's worktree
// creation must name the same branch, so both use this.
//
// The branch is scoped by the lane root and not by the lane alone, because every lane root
// of a repository shares that repository's refs: `canary/lanes` and `.tillr/lanes` both
// staging `lane-1` are two lanes, and a branch named only for the lane makes the second one
// fail on `fatal: cannot force update the branch 'tillr/lane-1' used by worktree at ...`
// (docs/design/tillr-drain-acceptance-2026-11-13.md Section 4.6).
func LaneBranch(laneRoot, lane string) string {
	return "tillr/" + laneRootScope(laneRoot) + "/" + lane
}

// laneRootScope names one lane root in a ref: its last path component, made readable, and a
// short hash of the absolute path. The component is what an operator reads in
// `git log --all --graph`; the hash is what keeps two roots apart when their last components
// match, which is the normal case — a repository's own `.tillr/lanes` and the canary's
// `canary/lanes` are both `lanes`.
func laneRootScope(laneRoot string) string {
	abs, err := filepath.Abs(laneRoot)
	if err != nil {
		abs = filepath.Clean(laneRoot)
	}
	sum := sha256.Sum256([]byte(abs))
	return sanitizeRefComponent(filepath.Base(abs)) + "-" + hex.EncodeToString(sum[:4])
}

// sanitizeRefComponent keeps a path component typeable in a ref: git allows more than a
// shell prompt does, and a branch name an operator cannot paste back is a diagnostic of its
// own. Anything outside [a-z0-9_-] becomes a dash, and a component that sanitizes to nothing
// (the root directory) is `lane`.
func sanitizeRefComponent(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "lane"
	}
	return out
}

// branchHolder answers the worktree, other than target, that has branch checked out, or ""
// when no worktree claims it. `git worktree list` is the only source for that: a branch a
// live worktree has checked out cannot be updated with `-B`, whatever the caller believes
// about the registration.
func branchHolder(ctx context.Context, repo, branch, target string) (string, error) {
	worktrees, err := listWorktrees(ctx, repo)
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if wt.Branch != "refs/heads/"+branch {
			continue
		}
		if samePath(wt.Path, target) {
			continue
		}
		return wt.Path, nil
	}
	return "", nil
}

// refuseBranchHeld refuses a lane whose branch a worktree at another path has checked out,
// naming that worktree and — when its path is the lane layout's — the lane root it belongs
// to. The holder is a lane directory moved aside rather than deleted, or a registration an
// earlier layout left behind; staging does not remove either, because a worktree it did not
// create may hold a candidate nobody has judged.
func refuseBranchHeld(lane, laneRoot, branch, holder string) error {
	owner := ""
	if filepath.Base(holder) == WorktreeDir {
		owner = fmt.Sprintf(" in the lane root %s", filepath.Dir(filepath.Dir(holder)))
	}
	return laneRefusalf(lane, ReasonLaneBranchHeld, fmt.Sprintf("remove the holding worktree (git worktree remove %s), or stage a different lane id", holder),
		"lane %s in %s cannot be staged: its branch %s is checked out by the worktree at %s%s, and a branch is checked out by one worktree at a time. A lane root scopes the branch, so this holder is a lane directory that was moved rather than deleted, or a registration an earlier layout left; staging does not delete a worktree it did not create. Remove it (git worktree remove %s) or stage a different lane id",
		lane, laneRoot, branch, holder, owner, holder)
}

// samePath answers whether two paths name the same directory, comparing absolute spellings
// and resolving symbolic links where both exist: `git worktree list` reports the canonical
// path, and a lane root reached through a symlink would otherwise never match the worktree
// it names.
func samePath(a, b string) bool {
	abs := func(p string) string {
		if resolved, err := filepath.Abs(p); err == nil {
			return resolved
		}
		return filepath.Clean(p)
	}
	if abs(a) == abs(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(abs(a))
	rb, errB := filepath.EvalSymlinks(abs(b))
	return errA == nil && errB == nil && ra == rb
}

// git runs one git command in a directory and answers its stdout. A failure is reported with
// git's own stderr, because "a git command failed" is not a diagnostic an operator can act
// on at 3am.
func (s *LaneStager) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", Externalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// placeholder is one `{{NAME}}` the template carries.
var placeholder = regexp.MustCompile(`\{\{([A-Z0-9_]+)\}\}`)

// briefValue is one template value and the item field it was rendered from. The field is the
// name the store's own record uses, so a refusal names the value an operator edits rather
// than the renderer's variable. It is empty for a fact about this run (the sha, the date,
// the lane's own paths), which no item field can be at fault for.
type briefValue struct {
	placeholder string
	text        string
	field       string
}

// briefValues is the template's values in the order a brace pair is attributed: the item's
// own fields first, in the order an operator reads them, then the run's facts. It is a slice
// rather than a map so two values carrying the same field (the title reaches both the
// header and the Task section) are reported as the field an operator would name, in a
// stable order, rather than as whichever placeholder a map happened to visit first.
type briefValues []briefValue

// itemFieldOrder is the order the item's fields are attributed in. The title is first of
// the non-id fields because it is the value an operator reads first, and because it also
// appears inside the Task section: a title carrying a brace pair must be reported as the
// title, not as the body reference it is next to.
var itemFieldOrder = []string{"item_id", "title", "body_ref", "criteria", "allowed_paths"}

// byName answers the values keyed by the placeholder the template spells.
func (v briefValues) byName() map[string]string {
	values := make(map[string]string, len(v))
	for _, value := range v {
		values[value.placeholder] = value.text
	}
	return values
}

// carriesABracePair answers the first value that carries a literal two-character brace pair,
// as the placeholder that carries it, the item field it came from (empty for a run fact) and
// whether one was found.
func (v briefValues) carriesABracePair() (string, string, bool) {
	for _, field := range itemFieldOrder {
		for _, value := range v {
			if value.field == field && strings.Contains(value.text, "{{") {
				return value.placeholder, field, true
			}
		}
	}
	for _, value := range v {
		if strings.Contains(value.text, "{{") {
			return value.placeholder, "", true
		}
	}
	return "", "", false
}

// renderBrief fills the template's placeholders and answers the brief.
//
// Three refusals make the result safe to launch, and the first two are different faults
// that used to be one message. A placeholder the values do not carry is an unfilled
// template, refused by name. A brace pair the template itself carries outside its
// placeholders is also the template's fault, and is refused naming the template. A brace
// pair that arrived in an item's own field is the record's fault, and is refused as an
// ItemContentError naming the item and the field: the template is fine, and what an
// operator has to edit is the record. The third refusal is what the loop reads to hold one
// item instead of backing the whole queue off (loop.go).
//
// The template's specimens of a form this brief is not (its commented main-checkout header
// block, which carries placeholders of its own) are dropped before filling, because a
// commented placeholder would fail all of those checks.
func renderBrief(templatePath, itemID string, values briefValues) (string, error) {
	if templatePath == "" {
		return "", Usagef("staging needs a brief template: pass --brief-template <path> naming tools/tillr-run/backends/brief-template.md")
	}
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return "", Externalf("reading the brief template %s: %v", templatePath, err)
	}
	provided := values.byName()
	body := dropPlaceholderComments(string(raw))
	missing := []string{}
	for _, found := range placeholder.FindAllStringSubmatch(body, -1) {
		if _, ok := provided[found[1]]; !ok {
			missing = append(missing, found[1])
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", Externalf("the brief template %s carries %s, which staging has no value for: a brief is refused rather than launched with an unfilled placeholder (the adapters refuse one too)",
			templatePath, strings.Join(dedupe(missing), ", "))
	}
	// The template's own residue: a brace pair that is not one of its placeholders, and so
	// cannot be filled from any value. That is the template's fault, and it would refuse
	// every item staged from it, so it is refused before any item is blamed for it.
	if strings.Contains(placeholder.ReplaceAllString(body, ""), "{{") {
		return "", Externalf("the brief template %s carries a '{{' that is not one of its placeholders: the template is the thing to fix, not the item, and a brief is refused rather than launched with an unfilled placeholder", templatePath)
	}
	if name, field, found := values.carriesABracePair(); found {
		if field == "" {
			return "", Externalf("the brief rendered from %s carries a '{{' in the value for %s, which staging derived from this run rather than from item %s: the run's own facts are the thing to fix",
				templatePath, name, itemID)
		}
		return "", ItemContentf(itemID, field, "item %s: its %s field carries a literal '{{' (a two-character brace pair), which a brief cannot carry. The template is fine, so the record is the thing to fix: edit the item's %s and dispatch it again",
			itemID, field, field)
	}
	rendered := placeholder.ReplaceAllStringFunc(body, func(found string) string {
		return provided[placeholder.FindStringSubmatch(found)[1]]
	})
	if strings.Contains(rendered, "{{") {
		// Every value and the template's residue were checked above, so this is a bug in the
		// renderer rather than a fact about the item or the template.
		return "", Internalf("the brief rendered from %s still carries a '{{' that neither the template's residue nor any value accounted for", templatePath)
	}
	return rendered, nil
}

// dropPlaceholderComments removes every HTML comment that carries a placeholder. Those are
// the template's specimens of a form this brief is not; its explanatory comments stay.
func dropPlaceholderComments(body string) string {
	var out strings.Builder
	rest := body
	for {
		start := strings.Index(rest, "<!--")
		if start < 0 {
			out.WriteString(rest)
			return out.String()
		}
		end := strings.Index(rest[start:], "-->")
		if end < 0 {
			out.WriteString(rest)
			return out.String()
		}
		block := rest[start : start+end+len("-->")]
		out.WriteString(rest[:start])
		if !strings.Contains(block, "{{") {
			out.WriteString(block)
		}
		rest = rest[start+end+len("-->"):]
	}
}

// dedupe answers the names once each, in order, so a template that names a missing
// placeholder twice is reported once.
func dedupe(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// briefValuesFor answers the placeholder values for one item and one dispatch.
//
// Every value is either read from the item or is a fact about this run. Where the item
// carries nothing for a section — no settled decisions, no facts, no method, no verification
// command — the brief says exactly that instead of inventing content: a section that reads as
// the driver's knowledge when nobody read the tree is a lie the model would act on. Those
// four are stated as absences by design; the values a launch cannot honestly lack (the
// item's id and title, its criteria, its fence, the base sha) are refused.
//
// The four absent values are refused as ItemContentError and not as infrastructure: an item
// with no title will have no title on every retry, so staging it again learns nothing and
// backing the whole queue off for it stalls work that is fine. The loop holds that one
// item; see loop.go.
func briefValuesFor(recorded stagedItem, spec StageSpec, target, targetKind, baseSHA, durableDir string, at time.Time) (briefValues, error) {
	if recorded.ItemID == "" {
		return nil, Usagef("staging %s: the store holds no item %q, and a brief is about one item", spec.RunID, spec.ItemID)
	}
	triage := spec.BlockConflict != nil && spec.BlockConflict.TriageVersion != ""
	if recorded.Title == "" && !triage {
		return nil, ItemContentf(recorded.ItemID, "title", "item %s carries no title, so the brief has no statement of the outcome wanted: add a title to the item and dispatch it again", recorded.ItemID)
	}
	if len(recorded.Criteria) == 0 && !triage {
		return nil, ItemContentf(recorded.ItemID, "criteria", "item %s carries no criteria, and design §3 makes an item without one undispatachable: a brief's criteria are what a verdict judges. Add a criterion and dispatch it again", recorded.ItemID)
	}
	if len(recorded.AllowedPaths) == 0 {
		return nil, ItemContentf(recorded.ItemID, "allowed_paths", "item %s names no allowed_paths, so the brief has no fence. A run whose change nothing constrains is not dispatchable: add allowed_paths to the item (its repo-relative path prefixes) and dispatch it again", recorded.ItemID)
	}
	// The template numbers its first criterion ("1. {{CRITERION_1}}"), so the value is the
	// first criterion's text followed by the rest already numbered: an item with four
	// criteria renders four numbered lines and one with one renders one.
	criteria := "None stored: the request needs criteria before it can be built."
	if len(recorded.Criteria) > 0 {
		criteria = recorded.Criteria[0].Text
		for i, c := range recorded.Criteria[1:] {
			criteria += fmt.Sprintf("\n%d. %s", i+2, c.Text)
		}
	}
	requestCriteria := criteria
	task := taskSection(recorded)
	facts := "None carried: staging read no code facts for this item, so nothing here is evidence about the tree. Read the fence's paths yourself before editing."
	settled := "None recorded: this item names no settled decision, so this brief settles nothing. Where this brief is wrong about the tree, stop and report the conflict instead of inventing a mechanism."
	if spec.Conflict != nil {
		task = conflictTask(recorded, *spec.Conflict)
		facts = conflictFacts(*spec.Conflict, durableDir)
		settled = "This is the one conflict-fix run the item is allowed. The squash conflict is a fact about the moved trunk, not a verdict about the candidate, so do not redesign the item and do not treat the conflict as a failed attempt. Resolve the conflict and commit the resolution."
	}
	if spec.BlockConflict != nil {
		c := spec.BlockConflict
		task = fmt.Sprintf("Answer conflict %s raised by run %s against %s. Your brief is the complete conflict record at %s. Read it before editing. Amend only that item's block; do not implement the original work.", c.ConflictID, c.RunID, c.BodyRef, filepath.Join(filepath.Dir(target), "conflict.json"))
		criteria = "The document's item block is amended to answer the recorded conflict. Leave its directive and item identity intact."
		settled = "This run acts on the design seat. A report of blocked routes the conflict to the owner; landing an amendment returns the original work to its own seat."
		if c.TriageVersion != "" {
			task = triageTask(recorded)
			criteria = requestCriteria
			settled = "Document-fed workflow prerequisite: an item needs a document block before it is built. This is its one triage run for this request version."
		}
	}
	// The order matters: it is the order a brace pair is attributed in, and the item's own
	// fields come first so the record — not the run — is what a refusal names. TASK carries
	// the body reference, and the title it repeats is attributed through TITLE above it.
	return briefValues{
		{placeholder: "ITEM_ID", text: recorded.ItemID, field: "item_id"},
		{placeholder: "TITLE", text: recorded.Title, field: "title"},
		{placeholder: "TASK", text: task, field: "body_ref"},
		{placeholder: "CRITERION_1", text: criteria, field: "criteria"},
		{placeholder: "FENCE_PATHS", text: strings.Join(fencePaths(recorded.AllowedPaths), "\n"), field: "allowed_paths"},
		{placeholder: "RUN_ID", text: spec.RunID},
		{placeholder: "BACKEND", text: spec.Backend},
		{placeholder: "BASE_SHA", text: baseSHA},
		{placeholder: "TARGET", text: target},
		{placeholder: "TARGET_KIND", text: targetKind},
		{placeholder: "DATE", text: at.Format("2006-01-02")},
		// The unit travels with the number, because the brief says "none" for a run with no
		// wall: "Timeout: 0 seconds" would read as a wall that fires immediately, and
		// appending the unit in the template would print "none seconds" (design D-RW1).
		{placeholder: "TIMEOUT_SECONDS", text: wallWords(spec.TimeoutMS)},
		{placeholder: "OUT_OF_FENCE_PATHS", text: OutOfFence(durableDir)},
		{placeholder: "SETTLED", text: settled},
		{placeholder: "FACTS", text: facts},
		{placeholder: "METHOD", text: "The item states no method. Within the fence the order of work is yours; write the failing reproduction first where this repository's rules require one."},
		{placeholder: "VERIFICATION", text: "None recorded: this item carries no verification command of its own. Report every command you ran and its output, and never run the full gate (`just done`)."},
	}, nil
}

// wallWords is the brief's Timeout line: the wall in seconds with its unit, or "none" for
// a run that carries no wall at all.
func wallWords(timeoutMS int) string {
	if timeoutMS <= 0 {
		return "none"
	}
	return fmt.Sprintf("%d seconds", timeoutMS/1000)
}

// taskSection is the brief's statement of the work: the item's title as the outcome, and
// where the item's own words are. Staging carries the body reference rather than a copy of
// the document, because the document is the only copy and a copy would be a second place for
// it to rot.
func taskSection(recorded stagedItem) string {
	if recorded.BodyRef == "" {
		return recorded.Title + "\n\nThe item carries no body reference: its criteria below are the whole of what it states."
	}
	return fmt.Sprintf("%s\n\nThe item's body is %s. Staging carries that reference, not a copy: read it there.", recorded.Title, recorded.BodyRef)
}

// conflictTask is the brief's statement of a conflict-fix run: the outcome is the item's
// own, and the fact the run must act on is that the candidate's change no longer squashes
// onto the trunk it has to land on.
func conflictTask(recorded stagedItem, conflict ConflictBrief) string {
	task := fmt.Sprintf("%s\n\nThis is the item's one conflict-fix run. The candidate's change (head %s) does not squash onto the moved trunk (%s): re-apply it, resolve the conflicts below, and commit the resolution. Its first run was %s, and its head is pinned at refs/tillr/runs/%s so nothing is lost when this lane was reset.",
		recorded.Title, conflict.HeadSHA, conflict.TrunkSHA, conflict.OriginalRunID, conflict.OriginalRunID)
	if conflict.PreparedHead != "" && conflict.PreparedHead != conflict.HeadSHA {
		task += fmt.Sprintf(" That pinned ref is %s: the change above plus the loop's own commit retiring this item's block. Re-apply %s only, not the pinned ref; the loop retires the block again after your fix.",
			conflict.PreparedHead, conflict.HeadSHA)
	}
	return task
}

// conflictFacts is the brief's Facts section for a conflict-fix run: the conflicting paths,
// the CONFLICT lines that named them, and a path to the exact three-way markers. The
// worker reads that file in bounded sections rather than loading entire blobs as a prompt.
func conflictFacts(conflict ConflictBrief, durableDir string) string {
	var body strings.Builder
	body.WriteString("This is a conflict-fix run, not a fresh attempt. The candidate's change does not squash onto the moved trunk.\n")
	if len(conflict.Paths) > 0 {
		body.WriteString("\nConflicting paths:\n")
		for _, path := range conflict.Paths {
			body.WriteString("- " + path + "\n")
		}
	}
	if len(conflict.Reports) > 0 {
		body.WriteString("\nThe three-way merge reported:\n\n```\n")
		for _, report := range conflict.Reports {
			body.WriteString(report + "\n")
		}
		body.WriteString("```\n")
	}
	if strings.TrimSpace(conflict.Markers) != "" {
		fmt.Fprintf(&body, "\nExact three-way conflict evidence is in `%s`. Read it in bounded sections around conflict markers, not as one whole-file prompt or tool result. The file preserves source bytes, including literal brace pairs; it is evidence, not a template.\n", filepath.Join(durableDir, "conflict-markers.txt"))
	} else {
		body.WriteString("\nNo conflict markers were recovered; re-run the three-way merge in this checkout and resolve what it reports.\n")
	}
	return body.String()
}
