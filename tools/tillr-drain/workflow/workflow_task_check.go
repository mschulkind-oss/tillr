package workflow

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
)

const maxCheckFileBytes = 256 * 1024

// CheckNodeTasks verifies that no writing stage's task contradicts its own
// write_scope or forbids committing.
func (w *Workflow) CheckNodeTasks(root string) error {
	for _, stage := range w.Stages {
		if stage.Access != "write" {
			continue
		}
		if err := ValidateNodeTask(root, stage.ID, stage.Access, stage.WriteScope.Paths, stage.Prompt); err != nil {
			return err
		}
	}
	return nil
}

// ValidateNodeTask refuses a writing node whose task instructs the worker to
// leave work uncommitted, names a file to change outside its write_scope, or
// names a check to pass that reads a file the node must change when that check
// is outside its write_scope.
func ValidateNodeTask(root, nodeID, access string, writeScope []string, prompt string) error {
	if access != "write" || strings.TrimSpace(prompt) == "" {
		return nil
	}
	if phrase, ok := taskForbidsCommit(prompt); ok {
		return fmt.Errorf("workflow-task-forbids-commit: node %s: task forbids committing (%q); a write node finishes by landing a commit or reporting an obstacle", nodeID, phrase)
	}
	if len(writeScope) == 0 {
		return nil
	}
	expanded, err := (WorkflowStage{ID: nodeID, WriteScope: WorkflowScope{Paths: writeScope}}).ScopePaths(nil)
	if err != nil {
		return err
	}
	cleanedScope := make([]string, len(expanded))
	for i, s := range expanded {
		cleanedScope[i] = path.Clean(s)
	}

	writeTargets, checkTargets := parsePromptTargets(root, prompt)
	for _, target := range writeTargets {
		if !InsideFence(path.Clean(target), cleanedScope) {
			return fmt.Errorf("workflow-task-outside-scope: node %s: task names %s to change outside write_scope", nodeID, target)
		}
	}

	if root == "" || len(checkTargets) == 0 {
		return nil
	}

	changedPaths := collectChangedFilePaths(root, writeTargets, writeScope)
	if len(changedPaths) == 0 {
		return nil
	}

	for _, checkTarget := range checkTargets {
		cleanCheck := path.Clean(checkTarget)
		if InsideFence(cleanCheck, cleanedScope) {
			continue
		}
		if refFile, changed, ok := checkReadsChangedPath(root, cleanCheck, cleanedScope, changedPaths); ok {
			return fmt.Errorf("workflow-task-outside-scope: node %s: check %s outside write_scope reads %s", nodeID, refFile, changed)
		}
	}
	return nil
}

var (
	reLeaveUncommitted = regexp.MustCompile(`(?i)\bleave\b[^.!?\n;]{0,60}\buncommitted\b`)
	reKeepUncommitted  = regexp.MustCompile(`(?i)\bkeep\b[^.!?\n;]{0,60}\buncommitted\b`)
	reLeaveInWorktree  = regexp.MustCompile(`(?i)\bleave\s+(?:all\s+|the\s+|your\s+)?(?:changes|edits|work|files)\s+in\s+the\s+worktree\b`)
	reDoNotCommit      = regexp.MustCompile(`(?i)\b(?:do\s+not|don't|dont)\s+commit\b(?:\s+([A-Za-z][A-Za-z0-9'_-]*(?:\s+[A-Za-z][A-Za-z0-9'_-]*)?))?`)
	reWithoutCommit    = regexp.MustCompile(`(?i)\b(?:without|skip|avoid)\s+committing\b(?:\s+([A-Za-z][A-Za-z0-9'_-]*))?`)
	reNegationPrefix   = regexp.MustCompile(`(?i)\b(?:do\s+not|don't|dont|not|never|without|must\s+not|should\s+not|cannot|can't)\s+$`)
	reLeaveNo          = regexp.MustCompile(`(?i)^leave\s+no\b`)
)

// taskForbidsCommit reports whether a write node's prompt instructs the worker
// to leave changes uncommitted or skip committing.
func taskForbidsCommit(prompt string) (string, bool) {
	for _, re := range []*regexp.Regexp{reLeaveUncommitted, reKeepUncommitted, reLeaveInWorktree} {
		for _, loc := range re.FindAllStringIndex(prompt, -1) {
			match := prompt[loc[0]:loc[1]]
			if reLeaveNo.MatchString(match) {
				continue
			}
			prefix := prompt[max(0, loc[0]-24):loc[0]]
			if reNegationPrefix.MatchString(prefix) {
				continue
			}
			return strings.TrimSpace(match), true
		}
	}
	for _, loc := range reDoNotCommit.FindAllStringSubmatchIndex(prompt, -1) {
		full := strings.TrimSpace(prompt[loc[0]:loc[1]])
		follow := ""
		if loc[2] >= 0 && loc[3] >= 0 {
			follow = strings.ToLower(strings.TrimSpace(prompt[loc[2]:loc[3]]))
		}
		if isGenericCommitObject(follow) {
			return full, true
		}
	}
	for _, loc := range reWithoutCommit.FindAllStringSubmatchIndex(prompt, -1) {
		full := strings.TrimSpace(prompt[loc[0]:loc[1]])
		follow := ""
		if loc[2] >= 0 && loc[3] >= 0 {
			follow = strings.ToLower(strings.TrimSpace(prompt[loc[2]:loc[3]]))
		}
		if isGenericCommitObject(follow) {
			return full, true
		}
	}
	return "", false
}

func isGenericCommitObject(follow string) bool {
	if follow == "" {
		return true
	}
	first := strings.Fields(follow)[0]
	switch first {
	case "yet", "it", "them", "this", "these", "anything", "changes", "edits", "work", "here", "now", "and", "or", "until", "before", "in", "on", "at", "after":
		return true
	case "the", "your", "all":
		fields := strings.Fields(follow)
		if len(fields) < 2 {
			return true
		}
		switch fields[1] {
		case "change", "changes", "edit", "edits", "work", "result", "results", "diff", "files":
			return true
		}
		return false
	}
	return false
}

var (
	reGoTestC      = regexp.MustCompile(`\bgo\s+test\b[^\n;&|]*?-C\s+([^\s` + "`" + `"'();,]+)\s+([^\s` + "`" + `"'();,]+)`)
	reCdGoTest     = regexp.MustCompile(`\bcd\s+([^\s` + "`" + `"'();,&|]+)\s*&&\s*go\s+test\b[^\n;&|]*?\s(\./[^\s` + "`" + `"'();,]*)`)
	reGoTestDirect = regexp.MustCompile(`\bgo\s+test\b[^\n;&|]*?\s(\./[^\s` + "`" + `"'();,]+)`)
	reScriptTest   = regexp.MustCompile(`\b(?:python3|python|pytest|sh|bash)\b[^\n;&|]*?\s([^\s` + "`" + `"'();,]+\.(?:py|sh))`)
	reGitRm        = regexp.MustCompile(`\b(?:git\s+rm|rm)\b(?:\s+-[A-Za-z]+)*\s+([^\s` + "`" + `"'();,]+)`)
	reMarkdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
)

func parsePromptTargets(root, prompt string) (writeTargets, checkTargets []string) {
	seenWrite := map[string]bool{}
	seenCheck := map[string]bool{}
	addWrite := func(p string) {
		p = normalizeRepoPath(p)
		if p != "" && !seenWrite[p] {
			seenWrite[p] = true
			writeTargets = append(writeTargets, p)
		}
	}
	addCheck := func(p string) {
		p = normalizeRepoPath(p)
		if p != "" && !seenCheck[p] {
			seenCheck[p] = true
			checkTargets = append(checkTargets, p)
		}
	}

	// Pass 1: extract explicit test/shell commands.
	for _, m := range reGoTestC.FindAllStringSubmatch(prompt, -1) {
		dir := normalizeRepoPath(m[1])
		pkg := strings.TrimSuffix(strings.TrimPrefix(m[2], "./"), "/...")
		if pkg == "." || pkg == "..." {
			pkg = ""
		}
		if dir != "" && !strings.HasPrefix(m[2], "-") {
			joined := dir
			if pkg != "" {
				joined = path.Clean(dir + "/" + pkg)
			}
			addCheck(joined)
		}
	}
	for _, m := range reCdGoTest.FindAllStringSubmatch(prompt, -1) {
		dir := normalizeRepoPath(m[1])
		pkg := strings.TrimSuffix(strings.TrimPrefix(m[2], "./"), "/...")
		if pkg == "." || pkg == "..." {
			pkg = ""
		}
		if dir != "" {
			joined := dir
			if pkg != "" {
				joined = path.Clean(dir + "/" + pkg)
			}
			addCheck(joined)
		}
	}
	// Only extract direct `go test ./pkg` when not part of `cd <dir> && go test` or `go test -C <dir>`.
	for _, line := range strings.Split(prompt, "\n") {
		if reGoTestC.MatchString(line) || reCdGoTest.MatchString(line) {
			continue
		}
		for _, m := range reGoTestDirect.FindAllStringSubmatch(line, -1) {
			pkg := strings.TrimSuffix(strings.TrimPrefix(m[1], "./"), "/...")
			if pkg != "" && pkg != "." && pkg != "..." {
				addCheck(pkg)
			}
		}
	}
	for _, m := range reScriptTest.FindAllStringSubmatch(prompt, -1) {
		if p, ok := extractRepoPath(root, m[1]); ok {
			addCheck(p)
		}
	}
	for _, m := range reGitRm.FindAllStringSubmatch(prompt, -1) {
		if p, ok := extractRepoPath(root, m[1]); ok {
			addWrite(p)
		}
	}

	// Pass 2: clause-level scan for write verbs, check verbs, and passive voice.
	const (
		modeNone = iota
		modeWrite
		modeCheck
	)
	mode := modeNone
	writeVerb := ""
	seenPathInMode := false
	listCarry := false

	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			mode = modeNone
			writeVerb = ""
			seenPathInMode = false
			listCarry = false
			continue
		}
		// Replace markdown links `[label](path)` with `path` so links parse cleanly.
		line = reMarkdownLink.ReplaceAllString(line, " $1 ")
		isBullet := strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
		if !listCarry || !isBullet {
			if !listCarry {
				mode = modeNone
				writeVerb = ""
				seenPathInMode = false
			} else if !isBullet {
				mode = modeNone
				writeVerb = ""
				seenPathInMode = false
				listCarry = false
			}
		}

		tokens := strings.Fields(line)
		negated := false
		prevWord := ""
		lastUnassignedPath := ""
		wordsSinceUnassigned := 0
		passiveAux := false

		for _, rawTok := range tokens {
			if rawTok == "-" || rawTok == "*" || rawTok == "&&" || rawTok == "|" {
				if rawTok == "&&" || rawTok == "|" {
					mode = modeNone
					writeVerb = ""
					seenPathInMode = false
					negated = false
				}
				continue
			}

			endsSentence := hasTrailingSentencePunct(rawTok)
			endsColon := strings.HasSuffix(rawTok, ":")
			lowerWord := strings.ToLower(strings.Trim(rawTok, "`\"'()[]{}.,:;!?"))

			// Check for repository path token first.
			if p, ok := extractRepoPath(root, rawTok); ok {
				switch mode {
				case modeWrite:
					addWrite(p)
					seenPathInMode = true
				case modeCheck:
					addCheck(p)
					seenPathInMode = true
				default:
					lastUnassignedPath = p
					wordsSinceUnassigned = 0
					passiveAux = false
				}
				prevWord = ""
				if endsSentence {
					mode = modeNone
					writeVerb = ""
					seenPathInMode = false
					negated = false
					lastUnassignedPath = ""
				}
				continue
			}

			if lastUnassignedPath != "" {
				wordsSinceUnassigned++
				if wordsSinceUnassigned > 5 {
					lastUnassignedPath = ""
					passiveAux = false
				}
			}

			// Handle passive voice: "<path> must be deleted", "<path> must pass".
			if lastUnassignedPath != "" {
				if lowerWord == "must" || lowerWord == "should" || lowerWord == "needs" || lowerWord == "need" || lowerWord == "to" || lowerWord == "be" || lowerWord == "is" || lowerWord == "are" {
					passiveAux = true
				} else if passiveAux {
					if _, isWrite := writeVerbs[lowerWord]; isWrite && !negated {
						addWrite(lastUnassignedPath)
						lastUnassignedPath = ""
						passiveAux = false
					} else if isCheckVerb(lowerWord) && !negated {
						addCheck(lastUnassignedPath)
						lastUnassignedPath = ""
						passiveAux = false
					}
				}
			}

			// Negation tracking.
			if lowerWord == "not" || lowerWord == "don't" || lowerWord == "dont" || lowerWord == "never" || lowerWord == "without" || lowerWord == "cannot" || lowerWord == "can't" {
				negated = true
				prevWord = lowerWord
				continue
			}

			// Check for write verb.
			if base, ok := writeVerbs[lowerWord]; ok {
				if negated {
					mode = modeNone
					writeVerb = ""
					seenPathInMode = false
					negated = false
				} else {
					mode = modeWrite
					writeVerb = base
					seenPathInMode = false
				}
				prevWord = lowerWord
				if endsColon {
					listCarry = true
				}
				if endsSentence {
					mode = modeNone
					writeVerb = ""
					seenPathInMode = false
				}
				continue
			}

			// Check for check verb.
			if isStrongCheckVerb(lowerWord) || (isWeakCheckVerb(lowerWord) && mode == modeNone && !isDeterminer(prevWord)) {
				if negated {
					mode = modeNone
					seenPathInMode = false
					negated = false
				} else {
					mode = modeCheck
					writeVerb = ""
					seenPathInMode = false
				}
				prevWord = lowerWord
				if endsColon {
					listCarry = true
				}
				if endsSentence {
					mode = modeNone
					seenPathInMode = false
				}
				continue
			}

			// In modeCheck, a bare gate check ID like `go-affected-test` resolves to `<checks_dir>/<id>.json` if present.
			if mode == modeCheck && root != "" && lowerWord != "" && !strings.Contains(lowerWord, "/") {
				checksDir := settings.DefaultChecksDir
				if s, err := settings.Read(root); err == nil {
					checksDir = s.ChecksDir()
				}
				checkJSON := path.Join(checksDir, lowerWord+".json")
				if st, err := os.Stat(filepath.Join(root, checkJSON)); err == nil && !st.IsDir() {
					addCheck(checkJSON)
					seenPathInMode = true
				}
			}

			// Read/reference verbs end write/check mode.
			if isReadVerb(lowerWord) {
				mode = modeNone
				writeVerb = ""
				seenPathInMode = false
				negated = false
			} else if mode == modeWrite && seenPathInMode && endsWriteTargetList(lowerWord, writeVerb) {
				mode = modeNone
				writeVerb = ""
				seenPathInMode = false
			}

			if lowerWord != "do" && lowerWord != "must" && lowerWord != "should" {
				negated = false
			}
			prevWord = lowerWord
			if endsColon && mode != modeNone {
				listCarry = true
			}
			if endsSentence {
				mode = modeNone
				writeVerb = ""
				seenPathInMode = false
				negated = false
				lastUnassignedPath = ""
			}
		}
	}
	return writeTargets, checkTargets
}

var writeVerbs = map[string]string{
	"edit": "edit", "edits": "edit", "edited": "edit", "editing": "edit",
	"modify": "modify", "modifies": "modify", "modified": "modify", "modifying": "modify",
	"update": "update", "updates": "update", "updated": "update", "updating": "update",
	"change": "change", "changes": "change", "changed": "change", "changing": "change",
	"delete": "delete", "deletes": "delete", "deleted": "delete", "deleting": "delete",
	"remove": "remove", "removes": "remove", "removed": "remove", "removing": "remove",
	"create": "create", "creates": "create", "created": "create", "creating": "create",
	"add": "add", "adds": "add", "added": "add", "adding": "add",
	"write": "write", "writes": "write", "written": "write", "writing": "write",
	"rewrite": "rewrite", "rewrites": "rewrite", "rewritten": "rewrite", "rewriting": "rewrite",
	"replace": "replace", "replaces": "replace", "replaced": "replace", "replacing": "replace",
	"patch": "patch", "patches": "patch", "patched": "patch", "patching": "patch",
	"fix": "fix", "fixes": "fix", "fixed": "fix", "fixing": "fix",
	"rename": "rename", "renames": "rename", "renamed": "rename", "renaming": "rename",
	"move": "move", "moves": "move", "moved": "move", "moving": "move",
	"drop": "drop", "drops": "drop", "dropped": "drop", "dropping": "drop",
	"refactor": "refactor", "refactors": "refactor", "refactored": "refactor", "refactoring": "refactor",
	"adjust": "adjust", "adjusts": "adjust", "adjusted": "adjust", "adjusting": "adjust",
	"adapt": "adapt", "adapts": "adapt", "adapted": "adapt", "adapting": "adapt",
	"migrate": "migrate", "migrates": "migrate", "migrated": "migrate", "migrating": "migrate",
	"regenerate": "regenerate", "regenerates": "regenerate", "regenerated": "regenerate", "regenerating": "regenerate",
	"touch": "touch", "touches": "touch", "touched": "touch", "touching": "touch",
}

func isStrongCheckVerb(w string) bool {
	switch w {
	case "pass", "passes", "passing", "run", "running", "verify", "verifying", "ensure", "ensuring", "confirm", "confirming", "execute", "executing":
		return true
	}
	return false
}

func isWeakCheckVerb(w string) bool {
	switch w {
	case "test", "testing", "check", "checking":
		return true
	}
	return false
}

func isCheckVerb(w string) bool {
	return isStrongCheckVerb(w) || isWeakCheckVerb(w)
}

func isReadVerb(w string) bool {
	switch w {
	case "read", "reading", "see", "inspect", "inspecting", "compare", "comparing", "follow", "following", "consult", "consulting", "reference", "referencing", "match", "matching":
		return true
	}
	return false
}

func isDeterminer(w string) bool {
	switch w {
	case "the", "a", "an", "this", "that", "these", "those", "each", "every", "its", "their", "our", "your":
		return true
	}
	return false
}

func endsWriteTargetList(w, verb string) bool {
	switch w {
	case "to":
		return verb != "rename" && verb != "move" && verb != "add"
	case "from":
		return verb != "remove" && verb != "delete" && verb != "drop"
	case "using", "per", "in", "against", "like", "based", "according", "instead", "rather", "while", "after", "before", "so", "because", "since", "that", "which", "where", "when", "if", "with":
		return true
	}
	return false
}

func hasTrailingSentencePunct(rawTok string) bool {
	trimmed := strings.TrimRight(rawTok, "`\"')]}")
	if trimmed == "" {
		return false
	}
	last := trimmed[len(trimmed)-1]
	return last == '.' || last == ';' || last == '?' || last == '!'
}

var knownFileExts = map[string]bool{
	".go": true, ".py": true, ".json": true, ".md": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".sh": true, ".toml": true, ".yaml": true, ".yml": true,
	".txt": true, ".sql": true, ".css": true, ".html": true, ".mod": true, ".sum": true,
}

var knownTopDirs = map[string]bool{
	"tools": true, "tests": true, "docs": true, "config": true, "yolo-packs": true, "src": true, "pkg": true, "cmd": true, "internal": true,
}

func normalizeRepoPath(raw string) string {
	s := strings.Trim(raw, "`\"'()[]{}")
	s = strings.TrimRight(s, ".,:;!?")
	s = strings.TrimPrefix(s, "./")
	if s == "" || strings.Contains(s, "://") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "-") || strings.ContainsAny(s, " \t\r\n*?[]") {
		return ""
	}
	cleaned := path.Clean(s)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}

func extractRepoPath(root, raw string) (string, bool) {
	trimmed := strings.Trim(raw, "`\"'()[]{}")
	trimmed = strings.TrimRight(trimmed, ".,:;!?")
	hasTrailingSlash := strings.HasSuffix(trimmed, "/")
	cleaned := normalizeRepoPath(raw)
	if cleaned == "" {
		return "", false
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
		for _, r := range seg {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == '@') {
				return "", false
			}
		}
	}
	if !strings.Contains(cleaned, "/") {
		// Allow root-level files only when they exist in root.
		if root != "" {
			if st, err := os.Stat(filepath.Join(root, cleaned)); err == nil && !st.IsDir() {
				return cleaned, true
			}
		}
		return "", false
	}
	if root != "" {
		if _, err := os.Stat(filepath.Join(root, cleaned)); err == nil {
			return cleaned, true
		}
	}
	ext := strings.ToLower(path.Ext(cleaned))
	if knownFileExts[ext] {
		return cleaned, true
	}
	first, _, _ := strings.Cut(cleaned, "/")
	if hasTrailingSlash && knownTopDirs[first] {
		return cleaned, true
	}
	if strings.Count(cleaned, "/") >= 2 && knownTopDirs[first] {
		return cleaned, true
	}
	return "", false
}

func collectChangedFilePaths(root string, writeTargets, writeScope []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = path.Clean(p)
		if p == "" || p == "." || seen[p] {
			return
		}
		// Skip directories; we match specific files a check reads.
		if strings.HasSuffix(p, "/") {
			return
		}
		if st, err := os.Stat(filepath.Join(root, p)); err == nil && st.IsDir() {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range writeTargets {
		add(p)
	}
	for _, p := range writeScope {
		if !strings.HasSuffix(p, "/") {
			add(p)
		}
	}
	return out
}

func checkReadsChangedPath(root, checkPath string, cleanedScope, changedPaths []string) (string, string, bool) {
	full := filepath.Join(root, checkPath)
	st, err := os.Stat(full)
	if err != nil {
		return "", "", false
	}
	if !st.IsDir() {
		if changed, ok := singleCheckFileReadsChanged(root, checkPath, changedPaths); ok {
			return checkPath, changed, true
		}
		return "", "", false
	}
	// Directory check (e.g. `go test -C tools/tillr-gate ./goaffected`): inspect test files in the directory.
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isTestFileName(name) {
			continue
		}
		rel := path.Join(checkPath, name)
		if InsideFence(rel, cleanedScope) {
			continue
		}
		if changed, ok := singleCheckFileReadsChanged(root, rel, changedPaths); ok {
			return rel, changed, true
		}
	}
	// Also walk subdirectories if no direct test file matched (for recursive package checks).
	var foundRef, foundChanged string
	_ = filepath.WalkDir(full, func(walkPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || foundRef != "" || d.IsDir() {
			return nil
		}
		if filepath.Dir(walkPath) == full || !isTestFileName(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, walkPath)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if InsideFence(rel, cleanedScope) {
			return nil
		}
		if changed, ok := singleCheckFileReadsChanged(root, rel, changedPaths); ok {
			foundRef = rel
			foundChanged = changed
			return fs.SkipAll
		}
		return nil
	})
	if foundRef != "" {
		return foundRef, foundChanged, true
	}
	return "", "", false
}

func isTestFileName(name string) bool {
	return strings.HasSuffix(name, "_test.go") ||
		strings.HasSuffix(name, ".test.py") ||
		strings.HasSuffix(name, "_test.py") ||
		(strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py")) ||
		strings.HasSuffix(name, ".test.ts") ||
		strings.HasSuffix(name, "_test.ts") ||
		strings.HasSuffix(name, ".test.js") ||
		strings.HasSuffix(name, "_test.js")
}

func singleCheckFileReadsChanged(root, checkRel string, changedPaths []string) (string, bool) {
	raw, err := readBoundedFile(filepath.Join(root, checkRel), maxCheckFileBytes)
	if err != nil {
		return "", false
	}
	content := string(raw)

	// If this is a gate check JSON declaration, check its exact `reads` entries and its command target.
	if strings.HasSuffix(checkRel, ".json") {
		var decl struct {
			Reads   []string `json:"reads"`
			Command []string `json:"command"`
		}
		if err := json.Unmarshal(raw, &decl); err == nil && (len(decl.Reads) > 0 || len(decl.Command) > 0) {
			for _, r := range decl.Reads {
				cleanRead := path.Clean(r)
				if strings.HasSuffix(r, "/") {
					continue
				}
				for _, changed := range changedPaths {
					if cleanRead == changed {
						return changed, true
					}
				}
			}
			for _, arg := range decl.Command {
				if p, ok := extractRepoPath(root, arg); ok && p != checkRel {
					if changed, ok := singleCheckFileReadsChanged(root, p, changedPaths); ok {
						return changed, true
					}
				}
			}
		}
	}

	for _, changed := range changedPaths {
		if fileReadsTarget(content, checkRel, changed) {
			return changed, true
		}
	}
	return "", false
}

func readBoundedFile(full string, limit int64) ([]byte, error) {
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}

func fileReadsTarget(content, checkRel, targetPath string) bool {
	if strings.Contains(content, targetPath) {
		return true
	}
	if matchJoinedSegments(content, strings.Split(targetPath, "/")) {
		return true
	}
	// Check relative path from the check file's directory (when target is under it).
	rel, err := filepath.Rel(path.Dir(checkRel), targetPath)
	if err == nil {
		rel = filepath.ToSlash(rel)
		if rel != "." && !strings.HasPrefix(rel, "../") {
			if strings.Contains(content, rel) {
				return true
			}
			if strings.Contains(rel, "/") && matchJoinedSegments(content, strings.Split(rel, "/")) {
				return true
			}
		}
	}
	// Check Python module import of a Python target file.
	if strings.HasSuffix(checkRel, ".py") && strings.HasSuffix(targetPath, ".py") {
		mod := strings.TrimSuffix(path.Base(targetPath), ".py")
		if mod != "" && mod != "__init__" {
			re := regexp.MustCompile(`(?m)^\s*(?:import|from)\s+(?:[A-Za-z0-9_.]*\.)?` + regexp.QuoteMeta(mod) + `\b`)
			if re.MatchString(content) {
				return true
			}
		}
	}
	return false
}

// matchJoinedSegments matches path-join expressions such as
// `ROOT / "tools" / "gate" / "go_affected.py"` or
// `filepath.Join(root, "tools", "gate", "checks", "go-affected.json")`.
func matchJoinedSegments(content string, segs []string) bool {
	if len(segs) < 2 {
		return false
	}
	parts := make([]string, len(segs))
	for i, seg := range segs {
		parts[i] = `["']` + regexp.QuoteMeta(seg) + `["']`
	}
	re := regexp.MustCompile(strings.Join(parts, `\s*[,/]\s*`))
	return re.MatchString(content)
}
