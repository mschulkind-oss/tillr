package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const timingDeclarationsPath = "tools/gate-timing-declarations.json"

var (
	deliveredPackTreesRe = regexp.MustCompile(`^yolo-packs/[^/]+/files/`)
	goDeltaDirectRe      = regexp.MustCompile(`time\.Since\([^)]*\)\s*(?:[<>]=?)`)
	goDeltaInlineRe      = regexp.MustCompile(`(\w+)\s*:=\s*time\.Since\([^)]*\)\s*;\s*\w+\s*(?:[<>]=?)`)
	goDeltaAssignRe      = regexp.MustCompile(`(\w+)\s*:?=\s*time\.Since\(`)
	jsDeltaDirectRe      = regexp.MustCompile(`(?:performance\.now\(\)|Date\.now\(\))\s*-\s*[\w.$]+\s*(?:[<>]=?)`)
	jsDeltaAssignRe      = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*(?:performance\.now\(\)|Date\.now\(\))\s*-\s*[\w.$]+`)
	pyDeltaDirectRe      = regexp.MustCompile(`time\.(?:monotonic|time|perf_counter)\(\)\s*-\s*[\w.]+\s*(?:[<>]=?)`)
	pyDeltaAssignRe      = regexp.MustCompile(`(\w+)\s*=\s*time\.(?:monotonic|time|perf_counter)\(\)\s*-\s*[\w.]+`)
)

const (
	undeclaredTimingPrefix = "a check asserts elapsed time, which the gate's timing rule forbids; assert a count, an order or an equality over what the product recorded, or declare it in tools/gate-timing-declarations.json with its reason and its repair: "
	shortDeadlinePrefix    = "a Go test waits with a short positive deadline (< 5m), which a loaded machine can miss; wait with hangGuard (5 minutes) instead of a short positive deadline, or declare it in tools/gate-timing-declarations.json with its reason and its repair: "
	staleTimingPrefix      = "a timing declaration no longer matches a hit, so it is a silencer rather than a record; remove it or fix the snippet: "
	hangGuardSeconds       = 5 * 60.0
)

var (
	allowedDurationNames = map[string]bool{
		"hangGuard":           true,
		"heartbeatStallBound": true,
	}
	goTimeUnits = map[string]float64{
		"Nanosecond":  1e-9,
		"Microsecond": 1e-6,
		"Millisecond": 1e-3,
		"Second":      1.0,
		"Minute":      60.0,
		"Hour":        3600.0,
	}
	goConstLineRe       = regexp.MustCompile(`^\s*const\s+(\w+)(?:\s+[\w.*\[\]]+)?\s*=\s*(.+)$`)
	goConstBlockStartRe = regexp.MustCompile(`^\s*const\s*\(\s*$`)
	goConstBlockEndRe   = regexp.MustCompile(`^\s*\)\s*$`)
	goConstBlockEntryRe = regexp.MustCompile(`^\s*(\w+)(?:\s+[\w.*\[\]]+)?\s*=\s*(.+)$`)
	goDirectAfterCaseRe = regexp.MustCompile(`^\s*case\s*<-\s*time\.After\(`)
	goAssignAfterRe     = regexp.MustCompile(`^\s*(\w+)\s*:?=\s*time\.After\(`)
	goAssignNowAddRe    = regexp.MustCompile(`^\s*(?:for\s+)?(\w+)\s*:?=\s*time\.Now\(\)\.Add\(`)
	goAssignTimerRe     = regexp.MustCompile(`^\s*(\w+)\s*:?=\s*time\.NewTimer\(`)
	goFailOrKillRe      = regexp.MustCompile(`\b(?:t\.(?:Fatal|Fatalf|Error|Errorf|Fail|FailNow)|panic|Process\.Kill)\s*\(`)
	goFuncDeclRe        = regexp.MustCompile(`^\s*func\b`)
)

type timingDeclaration struct {
	File    string `json:"file"`
	Snippet string `json:"snippet"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
	Repair  string `json:"repair"`
}

type timingHit struct {
	name    string
	number  int
	snippet string
}

func isTimingTestPath(name string) bool {
	if deliveredPackTreesRe.MatchString(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".forks" || part == "node_modules" || part == ".yolo" {
			return false
		}
	}
	base := path.Base(name)
	return strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py")
}

func matchGoInline(line string) bool {
	m := goDeltaInlineRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	// Verify the same variable is compared after ';'
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(m[1]) + `\s*:=\s*time\.Since\([^)]*\)\s*;\s*` + regexp.QuoteMeta(m[1]) + `\s*(?:[<>]=?)`)
	return re.MatchString(line)
}

func elapsedTimeHitsInText(name, text string) []timingHit {
	var directRe, assignRe *regexp.Regexp
	checkInline := false
	switch {
	case strings.HasSuffix(name, ".go"):
		directRe = goDeltaDirectRe
		assignRe = goDeltaAssignRe
		checkInline = true
	case strings.HasSuffix(name, ".mjs") || strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".js"):
		directRe = jsDeltaDirectRe
		assignRe = jsDeltaAssignRe
	case strings.HasSuffix(name, ".py"):
		directRe = pyDeltaDirectRe
		assignRe = pyDeltaAssignRe
	default:
		return nil
	}

	var hits []timingHit
	deltas := make(map[string]int)
	for i, line := range splitLines(text) {
		number := i + 1
		if directRe.MatchString(line) || (checkInline && matchGoInline(line)) {
			hits = append(hits, timingHit{name: name, number: number, snippet: strings.Join(strings.Fields(line), " ")})
			continue
		}
		if m := assignRe.FindStringSubmatch(line); m != nil {
			deltas[m[1]] = number
			continue
		}
		for v := range deltas {
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\b\s*(?:[<>]=?)`)
			if re.MatchString(line) {
				hits = append(hits, timingHit{name: name, number: number, snippet: strings.Join(strings.Fields(line), " ")})
				break
			}
		}
	}
	return hits
}

func stripGoLineComment(line string) string {
	inDouble := false
	inRaw := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inRaw {
			if ch == '`' {
				inRaw = false
			}
		} else if inDouble {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
		} else {
			if ch == '`' {
				inRaw = true
			} else if ch == '"' {
				inDouble = true
			} else if ch == '/' && i+1 < len(line) && line[i+1] == '/' {
				return line[:i]
			}
		}
	}
	return line
}

func extractBalancedParens(text string, openParenIdx int) (string, int, bool) {
	if openParenIdx < 0 || openParenIdx >= len(text) || text[openParenIdx] != '(' {
		return "", 0, false
	}
	depth := 0
	for idx := openParenIdx; idx < len(text); idx++ {
		switch text[idx] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[openParenIdx+1 : idx], idx + 1, true
			}
		}
	}
	return "", 0, false
}

func extractGoConsts(lines []string) map[string]string {
	consts := make(map[string]string)
	inBlock := false
	for _, raw := range lines {
		code := strings.TrimRight(stripGoLineComment(raw), " \t\r\n")
		if inBlock {
			if goConstBlockEndRe.MatchString(code) {
				inBlock = false
				continue
			}
			if m := goConstBlockEntryRe.FindStringSubmatch(code); m != nil {
				consts[m[1]] = strings.TrimSpace(m[2])
			}
		} else {
			if goConstBlockStartRe.MatchString(code) {
				inBlock = true
				continue
			}
			if m := goConstLineRe.FindStringSubmatch(code); m != nil {
				consts[m[1]] = strings.TrimSpace(m[2])
			}
		}
	}
	return consts
}

func resolveGoDurationSeconds(expr string, consts map[string]string, visiting map[string]bool) (float64, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" || allowedDurationNames[expr] {
		return 0, false
	}
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, false
	}
	var eval func(ast.Expr) (float64, bool)
	eval = func(e ast.Expr) (float64, bool) {
		switch n := e.(type) {
		case *ast.ParenExpr:
			return eval(n.X)
		case *ast.BasicLit:
			if n.Kind == token.INT || n.Kind == token.FLOAT {
				v, err := strconv.ParseFloat(strings.ReplaceAll(n.Value, "_", ""), 64)
				if err == nil {
					return v, true
				}
			}
			return 0, false
		case *ast.Ident:
			if allowedDurationNames[n.Name] || visiting[n.Name] {
				return 0, false
			}
			if cExpr, ok := consts[n.Name]; ok {
				next := make(map[string]bool, len(visiting)+1)
				for k, v := range visiting {
					next[k] = v
				}
				next[n.Name] = true
				return resolveGoDurationSeconds(cExpr, consts, next)
			}
			return 0, false
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "time" {
				if unit, ok := goTimeUnits[n.Sel.Name]; ok {
					return unit, true
				}
			}
			return 0, false
		case *ast.UnaryExpr:
			v, ok := eval(n.X)
			if !ok {
				return 0, false
			}
			switch n.Op {
			case token.ADD:
				return v, true
			case token.SUB:
				return -v, true
			}
			return 0, false
		case *ast.BinaryExpr:
			left, okL := eval(n.X)
			right, okR := eval(n.Y)
			if !okL || !okR {
				return 0, false
			}
			switch n.Op {
			case token.ADD:
				return left + right, true
			case token.SUB:
				return left - right, true
			case token.MUL:
				return left * right, true
			case token.QUO:
				if right != 0 {
					return left / right, true
				}
			}
			return 0, false
		}
		return 0, false
	}
	return eval(node)
}

func expandTabs4(s string) string {
	var b strings.Builder
	col := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			spaces := 4 - (col % 4)
			for k := 0; k < spaces; k++ {
				b.WriteByte(' ')
			}
			col += spaces
		} else {
			b.WriteByte(s[i])
			col++
		}
	}
	return b.String()
}

func indentWidth4(s string) int {
	trimmed := strings.TrimLeft(s, " \t")
	return len(expandTabs4(s)) - len(expandTabs4(trimmed))
}

func caseBodyFails(lines []string, caseIdx int, colonPos int) bool {
	caseLine := lines[caseIdx]
	caseIndent := indentWidth4(caseLine)
	inlineRest := strings.TrimSpace(stripGoLineComment(caseLine[colonPos+1:]))
	var bodyParts []string
	if inlineRest != "" {
		bodyParts = append(bodyParts, inlineRest)
	}
	for j := caseIdx + 1; j < len(lines); j++ {
		code := stripGoLineComment(lines[j])
		stripped := strings.TrimLeft(code, " \t")
		if strings.TrimSpace(stripped) == "" {
			continue
		}
		lineIndent := indentWidth4(code)
		if lineIndent <= caseIndent && (strings.HasPrefix(stripped, "case ") ||
			strings.HasPrefix(stripped, "case\t") ||
			strings.HasPrefix(stripped, "default:") ||
			strings.HasPrefix(stripped, "}")) {
			break
		}
		bodyParts = append(bodyParts, stripped)
	}
	if len(bodyParts) == 0 {
		return false
	}
	return goFailOrKillRe.MatchString(strings.Join(bodyParts, "\n"))
}

func varPassedToCall(code, v string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\b`)
	for _, loc := range re.FindAllStringIndex(code, -1) {
		start := loc[0]
		if start >= 2 && code[start-2:start] == "<-" {
			continue
		}
		prefix := code[:start]
		if strings.Count(prefix, "(") > strings.Count(prefix, ")") {
			return true
		}
	}
	return false
}

func shortDeadlineHitsInText(name, text string) []timingHit {
	if !strings.HasSuffix(name, "_test.go") {
		return nil
	}
	lines := splitLines(text)
	consts := extractGoConsts(lines)
	var hits []timingHit

	for idx, rawLine := range lines {
		code := stripGoLineComment(rawLine)
		snippet := strings.Join(strings.Fields(rawLine), " ")

		if loc := goDirectAfterCaseRe.FindStringIndex(code); loc != nil {
			if expr, afterIdx, ok := extractBalancedParens(code, loc[1]-1); ok {
				rest := strings.TrimLeft(code[afterIdx:], " \t")
				if strings.HasPrefix(rest, ":") {
					colonPos := afterIdx + strings.IndexByte(code[afterIdx:], ':')
					if dur, ok := resolveGoDurationSeconds(expr, consts, nil); ok && dur > 0 && dur < hangGuardSeconds {
						if caseBodyFails(lines, idx, colonPos) {
							hits = append(hits, timingHit{name: name, number: idx + 1, snippet: snippet})
						}
					}
				}
			}
			continue
		}

		if loc := goAssignAfterRe.FindStringSubmatchIndex(code); loc != nil {
			v := code[loc[2]:loc[3]]
			expr, _, ok := extractBalancedParens(code, loc[1]-1)
			if !ok {
				continue
			}
			dur, ok := resolveGoDurationSeconds(expr, consts, nil)
			if !ok || dur <= 0 || dur >= hangGuardSeconds {
				continue
			}
			caseRe := regexp.MustCompile(`^\s*case\s*<-\s*` + regexp.QuoteMeta(v) + `\s*:`)
			reassignRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\s*:?=`)
			for k := idx + 1; k < len(lines); k++ {
				codeK := stripGoLineComment(lines[k])
				if goFuncDeclRe.MatchString(codeK) || reassignRe.MatchString(codeK) {
					break
				}
				if caseRe.MatchString(codeK) {
					colonPos := strings.IndexByte(codeK, ':')
					if caseBodyFails(lines, k, colonPos) {
						hits = append(hits, timingHit{name: name, number: idx + 1, snippet: snippet})
						break
					}
				} else if varPassedToCall(codeK, v) {
					hits = append(hits, timingHit{name: name, number: idx + 1, snippet: snippet})
					break
				}
			}
			continue
		}

		if loc := goAssignNowAddRe.FindStringSubmatchIndex(code); loc != nil {
			v := code[loc[2]:loc[3]]
			expr, afterIdx, ok := extractBalancedParens(code, loc[1]-1)
			if !ok {
				continue
			}
			dur, ok := resolveGoDurationSeconds(expr, consts, nil)
			if !ok || dur <= 0 || dur >= hangGuardSeconds {
				continue
			}
			pollRe := regexp.MustCompile(`\btime\.(?:Now\(\)\.(?:After|Before)|Until)\(\s*` + regexp.QuoteMeta(v) + `\s*\)`)
			if pollRe.MatchString(code[afterIdx:]) {
				hits = append(hits, timingHit{name: name, number: idx + 1, snippet: snippet})
				continue
			}
			reassignRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\s*:?=`)
			for k := idx + 1; k < len(lines); k++ {
				codeK := stripGoLineComment(lines[k])
				if goFuncDeclRe.MatchString(codeK) || reassignRe.MatchString(codeK) {
					break
				}
				if pollRe.MatchString(codeK) {
					hits = append(hits, timingHit{name: name, number: idx + 1, snippet: snippet})
					break
				}
			}
			continue
		}

		if loc := goAssignTimerRe.FindStringSubmatchIndex(code); loc != nil {
			v := code[loc[2]:loc[3]]
			expr, _, ok := extractBalancedParens(code, loc[1]-1)
			if !ok {
				continue
			}
			dur, ok := resolveGoDurationSeconds(expr, consts, nil)
			if !ok || dur <= 0 || dur >= hangGuardSeconds {
				continue
			}
			caseRe := regexp.MustCompile(`^\s*case\s*<-\s*` + regexp.QuoteMeta(v) + `\.C\s*:`)
			reassignRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\s*:?=`)
			for k := idx + 1; k < len(lines); k++ {
				codeK := stripGoLineComment(lines[k])
				if goFuncDeclRe.MatchString(codeK) || reassignRe.MatchString(codeK) {
					break
				}
				if caseRe.MatchString(codeK) {
					colonPos := strings.IndexByte(codeK, ':')
					if caseBodyFails(lines, k, colonPos) {
						hits = append(hits, timingHit{name: name, number: idx + 1, snippet: snippet})
						break
					}
				}
			}
		}
	}
	return hits
}

func parseTimingDeclarations(raw []byte, label string) ([]timingDeclaration, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if len(top) != 1 || top["declarations"] == nil {
		return nil, fmt.Errorf("%s: expected a single 'declarations' key", label)
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(top["declarations"], &rawEntries); err != nil {
		return nil, fmt.Errorf("%s: 'declarations' must be a list", label)
	}
	entries := make([]timingDeclaration, 0, len(rawEntries))
	seen := make(map[[2]string]bool)
	required := []string{"file", "snippet", "kind", "reason", "repair"}
	for idx, rawEntry := range rawEntries {
		where := fmt.Sprintf("%s: declarations[%d]", label, idx)
		var obj map[string]any
		if err := json.Unmarshal(rawEntry, &obj); err != nil || obj == nil {
			return nil, fmt.Errorf("%s: expected an object", where)
		}
		if len(obj) != len(required) {
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return nil, fmt.Errorf("%s: expected keys file, snippet, kind, reason, repair; got %v", where, keys)
		}
		for _, k := range required {
			v, ok := obj[k]
			if !ok {
				keys := make([]string, 0, len(obj))
				for okKey := range obj {
					keys = append(keys, okKey)
				}
				sort.Strings(keys)
				return nil, fmt.Errorf("%s: expected keys file, snippet, kind, reason, repair; got %v", where, keys)
			}
			s, isStr := v.(string)
			if !isStr || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("%s needs a non-empty '%s'", where, k)
			}
		}
		var td timingDeclaration
		if err := json.Unmarshal(rawEntry, &td); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if td.Kind != "liveness" && td.Kind != "backlog" {
			return nil, fmt.Errorf("%s names kind %q, which is not liveness or backlog", where, td.Kind)
		}
		key := [2]string{td.File, td.Snippet}
		if seen[key] {
			return nil, fmt.Errorf("%s repeats the declaration for %v", where, key)
		}
		seen[key] = true
		entries = append(entries, td)
	}
	return entries, nil
}

func blobBytes(root, rev, filePath string) ([]byte, bool, error) {
	listing, err := gitRun(root, true, "ls-tree", "-z", rev, "--", filePath)
	if err != nil {
		return nil, false, err
	}
	if listing == "" {
		return nil, false, nil
	}
	entry, _, _ := strings.Cut(listing, "\x00")
	meta, found, ok := strings.Cut(entry, "\t")
	if !ok || found != filePath {
		return nil, false, nil
	}
	fields := strings.Fields(meta)
	if len(fields) != 3 {
		return nil, false, nil
	}
	mode, kind, obj := fields[0], fields[1], fields[2]
	if kind != "blob" || (mode != "100644" && mode != "100755") {
		return nil, false, nil
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "blob", obj)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("cannot read blob %s", obj)
		}
		return nil, false, errors.New(msg)
	}
	return stdout.Bytes(), true, nil
}

func elapsedTimeFindings(root, base, head string) ([]string, error) {
	diffOut, err := gitRun(root, true, "diff", "--name-only", "--no-renames", "-z", base, head)
	if err != nil {
		return nil, err
	}
	changedTests := make(map[string]bool)
	declChanged := false
	for _, p := range strings.Split(diffOut, "\x00") {
		if p == "" {
			continue
		}
		if isTimingTestPath(p) {
			changedTests[p] = true
		}
		if p == timingDeclarationsPath {
			declChanged = true
		}
	}
	if len(changedTests) == 0 && !declChanged {
		return nil, nil
	}

	var declarations []timingDeclaration
	rawHead, hasHeadDecl, err := blobBytes(root, head, timingDeclarationsPath)
	if err != nil {
		return nil, err
	}
	if hasHeadDecl {
		declarations, err = parseTimingDeclarations(rawHead, timingDeclarationsPath)
		if err != nil {
			return []string{err.Error()}, nil
		}
	}

	addedDeclKeys := make(map[[2]string]bool)
	removedDeclFiles := make(map[string]bool)
	if declChanged {
		rawBase, hasBaseDecl, err := blobBytes(root, base, timingDeclarationsPath)
		if err != nil {
			return nil, err
		}
		var baseDecls []timingDeclaration
		if hasBaseDecl {
			if parsed, err := parseTimingDeclarations(rawBase, timingDeclarationsPath); err == nil {
				baseDecls = parsed
			}
		}
		baseKeys := make(map[[2]string]bool, len(baseDecls))
		for _, d := range baseDecls {
			baseKeys[[2]string{d.File, d.Snippet}] = true
		}
		headKeys := make(map[[2]string]bool, len(declarations))
		for _, d := range declarations {
			k := [2]string{d.File, d.Snippet}
			headKeys[k] = true
			if !baseKeys[k] {
				addedDeclKeys[k] = true
			}
		}
		for k := range baseKeys {
			if !headKeys[k] && isTimingTestPath(k[0]) {
				removedDeclFiles[k[0]] = true
			}
		}
	}

	scanSet := make(map[string]bool)
	for p := range changedTests {
		scanSet[p] = true
	}
	for p := range removedDeclFiles {
		scanSet[p] = true
	}
	for k := range addedDeclKeys {
		if isTimingTestPath(k[0]) {
			scanSet[k[0]] = true
		}
	}
	scanPaths := make([]string, 0, len(scanSet))
	for p := range scanSet {
		scanPaths = append(scanPaths, p)
	}
	sort.Strings(scanPaths)

	declared := make(map[[2]string]timingDeclaration, len(declarations))
	for _, d := range declarations {
		declared[[2]string{d.File, d.Snippet}] = d
	}

	var elapsedHits, deadlineHits []timingHit
	for _, name := range scanPaths {
		content, ok, err := blobBytes(root, head, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		text := string(content)
		elapsedHits = append(elapsedHits, elapsedTimeHitsInText(name, text)...)
		deadlineHits = append(deadlineHits, shortDeadlineHitsInText(name, text)...)
	}

	var findings []string
	var undeclaredElapsed []string
	matched := make(map[[2]string]bool, len(elapsedHits)+len(deadlineHits))
	for _, h := range elapsedHits {
		k := [2]string{h.name, h.snippet}
		matched[k] = true
		if _, ok := declared[k]; !ok {
			undeclaredElapsed = append(undeclaredElapsed, fmt.Sprintf("%s:%d: %s", h.name, h.number, h.snippet))
		}
	}
	if len(undeclaredElapsed) > 0 {
		findings = append(findings, undeclaredTimingPrefix+strings.Join(undeclaredElapsed, "; "))
	}

	var undeclaredDeadlines []string
	for _, h := range deadlineHits {
		k := [2]string{h.name, h.snippet}
		matched[k] = true
		if _, ok := declared[k]; !ok {
			undeclaredDeadlines = append(undeclaredDeadlines, fmt.Sprintf("%s:%d: %s", h.name, h.number, h.snippet))
		}
	}
	if len(undeclaredDeadlines) > 0 {
		findings = append(findings, shortDeadlinePrefix+strings.Join(undeclaredDeadlines, "; "))
	}

	var stale []string
	for _, d := range declarations {
		k := [2]string{d.File, d.Snippet}
		if changedTests[d.File] || addedDeclKeys[k] {
			if !matched[k] {
				stale = append(stale, fmt.Sprintf("%s: %s", d.File, d.Snippet))
			}
		}
	}
	if len(stale) > 0 {
		findings = append(findings, staleTimingPrefix+strings.Join(stale, "; "))
	}
	return findings, nil
}

func cmdElapsedTime(args []string) int {
	failJudge := func(err error) int {
		fmt.Fprintf(os.Stderr, "elapsed-time: could not judge: %v\n", err)
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
	findings, err := elapsedTimeFindings(root, base, head)
	if err != nil {
		return failJudge(err)
	}
	if len(findings) > 0 {
		fmt.Println(strings.Join(findings, "\n"))
		return 1
	}
	return 0
}
