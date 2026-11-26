package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// gateErrorTriage reads the bounded triage log(s) named by the run's gate.verdict
// record so the drain can tell a gate program that could not start (for example,
// python3 unable to open a moved script in the candidate's tree) from a gate that
// was interrupted mid-run.
func (s *ProcessSteps) gateErrorTriage(candidate Candidate) string {
	if s.Root == "" || candidate.RunID == "" {
		return ""
	}
	runDir := RunDir(s.Root, candidate.RunID)
	payload, ok := readRecordPayload(filepath.Join(runDir, GateVerdictRecord))
	if !ok {
		return ""
	}
	var v struct {
		Checks []struct {
			LogRef string `json:"log_ref"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(payload, &v); err != nil {
		return ""
	}
	var parts []string
	for _, c := range v.Checks {
		if c.LogRef == "" {
			continue
		}
		raw, err := os.ReadFile(c.LogRef)
		if err != nil && !filepath.IsAbs(c.LogRef) {
			raw, err = os.ReadFile(filepath.Join(runDir, c.LogRef))
		}
		if err == nil && len(raw) > 0 {
			parts = append(parts, string(raw))
		}
	}
	return strings.Join(parts, "\n")
}

// gateStartErrorDetail reports the hold detail when the gate program could not start
// against this candidate (so gating the same commit again cannot reach a different
// answer). It returns "" when the gate was interrupted or failed after starting.
func gateStartErrorDetail(stderr, triage string) string {
	msg := gateStartError(stderr, triage)
	if msg == "" {
		return ""
	}
	return "the gate program could not start against this candidate: " + msg
}

var gateProgressMarkers = []string{"fork-check:", "triage ", "tillr-gate checks:"}

func gateStartError(stderr, triage string) string {
	// A gate killed by a signal was interrupted, not unable to start: keep it
	// as gate-no-verdict so the next pass gates the same candidate again.
	if strings.Contains(strings.ToLower(stderr), "killed by a signal") ||
		strings.Contains(strings.ToLower(triage), "killed by a signal") {
		return ""
	}
	for _, raw := range strings.Split(stderr, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "could not be started:") ||
			(strings.Contains(lower, "installed tillr-gate runner ") && strings.Contains(lower, " is missing or not a regular file")) ||
			strings.Contains(lower, "cannot locate tillr-gate installation for ") ||
			strings.Contains(lower, "tillr-gate is not on path") {
			return line
		}
	}
	var triageBody []string
	judgingStarted := false
	for _, raw := range strings.Split(triage, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "tillr-gate: gate program ") || strings.HasPrefix(line, "watchdog CPU source: ") {
			continue
		}
		for _, marker := range gateProgressMarkers {
			if strings.HasPrefix(line, marker) {
				judgingStarted = true
				break
			}
		}
		triageBody = append(triageBody, line)
	}
	if !judgingStarted {
		for _, line := range triageBody {
			if isGateStartErrorLine(line) {
				return line
			}
		}
	}
	for _, raw := range strings.Split(stderr, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" && isGateStartErrorLine(line) {
			return line
		}
	}
	return ""
}

func isGateStartErrorLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "can't open file '") ||
		strings.Contains(lower, ": cannot open ") ||
		strings.Contains(lower, ": can't open ") ||
		strings.Contains(lower, "cannot find module '") ||
		strings.Contains(lower, "executable file not found") {
		return true
	}
	if strings.Contains(lower, "fork/exec ") &&
		(strings.Contains(lower, ": no such file or directory") ||
			strings.Contains(lower, ": permission denied") ||
			strings.Contains(lower, ": exec format error")) {
		return true
	}
	return isShellStartError(lower)
}

func isShellStartError(lower string) bool {
	prog, rest, ok := strings.Cut(lower, ":")
	if !ok {
		return false
	}
	base := filepath.Base(strings.TrimSpace(prog))
	switch base {
	case "sh", "dash", "bash", "zsh", "env":
	default:
		return false
	}
	return strings.Contains(rest, ": no such file or directory") ||
		strings.Contains(rest, ": permission denied") ||
		strings.Contains(rest, ": is a directory") ||
		strings.Contains(rest, ": not found")
}

// releaseChangedCandidateGateFaults clears a gate-harness-error hold once the item's
// pending candidate carries a different commit than the one whose gate could not start.
// The same commit stays held until an operator clears the hold or supersedes it.
func (l *Loop) releaseChangedCandidateGateFaults(ctx context.Context) error {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var toClear []ItemFault
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		var err error
		toClear, err = changedCandidateGateFaults(tx)
		return err
	})
	r.Close()
	if err != nil || len(toClear) == 0 {
		return err
	}
	st, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	at := l.cfg.Now().UTC()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		current, err := changedCandidateGateFaults(tx.Reader())
		if err != nil {
			return err
		}
		for _, f := range current {
			if err := clearItemFault(tx, f, at); err != nil {
				return err
			}
		}
		return nil
	})
}

func changedCandidateGateFaults(r *store.Reader) ([]ItemFault, error) {
	faults, err := ReadItemFaults(r)
	if err != nil {
		return nil, err
	}
	if len(faults) == 0 {
		return nil, nil
	}
	works, err := readPending(r)
	if err != nil {
		return nil, err
	}
	var out []ItemFault
	for id, f := range faults {
		if f.Reason != StopGateHarnessError {
			continue
		}
		faultHead := gateFaultCandidateHead(f.WorkerHeadSHA, f.HeadSHA)
		if faultHead == "" {
			continue
		}
		w := works[id]
		if w == nil || w.Done || w.Candidate == nil {
			continue
		}
		pendingHead := gateFaultCandidateHead(w.Candidate.WorkerHeadSHA, w.Candidate.HeadSHA)
		if pendingHead != "" && pendingHead != faultHead {
			out = append(out, f)
		}
	}
	return out, nil
}

func gateFaultCandidateHead(workerHead, head string) string {
	if workerHead != "" {
		return workerHead
	}
	return head
}
