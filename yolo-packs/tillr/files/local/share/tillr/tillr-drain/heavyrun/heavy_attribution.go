package heavyrun

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

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/checkresult"
)

var errHeavyTipMoved = errors.New("trunk moved beyond the proved commit")

func (r HeavyRunner) requireCurrentTip(ctx context.Context, tip string) error {
	current, err := heavy.GitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
	if err == nil && current != tip {
		return errHeavyTipMoved
	}
	return err
}

type HeavyAttribution struct {
	Base, Tip, FirstRed string
	Reads               []string
	Failure             string
}

const HeavyAttributionsFile = "heavy-attributions.json"

type freshHeavyAttributionKey struct{}

// Compare recorded evidence, not log locations (which change with the tip).
// Missing or unreadable evidence cannot establish that two failures are the same.
func HeavyFailureFingerprint(check HeavyCheck) string {
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
func (r HeavyRunner) FindFirstRed(ctx context.Context, run HeavyRun, check HeavyCheck) (string, bool, string, error) {
	dir, err := HeavyDirectory(ctx, r.Root)
	if err != nil {
		return "", false, "", err
	}
	records := ReadHeavyAttributions(dir)
	// The suite's pass does not enumerate passing tests. Reuse immutable
	// commit replays for a case, never the case's inferred red episode.
	goCase := strings.HasPrefix(check.CheckID, GoTestPrefix)
	fresh, _ := ctx.Value(freshHeavyAttributionKey{}).(bool)
	failure := ""
	if !fresh && !goCase && ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome == checkresult.Fail {
		failure = HeavyFailureFingerprint(check)
	}
	prior, hadPrior := records[check.CheckID]
	if hadPrior && failure != "" && ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.CachedRecord, Word: "fail"}).Outcome == checkresult.Fail && failure == prior.Failure && prior.Base == run.BaseSHA && slices.Equal(prior.Reads, check.Reads) {
		commits, err := heavyFirstParentCommits(ctx, r.Root, run.BaseSHA, run.TipSHA)
		first, provedTip := slices.Index(commits, prior.FirstRed), slices.Index(commits, prior.Tip)
		if err == nil && first >= 0 && first <= provedTip {
			r.progressf("loop: heavy %s reused first red commit %s\n", check.CheckID, ShortSHA(prior.FirstRed))
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
	r.progressf("loop: heavy %s failed at tip %s; searching for the first red commit since green %s\n", check.CheckID, ShortSHA(run.TipSHA), ShortSHA(run.BaseSHA))
	red, fault, reason, err := r.firstRed(ctx, run.BaseSHA, run.TipSHA, check.CheckID, check.Reads)
	if failure != "" && err == nil && !fault && red != "" {
		records[check.CheckID] = HeavyAttribution{Base: run.BaseSHA, Tip: run.TipSHA, FirstRed: red, Reads: check.Reads, Failure: failure}
		if saveErr := PublishHeavyJSON(dir, HeavyAttributionsFile, records); saveErr != nil {
			r.progressf("loop: heavy %s first red commit not saved: %v\n", check.CheckID, saveErr)
		}
	}
	return red, fault, reason, err
}

func (r HeavyRunner) forgetHeavyAttribution(ctx context.Context, id string) error {
	dir, err := HeavyDirectory(ctx, r.Root)
	if err != nil {
		return err
	}
	records := ReadHeavyAttributions(dir)
	if _, exists := records[id]; !exists {
		return nil
	}
	delete(records, id)
	return PublishHeavyJSON(dir, HeavyAttributionsFile, records)
}

// RetainRedRepairs decides which historical failures still warrant action. Use one
// trunk snapshot for the batch: a landing during a focused replay does not restart
// attribution. The next whole proof can settle any repair that has become moot.
func (r HeavyRunner) RetainRedRepairs(ctx context.Context, run *HeavyRun, order []string, groups map[string][]HeavyCheck) ([]string, error) {
	if len(order) == 0 {
		return order, nil
	}
	tip, err := heavy.GitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
	if err != nil || tip == run.TipSHA {
		return order, err
	}
	ancestor, err := fixer.TrunkAncestor(ctx, r.Root, run.TipSHA, tip)
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
			case len(paths) == 0 || check.GoTimedOut:
				// The next whole proof can supersede a timeout without a focused replay.
			case len(check.Reads) != 0:
				for _, path := range paths {
					if fixer.ReadsCoverPath(check.Reads, path) {
						reason = "newer commits touched a declared input: " + path
						break
					}
				}
			default:
				// A named Go test has no declared read set. Ask just that test on
				// the sampled tip, not the whole tier. No verdict does not erase
				// the finished red; only a demonstrated pass supersedes it.
				red, err := r.Replay(ctx, tip, check.CheckID, "checking for a pass on the advanced trunk without declared inputs")
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if CleanupLost(err) {
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
			note := "repair deferred to proof of " + ShortSHA(tip) + ": " + reason
			r.progressf("loop: heavy %s at %s: %s\n", check.CheckID, ShortSHA(run.TipSHA), note)
			if ancestor {
				if recErr := fixer.RecordTrunkRepair(ctx, r.Root, tip, note); recErr != nil {
					r.progressf("loop: heavy %s trunk repair at %s not recorded: %v\n", check.CheckID, ShortSHA(tip), recErr)
				}
			}
			for i := range run.Checks {
				if run.Checks[i].CheckID == check.CheckID {
					run.Checks[i].Detail = JoinDetail(check.Detail, note)
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

func ReadHeavyAttributions(dir string) map[string]HeavyAttribution {
	var records map[string]HeavyAttribution
	if raw, err := os.ReadFile(filepath.Join(dir, HeavyAttributionsFile)); err == nil && json.Unmarshal(raw, &records) == nil && records != nil {
		return records
	}
	return map[string]HeavyAttribution{}
}

func (r HeavyRunner) ForgetGreenAttributions(ctx context.Context, checks []HeavyCheck) error {
	dir, err := HeavyDirectory(ctx, r.Root)
	if err != nil {
		return err
	}
	records := ReadHeavyAttributions(dir)
	changed := false
	for _, check := range checks {
		if _, exists := records[check.CheckID]; exists && ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome == checkresult.Pass {
			delete(records, check.CheckID)
			changed = true
		}
	}
	if changed {
		return PublishHeavyJSON(dir, HeavyAttributionsFile, records)
	}
	return nil
}

func CompletedHeavyRed(dir, base, tip string) (HeavyRun, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "heavy.json"))
	if err != nil {
		return HeavyRun{}, false
	}
	var run HeavyRun
	ok := json.Unmarshal(raw, &run) == nil && run.BaseSHA == base && run.TipSHA == tip && run.Result == "red" && run.Detail == ""
	return run, ok
}
