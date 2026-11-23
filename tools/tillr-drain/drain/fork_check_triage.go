package drain

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr/store"
)

type (
	forkCheckSpanMeta      = remotesync.ForkCheckSpanMeta
	forkCheckTriageFile    = remotesync.ForkCheckTriageFile
	forkCheckReportSection = remotesync.ForkCheckReportSection
)

func forkCheckDeclID(kind, label, root string) string {
	return remotesync.ForkCheckDeclID(kind, label, root)
}

func readFailingSpanMeta(spansPath string) []forkCheckSpanMeta {
	return remotesync.ReadFailingSpanMeta(spansPath)
}

func spanMatchesKindLabel(span forkCheckSpanMeta, kind, label, dir string) bool {
	return remotesync.SpanMatchesKindLabel(span, kind, label, dir)
}

func commitMatchesRev(recorded, rev string) bool {
	return remotesync.CommitMatchesRev(recorded, rev)
}

func readForkCheckTriageJSON(commonDir, rev string) []FailingCheck {
	return remotesync.ReadForkCheckTriageJSON(commonDir, rev)
}

func readForkCheckReportTxt(commonDir, rev string) []forkCheckReportSection {
	return remotesync.ReadForkCheckReportTxt(commonDir, rev)
}

func readStdoutCheckSections(text string) []forkCheckReportSection {
	return remotesync.ReadStdoutCheckSections(text)
}

func formatTriageBlock(checks []FailingCheck) string {
	return remotesync.FormatTriageBlock(checks)
}

func parseTriageReader(scanner *bufio.Scanner) []FailingCheck {
	return remotesync.ParseTriageReader(scanner)
}

func readTriageString(text string) []FailingCheck {
	return remotesync.ReadTriageString(text)
}

func sanitizeLogComponent(id string) string {
	return remotesync.SanitizeLogComponent(id)
}

func extractForkCheckLogs(ctx context.Context, dir, rev, spansPath, capturedLogPath, capturedText, outDir string) map[string]string {
	return remotesync.ExtractForkCheckLogs(ctx, dir, rev, spansPath, capturedLogPath, capturedText, outDir)
}

func copyLogWithWhole(root, src, dstDir, dstName string) (string, bool) {
	return remotesync.CopyLogWithWhole(root, src, dstDir, dstName)
}

type pendingLogCopy struct {
	path string
	body []byte
}

func fixerLogExcerpt(raw []byte) string {
	return remotesync.FixerLogExcerpt(raw)
}

func (s *LaneStager) prepareTrunkFixerFacts(ctx context.Context, recorded stagedItem, spec StageSpec) (string, []pendingLogCopy, error) {
	if !IsTrunkFixer(spec.ItemID) || s.Store == "" {
		return "", nil, nil
	}
	r, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return "", nil, err
	}
	defer r.Close()
	recordedLogs := map[string]string{}
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		rows, qErr := reader.Query(`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq ASC`, FixerLogsKind, spec.ItemID)
		if qErr != nil {
			return qErr
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if sErr := rows.Scan(&raw); sErr != nil {
				return sErr
			}
			var p struct {
				CheckLogs map[string]string `json:"check_logs"`
			}
			if json.Unmarshal([]byte(raw), &p) == nil {
				for id, ref := range p.CheckLogs {
					if id != "" && ref != "" {
						recordedLogs[id] = ref
					}
				}
			}
		}
		return rows.Err()
	})
	if err != nil || len(recordedLogs) == 0 {
		return "", nil, err
	}
	seen := map[string]bool{}
	var ordered []string
	for _, c := range recorded.Criteria {
		if c.CriterionID == "trunk-green" {
			for _, id := range fixerChecks(c.Text) {
				if !seen[id] {
					seen[id] = true
					ordered = append(ordered, id)
				}
			}
		}
	}
	var extra []string
	for id := range recordedLogs {
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	if len(extra) > 0 {
		// Preserve deterministic ordering across any check ids not listed in trunk-green.
		for i := 0; i < len(extra); i++ {
			for j := i + 1; j < len(extra); j++ {
				if extra[j] < extra[i] {
					extra[i], extra[j] = extra[j], extra[i]
				}
			}
		}
		ordered = append(ordered, extra...)
	}
	logsDir := filepath.Join(RunDir(s.Root, spec.RunID), "fixer.logs")
	if absDir, absErr := filepath.Abs(logsDir); absErr == nil {
		logsDir = absDir
	}
	var (
		checks    []EndingCheck
		excerpts  = map[string]string{}
		copies    []pendingLogCopy
		seenNames = map[string]bool{}
	)
	for idx, id := range ordered {
		src := recordedLogs[id]
		if src == "" {
			checks = append(checks, EndingCheck{CheckID: id})
			continue
		}
		resolvedSrc := src
		body, readErr := os.ReadFile(resolvedSrc)
		if readErr != nil && s.Root != "" && !filepath.IsAbs(resolvedSrc) {
			resolvedSrc = filepath.Join(s.Root, resolvedSrc)
			body, readErr = os.ReadFile(resolvedSrc)
		}
		if readErr != nil {
			checks = append(checks, EndingCheck{CheckID: id, LogRef: src})
			continue
		}
		dstName := filepath.Base(resolvedSrc)
		if dstName == "" || dstName == "." || dstName == "/" || seenNames[dstName] {
			suffix := ".log"
			if strings.HasSuffix(resolvedSrc, ".triage.log") {
				suffix = ".triage.log"
			}
			dstName = fmt.Sprintf("%02d-%s%s", idx, sanitizeLogComponent(id), suffix)
		}
		seenNames[dstName] = true
		dst := filepath.Join(logsDir, dstName)
		wholeBody := body
		if strings.HasSuffix(resolvedSrc, ".triage.log") && strings.HasSuffix(dstName, ".triage.log") {
			srcWhole := strings.TrimSuffix(resolvedSrc, ".triage.log") + ".log"
			if wBody, wErr := os.ReadFile(srcWhole); wErr == nil {
				dstWhole := strings.TrimSuffix(dst, ".triage.log") + ".log"
				copies = append(copies, pendingLogCopy{path: dstWhole, body: wBody})
				body = []byte(strings.ReplaceAll(string(body), srcWhole, dstWhole))
				wholeBody = wBody
			}
		}
		copies = append(copies, pendingLogCopy{path: dst, body: body})
		failing := readTriageString(string(wholeBody))
		if len(failing) == 0 && len(wholeBody) != len(body) {
			failing = readTriageString(string(body))
		}
		if len(failing) == 0 {
			excerpts[id] = fixerLogExcerpt(wholeBody)
		}
		checks = append(checks, EndingCheck{CheckID: id, LogRef: dst, Failing: failing})
	}
	if len(checks) == 0 {
		return "", nil, nil
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(fixerFailureSummary(checks, excerpts), "\n"))
	b.WriteString("\n\nFailing trunk checks, with each one's output kept with this run:\n")
	for _, c := range checks {
		if c.LogRef != "" {
			fmt.Fprintf(&b, "- `%s`: %s\n", c.CheckID, c.LogRef)
		}
	}
	return strings.TrimRight(b.String(), "\n"), copies, nil
}

func writeReplayCheckLog(ctx context.Context, dir, rev, checkID, capturedText, outDir string) string {
	return remotesync.WriteReplayCheckLog(ctx, dir, rev, checkID, capturedText, outDir)
}

func filterCheckLogs(logs map[string]string, allowed []string) map[string]string {
	return remotesync.FilterCheckLogs(logs, allowed)
}
