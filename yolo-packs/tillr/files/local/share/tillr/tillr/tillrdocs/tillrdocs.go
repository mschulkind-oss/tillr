// Package tillrdocs checks the tillr documentation contract: module READMEs,
// the tillr-operating skill, documented binary faces, and rolling-check policy.
package tillrdocs

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Modules lists the eight binary modules whose READMEs are checked.
var Modules = []string{
	"tillr-cli",
	"tillr-drain",
	"tillr-gate",
	"tillr-item",
	"tillr-land",
	"tillr-run",
	"tillr-store",
	"tillr-view",
}

// Binary maps each module directory name to its built binary name.
var Binary = map[string]string{
	"tillr-cli":   "tillr",
	"tillr-drain": "tillr-drain",
	"tillr-gate":  "tillr-gate",
	"tillr-item":  "tillr-item",
	"tillr-land":  "tillr-land",
	"tillr-run":   "tillr-run",
	"tillr-store": "tillr-store",
	"tillr-view":  "tillr-view",
}

// Nouns lists the seven nouns the tillr dispatcher forwards.
var Nouns = []string{"store", "item", "land", "drain", "run", "gate", "view"}

// SkillRelPath is the repo-relative path to the tillr-operating skill.
const SkillRelPath = "yolo-packs/tillr/skills/tillr-operating/SKILL.md"

var (
	titleRe      = regexp.MustCompile(`^> \*\*Known gaps\.\*\*`)
	gapRe        = regexp.MustCompile(`^> \d+\. \*\*\S`)
	fenceRe      = regexp.MustCompile("(?ms)^```[^\\n]*\\n(.*?)^```")
	optionLineRe = regexp.MustCompile(`^\s+--[A-Za-z]`)
	flagHeaderRe = regexp.MustCompile(`(?i)\b(flag|option|switch)\b`)
	envVarRe     = regexp.MustCompile(`"([A-Z][A-Z_]+)="`)
)

// Documents returns the nine layer-2 documents (8 module READMEs + SKILL.md).
func Documents(root string) []string {
	docs := make([]string, 0, len(Modules)+1)
	for _, mod := range Modules {
		docs = append(docs, filepath.Join(root, "tools", mod, "README.md"))
	}
	docs = append(docs, filepath.Join(root, SkillRelPath))
	return docs
}

func relPath(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

func textOf(root, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("missing document: %s", relPath(root, path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("missing document: %s: %w", relPath(root, path), err)
	}
	return string(b), nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// OpeningBlock returns the callout a document opens with, skipping headings and blanks.
func OpeningBlock(text string) []string {
	lines := splitLines(text)
	index := 0
	for index < len(lines) && (strings.TrimSpace(lines[index]) == "" || strings.HasPrefix(strings.TrimLeft(lines[index], " \t\r\n"), "#")) {
		index++
	}
	var block []string
	for index < len(lines) && strings.HasPrefix(lines[index], ">") {
		block = append(block, strings.TrimSpace(lines[index]))
		index++
	}
	return block
}

// CheckCallout verifies the eight module READMEs open with the same Known gaps callout.
func CheckCallout(root string) (string, error) {
	stamps := make(map[string]string, len(Modules))
	for _, module := range Modules {
		p := filepath.Join(root, "tools", module, "README.md")
		txt, err := textOf(root, p)
		if err != nil {
			return "", err
		}
		block := OpeningBlock(txt)
		if len(block) == 0 || block[0] != "> [!WARNING]" {
			first := "empty"
			if len(block) > 0 {
				first = fmt.Sprintf("[%q]", block[0])
			}
			return "", fmt.Errorf("tools/%s/README.md does not open with the Known gaps callout: its first block is %s", module, first)
		}
		if len(block) < 2 || !titleRe.MatchString(block[1]) {
			got := "nothing"
			if len(block) > 1 {
				got = block[1]
			}
			return "", fmt.Errorf("tools/%s/README.md's callout does not state its known gaps: expected a line '> **Known gaps.**', got %s", module, got)
		}
		hasGap := false
		for _, line := range block[2:] {
			if gapRe.MatchString(line) {
				hasGap = true
				break
			}
		}
		if !hasGap {
			return "", fmt.Errorf("tools/%s/README.md's Known gaps callout lists no gap: expected at least one numbered line '> 1. **<the gap>**'", module)
		}
		stamps[module] = strings.Join(block, "\n")
	}
	first := stamps[Modules[0]]
	var differing []string
	for _, module := range Modules {
		if stamps[module] != first {
			differing = append(differing, fmt.Sprintf("tools/%s/README.md", module))
		}
	}
	if len(differing) > 0 {
		sort.Strings(differing)
		return "", fmt.Errorf("the eight module READMEs carry different Known gaps banners, so a reader cannot tell which one is current: %s", strings.Join(differing, ", "))
	}
	gaps := 0
	for _, line := range strings.Split(first, "\n") {
		if gapRe.MatchString(line) {
			gaps++
		}
	}
	return fmt.Sprintf("every module README opens with the same Known gaps callout (%d gaps listed)", gaps), nil
}

// CheckNoFlagTable verifies no document carries a flag table or option list.
func CheckNoFlagTable(root string) (string, error) {
	docs := Documents(root)
	for _, p := range docs {
		txt, err := textOf(root, p)
		if err != nil {
			return "", err
		}
		relative := relPath(root, p)
		for idx, line := range splitLines(txt) {
			number := idx + 1
			stripped := strings.TrimSpace(line)
			if optionLineRe.MatchString(line) {
				return "", fmt.Errorf("%s:%d is an option list, which is a flag table by another name: name the face and let `--help` carry the flags. Line: %s", relative, number, stripped)
			}
			if !strings.HasPrefix(stripped, "|") {
				continue
			}
			rawCells := strings.Split(strings.Trim(stripped, "|"), "|")
			for _, rawCell := range rawCells {
				cell := strings.TrimSpace(rawCell)
				if strings.HasPrefix(cell, "--") {
					return "", fmt.Errorf("%s:%d lists a flag in a table, which is the page that disagrees with the binary a month later: name the face and let `--help` carry the flags. Line: %s", relative, number, stripped)
				}
				if flagHeaderRe.MatchString(cell) {
					return "", fmt.Errorf("%s:%d is a flag table: name the face and let `--help` carry the flags. Line: %s", relative, number, stripped)
				}
			}
		}
	}
	return fmt.Sprintf("no document carries a flag table (%d documents)", len(docs)), nil
}

var (
	recoverySectionRe = regexp.MustCompile(`(?ms)^## Recovery\s*\n(.*?)(?:^## |\z)`)
	clonedV2Re        = regexp.MustCompile(`\bitem-[a-z0-9-]+-v2\b`)
	recoveryForbidden = []struct {
		re  *regexp.Regexp
		msg string
	}{
		{regexp.MustCompile(`(?i)\bscript\w*\b|\b(?:python\d*|bash|sh)\s+[^\n` + "`" + `]+|\S+\.(?:py|sh)\b`), "a recovery script"},
		{regexp.MustCompile(`(?i)\bsqlite\w*\b|\b(?:edit|update|modify|write)\b[^\n.]*\bstore\b|\b(?:UPDATE|DELETE FROM|INSERT INTO)\b`), "a direct store edit"},
		{regexp.MustCompile(`(?i)\bgit\s+reset\b`), "git reset in a lane"},
	}
)

// RecoveryErrors checks that Recovery keeps the original item's identity and never names a replacement id.
func RecoveryErrors(text string) []string {
	m := recoverySectionRe.FindStringSubmatch(text)
	if m == nil {
		return []string{"operating skill has no Recovery section"}
	}
	body := m[1]
	var errors []string
	if !strings.Contains(body, "tillr drain retry") || !strings.Contains(body, "own id") {
		errors = append(errors, "Recovery must retry the failed item under its own id")
	}
	if clonedV2Re.MatchString(body) {
		errors = append(errors, "Recovery names a cloned -v2 item id")
	}
	for _, f := range recoveryForbidden {
		if f.re.MatchString(body) {
			errors = append(errors, fmt.Sprintf("Recovery names %s; use a supported tillr face", f.msg))
		}
	}
	return errors
}

// CheckRecovery verifies SKILL.md Recovery section.
func CheckRecovery(root string) (string, error) {
	txt, err := textOf(root, filepath.Join(root, SkillRelPath))
	if err != nil {
		return "", err
	}
	if errs := RecoveryErrors(txt); len(errs) > 0 {
		return "", fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return "Recovery uses supported commands under the same item id; cloned ids, scripts, store edits and git reset fixtures refused", nil
}

func sameArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CheckCommentIntakeDocumented verifies hand-driven comment intake is documented.
func CheckCommentIntakeDocumented(root string) (string, error) {
	expected := []string{"tillr", "item", "comments", "take"}
	for _, p := range []string{filepath.Join(root, "tools/tillr-item/README.md"), filepath.Join(root, SkillRelPath)} {
		invs, err := Invocations(root, p)
		if err != nil {
			return "", err
		}
		found := false
		for _, inv := range invs {
			if sameArgv(inv, expected) {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("%s omits the hand-driven comment intake command", relPath(root, p))
		}
	}
	return "hand-driven comment intake is documented in the item README and operating skill", nil
}

// CheckDesignerSessionSelectionDocumented verifies session selection and Pi compact step.
func CheckDesignerSessionSelectionDocumented(root string) (string, error) {
	for _, p := range []string{filepath.Join(root, "tools/tillr-drain/README.md"), filepath.Join(root, SkillRelPath)} {
		txt, err := textOf(root, p)
		if err != nil {
			return "", err
		}
		if !strings.Contains(txt, "#choosing-at-dispatch") || !strings.Contains(txt, "40,000") {
			return "", fmt.Errorf("%s omits the session selection policy", relPath(root, p))
		}
	}
	scratch, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "tillr-docs-compact-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	if err := BuildModule("tillr-run", "tillr-run", scratch, root); err != nil {
		return "", fmt.Errorf("building tillr-run failed: %w", err)
	}
	out, err := exec.Command(filepath.Join(scratch, "tillr-run"), "pi-compact", "--help").Output()
	if err != nil || !strings.Contains(string(out), "--session-id") {
		return "", fmt.Errorf("the documented Pi compact step must answer --help and accept a session id")
	}
	return "session selection and the Pi compact step are documented", nil
}

// CheckWorkerRefusalSessionDocumented verifies worker refusal continuation is documented as built.
func CheckWorkerRefusalSessionDocumented(root string) (string, error) {
	readme, err := textOf(root, filepath.Join(root, "tools/tillr-drain/README.md"))
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(readme, "### A refused run goes back to its worker", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("tools/tillr-drain/README.md omits worker refusal continuation section")
	}
	section := strings.SplitN(parts[1], "\n### ", 2)[0]
	for _, phrase := range []string{
		"same lane and session", "distinct run record", "worker_session_reason",
		"cannot-resume", "session-missing", "resume-failed", "new commit",
	} {
		if !strings.Contains(section, phrase) {
			return "", fmt.Errorf("worker refusal continuation omits %q", phrase)
		}
	}
	design, err := textOf(root, filepath.Join(root, "docs/design/tillr-judge-and-lifecycle.md"))
	if err != nil {
		return "", err
	}
	tparts := strings.SplitN(design, "### 4.4 How a Worker Run's Ending", 2)
	if len(tparts) < 2 {
		return "", fmt.Errorf("docs/design/tillr-judge-and-lifecycle.md omits 4.4 table")
	}
	var ending string
	for _, line := range splitLines(tparts[1]) {
		if strings.HasPrefix(line, "| 1. ") {
			ending = line
			break
		}
	}
	if !strings.HasSuffix(ending, "| Built. |") {
		return "", fmt.Errorf("worker ending 1 must be marked built")
	}
	return "worker refusal continuation and its fresh fallback are documented as built", nil
}

// CheckCacheSafeWorkingLoopDocumented verifies cache-safe test guidance.
func CheckCacheSafeWorkingLoopDocumented(root string) (string, error) {
	brief := filepath.Join(root, "tools/tillr-run/backends/brief-template.md")
	skill := filepath.Join(root, SkillRelPath)
	for _, p := range []string{brief, skill} {
		txt, err := textOf(root, p)
		if err != nil {
			return "", err
		}
		for _, instruction := range []string{
			"targeted `-run`", "narrowest test selection",
			"leave\nthe whole suite to the gate", "drop `-count=1`",
		} {
			if !strings.Contains(txt, instruction) {
				return "", fmt.Errorf("%s omits cache-safe test guidance: %s", relPath(root, p), instruction)
			}
		}
	}
	readme, err := textOf(root, filepath.Join(root, "tools/tillr-drain/README.md"))
	if err != nil {
		return "", err
	}
	cacheCheck := "cache_test.py"
	if _, err := os.Stat(filepath.Join(root, "tools/tillr-drain/cmd/cache-test/main.go")); err == nil {
		cacheCheck = "cmd/cache-test/main.go"
	}
	for _, evidence := range []string{"test binary declares the sibling sources", cacheCheck, "siblings.txt"} {
		if !strings.Contains(readme, evidence) {
			return "", fmt.Errorf("drain README omits its cache mechanism or regression check: %s", evidence)
		}
	}
	return "worker brief and operating skill use targeted tests and cache-safe full verification", nil
}

// CheckRunEnvironmentDocumented verifies run README lists every child environment variable.
func CheckRunEnvironmentDocumented(root string) (string, error) {
	readmePath := filepath.Join(root, "tools/tillr-run/README.md")
	txt, err := textOf(root, readmePath)
	if err != nil {
		return "", err
	}
	var blocks []string
	for _, m := range fenceRe.FindAllStringSubmatch(txt, -1) {
		blocks = append(blocks, m[1])
	}
	source, err := textOf(root, filepath.Join(root, "tools/tillr-run/run/exec.go"))
	if err != nil {
		return "", err
	}
	variables := make(map[string]bool)
	for _, m := range envVarRe.FindAllStringSubmatch(source, -1) {
		variables[m[1]] = true
	}
	var envBlocks []string
	for _, b := range blocks {
		if strings.Contains(b, "TILLR_ITEM_ID") {
			envBlocks = append(envBlocks, b)
		}
	}
	if len(envBlocks) != 1 {
		return "", fmt.Errorf("run README must list the child environment in one fenced block")
	}
	lines := splitLines(envBlocks[0])
	var listed []string
	listedSet := make(map[string]bool)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			listed = append(listed, fields[0])
			listedSet[fields[0]] = true
		}
	}
	same := len(listed) == len(variables) && len(listedSet) == len(variables)
	if same {
		for k := range variables {
			if !listedSet[k] {
				same = false
				break
			}
		}
	}
	if !same {
		var sorted []string
		for k := range variables {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		return "", fmt.Errorf("run README environment list %v differs from exec's %v", listed, sorted)
	}
	prose := fenceRe.ReplaceAllString(txt, "")
	if strings.Contains(prose, "YOLO_DURABLE_DIR") {
		return "", fmt.Errorf("run README names the scratch-directory variable outside a fence")
	}
	for _, line := range lines {
		if !strings.Contains(line, ":") {
			return "", fmt.Errorf("run README environment line omits when it is set: %s", line)
		}
	}
	for _, required := range []string{"scratch_env", "artifact_refs", "`scratch`", "node_id"} {
		if !strings.Contains(txt, required) {
			return "", fmt.Errorf("run README omits its request or scratch-record contract: %s", required)
		}
	}
	return "run README lists every child environment variable once in one fence, with conditions", nil
}

// CheckDocumentFedPrerequisiteDocumented verifies document-fed workflow prerequisite is documented.
func CheckDocumentFedPrerequisiteDocumented(root string) error {
	drain, err := textOf(root, filepath.Join(root, "tools/tillr-drain/README.md"))
	if err != nil {
		return err
	}
	skill, err := textOf(root, filepath.Join(root, SkillRelPath))
	if err != nil {
		return err
	}
	heading := "## Document-fed workflow prerequisite"
	if !strings.Contains(drain, heading) {
		return fmt.Errorf("the drain must name the document-fed workflow prerequisite separately")
	}
	section := strings.SplitN(strings.SplitN(drain, heading, 2)[1], "\n## ", 2)[0]
	for _, phrase := range []string{
		"before it is built", "triage-no-block", "question-open",
		"conflict-owner", "at most one run", "title, body reference, and criteria",
	} {
		if !strings.Contains(section, phrase) {
			return fmt.Errorf("the document-fed prerequisite omits %q", phrase)
		}
	}
	if !strings.Contains(skill, "#document-fed-workflow-prerequisite") {
		return fmt.Errorf("the operating skill must route the reader to the drain prerequisite")
	}
	return nil
}

// CheckRunTimingAndScheduleDocumented verifies view README and operating skill document run timing/schedule.
func CheckRunTimingAndScheduleDocumented(root string) (string, error) {
	readme, err := textOf(root, filepath.Join(root, "tools/tillr-view/README.md"))
	if err != nil {
		return "", err
	}
	skill, err := textOf(root, filepath.Join(root, SkillRelPath))
	if err != nil {
		return "", err
	}
	for _, required := range []string{
		"tillr-view run", "tillr view run", "slot-waits.jsonl",
		"slot_wait_ms", "tool_active_ms", "schedule", "time-order",
	} {
		if !strings.Contains(readme, required) {
			return "", fmt.Errorf("tools/tillr-view/README.md omits run timing/schedule contract: %s", required)
		}
	}
	for _, required := range []string{"tillr view run", "slot_wait_ms", "slot-waits.jsonl", "schedule"} {
		if !strings.Contains(skill, required) {
			return "", fmt.Errorf("%s omits run timing/schedule audit guidance: %s", SkillRelPath, required)
		}
	}
	return "view README and operating skill document tillr view run, slot-waits.jsonl, and schedule", nil
}

// CheckSeedRetirementDocumented verifies retired seed intake files are gone and places are documented.
func CheckSeedRetirementDocumented(root string) (string, error) {
	for _, retired := range []string{
		"config/tillr/seed-items.json",
		"tools/tillr-drain/seed_test.go",
		"tools/tillr-item/item/seed.go",
		"tools/gate/checks/seed-freeze.json",
		"tools/gate/checks/seed-freeze.py",
	} {
		if _, err := os.Stat(filepath.Join(root, retired)); err == nil {
			return "", fmt.Errorf("retired seed intake remains: %s", retired)
		}
	}
	for _, p := range []string{filepath.Join(root, "tools/tillr-view/web/README.md"), filepath.Join(root, SkillRelPath)} {
		txt, err := textOf(root, p)
		if err != nil {
			return "", err
		}
		if !strings.Contains(txt, "tillr item place") {
			return "", fmt.Errorf("%s omits the replacement priority command", relPath(root, p))
		}
	}
	rail, err := textOf(root, filepath.Join(root, "tools/tillr-view/web/src/components/QueueRail.tsx"))
	if err != nil {
		return "", err
	}
	if !strings.Contains(rail, "queue-row__place") || strings.Contains(rail, "UnrankedHeading") {
		return "", fmt.Errorf("queue rail must show places in one list")
	}
	if _, err := os.Stat(filepath.Join(root, "tools/tillr-view/web/tests/queue-unranked.test.tsx")); err == nil {
		return "", fmt.Errorf("retired unranked block test remains")
	}
	return "seed intake is retired and places have a writer", nil
}

// CheckWorkflowJudgeDocumented verifies operator and consumer docs describe judgment as a workflow step.
func CheckWorkflowJudgeDocumented(root string) (string, error) {
	pack := filepath.Join(root, "yolo-packs/tillr")
	for _, p := range []string{filepath.Join(pack, "README.md"), filepath.Join(pack, "skills/tillr-designs/SKILL.md")} {
		txt, err := textOf(root, p)
		if err != nil {
			return "", err
		}
		var stable string
		for _, line := range splitLines(txt) {
			if strings.Contains(line, "`item` (`apply`") {
				stable = line
				break
			}
		}
		if stable == "" || strings.Contains(stable, "`grant`") || !strings.Contains(stable, "`judge`") {
			return "", fmt.Errorf("%s must advertise judge, not the retired grant face", relPath(root, p))
		}
	}
	for _, p := range []string{
		filepath.Join(root, SkillRelPath),
		filepath.Join(pack, "tasks/autoqa-handoff.md"),
		filepath.Join(root, "docs/reference/tillr-item-graphs.md"),
	} {
		txt, err := textOf(root, p)
		if err != nil {
			return "", err
		}
		if !strings.Contains(txt, "`judge_classes`") {
			return "", fmt.Errorf("%s omits the evaluator's class list", relPath(root, p))
		}
	}
	return "operator and consumer docs describe judgment as a workflow step", nil
}

// CheckRefusedPushDocumented verifies refused-push states, repair landing, and push face are documented.
func CheckRefusedPushDocumented(root string) (string, error) {
	required := []struct {
		path    string
		phrases []string
	}{
		{"tools/tillr-drain/README.md", []string{"tillr drain push", "push-remote-declined", "push-patch-move-held", "remote-sync-conflict"}},
		{"tools/tillr-land/README.md", []string{"gated remote-sync repairs", "trunk_after", "replay_tip"}},
		{"tools/tillr-view/README.md", []string{"remote-sync-conflict", "push-remote-declined", "push-failed"}},
		{SkillRelPath, []string{"tillr drain push", "push-remote-declined", "remote-sync-conflict"}},
	}
	for _, req := range required {
		txt, err := textOf(root, filepath.Join(root, req.path))
		if err != nil {
			return "", err
		}
		for _, phrase := range req.phrases {
			if !strings.Contains(txt, phrase) {
				return "", fmt.Errorf("%s omits the refused-push term %q", req.path, phrase)
			}
		}
	}
	return "the refused-push states, repair landing and push face are documented", nil
}

func findRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = wd
	out, err := cmd.Output()
	if err == nil {
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			return trimmed
		}
	}
	return wd
}

// Main runs the full documentation contract check and returns an exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "FAIL unexpected arguments: %s\n", strings.Join(args, " "))
		return 2
	}
	root := findRepoRoot()

	type facesResult struct {
		buildErr   error
		helpErr    error
		checked    int
		fixtureErr error
	}
	var facesRes facesResult
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		facesRes.buildErr, facesRes.helpErr, facesRes.checked, facesRes.fixtureErr = RunFacesLane(root)
	}()

	ok := func(msg string) {
		fmt.Fprintf(stdout, "ok   %s\n", msg)
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "FAIL %s\n", err)
		return 1
	}

	if msg, err := CheckRefusedPushDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckWorkflowJudgeDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckRunTimingAndScheduleDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckSeedRetirementDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckRunEnvironmentDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckWorkerRefusalSessionDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckCacheSafeWorkingLoopDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if err := CheckDocumentFedPrerequisiteDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	}
	if msg, err := CheckDesignerSessionSelectionDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckCommentIntakeDocumented(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckRecovery(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckRollingEnabled(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckCallout(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}
	if msg, err := CheckNoFlagTable(root); err != nil {
		wg.Wait()
		return fail(err)
	} else {
		ok(msg)
	}

	wg.Wait()
	if facesRes.buildErr != nil {
		return fail(facesRes.buildErr)
	}
	if facesRes.helpErr != nil {
		return fail(facesRes.helpErr)
	}
	if facesRes.fixtureErr != nil {
		return fail(facesRes.fixtureErr)
	}
	ok(fmt.Sprintf("every documented face answers `--help` with its own flag set (%d invocations over %d documents)", facesRes.checked, len(Documents(root))))
	ok("broken or silent face `--help` fails with the documented face's finding")
	fmt.Fprintln(stdout, "the eight module READMEs state their judgment, and every face they name exists")
	return 0
}
