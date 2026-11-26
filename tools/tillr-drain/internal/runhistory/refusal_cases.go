package runhistory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/remotesync"
)

// A refused worker is told what failed in its own words, at the top of the answer, on both
// backends: the pi worker's parked `land` call gets it as the call's result
// (answerLandingRefusal) and a Claude worker's resumed turn opens with it (returnedBrief).
// Both read the same recorded fields, EndingCheck.Failing, so there is one composition.
//
// The source is the report the gate already wrote for this run: the judging program prints
// its triage packet into the gate's whole log (`triage <check>: <label>`, then one `case:`
// line per failing case with its `assertion:`, `reproduce (one file):` and `one case:`
// lines), and tools/tillr-gate keeps that log beside the run's evidence. The shared
// .git/yolo-fork-check/triage.json is not read: the newest failing run anywhere rewrites it,
// so it can describe another run's candidate. Nothing here is guessed; a log with no such
// block yields no cases, and the answer says so.

// FailingCase is one failing case as the gate's triage block named it.
type FailingCase = remotesync.FailingCase

// FailingCheck is one check the gate's own report named as failed, with its cases.
type FailingCheck = remotesync.FailingCheck

const (
	FailureHeading = "The gate's answer to your delivery:"
	NoCaseRecorded = "no failing case was recorded"
	// FailureSummaryMax bounds the summary; it is cut between cases, never inside a line.
	FailureSummaryMax = 4000
	TriageScanMax     = 4 << 20
)

// ReadTriageBlock reads the failing checks from the gate's whole log. logRef is the check's
// recorded log: either the whole log or its bounded `.triage.log` sibling, whose whole log
// is the name without `.triage`. An unreadable or block-less log answers nothing.
func ReadTriageBlock(logRef string) []FailingCheck {
	f, err := openCheckLog(logRef)
	if err != nil {
		return nil
	}
	defer f.Close()
	var checks []FailingCheck
	in := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), TriageScanMax)
	for scanner.Scan() {
		line := scanner.Text()
		if !in {
			in = strings.HasPrefix(line, "fork-check: triage -- ")
			continue
		}
		if line == "" || (line[0] != ' ' && line[0] != '\t') {
			break // the block is the indented lines after its heading
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
				// A case's own one-case command wins over its one-file command.
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

func openCheckLog(logRef string) (*os.File, error) {
	if logRef == "" {
		return nil, os.ErrNotExist
	}
	whole := strings.TrimSuffix(logRef, ".triage.log")
	if whole != logRef {
		whole += ".log"
	}
	f, err := os.Open(whole)
	if err != nil && whole != logRef {
		f, err = os.Open(logRef)
	}
	return f, err
}

type GoTestFailure struct {
	Package string
	Test    string
}

type GoTestLogResult struct {
	Failures      []GoTestFailure
	SawTestResult bool
}

// ReadGoTestFailures scans a check's log (following `.triage.log` to its whole `.log`
// sibling) for Go test failures and package results when no triage packet recorded cases.
func ReadGoTestFailures(logRef string) GoTestLogResult {
	f, err := openCheckLog(logRef)
	if err != nil {
		return GoTestLogResult{}
	}
	defer f.Close()

	var (
		res           GoTestLogResult
		pendingTests  []string
		inGoAffected  bool
		recorded      []GoTestFailure
		seen          = map[GoTestFailure]bool{}
		testsWithPkg  = map[string]bool{}
		pkgsWithTests = map[string]bool{}
	)
	addPending := func(name string) {
		for _, p := range pendingTests {
			if p == name {
				return
			}
		}
		pendingTests = append(pendingTests, name)
	}
	record := func(pkg, test string) {
		key := GoTestFailure{Package: pkg, Test: test}
		if seen[key] {
			return
		}
		seen[key] = true
		recorded = append(recorded, key)
		if pkg != "" && test != "" {
			testsWithPkg[test] = true
			pkgsWithTests[pkg] = true
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), TriageScanMax)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			inGoAffected = false
			continue
		}
		if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"Action"`) {
			var ev struct {
				Action  string `json:"Action"`
				Package string `json:"Package"`
				Test    string `json:"Test"`
			}
			if json.Unmarshal([]byte(trimmed), &ev) == nil && ev.Action != "" {
				if ev.Action == "pass" || ev.Action == "fail" || ev.Action == "skip" {
					res.SawTestResult = true
					if ev.Action == "fail" {
						if ev.Test != "" {
							record(ev.Package, ev.Test)
						} else if ev.Package != "" {
							record(ev.Package, "")
						}
					}
				}
				continue
			}
		}
		if shardLine, shardNames := ParseShardFailureLine(trimmed); shardLine != "" {
			res.SawTestResult = true
			rest := strings.TrimPrefix(shardLine, "go-test-shards: ")
			pkg := ""
			if fields := strings.Fields(rest); len(fields) >= 3 && fields[1] == "shard" {
				pkg = fields[0]
			}
			if len(shardNames) > 0 {
				for _, name := range shardNames {
					record(pkg, name)
				}
			} else if pkg != "" {
				record(pkg, "")
			}
			continue
		}
		if trimmed == "go-affected: failing packages and tests:" {
			inGoAffected = true
			res.SawTestResult = true
			continue
		}
		if inGoAffected {
			if strings.HasPrefix(line, "  ") {
				if pkg, rest, ok := strings.Cut(trimmed, ":"); ok {
					pkg = strings.TrimSpace(pkg)
					rest = strings.TrimSpace(rest)
					if rest != "" && !strings.Contains(rest, "(no failing test named)") && !strings.Contains(rest, "(package failure with no named test)") {
						for _, part := range strings.Split(rest, ",") {
							if name := strings.TrimSpace(part); name != "" {
								record(pkg, name)
							}
						}
					} else if pkg != "" {
						record(pkg, "")
					}
					continue
				}
			}
			inGoAffected = false
		}
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			res.SawTestResult = true
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "--- FAIL:"))
			if fields := strings.Fields(rest); len(fields) > 0 {
				addPending(fields[0])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "--- PASS:") || strings.HasPrefix(trimmed, "--- SKIP:") {
			res.SawTestResult = true
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "FAIL"); ok {
			if rest == "" {
				res.SawTestResult = true
				continue
			}
			if rest[0] == '\t' || rest[0] == ' ' {
				if fields := strings.Fields(rest); len(fields) >= 1 {
					pkg := fields[0]
					res.SawTestResult = true
					if len(pendingTests) > 0 {
						for _, t := range pendingTests {
							record(pkg, t)
						}
						pendingTests = nil
					} else {
						record(pkg, "")
					}
					continue
				}
			}
		}
		if strings.HasPrefix(trimmed, "ok\t") || strings.HasPrefix(trimmed, "ok  \t") ||
			strings.HasPrefix(trimmed, "?\t") || strings.HasPrefix(trimmed, "?   \t") {
			if fields := strings.Fields(trimmed); len(fields) >= 2 && (fields[0] == "ok" || fields[0] == "?") {
				res.SawTestResult = true
				pendingTests = nil
				continue
			}
		}
	}
	if scanner.Err() != nil {
		return GoTestLogResult{}
	}
	for _, t := range pendingTests {
		if !testsWithPkg[t] {
			record("", t)
		}
	}
	// Drop a parent test when one of its subtests failed in the same package, and drop
	// bare package failures when named failing tests were recorded for that package.
	hasSubtest := func(pkg, test string) bool {
		prefix := test + "/"
		for _, r := range recorded {
			if r.Package == pkg && strings.HasPrefix(r.Test, prefix) {
				return true
			}
		}
		return false
	}
	for _, r := range recorded {
		if r.Test == "" && pkgsWithTests[r.Package] {
			continue
		}
		if r.Test != "" && hasSubtest(r.Package, r.Test) {
			continue
		}
		res.Failures = append(res.Failures, r)
	}
	return res
}

// FailureSummary is the answer's opening: each failing check, each failing case's test
// name, file and line, assertion and reproduce command, then the instruction to run that
// named test. A check with no recorded case says "no failing case was recorded" and keeps
// its log path, as does a refusal that recorded no check. Callers use it for gate refusals.
func FailureSummary(checks []EndingCheck) string {
	var b strings.Builder
	b.WriteString(FailureHeading + "\n")
	if len(checks) == 0 {
		b.WriteString("the gate refused it, and " + NoCaseRecorded + "\n")
		return b.String()
	}
	ran := false
	for _, c := range checks {
		if len(c.Failing) == 0 {
			goRes := ReadGoTestFailures(c.LogRef)
			if len(goRes.Failures) > 0 {
				var part strings.Builder
				fmt.Fprintf(&part, "check %s\n", c.CheckID)
				for _, gf := range goRes.Failures {
					if gf.Test != "" {
						fmt.Fprintf(&part, "  case: %s\n", gf.Test)
						if gf.Package != "" {
							fmt.Fprintf(&part, "    package: %s\n", gf.Package)
						}
					} else if gf.Package != "" {
						fmt.Fprintf(&part, "  package: %s (no failing test named)\n", gf.Package)
					}
				}
				if b.Len()+part.Len() > FailureSummaryMax {
					b.WriteString("(more failing checks omitted; the gate's log has them)\n")
					goto done
				}
				b.WriteString(part.String())
				continue
			}
			if goRes.SawTestResult {
				fmt.Fprintf(&b, "check %s: Go tests passed", c.CheckID)
				if c.LogRef != "" {
					fmt.Fprintf(&b, "; the gate's log is %s", c.LogRef)
				}
				b.WriteString("\n")
				continue
			}
			fmt.Fprintf(&b, "check %s: %s", c.CheckID, NoCaseRecorded)
			if c.LogRef != "" {
				fmt.Fprintf(&b, "; the gate's log is %s", c.LogRef)
			}
			b.WriteString("\n")
			continue
		}
		for _, fc := range c.Failing {
			head := "check " + fc.ID
			if fc.Label != "" {
				head += " (" + fc.Label + ")"
			}
			var part strings.Builder
			part.WriteString(head + "\n")
			for _, cs := range fc.Cases {
				fmt.Fprintf(&part, "  case: %s\n", cs.Name)
				if cs.Where != "" {
					fmt.Fprintf(&part, "    at: %s\n", cs.Where)
				}
				if cs.Assertion != "" {
					fmt.Fprintf(&part, "    assertion: %s\n", cs.Assertion)
				}
				if cs.Reproduce != "" {
					fmt.Fprintf(&part, "    reproduce only this case: %s\n", cs.Reproduce)
					ran = true
				}
			}
			if len(fc.Cases) == 0 {
				var goRes GoTestLogResult
				if len(c.Failing) == 1 {
					goRes = ReadGoTestFailures(c.LogRef)
				}
				if len(goRes.Failures) > 0 {
					for _, gf := range goRes.Failures {
						if gf.Test != "" {
							fmt.Fprintf(&part, "  case: %s\n", gf.Test)
							if gf.Package != "" {
								fmt.Fprintf(&part, "    package: %s\n", gf.Package)
							}
						} else if gf.Package != "" {
							fmt.Fprintf(&part, "  package: %s (no failing test named)\n", gf.Package)
						}
					}
					if fc.Reproduce != "" {
						fmt.Fprintf(&part, "  reproduce with: %s\n", fc.Reproduce)
						ran = true
					}
				} else if goRes.SawTestResult {
					fmt.Fprintf(&part, "  Go tests passed")
					if fc.Reproduce != "" {
						fmt.Fprintf(&part, "; reproduce with: %s", fc.Reproduce)
						ran = true
					}
					part.WriteString("\n")
				} else {
					fmt.Fprintf(&part, "  %s", NoCaseRecorded)
					if fc.Reproduce != "" {
						fmt.Fprintf(&part, "; reproduce with: %s", fc.Reproduce)
						ran = true
					}
					part.WriteString("\n")
				}
			}
			if b.Len()+part.Len() > FailureSummaryMax {
				b.WriteString("(more failing checks omitted; the gate's log has them)\n")
				goto done
			}
			b.WriteString(part.String())
		}
	}
done:
	if ran {
		b.WriteString("Run the named test with its command above, not the whole suite and not the gate.\n")
	}
	return b.String()
}

const FixerFailureHeading = "The proof's output for the red trunk:"

// FixerFailureSummary formats the failing checks' triage cases, reproduce commands, and
// bounded output excerpts for a trunk fixer's brief, mirroring FailureSummary for gate
// refusals so a fixer worker never reruns a whole suite to discover what failed.
func FixerFailureSummary(checks []EndingCheck, excerpts map[string]string) string {
	oneLine := func(s string) string {
		s = strings.ReplaceAll(s, "\r", " ")
		return strings.ReplaceAll(s, "\n", " ")
	}
	var b strings.Builder
	b.WriteString(FixerFailureHeading + "\n")
	ran := false
	for _, c := range checks {
		if len(c.Failing) == 0 {
			fmt.Fprintf(&b, "check %s: %s", oneLine(c.CheckID), NoCaseRecorded)
			if c.LogRef != "" {
				fmt.Fprintf(&b, "; the proof's log is %s", oneLine(c.LogRef))
			}
			b.WriteString("\n")
			if ex := strings.TrimSpace(excerpts[c.CheckID]); ex != "" {
				for _, line := range strings.Split(ex, "\n") {
					line = strings.ReplaceAll(line, "\r", "")
					if b.Len()+len(line)+5 > FailureSummaryMax {
						b.WriteString("  (more output omitted; read the proof's log above)\n")
						break
					}
					b.WriteString("  | " + line + "\n")
				}
			}
			continue
		}
		for _, fc := range c.Failing {
			head := "check " + oneLine(fc.ID)
			if fc.Label != "" {
				head += " (" + oneLine(fc.Label) + ")"
			}
			var part strings.Builder
			part.WriteString(head + "\n")
			for _, cs := range fc.Cases {
				fmt.Fprintf(&part, "  case: %s\n", oneLine(cs.Name))
				if cs.Where != "" {
					fmt.Fprintf(&part, "    at: %s\n", oneLine(cs.Where))
				}
				if cs.Assertion != "" {
					fmt.Fprintf(&part, "    assertion: %s\n", oneLine(cs.Assertion))
				}
				if cs.Reproduce != "" {
					fmt.Fprintf(&part, "    reproduce only this case: %s\n", oneLine(cs.Reproduce))
					ran = true
				}
			}
			if len(fc.Cases) == 0 {
				fmt.Fprintf(&part, "  %s", NoCaseRecorded)
				if fc.Reproduce != "" {
					fmt.Fprintf(&part, "; reproduce with: %s", oneLine(fc.Reproduce))
					ran = true
				}
				part.WriteString("\n")
				if ex := strings.TrimSpace(excerpts[c.CheckID]); ex != "" {
					for _, line := range strings.Split(ex, "\n") {
						part.WriteString("  | " + strings.ReplaceAll(line, "\r", "") + "\n")
					}
				}
			}
			if b.Len()+part.Len() > FailureSummaryMax {
				b.WriteString("(more failing checks omitted; the proof's logs below have them)\n")
				goto done
			}
			b.WriteString(part.String())
		}
	}
done:
	if ran {
		b.WriteString("Run the named test with its command above, not the whole suite and not the gate.\n")
	}
	return b.String()
}

func IsShardSlotNoticeLine(line string) bool {
	return strings.HasPrefix(line, "go-test-shards: ") &&
		(strings.Contains(line, " waiting for a test slot") || strings.Contains(line, " yielding to a waiting "))
}

func ParseShardFailureLine(trimmed string) (string, []string) {
	candidate := trimmed
	if strings.HasPrefix(candidate, "tillr-roll: ") {
		if idx := strings.Index(candidate, "go-test-shards: "); idx >= 0 {
			candidate = candidate[idx:]
		}
	}
	if strings.HasPrefix(candidate, "go-test-shards: ") {
		if IsShardSlotNoticeLine(candidate) || strings.Contains(candidate, " tests passed,") {
			return "", nil
		}
		return candidate, shardFailureTestNames(candidate)
	}
	fields := strings.Fields(candidate)
	if len(fields) >= 4 && fields[1] == "shard" && (fields[3] == "failed" || fields[3] == "timed" || fields[3] == "cannot" || fields[3] == "missing") {
		normalized := "go-test-shards: " + candidate
		return normalized, shardFailureTestNames(normalized)
	}
	return "", nil
}

func shardFailureTestNames(line string) []string {
	idx := strings.Index(line, "failed (exit ")
	if idx < 0 {
		return nil
	}
	rest := line[idx:]
	colon := strings.Index(rest, "):")
	if colon < 0 {
		return nil
	}
	var names []string
	for _, part := range strings.Split(rest[colon+2:], ",") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}
