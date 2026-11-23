package remotesync

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/checkresult"
)

// FailingCase is one failing case as the gate's triage block named it.
type FailingCase struct {
	Name      string `json:"name"`
	Where     string `json:"where,omitempty"`
	Assertion string `json:"assertion,omitempty"`
	// Reproduce is the command that reproduces only this case: the packet's one-case
	// command when it has one, else its one-file command.
	Reproduce string `json:"reproduce,omitempty"`
}

// FailingCheck is one check the gate's own report named as failed, with its cases.
type FailingCheck struct {
	ID    string        `json:"id"`
	Label string        `json:"label,omitempty"`
	Cases []FailingCase `json:"cases,omitempty"`
	// Reproduce is the packet's command when it named no case.
	Reproduce string `json:"reproduce,omitempty"`
}

const triageScanMax = 4 << 20

var (
	forkCheckPidPattern     = regexp.MustCompile(`run-\d+`)
	forkCheckNonSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
	reportCommitPattern     = regexp.MustCompile(`^run:\s+\S+\s+commit\s+([0-9a-fA-F]{7,64})\b`)
)

// ForkCheckDeclID mirrors tools/fork-check.py's declaration_id(kind, label, root) so a
// triage packet or failure excerpt keyed by (kind, label) matches the span's check_id.
func ForkCheckDeclID(kind, label, root string) string {
	if kind == "storybook" {
		lower := strings.ToLower(label)
		switch {
		case strings.Contains(lower, "roster storybook"):
			return "storybook/roster"
		case strings.Contains(lower, "footer storybook"):
			return "storybook/footer"
		}
	}
	text := label
	if root != "" {
		text = strings.ReplaceAll(text, root, "{root}")
		if clean := filepath.Clean(root); clean != root {
			text = strings.ReplaceAll(text, clean, "{root}")
		}
	}
	text = forkCheckPidPattern.ReplaceAllString(text, "run-<pid>")
	slug := strings.Trim(forkCheckNonSlugPattern.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if slug == "" {
		return kind
	}
	return kind + "/" + slug
}

// ForkCheckSpanMeta holds the check span fields needed to match triage and report logs.
type ForkCheckSpanMeta struct {
	CheckID string `json:"check_id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Status  string `json:"status"`
}

// ReadFailingSpanMeta returns failing/errored/stalled check spans from spansPath.
func ReadFailingSpanMeta(spansPath string) []ForkCheckSpanMeta {
	if spansPath == "" {
		return nil
	}
	raw, err := os.ReadFile(spansPath)
	if err != nil {
		return nil
	}
	var doc struct {
		CheckSpans []ForkCheckSpanMeta `json:"check_spans"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []ForkCheckSpanMeta
	for _, s := range doc.CheckSpans {
		if s.CheckID == "" {
			continue
		}
		classified := ClassifyCheck(checkresult.Observation{Check: s.CheckID, Source: checkresult.TriageSpan, Word: s.Status})
		if classified.Outcome == checkresult.Fail || (classified.Outcome == checkresult.Unknown && (classified.Detail == checkresult.DetailError || classified.Detail == checkresult.DetailStalled)) {
			out = append(out, s)
		}
	}
	return out
}

// SpanMatchesKindLabel reports whether span matches the given kind/label under dir.
func SpanMatchesKindLabel(span ForkCheckSpanMeta, kind, label, dir string) bool {
	if kind == span.CheckID {
		return true
	}
	if span.Kind != "" && kind == span.Kind && label == span.Label {
		return true
	}
	if kind != "" && label != "" && ForkCheckDeclID(kind, label, dir) == span.CheckID {
		return true
	}
	return false
}

// ForkCheckTriageFile models <git-common-dir>/yolo-fork-check/triage.json.
type ForkCheckTriageFile struct {
	Commit string `json:"commit"`
	Checks []struct {
		Check      string `json:"check"`
		Label      string `json:"label"`
		Command    string `json:"command"`
		Reproduce  string `json:"reproduce"`
		SingleCase string `json:"single_case"`
		Cases      []struct {
			Name       string `json:"name"`
			File       string `json:"file"`
			Line       int    `json:"line"`
			Assertion  string `json:"assertion"`
			Reproduce  string `json:"reproduce"`
			SingleCase string `json:"single_case"`
		} `json:"cases"`
	} `json:"checks"`
}

// CommitMatchesRev reports whether a recorded commit prefix/full SHA matches rev.
func CommitMatchesRev(recorded, rev string) bool {
	recorded = strings.TrimSpace(recorded)
	rev = strings.TrimSpace(rev)
	if recorded == "" || rev == "" {
		return false
	}
	return recorded == rev || (len(recorded) >= 12 && strings.HasPrefix(rev, recorded)) || (len(rev) >= 12 && strings.HasPrefix(recorded, rev))
}

// ReadForkCheckTriageJSON reads triage.json under commonDir when its commit matches rev.
func ReadForkCheckTriageJSON(commonDir, rev string) []FailingCheck {
	if commonDir == "" || rev == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(commonDir, "yolo-fork-check", "triage.json"))
	if err != nil {
		return nil
	}
	var doc ForkCheckTriageFile
	if json.Unmarshal(raw, &doc) != nil || !CommitMatchesRev(doc.Commit, rev) {
		return nil
	}
	var out []FailingCheck
	for _, p := range doc.Checks {
		fc := FailingCheck{
			ID:        p.Check,
			Label:     p.Label,
			Reproduce: p.SingleCase,
		}
		if fc.Reproduce == "" {
			fc.Reproduce = p.Reproduce
		}
		for _, cs := range p.Cases {
			where := cs.File
			if where != "" && cs.Line > 0 {
				where = fmt.Sprintf("%s:%d", where, cs.Line)
			}
			repro := cs.SingleCase
			if repro == "" {
				repro = cs.Reproduce
			}
			if repro == "" {
				repro = fc.Reproduce
			}
			assertion := cs.Assertion
			if nl := strings.IndexByte(assertion, '\n'); nl >= 0 {
				assertion = assertion[:nl]
			}
			fc.Cases = append(fc.Cases, FailingCase{
				Name:      cs.Name,
				Where:     where,
				Assertion: assertion,
				Reproduce: repro,
			})
		}
		out = append(out, fc)
	}
	return out
}

// ForkCheckReportSection holds one check section from report.txt or stdout output.
type ForkCheckReportSection struct {
	Kind  string
	Label string
	Body  string
}

// ReadForkCheckReportTxt reads report.txt sections under commonDir when its commit matches rev.
func ReadForkCheckReportTxt(commonDir, rev string) []ForkCheckReportSection {
	if commonDir == "" || rev == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(commonDir, "yolo-fork-check", "report.txt"))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	matchedCommit := false
	for i := 0; i < len(lines) && i < 5; i++ {
		if m := reportCommitPattern.FindStringSubmatch(lines[i]); len(m) == 2 {
			if !CommitMatchesRev(m[1], rev) {
				return nil
			}
			matchedCommit = true
			break
		}
	}
	if !matchedCommit {
		return nil
	}
	var sections []ForkCheckReportSection
	var cur *ForkCheckReportSection
	var buf []string
	flush := func() {
		if cur != nil {
			cur.Body = strings.Join(buf, "\n")
			sections = append(sections, *cur)
			cur = nil
			buf = nil
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "=== ") {
			flush()
			rest := strings.TrimPrefix(line, "=== ")
			kind, label, ok := strings.Cut(rest, " -- ")
			if !ok {
				kind = strings.TrimSpace(rest)
			}
			cur = &ForkCheckReportSection{Kind: strings.TrimSpace(kind), Label: strings.TrimSpace(label)}
			buf = []string{line}
			continue
		}
		if cur != nil {
			buf = append(buf, line)
			if line == "--- end output ---" {
				flush()
			}
		}
	}
	flush()
	return sections
}

// ReadStdoutCheckSections extracts per-check `  run  <kind> ... <label>` blocks that
// include `       | ` failure lines from fork-check.py's stdout report.
func ReadStdoutCheckSections(text string) []ForkCheckReportSection {
	if text == "" {
		return nil
	}
	var sections []ForkCheckReportSection
	var cur *ForkCheckReportSection
	var buf []string
	hasPipe := false
	flush := func() {
		if cur != nil && hasPipe {
			cur.Body = strings.Join(buf, "\n")
			sections = append(sections, *cur)
		}
		cur = nil
		buf = nil
		hasPipe = false
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "  run  ") {
			flush()
			rest := strings.TrimSpace(strings.TrimPrefix(line, "  run  "))
			fields := strings.Fields(rest)
			if len(fields) >= 2 {
				kind := fields[0]
				label := strings.TrimSpace(strings.TrimPrefix(rest, kind))
				cur = &ForkCheckReportSection{Kind: kind, Label: label}
				buf = []string{line}
			}
			continue
		}
		if cur != nil {
			if strings.HasPrefix(line, "       ") {
				buf = append(buf, line)
				if strings.HasPrefix(line, "       | ") {
					hasPipe = true
				}
			} else {
				flush()
			}
		}
	}
	flush()
	return sections
}

// FormatTriageBlock formats failing checks as a fork-check triage block.
func FormatTriageBlock(checks []FailingCheck) string {
	if len(checks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "fork-check: triage -- %d failed check(s), diagnosed from this run's own output and never re-run:\n", len(checks))
	for _, fc := range checks {
		if fc.Label != "" {
			fmt.Fprintf(&b, "  triage %s: %s\n", fc.ID, fc.Label)
		} else {
			fmt.Fprintf(&b, "  triage %s: %s\n", fc.ID, fc.ID)
		}
		for _, cs := range fc.Cases {
			where := cs.Where
			if where == "" {
				where = "(no file named)"
			}
			fmt.Fprintf(&b, "       case: %s -- %s\n", cs.Name, where)
			if cs.Assertion != "" {
				fmt.Fprintf(&b, "       assertion: %s\n", cs.Assertion)
			}
			if cs.Reproduce != "" {
				fmt.Fprintf(&b, "       one case: %s\n", cs.Reproduce)
			}
		}
		if len(fc.Cases) == 0 && fc.Reproduce != "" {
			fmt.Fprintf(&b, "       no case named in the output; reproduce with: %s\n", fc.Reproduce)
		}
	}
	return b.String()
}

// ParseTriageReader scans fork-check's printed triage block from any line stream.
func ParseTriageReader(scanner *bufio.Scanner) []FailingCheck {
	var checks []FailingCheck
	in := false
	scanner.Buffer(make([]byte, 64<<10), triageScanMax)
	for scanner.Scan() {
		line := scanner.Text()
		if !in {
			in = strings.HasPrefix(line, "fork-check: triage -- ")
			continue
		}
		if line == "" || (line[0] != ' ' && line[0] != '\t') {
			break
		}
		text := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "  triage "):
			id, label, _ := strings.Cut(strings.TrimPrefix(text, "triage "), ": ")
			checks = append(checks, FailingCheck{ID: id, Label: label})
		case len(checks) == 0:
		case strings.HasPrefix(text, "case: "):
			name, where := strings.TrimPrefix(text, "case: "), ""
			if i := strings.LastIndex(name, " -- "); i >= 0 {
				name, where = name[:i], name[i+4:]
			}
			cur := &checks[len(checks)-1]
			cur.Cases = append(cur.Cases, FailingCase{Name: name, Where: where})
		case strings.HasPrefix(text, "assertion: "):
			if cur := &checks[len(checks)-1]; len(cur.Cases) > 0 {
				cur.Cases[len(cur.Cases)-1].Assertion = strings.TrimPrefix(text, "assertion: ")
			}
		case strings.HasPrefix(text, "reproduce (one file): "), strings.HasPrefix(text, "one case: "):
			cur := &checks[len(checks)-1]
			if len(cur.Cases) > 0 {
				_, cmd, _ := strings.Cut(text, ": ")
				c := &cur.Cases[len(cur.Cases)-1]
				if c.Reproduce == "" || strings.HasPrefix(text, "one case: ") {
					c.Reproduce = cmd
				}
			}
		case strings.HasPrefix(text, "no case named in the output; reproduce with: "):
			checks[len(checks)-1].Reproduce = strings.TrimPrefix(text, "no case named in the output; reproduce with: ")
		}
	}
	if scanner.Err() != nil {
		return nil
	}
	return checks
}

// ReadTriageString scans fork-check's printed triage block from text.
func ReadTriageString(text string) []FailingCheck {
	if text == "" {
		return nil
	}
	return ParseTriageReader(bufio.NewScanner(strings.NewReader(text)))
}

// SanitizeLogComponent replaces unsafe filename characters in id with '-'.
func SanitizeLogComponent(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, id)
}

// ExtractForkCheckLogs snapshots per-check triage packets and failure excerpts for the
// failing spans recorded in spansPath before any later replay or cleanup can overwrite
// <git-common-dir>/yolo-fork-check/{triage.json,report.txt}. Only artifacts whose
// recorded commit matches rev are read.
func ExtractForkCheckLogs(ctx context.Context, dir, rev, spansPath, capturedLogPath, capturedText, outDir string) map[string]string {
	spans := ReadFailingSpanMeta(spansPath)
	if len(spans) == 0 || outDir == "" {
		return nil
	}
	if capturedText == "" && capturedLogPath != "" {
		if raw, err := os.ReadFile(capturedLogPath); err == nil {
			capturedText = string(raw)
		}
	}
	if rev == "" && dir != "" {
		rev, _ = heavy.GitOutput(ctx, dir, "rev-parse", "HEAD")
	}
	commonDir := ""
	if dir != "" {
		commonDir, _ = heavy.GitOutput(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	}
	triageFromStdout := ReadTriageString(capturedText)
	triageFromJSON := ReadForkCheckTriageJSON(commonDir, rev)
	reportSections := ReadForkCheckReportTxt(commonDir, rev)
	stdoutSections := ReadStdoutCheckSections(capturedText)

	var out map[string]string
	for idx, span := range spans {
		var matchedTriage []FailingCheck
		for _, fc := range triageFromStdout {
			if SpanMatchesKindLabel(span, fc.ID, fc.Label, dir) {
				matchedTriage = append(matchedTriage, fc)
			}
		}
		if len(matchedTriage) == 0 {
			for _, fc := range triageFromJSON {
				if SpanMatchesKindLabel(span, fc.ID, fc.Label, dir) {
					matchedTriage = append(matchedTriage, fc)
				}
			}
		}
		excerpt := ""
		for _, sec := range reportSections {
			if SpanMatchesKindLabel(span, sec.Kind, sec.Label, dir) {
				excerpt = sec.Body
				break
			}
		}
		if excerpt == "" {
			for _, sec := range stdoutSections {
				if SpanMatchesKindLabel(span, sec.Kind, sec.Label, dir) {
					excerpt = sec.Body
					break
				}
			}
		}
		if len(matchedTriage) == 0 && excerpt == "" && strings.TrimSpace(capturedText) == "" {
			continue
		}
		var combined strings.Builder
		if block := FormatTriageBlock(matchedTriage); block != "" {
			combined.WriteString(block)
		}
		switch {
		case excerpt != "":
			if combined.Len() > 0 {
				combined.WriteByte('\n')
			}
			combined.WriteString(strings.TrimRight(excerpt, "\n") + "\n")
		case strings.TrimSpace(capturedText) != "":
			if combined.Len() > 0 {
				combined.WriteByte('\n')
			}
			combined.WriteString(strings.TrimRight(capturedText, "\n") + "\n")
		}
		if combined.Len() == 0 {
			continue
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			continue
		}
		dst := filepath.Join(outDir, fmt.Sprintf("fork-check-%02d-%s.log", idx, SanitizeLogComponent(span.CheckID)))
		if err := os.WriteFile(dst, []byte(combined.String()), 0o644); err != nil {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[span.CheckID] = dst
	}
	return out
}

// CopyLogWithWhole copies src into dstDir under dstName, and when src ends in
// `.triage.log` with a readable `.log` sibling, copies that whole log beside it too and
// updates the triage header's pointer so both copies stay self-contained with the run.
func CopyLogWithWhole(root, src, dstDir, dstName string) (string, bool) {
	if src == "" || dstDir == "" || dstName == "" {
		return "", false
	}
	resolvedSrc := src
	body, err := os.ReadFile(resolvedSrc)
	if err != nil && root != "" && !filepath.IsAbs(resolvedSrc) {
		resolvedSrc = filepath.Join(root, resolvedSrc)
		body, err = os.ReadFile(resolvedSrc)
	}
	if err != nil {
		return "", false
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", false
	}
	dst := filepath.Join(dstDir, dstName)
	if strings.HasSuffix(resolvedSrc, ".triage.log") && strings.HasSuffix(dstName, ".triage.log") {
		srcWhole := strings.TrimSuffix(resolvedSrc, ".triage.log") + ".log"
		if wholeBody, wErr := os.ReadFile(srcWhole); wErr == nil {
			dstWhole := strings.TrimSuffix(dst, ".triage.log") + ".log"
			if os.WriteFile(dstWhole, wholeBody, 0o644) == nil {
				if absWhole, absErr := filepath.Abs(dstWhole); absErr == nil {
					dstWhole = absWhole
				}
				body = []byte(strings.ReplaceAll(string(body), srcWhole, dstWhole))
			}
		}
	}
	if os.WriteFile(dst, body, 0o644) != nil {
		return "", false
	}
	if abs, absErr := filepath.Abs(dst); absErr == nil {
		return abs, true
	}
	return dst, true
}

// FixerLogExcerpt extracts a bounded failure excerpt from raw log bytes.
func FixerLogExcerpt(raw []byte) string {
	const window = 64 << 10
	trimmed := false
	if len(raw) > window {
		raw = raw[len(raw)-window:]
		trimmed = true
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if trimmed && len(lines) > 0 {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	keep := map[int]bool{}
	picked := 0
	for i := len(lines) - 1; i >= 0 && picked < 14; i-- {
		l := lines[i]
		if strings.Contains(l, "FAIL") || strings.Contains(l, "panic:") || strings.Contains(l, "AssertionError") || strings.Contains(l, "Error:") || strings.Contains(l, "Traceback") || strings.Contains(l, "fatal:") {
			keep[i] = true
			picked++
			if strings.Contains(l, "--- FAIL:") {
				for j := i + 1; j < len(lines) && j <= i+2; j++ {
					if strings.HasPrefix(lines[j], " ") || strings.HasPrefix(lines[j], "\t") {
						keep[j] = true
					}
				}
			}
		}
	}
	for i := max(0, len(lines)-5); i < len(lines); i++ {
		keep[i] = true
	}
	var out []string
	for i, line := range lines {
		if keep[i] {
			out = append(out, line)
		}
	}
	if len(out) > 20 {
		out = out[len(out)-20:]
	}
	joined := strings.Join(out, "\n")
	if len(joined) > 1500 {
		joined = "..." + joined[len(joined)-1500:]
	}
	return joined
}

// WriteReplayCheckLog writes a replay diagnostic log for checkID under outDir.
func WriteReplayCheckLog(ctx context.Context, dir, rev, checkID, capturedText, outDir string) string {
	if outDir == "" || checkID == "" {
		return ""
	}
	span := ForkCheckSpanMeta{CheckID: checkID, Status: "FAIL"}
	triage := ReadTriageString(capturedText)
	excerpt := ""
	if len(triage) == 0 && dir != "" && rev != "" {
		if commonDir, err := heavy.GitOutput(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && commonDir != "" {
			for _, fc := range ReadForkCheckTriageJSON(commonDir, rev) {
				if SpanMatchesKindLabel(span, fc.ID, fc.Label, dir) {
					triage = append(triage, fc)
				}
			}
			if strings.TrimSpace(capturedText) == "" {
				for _, sec := range ReadForkCheckReportTxt(commonDir, rev) {
					if SpanMatchesKindLabel(span, sec.Kind, sec.Label, dir) {
						excerpt = sec.Body
						break
					}
				}
			}
		}
	}
	var combined strings.Builder
	if len(ReadTriageString(capturedText)) == 0 {
		if block := FormatTriageBlock(triage); block != "" {
			combined.WriteString(block)
		}
	}
	switch {
	case strings.TrimSpace(capturedText) != "":
		if combined.Len() > 0 {
			combined.WriteByte('\n')
		}
		combined.WriteString(strings.TrimRight(capturedText, "\n") + "\n")
	case excerpt != "":
		if combined.Len() > 0 {
			combined.WriteByte('\n')
		}
		combined.WriteString(strings.TrimRight(excerpt, "\n") + "\n")
	}
	if combined.Len() == 0 {
		return ""
	}
	if os.MkdirAll(outDir, 0o755) != nil {
		return ""
	}
	dst := filepath.Join(outDir, "replay-"+SanitizeLogComponent(checkID)+".log")
	if os.WriteFile(dst, []byte(combined.String()), 0o644) != nil {
		return ""
	}
	return dst
}

// FilterCheckLogs returns only entries from logs whose keys appear in allowed with non-empty values.
func FilterCheckLogs(logs map[string]string, allowed []string) map[string]string {
	if len(logs) == 0 || len(allowed) == 0 {
		return nil
	}
	want := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		if id != "" {
			want[id] = true
		}
	}
	var out map[string]string
	for id, ref := range logs {
		if want[id] && ref != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[id] = ref
		}
	}
	return out
}
