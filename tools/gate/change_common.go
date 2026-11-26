package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/checkresult"
)

const (
	tsSizeWarnLines         = 2000
	tsSizeRefuseLines       = 3000
	unlandedMarker          = "[unlanded]"
	documentMemoryFmt       = "documents-1"
	watchMainDoc            = "docs/design/tillr-watch.md"
	watchRecordsPrefix      = "docs/design/tillr-watch/"
	watchDecisionRecordsDoc = "docs/design/tillr-watch/decision-record-files.md"
)

var (
	upstreamRefRe   = regexp.MustCompile(`^refs/(?:heads/patch/|published/patch/|remotes/[^/]+/patch/|remotes/upstream/|tags/)`)
	hunkStartRe     = regexp.MustCompile(`\+(\d+)`)
	wordRe          = regexp.MustCompile(`[^\s()<>\[\]{}"'` + "`" + `|,*]+`)
	watchHTMLTagRe  = regexp.MustCompile(`(?i)<a\s+[^>]*>`)
	watchHTMLAttrRe = regexp.MustCompile(`(?i)\s(?:id|name)\s*=\s*(?:"([^"]+)"|'([^']+)')`)
	watchVantageQRe = regexp.MustCompile(`<!--\s*vantage:\s*question\s+id=([^\s]+)`)
	linkPatterns    = []*regexp.Regexp{
		regexp.MustCompile(`\]\(\s*<([^>\n]*)>`),
		regexp.MustCompile(`\]\(\s*([^)\s]+)`),
		regexp.MustCompile(`(?m)^ {0,3}\[[^\]\n]+\]:\s*<?([^\s>]+)`),
		regexp.MustCompile(`(?i)(?:href|src)\s*=\s*["']([^"'\n]+)["']`),
	}
)

var documentCheckFiles = []string{
	"tools/gate/checks/documents.json",
	"tools/gate/change_common.go",
	"tools/gate/roadmap_sections.go",
	"tools/gate/main.go",
	"tools/gate/repo-gate",
}

var documentConfigNames = map[string]bool{
	".vantage.toml":  true,
	".vantageignore": true,
}

func gitRun(root string, check bool, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if check && err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "git could not read the change"
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

func validateBaseHead(root, base, head string) error {
	for _, pair := range []struct{ label, rev string }{{"base", base}, {"head", head}} {
		if pair.rev == "" {
			return fmt.Errorf("missing %s argument", pair.label)
		}
		if _, err := gitRun(root, true, "rev-parse", "--verify", pair.rev+"^{commit}"); err != nil {
			return err
		}
	}
	cmd := exec.Command("git", "-C", root, "merge-base", "--is-ancestor", base, head)
	if err := cmd.Run(); err != nil {
		return errors.New("base is not an ancestor of head")
	}
	return nil
}

func addedMarkdownLines(root, base, head string, paths []string) (map[string][]int, error) {
	args := append([]string{"diff", "--unified=0", "--no-renames", "--no-color", base, head, "--"}, paths...)
	diff, err := gitRun(root, true, args...)
	if err != nil {
		return nil, err
	}
	added := make(map[string][]int)
	currentPath := ""
	hasPath := false
	number := 0
	for _, line := range splitLines(diff) {
		if strings.HasPrefix(line, "+++ b/") {
			currentPath = line[len("+++ b/"):]
			hasPath = true
			if _, ok := added[currentPath]; !ok {
				added[currentPath] = nil
			}
		} else if strings.HasPrefix(line, "@@") {
			if m := hunkStartRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				number = n
			} else {
				number = 0
			}
		} else if hasPath && strings.HasPrefix(line, "+") {
			added[currentPath] = append(added[currentPath], number)
			number++
		}
	}
	return added, nil
}

func isHexOrAlnum(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isLowerHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
}

func findSHACitations(line string) []string {
	var out []string
	i := 0
	for i < len(line) {
		if !isLowerHex(line[i]) {
			i++
			continue
		}
		j := i
		for j < len(line) && isLowerHex(line[j]) {
			j++
		}
		length := j - i
		leftOK := i == 0 || !isHexOrAlnum(line[i-1])
		rightOK := j == len(line) || !isHexOrAlnum(line[j])
		if length >= 7 && length <= 40 && leftOK && rightOK {
			out = append(out, line[i:j])
		}
		i = j
	}
	return out
}

func reachableFromUpstream(root, commit string) bool {
	refs, _ := gitRun(root, false, "for-each-ref", "--contains", commit, "--format=%(refname)")
	for _, name := range splitLines(refs) {
		if upstreamRefRe.MatchString(name) {
			return true
		}
	}
	return false
}

func unreachableCitations(root, base, head string, paths []string) ([]string, error) {
	if base == "" || len(paths) == 0 {
		return nil, nil
	}
	added, err := addedMarkdownLines(root, base, head, paths)
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]string)
	resolvedKnown := make(map[string]bool)
	prior := make(map[string]string)
	var findings []string

	sortedPaths := make([]string, 0, len(added))
	for p := range added {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, p := range sortedPaths {
		shownHead, err := gitRun(root, false, "show", head+":"+p)
		if err != nil || shownHead == "" {
			continue
		}
		lines := splitLines(shownHead)
		if _, ok := prior[p]; !ok {
			cmd := exec.Command("git", "-C", root, "show", base+":"+p)
			out, err := cmd.Output()
			if err == nil {
				prior[p] = string(out)
			} else {
				prior[p] = ""
			}
		}
		inside := make([]bool, len(lines))
		fenced := false
		for i, l := range lines {
			inside[i] = fenced
			trimmed := strings.TrimLeft(l, " \t\v\f\r")
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fenced = !fenced
			}
		}
		for _, number := range added[p] {
			if number < 1 || number > len(lines) {
				continue
			}
			line := lines[number-1]
			if inside[number-1] || strings.Contains(line, unlandedMarker) {
				continue
			}
			for _, token := range findSHACitations(line) {
				if strings.Contains(prior[p], token) {
					continue
				}
				if !resolvedKnown[token] {
					resolvedKnown[token] = true
					cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", token+"^{commit}")
					if out, err := cmd.Output(); err == nil {
						resolved[token] = strings.TrimSpace(string(out))
					}
				}
				commit := resolved[token]
				if commit == "" {
					continue
				}
				anc := exec.Command("git", "-C", root, "merge-base", "--is-ancestor", commit, head)
				if anc.Run() == nil {
					continue
				}
				if reachableFromUpstream(root, commit) {
					continue
				}
				findings = append(findings, fmt.Sprintf(
					"%s:%d: %s: resolves to a commit that is not an ancestor of HEAD and is not reachable from an upstream ref; mark the line %s if that is intended -- %s",
					p, number, token, unlandedMarker, strings.TrimSpace(line),
				))
			}
		}
	}
	return findings, nil
}

func formatCommaInt(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	rem := len(s) % 3
	if rem > 0 {
		out = append(out, s[:rem]...)
	}
	for i := rem; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}

func trackedTypeScriptLines(root, head string) []struct {
	path  string
	lines int
} {
	counted, _ := gitRun(root, false, "grep", "-c", "", head, "--", "*.ts")
	type item struct {
		path  string
		lines int
	}
	var sizes []item
	prefix := head + ":"
	for _, line := range splitLines(counted) {
		idx := strings.LastIndex(line, ":")
		if idx < 0 {
			continue
		}
		p := line[:idx]
		n, err := strconv.Atoi(line[idx+1:])
		if err != nil {
			continue
		}
		p = strings.TrimPrefix(p, prefix)
		sizes = append(sizes, item{path: p, lines: n})
	}
	sort.Slice(sizes, func(i, j int) bool {
		if sizes[i].lines != sizes[j].lines {
			return sizes[i].lines > sizes[j].lines
		}
		return sizes[i].path < sizes[j].path
	})
	var res []struct {
		path  string
		lines int
	}
	for _, s := range sizes {
		res = append(res, struct {
			path  string
			lines int
		}{s.path, s.lines})
	}
	return res
}

func addedTypeScript(root, base, head string) (map[string]bool, error) {
	diff, err := gitRun(root, true, "diff", "--name-only", "--no-renames", "--diff-filter=A", "-z", base, head, "--", "*.ts")
	if err != nil {
		return nil, err
	}
	added := make(map[string]bool)
	for _, p := range strings.Split(diff, "\x00") {
		if p != "" {
			added[p] = true
		}
	}
	return added, nil
}

func typeScriptSizeFindings(root, base, head string) ([]string, []string, error) {
	sizes := trackedTypeScriptLines(root, head)
	var added map[string]bool
	var err error
	if base != "" {
		added, err = addedTypeScript(root, base, head)
		if err != nil {
			return nil, nil, err
		}
	}
	var warnings, refusals []string
	for _, s := range sizes {
		if s.lines > tsSizeRefuseLines && added[s.path] {
			refusals = append(refusals, fmt.Sprintf(
				"%s: %s lines added by this change, over the %s-line budget for a new .ts file -- split it",
				s.path, formatCommaInt(s.lines), formatCommaInt(tsSizeRefuseLines),
			))
		} else if s.lines > tsSizeWarnLines {
			warnings = append(warnings, fmt.Sprintf(
				"%s: %s lines, over the %s-line budget for a .ts file",
				s.path, formatCommaInt(s.lines), formatCommaInt(tsSizeWarnLines),
			))
		}
	}
	if base == "" {
		warnings = append(warnings, fmt.Sprintf(
			"no diff base, so no .ts file can be called added by this change and the %s-line refusal was not judged",
			formatCommaInt(tsSizeRefuseLines),
		))
	}
	return warnings, refusals, nil
}

func identityFindings(root, base, head string) ([]string, error) {
	for _, key := range []string{"user.name", "user.email"} {
		cmd := exec.Command("git", "config", "--show-scope", "--get-all", key)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = "cannot read deployed identity"
				}
				return nil, errors.New(msg)
			}
		}
		var values []string
		for _, line := range splitLines(stdout.String()) {
			scope, value, _ := strings.Cut(line, "\t")
			if scope == "local" || scope == "worktree" {
				label := scope
				if scope == "local" {
					label = "repo-local"
				}
				return []string{fmt.Sprintf("%s %s overrides the deployed identity; unset the %s key", label, key, scope)}, nil
			}
			values = append(values, strings.TrimSpace(value))
		}
		if len(values) == 0 || values[len(values)-1] == "" {
			return nil, fmt.Errorf("landing identity is not configured (%s)", key)
		}
	}

	policyPath := filepath.Join(root, "tools", "gate", "allowed-commit-identities.json")
	policyBytes, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, err
	}
	var allowedPairs [][]string
	if err := json.Unmarshal(policyBytes, &allowedPairs); err != nil {
		return nil, err
	}
	allowed := make(map[[2]string]bool, len(allowedPairs))
	for _, pair := range allowedPairs {
		if len(pair) == 2 {
			allowed[[2]string{pair[0], pair[1]}] = true
		}
	}

	logOut, err := gitRun(root, true, "log", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce", base+".."+head)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, line := range splitLines(logOut) {
		parts := strings.Split(line, "\x1f")
		if len(parts) != 5 {
			continue
		}
		sha, an, ae, cn, ce := parts[0], parts[1], parts[2], parts[3], parts[4]
		for _, role := range []struct {
			role, name, email string
		}{
			{"author", an, ae},
			{"committer", cn, ce},
		} {
			if !allowed[[2]string{role.name, role.email}] {
				findings = append(findings, fmt.Sprintf("%s: %s %s <%s> is not an allowed repository identity", sha, role.role, role.name, role.email))
			}
		}
	}
	return findings, nil
}

type treeEntry struct {
	mode string
	kind string
	obj  string
}

func treeEntries(root, head string) (map[string]treeEntry, error) {
	out, err := gitRun(root, true, "ls-tree", "-r", "-t", "-z", head)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]treeEntry)
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		meta, p, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			continue
		}
		entries[p] = treeEntry{mode: fields[0], kind: fields[1], obj: fields[2]}
	}
	return entries, nil
}

func namedInputs(text, docPath string, entries map[string]treeEntry) (map[string]string, map[string]bool) {
	here := path.Dir(docPath)
	if here == "." {
		here = ""
	}
	named := make(map[string]string)
	absent := make(map[string]bool)

	resolve := func(token string, linked bool) {
		if strings.Contains(token, "://") || strings.HasPrefix(token, "mailto:") || strings.HasPrefix(token, "#") {
			return
		}
		basePart, _, _ := strings.Cut(token, "#")
		basePart, _, _ = strings.Cut(basePart, "?")
		if unq, err := url.PathUnescape(basePart); err == nil {
			basePart = unq
		}
		basePart = strings.TrimRight(basePart, ".:,")
		basePart = strings.TrimRight(basePart, "/")
		if basePart == "" {
			return
		}
		candidates := []string{
			path.Clean(path.Join(here, basePart)),
			path.Clean(strings.TrimLeft(basePart, "/")),
		}
		for _, candidate := range candidates {
			if strings.HasPrefix(candidate, "..") || candidate == "." {
				continue
			}
			entry, exists := entries[candidate]
			if exists && (linked || entry.kind == "blob") {
				named[candidate] = entry.obj
			} else if linked && !exists {
				absent[candidate] = true
			}
		}
	}

	for _, pat := range linkPatterns {
		for _, m := range pat.FindAllStringSubmatch(text, -1) {
			resolve(strings.TrimSpace(m[1]), true)
		}
	}
	for _, tok := range wordRe.FindAllString(text, -1) {
		resolve(tok, false)
	}
	return named, absent
}

func trackedChanges(root string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	sort.Strings(paths)
	args := []string{"status", "--porcelain", "-z", "--no-renames", "--untracked-files=no", "--ignore-submodules=all", "--"}
	for _, p := range paths {
		args = append(args, ":(literal)"+p)
	}
	status, err := gitRun(root, true, args...)
	if err != nil {
		return nil, err
	}
	changed := make(map[string]bool)
	for _, entry := range strings.Split(status, "\x00") {
		if len(entry) > 3 {
			changed[entry[3:]] = true
		}
	}
	return changed, nil
}

func documentKeys(root, head string, documents []string) (map[string]string, map[string][]string, error) {
	entries, err := treeEntries(root, head)
	if err != nil {
		return nil, nil, err
	}
	configs := make(map[string]string)
	for p, e := range entries {
		if documentConfigNames[path.Base(p)] {
			configs[p] = e.obj
		}
	}
	roadmaps := make(map[string]string)
	for p := range configs {
		if path.Base(p) == ".vantage.toml" {
			content, err := gitRun(root, true, "show", head+":"+p)
			if err != nil {
				return nil, nil, err
			}
			if strings.Contains(content, "unrouted") {
				for rp, re := range entries {
					if path.Base(rp) == "roadmap.md" {
						roadmaps[rp] = re.obj
					}
				}
				break
			}
		}
	}
	code := make(map[string]string, len(documentCheckFiles))
	for _, p := range documentCheckFiles {
		if e, ok := entries[p]; ok {
			code[p] = e.obj
		} else {
			code[p] = "absent"
		}
	}

	keys := make(map[string]string, len(documents))
	watched := make(map[string]map[string]bool, len(documents))
	unsettled := make(map[string][]string)
	allWatched := make(map[string]bool)

	for _, docPath := range documents {
		entry := entries[docPath]
		here := path.Dir(docPath)
		if here == "." {
			here = ""
		}
		var siblings []string
		for p := range entries {
			d := path.Dir(p)
			if d == "." {
				d = ""
			}
			if d == here {
				siblings = append(siblings, p)
			}
		}
		sort.Strings(siblings)

		text, err := gitRun(root, true, "cat-file", "blob", entry.obj)
		if err != nil {
			return nil, nil, err
		}
		named, absent := namedInputs(text, docPath, entries)
		var symlinks []string
		for p := range named {
			if entries[p].mode == "120000" {
				symlinks = append(symlinks, p)
			}
		}
		sort.Strings(symlinks)
		if len(symlinks) > 0 {
			unsettled[docPath] = symlinks
		}

		identity := map[string]any{
			"format":   documentMemoryFmt,
			"check":    "documents",
			"code":     code,
			"configs":  configs,
			"roadmaps": roadmaps,
			"path":     docPath,
			"mode":     entry.mode,
			"blob":     entry.obj,
			"siblings": siblings,
			"named":    named,
		}
		raw, err := json.Marshal(identity)
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(raw)
		keys[docPath] = hex.EncodeToString(sum[:])

		w := make(map[string]bool)
		for p := range named {
			w[p] = true
		}
		for _, p := range siblings {
			w[p] = true
		}
		for p := range configs {
			w[p] = true
		}
		for p := range roadmaps {
			w[p] = true
		}
		watched[docPath] = w
		for p := range w {
			allWatched[p] = true
		}

		var ancestors []string
		step := here
		for {
			ancestors = append(ancestors, step)
			if step == "" {
				break
			}
			step = path.Dir(step)
			if step == "." {
				step = ""
			}
		}
		candidates := make(map[string]bool)
		for p := range absent {
			candidates[p] = true
		}
		for _, dir := range ancestors {
			for name := range documentConfigNames {
				candidates[path.Join(dir, name)] = true
			}
		}
		var onDisk []string
		for cand := range candidates {
			if _, inHead := entries[cand]; !inHead {
				if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(cand))); err == nil {
					onDisk = append(onDisk, cand)
				}
			}
		}
		sort.Strings(onDisk)
		if len(onDisk) > 0 {
			unsettled[docPath] = append(unsettled[docPath], onDisk...)
		}
	}

	allWatchedSlice := make([]string, 0, len(allWatched))
	for p := range allWatched {
		allWatchedSlice = append(allWatchedSlice, p)
	}
	changed, err := trackedChanges(root, allWatchedSlice)
	if err != nil {
		return nil, nil, err
	}
	for _, docPath := range documents {
		var differ []string
		for ch := range changed {
			for w := range watched[docPath] {
				if ch == w || strings.HasPrefix(ch, w+"/") {
					differ = append(differ, ch)
					break
				}
			}
		}
		sort.Strings(differ)
		if len(differ) > 0 {
			unsettled[docPath] = append(unsettled[docPath], differ...)
		}
	}
	return keys, unsettled, nil
}

func resultStoreDir(root string) (string, error) {
	common, err := gitRun(root, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSpace(common), "tillr-gate", "results"), nil
}

type storedResultRecord struct {
	CheckID    string `json:"check_id"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	Time       string `json:"time"`
	Tree       string `json:"tree"`
	Tool       string `json:"tool"`
}

func readStoredResult(store, key, verdict, checkID string) *storedResultRecord {
	data, err := os.ReadFile(filepath.Join(store, key+"."+verdict+".json"))
	if err != nil {
		return nil
	}
	var rec storedResultRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil
	}
	if rec.Result == "" || rec.Time == "" || !utf8.ValidString(rec.Result) {
		return nil
	}
	selectedID := checkID
	if selectedID == "" {
		if rec.CheckID != "" {
			selectedID = rec.CheckID
		} else {
			selectedID = "documents"
		}
	}
	answer := checkresult.Classify(checkresult.Observation{
		Check:  selectedID,
		Source: checkresult.CachedRecord,
		Word:   rec.Result,
	})
	if string(answer.Outcome) == verdict {
		return &rec
	}
	return nil
}

func writeStoredResult(store, key, verdict string, durationMS int64, tree, tool, checkID string) {
	if checkID == "" {
		checkID = "documents"
	}
	rec := storedResultRecord{
		CheckID:    checkID,
		Result:     verdict,
		DurationMS: durationMS,
		Time:       time.Now().UTC().Format(time.RFC3339Nano),
		Tree:       tree,
		Tool:       tool,
	}
	if err := os.MkdirAll(store, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "documents: could not remember a %s (%v); the verdict stands\n", verdict, err)
		return
	}
	f, err := os.CreateTemp(store, ".result-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "documents: could not remember a %s (%v); the verdict stands\n", verdict, err)
		return
	}
	tmpName := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	enc, err := json.Marshal(rec)
	if err != nil {
		_ = f.Close()
		fmt.Fprintf(os.Stderr, "documents: could not remember a %s (%v); the verdict stands\n", verdict, err)
		return
	}
	if _, err := f.Write(enc); err != nil {
		_ = f.Close()
		fmt.Fprintf(os.Stderr, "documents: could not remember a %s (%v); the verdict stands\n", verdict, err)
		return
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "documents: could not remember a %s (%v); the verdict stands\n", verdict, err)
		return
	}
	if err := os.Rename(tmpName, filepath.Join(store, key+"."+verdict+".json")); err != nil {
		fmt.Fprintf(os.Stderr, "documents: could not remember a %s (%v); the verdict stands\n", verdict, err)
		return
	}
	cleanup = false
}

func rendererVersion() string {
	cmd := exec.Command("uvx", "vantage-check", "version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	words := strings.Fields(string(out))
	if len(words) == 0 {
		return ""
	}
	return words[len(words)-1]
}

type vantageFinding struct {
	File     string `json:"file"`
	Line     any    `json:"line"`
	Column   any    `json:"column"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type vantageReport struct {
	Tool     string           `json:"tool"`
	Version  string           `json:"version"`
	Findings []vantageFinding `json:"findings"`
	Failures []map[string]any `json:"failures"`
}

func reportedPath(root, name string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		if rel, err := filepath.Rel(root, name); err == nil {
			name = rel
		}
	}
	return path.Clean(filepath.ToSlash(name))
}

func renderDocuments(root string, paths []string, locations map[string]roadmapLocation) (int, *vantageReport, int64) {
	started := time.Now()
	args := append([]string{"vantage-check", "check", "--format", "json"}, paths...)
	cmd := exec.Command("uvx", args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	durationMS := time.Since(started).Milliseconds()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 127
		}
	}

	var report *vantageReport
	var rawMap map[string]json.RawMessage
	if json.Unmarshal(stdout.Bytes(), &rawMap) == nil {
		if _, hasFindings := rawMap["findings"]; hasFindings {
			var parsed vantageReport
			if json.Unmarshal(stdout.Bytes(), &parsed) == nil && parsed.Findings != nil {
				report = &parsed
			}
		}
	}
	if report != nil {
		for _, f := range report.Findings {
			name := f.File
			lineVal := f.Line
			if loc, ok := locations[reportedPath(root, name)]; ok {
				name = loc.path
				switch v := lineVal.(type) {
				case float64:
					lineVal = int(v) + loc.lineOffset
				}
			}
			fmt.Printf("%s:%v:%v: %s %s %s\n", name, lineVal, f.Column, f.Severity, f.Rule, f.Message)
		}
		for _, failure := range report.Failures {
			b, _ := json.Marshal(failure)
			fmt.Printf("could not check: %s\n", string(b))
		}
	} else if strings.TrimSpace(stdout.String()) != "" {
		fmt.Print(stdout.String())
	}
	fmt.Fprint(os.Stderr, stderr.String())
	return code, report, durationMS
}

func judgeDocuments(root, head string, documents []string) (int, error) {
	keys, unsettled, err := documentKeys(root, head, documents)
	if err != nil {
		return 4, err
	}
	paths, locations, temporary, err := prepareRoadmapSections(root, head, documents, keys, unsettled)
	if err != nil {
		return 4, err
	}
	defer cleanupRoadmapSections(temporary)
	return judgeDocumentSet(root, head, paths, keys, unsettled, locations)
}

func judgeDocumentSet(root, head string, documents []string, keys map[string]string, unsettled map[string][]string, locations map[string]roadmapLocation) (int, error) {
	store, err := resultStoreDir(root)
	if err != nil {
		return 4, err
	}
	treeOut, err := gitRun(root, true, "rev-parse", head+"^{tree}")
	if err != nil {
		return 4, err
	}
	tree := strings.TrimSpace(treeOut)

	remembered := make(map[string]*storedResultRecord)
	for _, p := range documents {
		passed := readStoredResult(store, keys[p], "pass", "")
		if passed != nil && len(unsettled[p]) == 0 && readStoredResult(store, keys[p], "fail", "") == nil {
			remembered[p] = passed
		}
	}
	var pending []string
	for _, p := range documents {
		if _, ok := remembered[p]; !ok {
			pending = append(pending, p)
		}
	}

	var code int
	var report *vantageReport
	var durationMS int64
	var version string
	var stale []string

	if len(pending) > 0 {
		code, report, durationMS = renderDocuments(root, pending, locations)
		if report != nil {
			version = report.Version
		}
		for _, passed := range remembered {
			if version != "" && passed.Tool != version {
				stale = append(stale, passed.Tool)
			}
		}
	} else {
		version = rendererVersion()
		for _, passed := range remembered {
			if passed.Tool != version {
				stale = append(stale, passed.Tool)
			}
		}
	}

	if len(stale) > 0 {
		fmt.Fprintf(os.Stderr, "documents: %d remembered pass(es) are not from renderer %s; judging again\n", len(stale), version)
		pending = append([]string(nil), documents...)
		code, report, durationMS = renderDocuments(root, pending, locations)
		if report != nil {
			version = report.Version
		} else {
			version = ""
		}
	}

	pendingSet := make(map[string]bool, len(pending))
	for _, p := range pending {
		pendingSet[p] = true
	}
	for _, p := range documents {
		if !pendingSet[p] {
			label := p
			if loc, ok := locations[p]; ok {
				label = fmt.Sprintf("%s (section at line %d)", loc.path, loc.startLine)
			}
			fmt.Fprintf(os.Stderr, "documents: %s: remembered pass at %s\n", label, remembered[p].Time)
		}
	}
	if len(documents) > 1 || len(pending) < len(documents) {
		units := "document(s)"
		if len(locations) > 0 {
			units = "render unit(s)"
		}
		fmt.Fprintf(os.Stderr, "documents: %d %s: %d rendered, %d remembered\n", len(documents), units, len(pending), len(documents)-len(pending))
	}
	if len(pending) == 0 {
		return 0, nil
	}

	var findings []vantageFinding
	if report != nil {
		findings = report.Findings
	}
	answer := checkresult.Classify(checkresult.Observation{
		Check:      "documents",
		Source:     checkresult.DocumentRender,
		ExitCode:   &code,
		ReportRead: report != nil,
		HasFinding: len(findings) > 0,
	})
	if answer.Outcome != checkresult.Pass && answer.Outcome != checkresult.Fail {
		return 4, fmt.Errorf("uvx vantage-check exited %d without a document finding", code)
	}
	verdict := 0
	if answer.Outcome == checkresult.Fail {
		verdict = 1
	}
	if version == "" {
		return verdict, nil
	}

	unchecked := make(map[string]bool)
	if report != nil {
		for _, fail := range report.Failures {
			if fName, ok := fail["file"].(string); ok {
				if rp := reportedPath(root, fName); rp != "" {
					unchecked[rp] = true
				}
			}
		}
	}
	flagged := make(map[string]bool)
	errorsSet := make(map[string]bool)
	for _, f := range findings {
		rp := reportedPath(root, f.File)
		if rp != "" {
			flagged[rp] = true
			if f.Severity == checkresult.DetailError {
				errorsSet[rp] = true
			}
		}
	}
	if answer.Outcome == checkresult.Fail {
		allPending := len(errorsSet) > 0
		for e := range errorsSet {
			if !pendingSet[e] {
				allPending = false
				break
			}
		}
		if !allPending {
			fmt.Fprintln(os.Stderr, "documents: the run failed on findings it does not attribute to a document it was given; nothing is remembered from it")
			return verdict, nil
		}
	}
	for _, p := range pending {
		if unchecked[p] {
			continue
		}
		if errorsSet[p] {
			writeStoredResult(store, keys[p], "fail", durationMS, tree, version, "documents")
		} else if flagged[p] {
			continue
		} else if len(unsettled[p]) > 0 {
			fmt.Fprintf(os.Stderr, "documents: %s: pass not remembered; it read what differs on disk from the commit: %s\n", p, strings.Join(unsettled[p], ", "))
		} else {
			writeStoredResult(store, keys[p], "pass", durationMS, tree, version, "documents")
		}
	}
	return verdict, nil
}

func watchDTWAnchors(text string) map[string]bool {
	anchors := make(map[string]bool)
	inFence := false
	var fenceChar byte
	for _, line := range splitLines(text) {
		trimmed := strings.TrimLeft(line, " \t\v\f\r")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if !inFence {
				inFence = true
				fenceChar = trimmed[0]
			} else if trimmed[0] == fenceChar {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		if match := markdownHeadingRe.FindStringSubmatch(line); match != nil {
			slug := markdownSlug(match[1])
			if strings.HasPrefix(slug, "d-tw") {
				anchors[slug] = true
			}
		}
		for _, tag := range watchHTMLTagRe.FindAllString(line, -1) {
			for _, m := range watchHTMLAttrRe.FindAllStringSubmatch(tag, -1) {
				val := m[1]
				if val == "" {
					val = m[2]
				}
				lower := strings.ToLower(strings.TrimSpace(val))
				if strings.HasPrefix(lower, "d-tw") {
					anchors[lower] = true
				}
			}
		}
		for _, m := range watchVantageQRe.FindAllStringSubmatch(line, -1) {
			val := strings.TrimSuffix(m[1], "-->")
			val = strings.Trim(val, "\"'")
			lower := strings.ToLower(strings.TrimSpace(val))
			if strings.HasPrefix(lower, "d-tw") {
				anchors[lower] = true
			}
		}
	}
	return anchors
}

func watchRecordDependsOnMain(docPath, text string) bool {
	entries := frontmatterEntries(text)
	if len(entries) == 0 {
		return false
	}
	docDir := path.Dir(docPath)
	checkTarget := func(raw string) bool {
		target, _, _ := strings.Cut(strings.TrimSpace(raw), "#")
		target = strings.TrimSpace(target)
		if target == "" || strings.HasPrefix(target, "/") || strings.Contains(target, "://") {
			return false
		}
		return path.Clean(path.Join(docDir, target)) == watchMainDoc
	}
	for _, c := range frontmatterCitations(entries) {
		if c.field != "depends-on" {
			continue
		}
		val := strings.TrimSpace(c.value)
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			for _, part := range strings.Split(val[1:len(val)-1], ",") {
				candidate := strings.Trim(strings.TrimSpace(part), "\"'")
				if checkTarget(candidate) {
					return true
				}
			}
			continue
		}
		if checkTarget(val) {
			return true
		}
	}
	return false
}

func watchDecisionRecordFindings(root, base, head string) ([]string, error) {
	diffOut, err := gitRun(root, true, "diff", "--name-status", "--no-renames", "-z", base, head)
	if err != nil {
		return nil, err
	}
	var mainStatus string
	var addedRecords []string
	var modifiedOrDeletedExisting []string

	fields := strings.Split(diffOut, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status, p := fields[i], fields[i+1]
		if status == "" || p == "" || !strings.HasSuffix(p, ".md") {
			continue
		}
		excluded := false
		for _, part := range strings.Split(p, "/") {
			if part == ".forks" || part == "node_modules" || part == ".yolo" {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		isMain := p == watchMainDoc
		isRecord := strings.HasPrefix(p, watchRecordsPrefix)
		if !isMain && !isRecord {
			continue
		}
		if isMain {
			mainStatus = status
		}
		if isRecord && status == "A" {
			addedRecords = append(addedRecords, p)
		}
		if status == "M" || status == "T" || status == "D" {
			modifiedOrDeletedExisting = append(modifiedOrDeletedExisting, p)
		}
	}

	if mainStatus != "A" && mainStatus != "M" && mainStatus != "T" && len(addedRecords) == 0 {
		return nil, nil
	}

	sort.Strings(addedRecords)
	sort.Strings(modifiedOrDeletedExisting)

	var findings []string

	// Clause 1: new D-TW anchor introduced in docs/design/tillr-watch.md.
	if mainStatus == "A" || mainStatus == "M" || mainStatus == "T" {
		headText, err := gitRun(root, true, "show", head+":"+watchMainDoc)
		if err != nil {
			return nil, err
		}
		headAnchors := watchDTWAnchors(headText)
		var baseAnchors map[string]bool
		if mainStatus != "A" {
			baseText, err := gitRun(root, true, "show", base+":"+watchMainDoc)
			if err != nil {
				return nil, err
			}
			baseAnchors = watchDTWAnchors(baseText)
		}
		var newAnchors []string
		for anchor := range headAnchors {
			if !baseAnchors[anchor] {
				newAnchors = append(newAnchors, anchor)
			}
		}
		sort.Strings(newAnchors)
		for _, anchor := range newAnchors {
			findings = append(findings, fmt.Sprintf(
				"%s: introduces new D-TW anchor %s inline (clause 1); put each new tillr-watch decision record in its own file under docs/design/tillr-watch/<slug>.md without editing %s (see %s)",
				watchMainDoc, anchor, watchMainDoc, watchDecisionRecordsDoc,
			))
		}
	}

	// Clause 2: added record file combined with edits to main design or existing record file.
	if len(addedRecords) > 0 && len(modifiedOrDeletedExisting) > 0 {
		addedList := strings.Join(addedRecords, ", ")
		for _, existingPath := range modifiedOrDeletedExisting {
			findings = append(findings, fmt.Sprintf(
				"%s: modified or deleted alongside added record %s (clause 2); put each new tillr-watch decision record in its own file under docs/design/tillr-watch/<slug>.md without editing %s or existing files in %s (see %s)",
				existingPath, addedList, watchMainDoc, watchRecordsPrefix, watchDecisionRecordsDoc,
			))
		}
	}

	// Clause 3: added record file missing depends-on resolving to docs/design/tillr-watch.md.
	for _, addedPath := range addedRecords {
		text, err := gitRun(root, true, "show", head+":"+addedPath)
		if err != nil {
			return nil, err
		}
		if !watchRecordDependsOnMain(addedPath, text) {
			findings = append(findings, fmt.Sprintf(
				"%s: frontmatter depends-on does not resolve to %s (clause 3); put each new tillr-watch decision record in its own file under docs/design/tillr-watch/<slug>.md with frontmatter depends-on including ../tillr-watch.md (see %s)",
				addedPath, watchMainDoc, watchDecisionRecordsDoc,
			))
		}
	}

	return findings, nil
}

func runChangeCheck(kind string, args []string) int {
	failJudge := func(err error) int {
		fmt.Fprintf(os.Stderr, "%s: could not judge: %v\n", kind, err)
		return 4
	}
	if len(args) != 2 {
		return failJudge(errors.New("missing base and head arguments; supply BASE HEAD"))
	}
	base, head := args[0], args[1]
	root, err := os.Getwd()
	if err != nil {
		return failJudge(err)
	}
	if err := validateBaseHead(root, base, head); err != nil {
		return failJudge(err)
	}
	if kind == "commit-identity" {
		findings, err := identityFindings(root, base, head)
		if err != nil {
			return failJudge(err)
		}
		if len(findings) > 0 {
			fmt.Println(strings.Join(findings, "\n"))
			return 1
		}
		return 0
	}

	changedOut, err := gitRun(root, true, "diff", "--name-only", "--no-renames", "-z", base, head)
	if err != nil {
		return failJudge(err)
	}
	var documents []string
	for _, p := range strings.Split(changedOut, "\x00") {
		if p == "" || !strings.HasSuffix(p, ".md") {
			continue
		}
		excluded := false
		for _, part := range strings.Split(p, "/") {
			if part == ".forks" || part == "node_modules" || part == ".yolo" {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		lsOut, err := gitRun(root, true, "ls-tree", "--name-only", head, "--", p)
		if err != nil {
			return failJudge(err)
		}
		if strings.TrimSpace(lsOut) != "" {
			documents = append(documents, p)
		}
	}

	switch kind {
	case "citations":
		findings, err := unreachableCitations(root, base, head, documents)
		if err != nil {
			return failJudge(err)
		}
		if len(findings) > 0 {
			fmt.Println(strings.Join(findings, "\n"))
			return 1
		}
		return 0
	case "file-size":
		_, findings, err := typeScriptSizeFindings(root, base, head)
		if err != nil {
			return failJudge(err)
		}
		if len(findings) > 0 {
			fmt.Println(strings.Join(findings, "\n"))
			return 1
		}
		return 0
	case "documents":
		if len(documents) == 0 {
			return 0
		}
		headRev, err := gitRun(root, true, "rev-parse", "HEAD")
		if err != nil {
			return failJudge(err)
		}
		namedRev, err := gitRun(root, true, "rev-parse", head)
		if err != nil {
			return failJudge(err)
		}
		if strings.TrimSpace(headRev) != strings.TrimSpace(namedRev) {
			return failJudge(errors.New("documents require the named head to be checked out"))
		}
		dirtyArgs := append([]string{"status", "--porcelain", "--"}, documents...)
		dirtyOut, err := gitRun(root, true, dirtyArgs...)
		if err != nil {
			return failJudge(err)
		}
		if strings.TrimSpace(dirtyOut) != "" {
			return failJudge(errors.New("changed Markdown files are dirty"))
		}
		findings, err := watchDecisionRecordFindings(root, base, head)
		if err != nil {
			return failJudge(err)
		}
		if len(findings) > 0 {
			fmt.Println(strings.Join(findings, "\n"))
			return 1
		}
		code, err := judgeDocuments(root, head, documents)
		if err != nil {
			return failJudge(err)
		}
		return code
	default:
		return failJudge(fmt.Errorf("unknown change check %s", kind))
	}
}
