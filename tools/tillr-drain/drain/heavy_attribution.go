package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var errHeavyTipMoved = errors.New("trunk moved during proof attribution")

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
}

const heavyAttributionsFile = "heavy-attributions.json"

// Only a completed search that names a commit is reusable. A later red inherits
// it within the same red episode and green baseline, never across a known pass.
func (r HeavyRunner) findFirstRed(ctx context.Context, run HeavyRun, check HeavyCheck) (string, bool, string, error) {
	if err := r.requireCurrentTip(ctx, run.TipSHA); err != nil {
		return "", false, "", err
	}
	dir, err := heavyDirectory(ctx, r.Root)
	if err != nil {
		return "", false, "", err
	}
	records := readHeavyAttributions(dir)
	// The suite's pass does not enumerate passing tests. Reuse immutable
	// commit replays for a case, never the case's inferred red episode.
	goCase := strings.HasPrefix(check.CheckID, goTestPrefix)
	if prior := records[check.CheckID]; !goCase && prior.FirstRed != "" && prior.Base == run.BaseSHA && slices.Equal(prior.Reads, check.Reads) {
		if ancestor, err := trunkAncestor(ctx, r.Root, prior.Tip, run.TipSHA); err == nil && ancestor {
			r.progressf("loop: heavy %s reused first red commit %s\n", check.CheckID, shortSHA(prior.FirstRed))
			return prior.FirstRed, false, "", nil
		}
	}
	r.progressf("loop: heavy %s failed at tip %s; searching for the first red commit since green %s\n", check.CheckID, shortSHA(run.TipSHA), shortSHA(run.BaseSHA))
	red, fault, reason, err := r.firstRed(ctx, run.BaseSHA, run.TipSHA, check.CheckID, check.Reads)
	if !goCase && err == nil && !fault && red != "" {
		records[check.CheckID] = heavyAttribution{run.BaseSHA, run.TipSHA, red, check.Reads}
		if saveErr := publishHeavyJSON(dir, heavyAttributionsFile, records); saveErr != nil {
			r.progressf("loop: heavy %s first red commit not saved: %v\n", check.CheckID, saveErr)
		}
	}
	if err == nil {
		err = r.requireCurrentTip(ctx, run.TipSHA)
	}
	return red, fault, reason, err
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
