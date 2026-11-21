package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

var errHeavyTipMoved = errors.New("trunk moved beyond the proved commit")

func (r HeavyRunner) requireCurrentTip(ctx context.Context, tip string) error {
	current, err := heavyGitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
	if err == nil && current != tip {
		return errHeavyTipMoved
	}
	return err
}

type heavyAttribution struct {
	Base, Tip, FirstRed string
	Reads               []string
	Failure             string
}

const heavyAttributionsFile = "heavy-attributions.json"

type freshHeavyAttributionKey struct{}

// Compare recorded evidence, not log locations (which change with the tip).
// Missing or unreadable evidence cannot establish that two failures are the same.
func heavyFailureFingerprint(check HeavyCheck) string {
	detail := strings.TrimSpace(check.Detail)
	log := check.LogRef
	if log == "" {
		log = check.LogPath
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%d:%s", len(detail), detail)
	if log != "" {
		f, err := os.Open(log)
		if err != nil {
			return ""
		}
		defer f.Close()
		n, err := io.Copy(hash, f)
		if err != nil || n == 0 && detail == "" {
			return ""
		}
	} else if detail == "" {
		return ""
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// Reuse only a proved transition in the same range and red episode, with
// matching failure evidence. Old records without evidence are not reusable.
func (r HeavyRunner) findFirstRed(ctx context.Context, run HeavyRun, check HeavyCheck) (string, bool, string, error) {
	dir, err := heavyDirectory(ctx, r.Root)
	if err != nil {
		return "", false, "", err
	}
	records := readHeavyAttributions(dir)
	// The suite's pass does not enumerate passing tests. Reuse immutable
	// commit replays for a case, never the case's inferred red episode.
	goCase := strings.HasPrefix(check.CheckID, goTestPrefix)
	fresh, _ := ctx.Value(freshHeavyAttributionKey{}).(bool)
	failure := ""
	if !fresh && !goCase && check.Result == "fail" {
		failure = heavyFailureFingerprint(check)
	}
	prior, hadPrior := records[check.CheckID]
	if failure != "" && failure == prior.Failure && prior.Base == run.BaseSHA && slices.Equal(prior.Reads, check.Reads) {
		commits, err := heavyFirstParentCommits(ctx, r.Root, run.BaseSHA, run.TipSHA)
		first, provedTip := slices.Index(commits, prior.FirstRed), slices.Index(commits, prior.Tip)
		if err == nil && first >= 0 && first <= provedTip {
			r.progressf("loop: heavy %s reused first red commit %s\n", check.CheckID, shortSHA(prior.FirstRed))
			return prior.FirstRed, false, "", nil
		}
	}
	// A different failure ends the recorded episode even if we cannot attribute it.
	if hadPrior {
		if err := r.forgetHeavyAttribution(ctx, check.CheckID); err != nil {
			return "", false, "", err
		}
		delete(records, check.CheckID)
	}
	r.progressf("loop: heavy %s failed at tip %s; searching for the first red commit since green %s\n", check.CheckID, shortSHA(run.TipSHA), shortSHA(run.BaseSHA))
	red, fault, reason, err := r.firstRed(ctx, run.BaseSHA, run.TipSHA, check.CheckID, check.Reads)
	if failure != "" && err == nil && !fault && red != "" {
		records[check.CheckID] = heavyAttribution{Base: run.BaseSHA, Tip: run.TipSHA, FirstRed: red, Reads: check.Reads, Failure: failure}
		if saveErr := publishHeavyJSON(dir, heavyAttributionsFile, records); saveErr != nil {
			r.progressf("loop: heavy %s first red commit not saved: %v\n", check.CheckID, saveErr)
		}
	}
	return red, fault, reason, err
}

func (r HeavyRunner) forgetHeavyAttribution(ctx context.Context, id string) error {
	dir, err := heavyDirectory(ctx, r.Root)
	if err != nil {
		return err
	}
	records := readHeavyAttributions(dir)
	if _, exists := records[id]; !exists {
		return nil
	}
	delete(records, id)
	return publishHeavyJSON(dir, heavyAttributionsFile, records)
}

// retainRedRepairs decides which historical failures still warrant action. Use one
// trunk snapshot for the batch: a landing during a focused replay does not restart
// attribution. The next whole proof can settle any repair that has become moot.
func (r HeavyRunner) retainRedRepairs(ctx context.Context, run *HeavyRun, order []string, groups map[string][]HeavyCheck) ([]string, error) {
	if len(order) == 0 {
		return order, nil
	}
	tip, err := heavyGitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
	if err != nil || tip == run.TipSHA {
		return order, err
	}
	ancestor, err := trunkAncestor(ctx, r.Root, run.TipSHA, tip)
	if err != nil {
		return nil, err
	}
	var paths []string
	if ancestor {
		// Inspect each first-parent delta, including a merge's change to trunk,
		// rather than hiding paths touched and then restored by two landings.
		raw, err := exec.CommandContext(ctx, "git", "-C", r.Root, "log", "--first-parent", "--diff-merges=first-parent", "--format=", "--name-only", "--no-renames", "-z", run.TipSHA+".."+tip, "--").Output()
		if err != nil {
			return nil, err
		}
		if len(raw) != 0 {
			paths = strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		}
	}
	remaining := order[:0]
	for _, sha := range order {
		keep := groups[sha][:0]
		for _, check := range groups[sha] {
			reason := ""
			switch {
			case !ancestor:
				reason = "trunk was rewritten"
			case len(paths) == 0:
			case len(check.Reads) != 0:
				for _, path := range paths {
					if readsCoverPath(check.Reads, path) {
						reason = "newer commits touched a declared input: " + path
						break
					}
				}
			default:
				// A named Go test has no declared read set. Ask just that test on
				// the sampled tip, not the whole tier. No verdict does not erase
				// the finished red; only a demonstrated pass supersedes it.
				red, err := r.replay(ctx, tip, check.CheckID, "checking for a pass on the advanced trunk without declared inputs")
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if cleanupLost(err) {
					return nil, err
				}
				if err == nil && !red {
					reason = "the focused check passed on the advanced trunk"
				}
			}
			if reason == "" {
				keep = append(keep, check)
				continue
			}
			note := "repair deferred to proof of " + shortSHA(tip) + ": " + reason
			r.progressf("loop: heavy %s at %s: %s\n", check.CheckID, shortSHA(run.TipSHA), note)
			for i := range run.Checks {
				if run.Checks[i].CheckID == check.CheckID {
					run.Checks[i].Detail = joinDetail(check.Detail, note)
				}
			}
		}
		groups[sha] = keep
		if len(keep) != 0 {
			remaining = append(remaining, sha)
		}
	}
	return remaining, nil
}

func readHeavyAttributions(dir string) map[string]heavyAttribution {
	var records map[string]heavyAttribution
	if raw, err := os.ReadFile(filepath.Join(dir, heavyAttributionsFile)); err == nil && json.Unmarshal(raw, &records) == nil && records != nil {
		return records
	}
	return map[string]heavyAttribution{}
}

func (r HeavyRunner) forgetGreenAttributions(ctx context.Context, checks []HeavyCheck) error {
	dir, err := heavyDirectory(ctx, r.Root)
	if err != nil {
		return err
	}
	records := readHeavyAttributions(dir)
	changed := false
	for _, check := range checks {
		if _, exists := records[check.CheckID]; check.Result == "pass" && exists {
			delete(records, check.CheckID)
			changed = true
		}
	}
	if changed {
		return publishHeavyJSON(dir, heavyAttributionsFile, records)
	}
	return nil
}

func completedHeavyRed(dir, base, tip string) (HeavyRun, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "heavy.json"))
	if err != nil {
		return HeavyRun{}, false
	}
	var run HeavyRun
	ok := json.Unmarshal(raw, &run) == nil && run.BaseSHA == base && run.TipSHA == tip && run.Result == "red" && run.Detail == ""
	return run, ok
}
