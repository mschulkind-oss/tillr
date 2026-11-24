package drain

import (
	"bufio"
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
	failureHeading = "The gate's answer to your delivery:"
	noCaseRecorded = "no failing case was recorded"
	// failureSummaryMax bounds the summary; it is cut between cases, never inside a line.
	failureSummaryMax = 4000
	triageScanMax     = 4 << 20
)

// readTriageBlock reads the failing checks from the gate's whole log. logRef is the check's
// recorded log: either the whole log or its bounded `.triage.log` sibling, whose whole log
// is the name without `.triage`. An unreadable or block-less log answers nothing.
func readTriageBlock(logRef string) []FailingCheck {
	if logRef == "" {
		return nil
	}
	whole := strings.TrimSuffix(logRef, ".triage.log")
	if whole != logRef {
		whole += ".log"
	}
	f, err := os.Open(whole)
	if err != nil && whole != logRef {
		f, err = os.Open(logRef)
	}
	if err != nil {
		return nil
	}
	defer f.Close()
	var checks []FailingCheck
	in := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), triageScanMax)
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

// failureSummary is the answer's opening: each failing check, each failing case's test
// name, file and line, assertion and reproduce command, then the instruction to run that
// named test. A check with no recorded case says "no failing case was recorded" and keeps
// its log path, as does a refusal that recorded no check. Callers use it for gate refusals.
func failureSummary(checks []EndingCheck) string {
	var b strings.Builder
	b.WriteString(failureHeading + "\n")
	if len(checks) == 0 {
		b.WriteString("the gate refused it, and " + noCaseRecorded + "\n")
		return b.String()
	}
	ran := false
	for _, c := range checks {
		if len(c.Failing) == 0 {
			fmt.Fprintf(&b, "check %s: %s", c.CheckID, noCaseRecorded)
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
				fmt.Fprintf(&part, "  %s", noCaseRecorded)
				if fc.Reproduce != "" {
					fmt.Fprintf(&part, "; reproduce with: %s", fc.Reproduce)
					ran = true
				}
				part.WriteString("\n")
			}
			if b.Len()+part.Len() > failureSummaryMax {
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

const fixerFailureHeading = "The proof's output for the red trunk:"

// fixerFailureSummary formats the failing checks' triage cases, reproduce commands, and
// bounded output excerpts for a trunk fixer's brief, mirroring failureSummary for gate
// refusals so a fixer worker never reruns a whole suite to discover what failed.
func fixerFailureSummary(checks []EndingCheck, excerpts map[string]string) string {
	oneLine := func(s string) string {
		s = strings.ReplaceAll(s, "\r", " ")
		return strings.ReplaceAll(s, "\n", " ")
	}
	var b strings.Builder
	b.WriteString(fixerFailureHeading + "\n")
	ran := false
	for _, c := range checks {
		if len(c.Failing) == 0 {
			fmt.Fprintf(&b, "check %s: %s", oneLine(c.CheckID), noCaseRecorded)
			if c.LogRef != "" {
				fmt.Fprintf(&b, "; the proof's log is %s", oneLine(c.LogRef))
			}
			b.WriteString("\n")
			if ex := strings.TrimSpace(excerpts[c.CheckID]); ex != "" {
				for _, line := range strings.Split(ex, "\n") {
					line = strings.ReplaceAll(line, "\r", "")
					if b.Len()+len(line)+5 > failureSummaryMax {
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
				fmt.Fprintf(&part, "  %s", noCaseRecorded)
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
			if b.Len()+part.Len() > failureSummaryMax {
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
