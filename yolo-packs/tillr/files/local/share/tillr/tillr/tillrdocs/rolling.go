package tillrdocs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/packdocs"
)

// RollingHistory lists the only exempted historical records that may keep a disabling loop invocation.
var RollingHistory = map[string]string{
	"docs/reference/tillr-claude-backend.md": "Records the scratch drain measurement with rolling checks off.",
}

var (
	lineContRe      = regexp.MustCompile(`\\\r?\n`)
	pythonShebangRe = regexp.MustCompile(`^#![^\n]*python`)
	shlexSafeRe     = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
)

func stripShellQuotes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'', '"', '\\':
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func mightNameRolling(text string) bool {
	if strings.Contains(text, "--rolling") {
		return true
	}
	return strings.ContainsAny(text, "'\"\\") && strings.Contains(stripShellQuotes(text), "--rolling")
}

// ShlexSplit tokenizes a shell command line like Python's
// shlex.shlex(line, posix=True, punctuation_chars=";&|()") with whitespace_split=True.
func ShlexSplit(line string) ([]string, error) {
	const punct = ";&|()"
	var tokens []string
	var buf strings.Builder
	hasToken := false
	inQuote := byte(0) // 0, '\'', or '"'
	inPunct := false

	flush := func() {
		if hasToken {
			tokens = append(tokens, buf.String())
			buf.Reset()
			hasToken = false
			inPunct = false
		}
	}

	for i := 0; i < len(line); i++ {
		c := line[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
				continue
			}
			if inQuote == '"' && c == '\\' && i+1 < len(line) {
				next := line[i+1]
				if next == '"' || next == '\\' {
					buf.WriteByte(next)
					i++
					continue
				}
			}
			buf.WriteByte(c)
			continue
		}

		// Outside quotes.
		if inPunct {
			if strings.IndexByte(punct, c) >= 0 {
				buf.WriteByte(c)
				continue
			}
			flush()
		}

		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			flush()
			continue
		}
		if c == '#' && !hasToken {
			// Comment to end of line.
			break
		}
		if c == '\'' || c == '"' {
			inQuote = c
			hasToken = true
			continue
		}
		if c == '\\' {
			if i+1 >= len(line) {
				return nil, fmt.Errorf("no escaped character")
			}
			i++
			buf.WriteByte(line[i])
			hasToken = true
			continue
		}
		if strings.IndexByte(punct, c) >= 0 {
			flush()
			buf.WriteByte(c)
			hasToken = true
			inPunct = true
			continue
		}
		buf.WriteByte(c)
		hasToken = true
	}

	if inQuote != 0 {
		return nil, fmt.Errorf("no closing quotation")
	}
	flush()
	return tokens, nil
}

// ShlexJoin joins argv into a shell-escaped command string matching Python's shlex.join.
func ShlexJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		if arg == "" {
			parts[i] = "''"
		} else if shlexSafeRe.MatchString(arg) {
			parts[i] = arg
		} else {
			parts[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
	}
	return strings.Join(parts, " ")
}

// RollingDisabled returns disabling loop commands in text, not mentions of the flag or quoted fixtures.
func RollingDisabled(text string, markdown, pythonScript bool) []string {
	var hits []string
	var blobs []string
	if markdown {
		blobs = append(blobs, text)
		blobs = append(blobs, CodeBlobs(text)...)
	} else {
		blobs = append(blobs, text)
		if pythonScript || pythonShebangRe.MatchString(text) {
			blobs = append(blobs, PythonCommandBlobs(text)...)
		}
	}

	for _, blob := range blobs {
		if !mightNameRolling(blob) {
			continue
		}
		joined := blob
		if strings.Contains(blob, "\\") {
			joined = lineContRe.ReplaceAllString(blob, " ")
		}
		for _, line := range splitLines(joined) {
			if !mightNameRolling(line) {
				continue
			}
			words, err := ShlexSplit(line)
			if err != nil {
				// A prose/source line with unmatched quotes is not a shell command.
				continue
			}
			for index, word := range words {
				var start int
				base := word
				if slash := strings.LastIndexByte(word, '/'); slash >= 0 {
					base = word[slash+1:]
				}
				if base == "tillr-drain" && index+1 < len(words) && words[index+1] == "loop" {
					start = index + 2
				} else if word == "tillr" && index+2 < len(words) && words[index+1] == "drain" && words[index+2] == "loop" {
					start = index + 3
				} else {
					continue
				}
				for pos := start; pos < len(words); pos++ {
					tok := words[pos]
					if tok == ";" || tok == "&&" || tok == "||" || tok == "|" || tok == ")" {
						break
					}
					if tok == "--rolling=" || (tok == "--rolling" && pos+1 < len(words) && words[pos+1] == "") {
						hits = append(hits, strings.TrimSpace(line))
					}
				}
			}
		}
	}
	return hits
}

// RollingPolicyErrors checks tracked working bytes and stale/invalid historical exceptions.
func RollingPolicyErrors(files map[string]string, history map[string]string) []string {
	hits := make(map[string][]string, len(files))
	for name, body := range files {
		hits[name] = RollingDisabled(body, strings.HasSuffix(name, ".md"), strings.HasSuffix(name, ".py"))
	}
	var errors []string
	for name, commands := range hits {
		if len(commands) > 0 {
			if _, ok := history[packdocs.SourceOf(name)]; !ok {
				errors = append(errors, fmt.Sprintf("%s: disables the after-merge check: %s", name, commands[0]))
			}
		}
	}
	for name, reason := range history {
		if !strings.HasPrefix(name, "docs/") || !strings.HasSuffix(name, ".md") || strings.Contains(name, "/skills/") {
			errors = append(errors, fmt.Sprintf("%s: only historical documents may be exempted", name))
		}
		if strings.TrimSpace(reason) == "" {
			errors = append(errors, fmt.Sprintf("%s: historical exception has no reason", name))
		}
		if len(hits[name]) == 0 {
			errors = append(errors, fmt.Sprintf("%s: stale historical exception (no disabling loop command)", name))
		}
	}
	return errors
}

// CheckRollingEnabled checks all tracked UTF-8 files in the repository against RollingHistory.
func CheckRollingEnabled(root string) (string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-files: %w", err)
	}
	files := make(map[string]string)
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		name := string(raw)
		p := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(b) {
			continue
		}
		files[name] = string(b)
	}
	if errs := RollingPolicyErrors(files, RollingHistory); len(errs) > 0 {
		return "", fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return fmt.Sprintf("tracked loop invocations keep the after-merge check on (%d text files; %d named historical exception)", len(files), len(RollingHistory)), nil
}

// --- Lightweight Python static analyzer for PythonCommandBlobs ---

type pyTokKind int

const (
	pyEOF pyTokKind = iota
	pyNewline
	pyIndent
	pyDedent
	pyIdent
	pyString
	pyNumber
	pyOp // punctuation / operators
)

type pyTok struct {
	kind pyTokKind
	val  string
}

func tokenizePython(src string) []pyTok {
	var toks []pyTok
	indents := []int{0}
	bracketDepth := 0
	i := 0
	n := len(src)
	atLineStart := true

	for i < n {
		if atLineStart && bracketDepth == 0 {
			col := 0
			for i < n && (src[i] == ' ' || src[i] == '\t') {
				if src[i] == '\t' {
					col += 8 - (col % 8)
				} else {
					col++
				}
				i++
			}
			if i >= n {
				break
			}
			if src[i] == '#' {
				for i < n && src[i] != '\n' {
					i++
				}
				if i < n {
					i++
				}
				continue
			}
			if src[i] == '\r' || src[i] == '\n' {
				if src[i] == '\r' && i+1 < n && src[i+1] == '\n' {
					i += 2
				} else {
					i++
				}
				continue
			}
			top := indents[len(indents)-1]
			if col > top {
				indents = append(indents, col)
				toks = append(toks, pyTok{kind: pyIndent})
			} else {
				for col < indents[len(indents)-1] && len(indents) > 1 {
					indents = indents[:len(indents)-1]
					toks = append(toks, pyTok{kind: pyDedent})
				}
			}
			atLineStart = false
		}

		if i >= n {
			break
		}
		c := src[i]

		// Line continuation.
		if c == '\\' && i+1 < n && (src[i+1] == '\n' || (src[i+1] == '\r' && i+2 < n && src[i+2] == '\n')) {
			i++
			if src[i] == '\r' {
				i++
			}
			i++
			continue
		}

		if c == ' ' || c == '\t' || c == '\r' {
			i++
			continue
		}
		if c == '\n' {
			i++
			if bracketDepth == 0 {
				toks = append(toks, pyTok{kind: pyNewline})
				atLineStart = true
			}
			continue
		}
		if c == '#' {
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}

		// String literals (including optional r/b/f/u prefix).
		if strVal, endIdx, isStr := scanPyString(src, i); isStr {
			if strVal.plain {
				toks = append(toks, pyTok{kind: pyString, val: strVal.text})
			} else {
				// f-string or bytes: treat as dynamic/non-string-constant
				toks = append(toks, pyTok{kind: pyNumber, val: "0"})
			}
			i = endIdx
			continue
		}

		// Identifiers / keywords.
		if isIdentStart(c) {
			start := i
			i++
			for i < n && isIdentCont(src[i]) {
				i++
			}
			toks = append(toks, pyTok{kind: pyIdent, val: src[start:i]})
			continue
		}

		// Numbers.
		if c >= '0' && c <= '9' {
			start := i
			for i < n && (isIdentCont(src[i]) || src[i] == '.') {
				i++
			}
			toks = append(toks, pyTok{kind: pyNumber, val: src[start:i]})
			continue
		}

		// Brackets.
		if c == '(' || c == '[' || c == '{' {
			bracketDepth++
			toks = append(toks, pyTok{kind: pyOp, val: string(c)})
			i++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if bracketDepth > 0 {
				bracketDepth--
			}
			toks = append(toks, pyTok{kind: pyOp, val: string(c)})
			i++
			continue
		}

		// Multi-char operators.
		if i+1 < n {
			two := src[i : i+2]
			if two == "+=" || two == "==" || two == "!=" || two == "<=" || two == ">=" || two == "->" || two == ":=" || two == "**" || two == "//" {
				toks = append(toks, pyTok{kind: pyOp, val: two})
				i += 2
				continue
			}
		}

		toks = append(toks, pyTok{kind: pyOp, val: string(c)})
		i++
	}

	if len(toks) > 0 && toks[len(toks)-1].kind != pyNewline {
		toks = append(toks, pyTok{kind: pyNewline})
	}
	for len(indents) > 1 {
		indents = indents[:len(indents)-1]
		toks = append(toks, pyTok{kind: pyDedent})
	}
	toks = append(toks, pyTok{kind: pyEOF})
	return toks
}

type pyStrResult struct {
	text  string
	plain bool
}

func scanPyString(src string, i int) (pyStrResult, int, bool) {
	n := len(src)
	prefixEnd := i
	raw := false
	plain := true
	for prefixEnd < n && prefixEnd-i < 2 {
		ch := src[prefixEnd]
		if ch == 'r' || ch == 'R' {
			raw = true
			prefixEnd++
		} else if ch == 'u' || ch == 'U' {
			prefixEnd++
		} else if ch == 'b' || ch == 'B' || ch == 'f' || ch == 'F' {
			plain = false
			prefixEnd++
		} else {
			break
		}
	}
	if prefixEnd >= n || (src[prefixEnd] != '\'' && src[prefixEnd] != '"') {
		return pyStrResult{}, i, false
	}
	quote := src[prefixEnd]
	triple := prefixEnd+2 < n && src[prefixEnd+1] == quote && src[prefixEnd+2] == quote
	pos := prefixEnd + 1
	if triple {
		pos = prefixEnd + 3
	}
	var b strings.Builder
	for pos < n {
		if triple {
			if pos+2 < n && src[pos] == quote && src[pos+1] == quote && src[pos+2] == quote {
				return pyStrResult{text: b.String(), plain: plain}, pos + 3, true
			}
		} else {
			if src[pos] == quote {
				return pyStrResult{text: b.String(), plain: plain}, pos + 1, true
			}
			if src[pos] == '\n' {
				return pyStrResult{}, i, false
			}
		}
		if src[pos] == '\\' && !raw && pos+1 < n {
			next := src[pos+1]
			switch next {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\', '\'', '"':
				b.WriteByte(next)
			case '\n':
				// line continuation inside string
			default:
				b.WriteByte('\\')
				b.WriteByte(next)
			}
			pos += 2
			continue
		}
		if src[pos] == '\\' && raw && pos+1 < n {
			b.WriteByte('\\')
			b.WriteByte(src[pos+1])
			pos += 2
			continue
		}
		b.WriteByte(src[pos])
		pos++
	}
	return pyStrResult{}, i, false
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (c >= 0x80 && unicode.IsLetter(rune(c)))
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// pyVal represents a tracked static value in PythonCommandBlobs.
type pyVal interface {
	isPyVal()
	clone() pyVal
	equals(other pyVal) bool
}

type pyStr struct{ s string }

func (p *pyStr) isPyVal()            {}
func (p *pyStr) clone() pyVal        { return &pyStr{s: p.s} }
func (p *pyStr) equals(o pyVal) bool { q, ok := o.(*pyStr); return ok && p.s == q.s }

type pyList struct{ items []string }

func (p *pyList) isPyVal() {}
func (p *pyList) clone() pyVal {
	cp := make([]string, len(p.items))
	copy(cp, p.items)
	return &pyList{items: cp}
}
func (p *pyList) equals(o pyVal) bool {
	q, ok := o.(*pyList)
	if !ok || len(p.items) != len(q.items) {
		return false
	}
	for i := range p.items {
		if p.items[i] != q.items[i] {
			return false
		}
	}
	return true
}

type pyTuple struct{ alts []pyVal }

func (p *pyTuple) isPyVal() {}
func (p *pyTuple) clone() pyVal {
	cp := make([]pyVal, len(p.alts))
	for i, a := range p.alts {
		if a != nil {
			cp[i] = a.clone()
		}
	}
	return &pyTuple{alts: cp}
}
func (p *pyTuple) equals(o pyVal) bool {
	q, ok := o.(*pyTuple)
	if !ok || len(p.alts) != len(q.alts) {
		return false
	}
	for i := range p.alts {
		if !pyValEqual(p.alts[i], q.alts[i]) {
			return false
		}
	}
	return true
}

func pyValEqual(a, b pyVal) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.equals(b)
}

func cloneEnv(env map[string]pyVal) map[string]pyVal {
	out := make(map[string]pyVal, len(env))
	for k, v := range env {
		if v != nil {
			out[k] = v.clone()
		} else {
			out[k] = nil
		}
	}
	return out
}

func flattenAlts(v pyVal) []pyVal {
	if t, ok := v.(*pyTuple); ok {
		return t.alts
	}
	return []pyVal{v}
}

type pyAnalyzer struct {
	toks   []pyTok
	pos    int
	values map[string]pyVal
	blobs  []string
}

func (a *pyAnalyzer) peek() pyTok {
	if a.pos >= len(a.toks) {
		return pyTok{kind: pyEOF}
	}
	return a.toks[a.pos]
}

func (a *pyAnalyzer) next() pyTok {
	t := a.peek()
	if a.pos < len(a.toks) {
		a.pos++
	}
	return t
}

func (a *pyAnalyzer) emit(v pyVal) {
	if v == nil {
		return
	}
	switch x := v.(type) {
	case *pyTuple:
		for _, alt := range x.alts {
			a.emit(alt)
		}
	case *pyList:
		a.blobs = append(a.blobs, ShlexJoin(x.items))
	case *pyStr:
		a.blobs = append(a.blobs, x.s)
	}
}

func (a *pyAnalyzer) skipToNewline() {
	for a.peek().kind != pyNewline && a.peek().kind != pyEOF {
		a.next()
	}
	if a.peek().kind == pyNewline {
		a.next()
	}
}

func (a *pyAnalyzer) parseSuite() {
	if a.peek().kind == pyNewline {
		a.next()
		if a.peek().kind != pyIndent {
			return
		}
		a.next()
		for a.peek().kind != pyDedent && a.peek().kind != pyEOF {
			before := a.pos
			a.parseStmt()
			if a.pos == before {
				a.next()
			}
		}
		if a.peek().kind == pyDedent {
			a.next()
		}
		return
	}
	a.parseSimpleStmt()
}

func (a *pyAnalyzer) parseStmt() {
	for a.peek().kind == pyNewline {
		a.next()
	}
	if a.peek().kind == pyEOF || a.peek().kind == pyDedent {
		return
	}
	t := a.peek()
	if t.kind == pyOp && t.val == "@" {
		// Decorator line.
		a.skipToNewline()
		a.parseStmt()
		return
	}
	if t.kind == pyIdent {
		switch t.val {
		case "def":
			a.parseFuncDef()
			return
		case "async":
			if a.pos+1 < len(a.toks) && a.toks[a.pos+1].kind == pyIdent && a.toks[a.pos+1].val == "def" {
				a.next()
				a.parseFuncDef()
				return
			}
		case "if":
			a.parseIf(false)
			return
		case "class", "for", "while", "try", "except", "finally", "with":
			for a.peek().kind != pyNewline && a.peek().kind != pyEOF {
				if a.peek().kind == pyOp && a.peek().val == ":" {
					a.next()
					a.parseSuite()
					return
				}
				a.next()
			}
			a.skipToNewline()
			return
		}
	}
	a.parseSimpleStmt()
}

func (a *pyAnalyzer) parseFuncDef() {
	a.next() // 'def'
	if a.peek().kind == pyIdent {
		a.next() // func name
	}
	var params []string
	if a.peek().kind == pyOp && a.peek().val == "(" {
		a.next()
		depth := 1
		expectParam := true
		for depth > 0 && a.peek().kind != pyEOF {
			tok := a.next()
			if tok.kind == pyOp {
				switch tok.val {
				case "(", "[", "{":
					depth++
				case ")", "]", "}":
					depth--
				case ",":
					if depth == 1 {
						expectParam = true
					}
				case ":", "=":
					if depth == 1 {
						expectParam = false
					}
				}
			} else if tok.kind == pyIdent && depth == 1 && expectParam {
				params = append(params, tok.val)
				expectParam = false
			}
		}
	}
	for a.peek().kind != pyNewline && a.peek().kind != pyEOF {
		if a.peek().kind == pyOp && a.peek().val == ":" {
			a.next()
			break
		}
		a.next()
	}
	outer := a.values
	a.values = cloneEnv(outer)
	for _, p := range params {
		delete(a.values, p)
	}
	a.parseSuite()
	a.values = outer
}

func (a *pyAnalyzer) parseIf(isElif bool) {
	a.next() // 'if' or 'elif'
	// Capture test tokens up to ':'.
	testKnown := false
	testVal := false
	if a.peek().kind == pyIdent && (a.peek().val == "True" || a.peek().val == "False") {
		if a.pos+1 < len(a.toks) && a.toks[a.pos+1].kind == pyOp && a.toks[a.pos+1].val == ":" {
			testKnown = true
			testVal = a.peek().val == "True"
		}
	}
	for a.peek().kind != pyNewline && a.peek().kind != pyEOF && a.peek().kind != pyDedent {
		if a.peek().kind == pyOp && a.peek().val == ":" {
			a.next()
			break
		}
		before := a.pos
		a.parseExpr()
		if a.pos == before {
			a.next()
		}
	}
	if testKnown {
		if testVal {
			a.parseSuite()
			// Skip any remaining elif/else clauses.
			a.skipElseClauses()
		} else {
			// Skip body by running in a discarded env or just parsing without keeping.
			saved := a.values
			a.values = cloneEnv(saved)
			a.parseSuite()
			a.values = saved
			a.parseElseChain()
		}
		return
	}

	incoming := cloneEnv(a.values)
	a.parseSuite()
	yes := a.values
	a.values = incoming
	a.parseElseChain()
	no := a.values

	merged := make(map[string]pyVal)
	for k, v := range yes {
		if nv, ok := no[k]; ok && pyValEqual(v, nv) {
			merged[k] = v
		} else {
			alts := append([]pyVal{}, flattenAlts(v)...)
			alts = append(alts, flattenAlts(no[k])...)
			merged[k] = &pyTuple{alts: alts}
		}
	}
	for k, nv := range no {
		if _, ok := yes[k]; ok {
			continue
		}
		alts := append([]pyVal{nil}, flattenAlts(nv)...)
		merged[k] = &pyTuple{alts: alts}
	}
	a.values = merged
}

func (a *pyAnalyzer) parseElseChain() {
	if a.peek().kind == pyIdent && a.peek().val == "elif" {
		a.parseIf(true)
		return
	}
	if a.peek().kind == pyIdent && a.peek().val == "else" {
		a.next()
		if a.peek().kind == pyOp && a.peek().val == ":" {
			a.next()
		}
		a.parseSuite()
	}
}

func (a *pyAnalyzer) skipElseClauses() {
	saved := a.values
	for a.peek().kind == pyIdent && (a.peek().val == "elif" || a.peek().val == "else") {
		a.next()
		for a.peek().kind != pyNewline && a.peek().kind != pyEOF {
			if a.peek().kind == pyOp && a.peek().val == ":" {
				a.next()
				break
			}
			a.next()
		}
		a.values = cloneEnv(saved)
		a.parseSuite()
	}
	a.values = saved
}

func (a *pyAnalyzer) parseSimpleStmt() {
	for a.peek().kind != pyNewline && a.peek().kind != pyEOF && a.peek().kind != pyDedent {
		if a.peek().kind == pyOp && a.peek().val == ";" {
			a.next()
			continue
		}
		if a.peek().kind == pyIdent && a.peek().val == "return" {
			a.next()
			if a.peek().kind != pyNewline && a.peek().kind != pyEOF && a.peek().kind != pyDedent && !(a.peek().kind == pyOp && a.peek().val == ";") {
				before := a.pos
				val := a.parseExpr()
				a.emit(val)
				if a.pos == before {
					a.next()
				}
			}
			continue
		}
		// Check for assignment: name = expr | name += expr | name: type = expr
		if a.peek().kind == pyIdent && a.pos+1 < len(a.toks) {
			nextTok := a.toks[a.pos+1]
			if nextTok.kind == pyOp && (nextTok.val == "=" || nextTok.val == "+=") {
				name := a.next().val
				op := a.next().val
				val := a.parseExpr()
				if op == "=" {
					if val == nil {
						delete(a.values, name)
					} else {
						a.values[name] = val
					}
				} else if op == "+=" {
					left := a.values[name]
					if l, ok := left.(*pyList); ok {
						if r, ok := val.(*pyList); ok {
							l.items = append(l.items, r.items...)
						}
					} else if l, ok := left.(*pyStr); ok {
						if r, ok := val.(*pyStr); ok {
							a.values[name] = &pyStr{s: l.s + r.s}
						}
					}
				}
				continue
			}
			if nextTok.kind == pyOp && nextTok.val == ":" {
				// Annotated assignment: name: Type = expr
				name := a.next().val
				a.next() // ':'
				for a.peek().kind != pyNewline && a.peek().kind != pyEOF && !(a.peek().kind == pyOp && (a.peek().val == "=" || a.peek().val == ";")) {
					a.next()
				}
				if a.peek().kind == pyOp && a.peek().val == "=" {
					a.next()
					val := a.parseExpr()
					if val == nil {
						delete(a.values, name)
					} else {
						a.values[name] = val
					}
				} else {
					delete(a.values, name)
				}
				continue
			}
		}
		before := a.pos
		a.parseExpr()
		// If there's an unconsumed token on this line, advance to avoid infinite loops.
		if a.pos == before && a.peek().kind != pyNewline && a.peek().kind != pyEOF && a.peek().kind != pyDedent && !(a.peek().kind == pyOp && a.peek().val == ";") {
			a.next()
		}
	}
	if a.peek().kind == pyNewline {
		a.next()
	}
}

func (a *pyAnalyzer) parseExpr() pyVal {
	left := a.parseAtom()
	for a.peek().kind == pyOp && a.peek().val == "+" {
		a.next()
		right := a.parseAtom()
		left = combineAdd(left, right)
	}
	return left
}

func combineAdd(left, right pyVal) pyVal {
	lefts := flattenAlts(left)
	rights := flattenAlts(right)
	var variants []pyVal
	for _, one := range lefts {
		for _, two := range rights {
			if l, ok := one.(*pyList); ok {
				cp := make([]string, len(l.items))
				copy(cp, l.items)
				if r, ok := two.(*pyList); ok {
					cp = append(cp, r.items...)
				} else {
					cp = append(cp, "<dynamic>")
				}
				variants = append(variants, &pyList{items: cp})
			} else if l, ok := one.(*pyStr); ok {
				if r, ok := two.(*pyStr); ok {
					variants = append(variants, &pyStr{s: l.s + r.s})
				}
			}
		}
	}
	if len(variants) == 0 {
		return nil
	}
	if len(variants) == 1 {
		return variants[0]
	}
	return &pyTuple{alts: variants}
}

func (a *pyAnalyzer) parseAtom() pyVal {
	if a.peek().kind == pyEOF || a.peek().kind == pyNewline {
		return nil
	}
	var val pyVal
	var baseName string
	tok := a.peek()
	switch {
	case tok.kind == pyString:
		a.next()
		val = &pyStr{s: tok.val}
	case tok.kind == pyIdent:
		a.next()
		baseName = tok.val
		val = a.values[tok.val]
	case tok.kind == pyNumber:
		a.next()
	case tok.kind == pyOp && (tok.val == "[" || tok.val == "("):
		closeCh := "]"
		if tok.val == "(" {
			closeCh = ")"
		}
		a.next()
		// Check if parenthesized single expression vs tuple/list.
		var elts []pyVal
		hasComma := false
		for a.peek().kind != pyEOF && !(a.peek().kind == pyOp && a.peek().val == closeCh) {
			before := a.pos
			elts = append(elts, a.parseExpr())
			if a.pos == before {
				a.next()
				continue
			}
			if a.peek().kind == pyOp && a.peek().val == "," {
				hasComma = true
				a.next()
			} else if a.peek().kind == pyOp && a.peek().val == closeCh {
				break
			} else {
				// Complex expression inside brackets (e.g. comprehension).
				for a.peek().kind != pyEOF && !(a.peek().kind == pyOp && a.peek().val == closeCh) {
					if a.peek().kind == pyOp && (a.peek().val == "(" || a.peek().val == "[" || a.peek().val == "{") {
						a.parseAtom()
					} else {
						a.next()
					}
				}
				break
			}
		}
		if a.peek().kind == pyOp && a.peek().val == closeCh {
			a.next()
		}
		if closeCh == ")" && !hasComma && len(elts) == 1 {
			val = elts[0]
		} else {
			variants := [][]string{{}}
			for _, part := range elts {
				choices := flattenAlts(part)
				var next [][]string
				for _, prefix := range variants {
					for _, ch := range choices {
						row := make([]string, len(prefix)+1)
						copy(row, prefix)
						if s, ok := ch.(*pyStr); ok {
							row[len(prefix)] = s.s
						} else {
							row[len(prefix)] = "<dynamic>"
						}
						next = append(next, row)
					}
				}
				variants = next
			}
			if len(variants) == 1 {
				val = &pyList{items: variants[0]}
			} else {
				alts := make([]pyVal, len(variants))
				for i, v := range variants {
					alts[i] = &pyList{items: v}
				}
				val = &pyTuple{alts: alts}
			}
		}
	case tok.kind == pyOp && tok.val == "{":
		a.next()
		depth := 1
		for depth > 0 && a.peek().kind != pyEOF {
			t := a.next()
			if t.kind == pyOp {
				if t.val == "{" {
					depth++
				} else if t.val == "}" {
					depth--
				}
			}
		}
	default:
		return nil
	}

	// Postfix attribute access / calls / subscripts.
	for a.peek().kind == pyOp {
		if a.peek().val == "." {
			a.next()
			if a.peek().kind != pyIdent {
				break
			}
			attr := a.next().val
			if a.peek().kind == pyOp && a.peek().val == "(" {
				posArgs, kwArgs := a.parseCallArgs()
				var command pyVal
				hasCommand := false
				if len(posArgs) > 0 {
					command = posArgs[0]
					hasCommand = true
				} else if kw, ok := kwArgs["args"]; ok {
					command = kw
					hasCommand = true
				}
				if hasCommand && baseName != "" {
					for _, alt := range flattenAlts(a.values[baseName]) {
						if argv, ok := alt.(*pyList); ok {
							if attr == "append" {
								if s, ok := command.(*pyStr); ok {
									argv.items = append(argv.items, s.s)
								}
							} else if attr == "extend" {
								if r, ok := command.(*pyList); ok {
									argv.items = append(argv.items, r.items...)
								}
							}
						}
					}
				}
				if hasCommand {
					switch attr {
					case "run", "Popen", "call", "check_call", "check_output", "system":
						a.emit(command)
					}
				}
				val = nil
				baseName = ""
			} else {
				val = nil
				baseName = ""
			}
		} else if a.peek().val == "(" {
			a.parseCallArgs()
			val = nil
			baseName = ""
		} else if a.peek().val == "[" {
			a.next()
			depth := 1
			for depth > 0 && a.peek().kind != pyEOF {
				t := a.next()
				if t.kind == pyOp {
					if t.val == "[" {
						depth++
					} else if t.val == "]" {
						depth--
					}
				}
			}
			val = nil
			baseName = ""
		} else {
			break
		}
	}
	return val
}

func (a *pyAnalyzer) parseCallArgs() ([]pyVal, map[string]pyVal) {
	a.next() // '('
	var pos []pyVal
	kw := make(map[string]pyVal)
	for a.peek().kind != pyEOF && !(a.peek().kind == pyOp && a.peek().val == ")") {
		if a.peek().kind == pyIdent && a.pos+1 < len(a.toks) && a.toks[a.pos+1].kind == pyOp && a.toks[a.pos+1].val == "=" {
			k := a.next().val
			a.next() // '='
			kw[k] = a.parseExpr()
		} else {
			pos = append(pos, a.parseExpr())
		}
		if a.peek().kind == pyOp && a.peek().val == "," {
			a.next()
		} else if a.peek().kind == pyOp && a.peek().val == ")" {
			break
		} else {
			// Skip unexpected tokens inside call arg until ',' or ')'.
			depth := 0
			for a.peek().kind != pyEOF {
				t := a.peek()
				if t.kind == pyOp {
					if t.val == "(" || t.val == "[" || t.val == "{" {
						depth++
					} else if t.val == ")" || t.val == "]" || t.val == "}" {
						if depth == 0 {
							break
						}
						depth--
					} else if t.val == "," && depth == 0 {
						a.next()
						break
					}
				}
				a.next()
			}
		}
	}
	if a.peek().kind == pyOp && a.peek().val == ")" {
		a.next()
	}
	return pos, kw
}

// PythonCommandBlobs extracts literal command strings/argvs from Python source without executing it.
func PythonCommandBlobs(text string) []string {
	toks := tokenizePython(text)
	an := &pyAnalyzer{
		toks:   toks,
		values: make(map[string]pyVal),
	}
	for an.peek().kind != pyEOF {
		before := an.pos
		an.parseStmt()
		if an.pos == before {
			an.next()
		}
	}
	return an.blobs
}
