// Package heavy is the read-only proof-ref and run-report contract shared by drain and view.
package heavy

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// GreenRef is the single proof pointer. No rolling_state row is consulted.
const (
	GreenRef        = "refs/tillr/green"
	RunningLockFile = "heavy-running.lock"
	RunningFile     = "heavy-running.json"
)

var classifyCheck = checkresult.Classify

// HeavyCheck carries a check's verdict and attribution, not just the program's exit.
// A budget is a reported target; only a failing verdict or repeated stall is red.
type HeavyCheck struct {
	CheckID         string   `json:"check_id"`
	Result          string   `json:"result"`
	DurationMS      int64    `json:"duration_ms"`
	BudgetMS        int64    `json:"budget_ms,omitempty"`
	OverBudgetMS    int64    `json:"over_budget_ms,omitempty"`
	Reads           []string `json:"reads,omitempty"`
	Needs           []string `json:"needs,omitempty"`
	Tools           []string `json:"tools,omitempty"`
	FirstRedLanding string   `json:"first_red_landing,omitempty"`
	GateFault       bool     `json:"gate_fault,omitempty"`
	Detail          string   `json:"detail,omitempty"`
	// LogPath is the file holding the full output of the program that judged this check
	// when it failed, under the proof's log directory for the commit. Empty when no
	// output was kept.
	LogPath string `json:"log_path,omitempty"`
	// LogRef is the per-check triage or output log recorded by tillr-gate or extracted
	// from the judging program's output for this check.
	LogRef string `json:"log_ref,omitempty"`
	Stalls int    `json:"stalls,omitempty"`
	// GoTimedOut prevents replay of a test that timed out or deadlocked, or its suite.
	GoTimedOut bool `json:"go_timed_out,omitempty"`
	// RevertedBy is the trunk commit that reverted FirstRedLanding automatically, and
	// RevertRefused is why the automatic revert ended "not possible" so a fixer was filed
	// instead (docs/reference/gate.md#a-red-proof, graph design OQ-GP1). Both are empty when the
	// revert was not attempted.
	RevertedBy    string `json:"reverted_by,omitempty"`
	RevertRefused string `json:"revert_refused,omitempty"`
}
type GreenProof struct {
	Commit   string    `json:"commit"`
	ProvedAt time.Time `json:"proved_at"`
	// Passed names every check the green run judged passing. A trunk fixer is resolved
	// by a proof only when each check it was filed for is in this list; an older proof
	// without it resolves nothing.
	Passed []string `json:"passed,omitempty"`
}

type HeavyRun struct {
	ReusedChecks []string     `json:"reused_checks,omitempty"`
	DurationMS   int64        `json:"duration_ms"`
	BaseSHA      string       `json:"base_sha"`
	TipSHA       string       `json:"tip_sha"`
	Result       string       `json:"result"`
	Checks       []HeavyCheck `json:"checks"`
	Detail       string       `json:"detail,omitempty"`
}

// HeavyRunning is <git-common-dir>/tillr-gate/heavy-running.json while a proof
// attempt holds an exclusive flock on RunningLockFile.
type HeavyRunning struct {
	BaseSHA   string    `json:"base_sha,omitempty"`
	TipSHA    string    `json:"tip_sha"`
	StartedAt time.Time `json:"started_at"`
}
type HeavyOutcome struct {
	Busy bool       `json:"busy,omitempty"`
	Runs []HeavyRun `json:"runs"`
}
type HeavyStatus struct {
	GreenProvedAt     time.Time    `json:"green_proved_at"`
	GreenCommit       string       `json:"green_commit"`
	TipCommit         string       `json:"tip_commit"`
	LandingsSince     int          `json:"landings_since"`
	AtGreen           bool         `json:"at_green"`
	FailingChecks     []HeavyCheck `json:"failing_checks"`
	UnavailableChecks []HeavyCheck `json:"unavailable_checks"`
	Overruns          []HeavyCheck `json:"overruns"`
	Result            string       `json:"result"`
	Detail            string       `json:"detail,omitempty"`
	// Rewrite says in one line that the drain absorbed a rewrite of the trunk (a pull that
	// rebased main), when, and how many commits it mapped. Empty when none was recorded.
	Rewrite string `json:"rewrite,omitempty"`
}

func GitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, raw)
	}
	return strings.TrimSpace(string(raw)), nil
}
func Directory(ctx context.Context, root string) (string, error) {
	common, err := GitOutput(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return filepath.Join(common, "tillr-gate"), err
}
func Green(ctx context.Context, root string) (string, error) {
	// show-ref --verify exits 1 only for an absent pointer; other git failures are faults.
	cmd := exec.CommandContext(ctx, "git", "-C", root, "show-ref", "--verify", "--quiet", GreenRef)
	_, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", GreenRef, err)
	}
	return GitOutput(ctx, root, "rev-parse", GreenRef+"^{commit}")
}
func Landings(ctx context.Context, root, storePath, base, tip string) ([]string, error) {
	rev := tip
	if base != "" {
		rev = base + ".." + tip
	}
	raw, err := GitOutput(ctx, root, "rev-list", "--first-parent", "--reverse", rev)
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(raw)
	if storePath == "" {
		return commits, nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	landed := map[string]bool{}
	err = s.ReadTx(ctx, func(reader *store.Reader) error {
		rows, err := reader.Query(`SELECT trunk_after FROM landings WHERE result = 'landed'`)
		if err != nil {
			// A store that has never linked the land writer has no landing history.
			if strings.Contains(err.Error(), "no such table: landings") {
				return nil
			}
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sha string
			if err := rows.Scan(&sha); err != nil {
				return err
			}
			landed[sha] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// A landing recorded under a sha a rewrite replaced counts at the new sha.
		for old := range landed {
			now, err := RewriteResolve(reader, old)
			if err != nil {
				return err
			}
			if now != old {
				landed[now] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	selected := []string{}
	for _, sha := range commits {
		if landed[sha] {
			selected = append(selected, sha)
		}
	}
	return selected, nil
}

// ReadStatus reads the ref and the last atomic run report. It does not read the
// retired watermark. A missing report is visibly unjudged, not an invented pass.
func ReadStatus(ctx context.Context, root, storePath string) (HeavyStatus, error) {
	status := HeavyStatus{Result: "unjudged", FailingChecks: []HeavyCheck{}, UnavailableChecks: []HeavyCheck{}, Overruns: []HeavyCheck{}}
	dir, err := Directory(ctx, root)
	if err != nil {
		return status, err
	}
	status.GreenCommit, err = Green(ctx, root)
	if err != nil {
		return status, err
	}
	rawProof, e := os.ReadFile(filepath.Join(dir, "green-"+status.GreenCommit+".json"))
	if os.IsNotExist(e) {
		// Older runners recorded one proof; accept it only when its commit
		// matches the pointer, just as with commit-specific evidence.
		rawProof, e = os.ReadFile(filepath.Join(dir, "green.json"))
	}
	if e == nil {
		var proof GreenProof
		if e = json.Unmarshal(rawProof, &proof); e != nil {
			return status, e
		}
		if proof.Commit == status.GreenCommit {
			status.GreenProvedAt = proof.ProvedAt
		}
	} else if !os.IsNotExist(e) {
		return status, e
	}
	status.TipCommit, err = GitOutput(ctx, root, "rev-parse", "refs/heads/main"+"^{commit}")
	if err != nil {
		return status, err
	}
	head, err := GitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return status, err
	}
	status.AtGreen = head == status.GreenCommit
	if status.GreenCommit != "" {
		if _, err = GitOutput(ctx, root, "merge-base", "--is-ancestor", status.GreenCommit, status.TipCommit); err != nil {
			status.Detail = "green pointer is not an ancestor of trunk; trunk rewritten"
		}
	}
	if status.Detail == "" {
		landings, err := Landings(ctx, root, storePath, status.GreenCommit, status.TipCommit)
		if err != nil {
			return status, err
		}
		status.LandingsSince = len(landings)
	}
	if storePath != "" {
		if status.Rewrite, err = rewriteLine(ctx, storePath); err != nil {
			return status, err
		}
	}
	rewriteDetail := status.Detail
	running, active, err := readRunning(dir)
	if err != nil {
		return status, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "heavy.json"))
	if err != nil && !os.IsNotExist(err) {
		return status, err
	}
	if err == nil {
		var run HeavyRun
		if err = json.Unmarshal(raw, &run); err != nil {
			return status, err
		}
		status.Result = run.Result
		if run.Detail != "" {
			status.Detail = run.Detail
		}
		for _, check := range run.Checks {
			classified := classifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result})
			if classified.Outcome == checkresult.Fail {
				status.FailingChecks = append(status.FailingChecks, check)
			}
			if classified.Outcome == checkresult.Unavailable {
				status.UnavailableChecks = append(status.UnavailableChecks, check)
			}
			if check.OverBudgetMS > 0 {
				status.Overruns = append(status.Overruns, check)
			}
		}
	}
	if active {
		status.Result = "running"
		status.Detail = runningDetail(running, rewriteDetail)
	}
	return status, nil
}

func readRunning(dir string) (HeavyRunning, bool, error) {
	lock, err := os.Open(filepath.Join(dir, RunningLockFile))
	if os.IsNotExist(err) {
		return HeavyRunning{}, false, nil
	}
	if err != nil {
		return HeavyRunning{}, false, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err == nil {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		return HeavyRunning{}, false, nil
	} else if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
		return HeavyRunning{}, false, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, RunningFile))
	if os.IsNotExist(err) {
		return HeavyRunning{}, false, nil
	}
	if err != nil {
		return HeavyRunning{}, false, err
	}
	var running HeavyRunning
	if err := json.Unmarshal(raw, &running); err != nil {
		return HeavyRunning{}, false, err
	}
	return running, true, nil
}

func runningDetail(running HeavyRunning, rewriteDetail string) string {
	if rewriteDetail != "" {
		return rewriteDetail
	}
	tip := running.TipSHA
	if len(tip) > 12 {
		tip = tip[:12]
	}
	if tip == "" {
		return ""
	}
	return "proving " + tip
}

// rewriteLine is the one-line note of the latest absorbed rewrite, or "" when none.
func rewriteLine(ctx context.Context, storePath string) (string, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", err
	}
	defer s.Close()
	line := ""
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		a, ok, err := LatestRewrite(r)
		if ok {
			line = a.Line()
		}
		return err
	})
	return line, err
}
