// Package heavy is the read-only proof-ref and run-report contract shared by drain and view.
package heavy

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mschulkind-oss/tillr/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GreenRef is the single proof pointer. No rolling_state row is consulted.
const GreenRef = "refs/tillr/green"

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
	Stalls          int      `json:"stalls,omitempty"`
}
type GreenProof struct {
	Commit   string    `json:"commit"`
	ProvedAt time.Time `json:"proved_at"`
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
		return rows.Err()
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
	raw, err := os.ReadFile(filepath.Join(dir, "heavy.json"))
	if os.IsNotExist(err) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	var run HeavyRun
	if err = json.Unmarshal(raw, &run); err != nil {
		return status, err
	}
	status.Result = run.Result
	if run.Detail != "" {
		status.Detail = run.Detail
	}
	for _, check := range run.Checks {
		if check.Result == "fail" {
			status.FailingChecks = append(status.FailingChecks, check)
		}
		if check.Result == "unavailable" {
			status.UnavailableChecks = append(status.UnavailableChecks, check)
		}
		if check.OverBudgetMS > 0 {
			status.Overruns = append(status.Overruns, check)
		}
	}
	return status, nil
}
