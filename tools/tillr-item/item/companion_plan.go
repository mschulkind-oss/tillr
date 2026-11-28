// Architecture and invariants: docs/design/tillr-per-step-question-holds.md Section 7.2 (Slice 2),
// docs/design/tillr-queue-mirrors-designs.md Section 7
package item

import (
	"crypto/sha1" //nolint:gosec // git's blob identity is SHA-1; this is not a security use
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// A companion plan `<dir>/<stem>-plan.md` carries the buildable tasks of the design
// `<dir>/<stem>.md` under `## Tasks`. This file reads and validates that section; sync_design.go
// decides which plans are read and writes what they declare.

var (
	taskHeadingStart = regexp.MustCompile(`^###\s+Task\b`)
	taskHeading      = regexp.MustCompile(`^###\s+Task\s+([^:\s]*)\s*:\s*(.*?)\s*$`)
	localTaskID      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	metaBullet       = regexp.MustCompile(`^[-*+]\s+\*\*(.+?):\*\*[ \t]*(.*)$`)
	questionDirOpen  = regexp.MustCompile(`^\s*<!--\s*vantage:\s*(question|oq)\b`)
	stampedAt        = regexp.MustCompile("\\*\\*Design stamped at:\\*\\*\\s*`?([0-9a-f]{40})`?")
	waitRefToken     = regexp.MustCompile("`([^`\\s#]+#[^`\\s]+)`")
	waitQuoted       = regexp.MustCompile(`\(\s*"([^"]*)"\s*\)`)
	waitSectionHash  = regexp.MustCompile(`\[section-sha256:\s*([0-9a-fA-F]*)\s*\]`)
)

const (
	ruleHasQuestion = "plan-has-question"
	ruleTaskInvalid = "plan-task-invalid"
)

// companionTask is one valid task of a companion plan: the item it declares and the
// question waits it records.
type companionTask struct {
	payload itemPayload
	heading string
	line    int
	local   string
	waits   []QuestionRef
}

// companionPlan is one companion plan read once: the tasks it declares and what is wrong
// with it. A plan with a finding declares no tasks.
type companionPlan struct {
	path     string
	parent   string
	tasks    []companionTask
	findings []SyncNote
}

// companionPlanPath is the plan a design pairs with by filename.
func companionPlanPath(parent string) string {
	return strings.TrimSuffix(parent, ".md") + "-plan.md"
}

// companionParentPath is the design a plan pairs with by filename, or "".
func companionParentPath(plan string) string {
	if !strings.HasSuffix(plan, "-plan.md") {
		return ""
	}
	return strings.TrimSuffix(plan, "-plan.md") + ".md"
}

// companionSlug is the plan's id stem: its design's path without docs/, design/ or .md.
func companionSlug(parent string) string {
	slug := strings.TrimPrefix(path.Clean(parent), "docs/")
	slug = strings.TrimPrefix(slug, "design/")
	slug = strings.TrimSuffix(slug, ".md")
	return strings.Trim(reconcileSlugRun.ReplaceAllString(strings.ToLower(slug), "-"), "-")
}

// gitBlobSHA is the identity git gives content: the SHA-1 of `blob <size>\0` and the bytes.
func gitBlobSHA(content []byte) string {
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// planStampedAt is the blob sha a plan says it was cut for, or "".
func planStampedAt(content []byte) string {
	lines := splitLines(content)
	fence := fenceState{}
	for _, line := range lines {
		if fence.step(line) {
			continue
		}
		if m := stampedAt.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// frontmatterValues answers a top-level frontmatter key's scalar or list values.
func frontmatterValues(content []byte, key string) (values []string, present bool) {
	lines := splitLines(content)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, false
	}
	unquote := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
			s = s[1 : len(s)-1]
		}
		return s
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return nil, false
		}
		if !strings.HasPrefix(lines[i], key+":") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(lines[i], key+":"))
		if rest != "" {
			rest = strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]")
			for _, v := range strings.Split(rest, ",") {
				if v = unquote(v); v != "" {
					values = append(values, v)
				}
			}
			return values, true
		}
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "- ") {
				values = append(values, unquote(strings.TrimPrefix(t, "- ")))
				continue
			}
			if t == "" || t == "---" || !strings.HasPrefix(lines[j], " ") {
				break
			}
		}
		return values, true
	}
	return nil, false
}

// parseCompanionPlan reads one plan's tasks. byPath resolves a prose wait's document.
func parseCompanionPlan(plan SyncDocument, parent string, byPath map[string]SyncDocument) companionPlan {
	out := companionPlan{path: plan.Path, parent: parent}
	lines := splitLines(plan.Content)
	fenced := make([]bool, len(lines))
	fence := fenceState{}
	for i, line := range lines {
		fenced[i] = fence.step(line)
	}
	bad := func(line int, heading, id, format string, args ...any) {
		out.findings = append(out.findings, SyncNote{File: plan.Path, Heading: heading, Line: line, ItemID: id, Rule: ruleTaskInvalid, Message: fmt.Sprintf(format, args...)})
	}
	if actions, ok := frontmatterValues(plan.Content, "planning-actions"); !ok || len(actions) != 1 || actions[0] != "none" {
		bad(1, "", "", "a companion plan carries `planning-actions: none` in its frontmatter")
	}
	named := false
	deps, _ := frontmatterValues(plan.Content, "depends-on")
	for _, dep := range deps {
		dep, _, _ = strings.Cut(dep, "#")
		if path.Base(dep) == path.Base(parent) {
			named = true
		}
	}
	if !named {
		bad(1, "", "", "a companion plan lists %s under `depends-on` in its frontmatter", path.Base(parent))
	}

	heading := func(i int) (int, string, bool) {
		if fenced[i] {
			return 0, "", false
		}
		m := headingLine.FindStringSubmatch(lines[i])
		if m == nil {
			return 0, "", false
		}
		return len(m[1]), strings.TrimSpace(strings.TrimRight(m[2], "#")), true
	}
	start, end := -1, len(lines)
	for i := range lines {
		level, title, ok := heading(i)
		switch {
		case !ok:
		case start < 0 && level == 2 && title == "Tasks":
			start = i + 1
		case start >= 0 && level <= 2:
			end = i
		}
		if end != len(lines) {
			break
		}
	}
	if start < 0 {
		if len(out.findings) == 0 {
			bad(1, "", "", "a DECIDED companion plan has no `## Tasks` section")
		}
		return out
	}

	slug := companionSlug(parent)
	seen := map[string]bool{}
	local := map[string]string{} // local id -> item id
	type pending struct {
		task  companionTask
		after []string
	}
	var tasks []pending
	for i := start; i < end; i++ {
		level, title, ok := heading(i)
		if !ok || level != 3 || !taskHeadingStart.MatchString(lines[i]) {
			continue
		}
		bodyEnd := end
		for j := i + 1; j < end; j++ {
			if l, _, ok := heading(j); ok && l <= 3 {
				bodyEnd = j
				break
			}
		}
		m := taskHeading.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m == nil {
			bad(i+1, title, "", "a task heading reads `### Task <local_id>: <Title>`")
			i = bodyEnd - 1
			continue
		}
		id, taskTitle := m[1], strings.TrimSpace(m[2])
		fail := func(format string, args ...any) { bad(i+1, title, "", format, args...) }
		if !localTaskID.MatchString(id) {
			fail("task id %q must match ^[a-z0-9][a-z0-9-]*$", id)
		} else if seen[id] {
			fail("task id %q appears twice in this plan", id)
		}
		seen[id] = true
		if taskTitle == "" {
			fail("task %q has an empty title", id)
		}

		// Metadata bullets sit at the left margin; their nested lines belong to them.
		type meta struct {
			inline string
			nested []string
			line   int
		}
		metas := map[string]meta{}
		key := ""
		for j := i + 1; j < bodyEnd; j++ {
			line := lines[j]
			if fenced[j] {
				key = ""
				continue
			}
			if mm := metaBullet.FindStringSubmatch(line); mm != nil {
				key = strings.TrimSpace(mm[1])
				cur := metas[key]
				cur.inline, cur.line = strings.TrimSpace(mm[2]), j+1
				metas[key] = cur
				continue
			}
			if key == "" {
				continue
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			if line[0] == ' ' || line[0] == '\t' {
				cur := metas[key]
				cur.nested = append(cur.nested, line)
				metas[key] = cur
				continue
			}
			key = ""
		}

		itemID := "item-" + slug + "-" + id
		if v, ok := metas["ID"]; ok {
			explicit := strings.Trim(strings.TrimSpace(v.inline), "`")
			if !itemIDShape.MatchString(explicit) {
				fail("ID %q does not match ^item-[a-z0-9-]+$", explicit)
			} else {
				itemID = explicit
			}
		}
		local[id] = itemID
		payload := itemPayload{ItemID: itemID, Title: taskTitle, DesignDoc: parent, BodyRef: plan.Path + "#" + headingSlug(title), Seat: SeatBuild, JudgeMode: ""}
		if v, ok := metas["Seat"]; ok {
			seat := strings.Trim(strings.TrimSpace(v.inline), "`")
			if !ValidSeatName(seat) {
				fail(invalidSeatName, seat)
			}
			payload.Seat = seat
		}
		if v, ok := metas["Workflow"]; ok {
			ref := strings.Trim(strings.TrimSpace(v.inline), "`")
			if !WorkflowRefValid(ref) {
				fail("workflow %q must have the form <name>@v<N>", ref)
			}
			payload.Workflow = ref
		}
		if v, ok := metas["Express"]; ok {
			switch strings.TrimSpace(v.inline) {
			case "true":
				payload.Express = true
			case "false":
			default:
				fail("Express must be true or false")
			}
		}
		if v, ok := metas["Judge"]; ok {
			switch strings.TrimSpace(v.inline) {
			case "agent":
				payload.JudgeMode = "agent"
			case "mechanical":
			default:
				fail("Judge must be agent or mechanical")
			}
		}
		var after []string
		if v, ok := metas["After"]; ok && strings.TrimSpace(v.inline) != "none" {
			for _, ref := range splitList(strings.ReplaceAll(v.inline, "`", "")) {
				after = append(after, ref)
			}
		}

		// Criteria: nested `- **c<N>:**` lines, continued by following plain lines.
		var criteria []criterion
		if v, ok := metas["Criteria"]; ok {
			current := -1
			ids := map[string]bool{}
			for _, raw := range v.nested {
				t := strings.TrimSpace(raw)
				if cm := criterionLine.FindStringSubmatch(t); cm != nil {
					if ids[cm[1]] {
						fail("criterion %q appears twice", cm[1])
						current = -1
						continue
					}
					ids[cm[1]] = true
					criteria = append(criteria, criterion{CriterionID: cm[1], Text: strings.TrimSpace(cm[2])})
					current = len(criteria) - 1
					continue
				}
				if listMarker.MatchString(t) {
					current = -1
					continue
				}
				if current >= 0 {
					criteria[current].Text = strings.TrimSpace(criteria[current].Text + " " + t)
				}
			}
		}
		switch {
		case len(criteria) == 0:
			fail("task %q has no `- **c1:**` criterion under `- **Criteria:**`", id)
		case len(criteria) > 8:
			fail("task %q has %d criteria; a task carries 1 to 8", id, len(criteria))
		}
		for _, c := range criteria {
			if c.Text == "" {
				fail("criterion %q of task %q has no text", c.CriterionID, id)
			}
		}
		if v, ok := metas["Claims"]; ok {
			for _, entry := range splitList(strings.ReplaceAll(v.inline, "`", "")) {
				cid, class, found := strings.Cut(entry, "=")
				if !found {
					cid, class, found = strings.Cut(entry, ":")
				}
				cid, class = strings.TrimSpace(cid), strings.TrimSpace(class)
				if !found || !claimClasses[class] {
					fail("Claims carries %q: name a criterion and a known claim class", entry)
					continue
				}
				hit := false
				for k := range criteria {
					if criteria[k].CriterionID == cid {
						criteria[k].ClaimClass, hit = class, true
					}
				}
				if !hit {
					fail("Claims names criterion %q, which the task does not carry", cid)
				}
			}
		}
		if err := checkCriteria(itemID, criteria); err != nil {
			fail("%s", err)
		}
		sort.Slice(criteria, func(x, y int) bool { return criteria[x].CriterionID < criteria[y].CriterionID })
		payload.Criteria = criteria

		// Waits on questions.
		var waits []QuestionRef
		if v, ok := metas["Waits on questions"]; ok {
			refs, err := parseWaits(v.inline, v.nested, byPath)
			if err != "" {
				fail("%s", err)
			}
			waits = refs
		}
		tasks = append(tasks, pending{
			task:  companionTask{payload: payload, heading: title, line: i + 1, local: id, waits: waits},
			after: after,
		})
		i = bodyEnd - 1
	}

	// Resolve After: against siblings, then check for a cycle among them.
	edges := map[string][]string{}
	for k := range tasks {
		t := &tasks[k]
		for _, ref := range t.after {
			if full, ok := local[ref]; ok {
				t.task.payload.BlockedBy = append(t.task.payload.BlockedBy, full)
				edges[t.task.local] = append(edges[t.task.local], ref)
				continue
			}
			if itemIDShape.MatchString(ref) {
				t.task.payload.BlockedBy = append(t.task.payload.BlockedBy, ref)
				continue
			}
			bad(t.task.line, t.task.heading, "", "After names %q, which is neither a task of this plan nor an item id", ref)
		}
		sort.Strings(t.task.payload.BlockedBy)
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		for _, next := range edges[id] {
			if visit(next) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for k := range tasks {
		if visit(tasks[k].task.local) {
			bad(tasks[k].task.line, tasks[k].task.heading, "", "the `After:` references of this plan form a cycle")
			break
		}
	}
	if len(tasks) == 0 && len(out.findings) == 0 {
		bad(start, "Tasks", "", "the `## Tasks` section holds no `### Task <local_id>: <Title>` entry")
	}
	for _, t := range tasks {
		out.tasks = append(out.tasks, t.task)
	}
	if len(out.findings) > 0 {
		out.tasks = nil
	}
	return out
}

// parseWaits reads one `- **Waits on questions:**` bullet. A marked wait names an `OQ-*`
// id; an unmarked prose wait names a heading anchor with its quoted question, and records
// the hash of that section as the document now stands (OQ-PSH1).
func parseWaits(inline string, nested []string, byPath map[string]SyncDocument) ([]QuestionRef, string) {
	text := strings.TrimSpace(inline)
	if text == "none" && len(nested) == 0 {
		return nil, ""
	}
	for _, n := range nested {
		text += " " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(n), "- "))
	}
	text = strings.TrimSpace(text)
	if text == "" || text == "none" {
		return nil, ""
	}
	locs := waitRefToken.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil, fmt.Sprintf("Waits on questions %q names no `<doc>#<id>` reference", text)
	}
	var refs []QuestionRef
	seen := map[string]bool{}
	for k, loc := range locs {
		ref := text[loc[2]:loc[3]]
		endAt := len(text)
		if k+1 < len(locs) {
			endAt = locs[k+1][0]
		}
		rest := text[loc[1]:endAt]
		doc, id, _ := strings.Cut(ref, "#")
		doc = path.Clean(doc)
		q := QuestionRef{Key: doc + "#" + id, DocPath: doc}
		quoted := ""
		if qm := waitQuoted.FindStringSubmatch(rest); qm != nil {
			quoted = qm[1]
		}
		rest = waitQuoted.ReplaceAllString(rest, "")
		rest = waitSectionHash.ReplaceAllString(rest, "")
		q.Reason = strings.Trim(strings.TrimSpace(rest), " ,;—–-:")
		switch {
		case oqShaped.MatchString(id) && quoted == "":
			q.QuestionID = id
		default:
			src, ok := byPath[doc]
			if !ok {
				return nil, fmt.Sprintf("Waits on questions names %q, which is not a tracked document", doc)
			}
			hash, found := SectionSHA256(string(src.Content), id)
			if !found {
				return nil, fmt.Sprintf("%s has no heading with the anchor %q", doc, id)
			}
			if quoted == "" {
				return nil, fmt.Sprintf("the prose wait %q needs the quoted question in parentheses", ref)
			}
			q.HeadingSlug, q.QuotedQuestion, q.SectionSHA256 = id, quoted, hash
		}
		if seen[q.Key] {
			continue
		}
		seen[q.Key] = true
		refs = append(refs, q)
	}
	return refs, ""
}

// planHasQuestionDirective answers the line of the first unfenced `question` or `oq`
// directive in content, or 0.
func planHasQuestionDirective(content []byte) int {
	fence := fenceState{}
	for i, line := range splitLines(content) {
		if fence.step(line) {
			continue
		}
		if questionDirOpen.MatchString(line) {
			return i + 1
		}
	}
	return 0
}
