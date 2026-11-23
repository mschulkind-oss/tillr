package heavyrun

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr/checkresult"
)

// One tip's successful checks survive process replacement. The heavy lock owns
// this file; attribution uses a context without it and always executes afresh.
type HeavyProgress struct {
	TipSHA string                `json:"tip_sha"`
	Checks map[string]HeavyCheck `json:"checks"`
	// PassedAt is when each check in Checks passed on TipSHA. It makes one check's pass
	// proof for a trunk fixer named for that check (resolveProvedFixers) while the rest of
	// the run is still going or is red for another reason. A check without a time is no
	// such proof.
	PassedAt map[string]time.Time `json:"passed_at,omitempty"`
	dir      string
	reused   []string
}
type HeavyProgressKey struct{}

// LoadHeavyProgress answers the passes that still stand at tip. A pass recorded at an
// ancestor of tip stands too when no path the check reads changed between the two, so a
// loop restarted across unrelated landings does not start every check again.
func LoadHeavyProgress(ctx context.Context, root, dir, tip string) (*HeavyProgress, error) {
	p := &HeavyProgress{TipSHA: tip, Checks: map[string]HeavyCheck{}, dir: dir}
	raw, err := os.ReadFile(filepath.Join(dir, "heavy-progress.json"))
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var prior HeavyProgress
	if err := json.Unmarshal(raw, &prior); err != nil {
		return nil, err
	}
	if prior.TipSHA == tip && prior.Checks != nil {
		p.Checks = prior.Checks
		p.PassedAt = prior.PassedAt
		return p, nil
	}
	if prior.TipSHA == "" || prior.TipSHA == tip || len(prior.Checks) == 0 {
		return p, nil
	}
	ancestor, err := fixer.TrunkAncestor(ctx, root, prior.TipSHA, tip)
	if err != nil || !ancestor {
		return p, nil
	}
	raw, err = exec.CommandContext(ctx, "git", "-C", root, "diff", "--name-only", "--no-renames", "-z", prior.TipSHA, tip, "--").Output()
	if err != nil {
		return p, nil
	}
	changed := []string{}
	for _, name := range strings.Split(string(raw), "\x00") {
		if name != "" {
			changed = append(changed, name)
		}
	}
	carried := false
	for id, check := range prior.Checks {
		if ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.CachedRecord, Word: check.Result}).Outcome != checkresult.Pass {
			continue
		}
		if len(changed) == 0 || len(check.Reads) > 0 && !HeavyReadsChanged(check.Reads, changed) {
			p.Checks[id] = check
			if at, ok := prior.PassedAt[id]; ok {
				if p.PassedAt == nil {
					p.PassedAt = map[string]time.Time{}
				}
				p.PassedAt[id] = at
			}
			carried = true
		}
	}
	if carried {
		if err := PublishHeavyJSON(dir, "heavy-progress.json", p); err != nil {
			return nil, err
		}
	}
	return p, nil
}
func ReuseHeavyCheck(ctx context.Context, id string) (HeavyCheck, bool) {
	p, _ := ctx.Value(HeavyProgressKey{}).(*HeavyProgress)
	if p == nil {
		return HeavyCheck{}, false
	}
	check, ok := p.Checks[id]
	if !ok || ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.CachedRecord, Word: check.Result}).Outcome != checkresult.Pass {
		return HeavyCheck{}, false
	}
	p.reused = append(p.reused, id)
	return check, true
}
func SaveHeavyCheck(ctx context.Context, check HeavyCheck) error {
	p, _ := ctx.Value(HeavyProgressKey{}).(*HeavyProgress)
	if p == nil || ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.CachedRecord, Word: check.Result}).Outcome != checkresult.Pass {
		return nil
	}
	p.Checks[check.CheckID] = check
	if p.PassedAt == nil {
		p.PassedAt = map[string]time.Time{}
	}
	if _, known := p.PassedAt[check.CheckID]; !known {
		p.PassedAt[check.CheckID] = time.Now().UTC()
	}
	return PublishHeavyJSON(p.dir, "heavy-progress.json", p)
}
func PublishHeavyJSON(dir, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".heavy-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(raw, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, name))
}

const HeavyReplaysFile = "heavy-replays.json"

// ReadHeavyReplay answers the verdict an earlier attribution replay recorded for one
// commit and check. A missing or unreadable file is no record: the replay runs.
func ReadHeavyReplay(dir, sha, id string) (red, ok bool) {
	raw, err := os.ReadFile(filepath.Join(dir, HeavyReplaysFile))
	if err != nil {
		return false, false
	}
	var saved map[string]bool
	if json.Unmarshal(raw, &saved) != nil {
		return false, false
	}
	recorded, ok := saved[sha+" "+id]
	if !ok {
		return false, false
	}
	word := "pass"
	if recorded {
		word = "fail"
	}
	switch ClassifyCheck(checkresult.Observation{Check: id, Source: checkresult.CachedRecord, Word: word}).Outcome {
	case checkresult.Fail:
		return true, true
	case checkresult.Pass:
		return false, true
	default:
		return false, false
	}
}

// SaveHeavyReplay records one completed replay verdict, keeping the earlier ones.
func SaveHeavyReplay(dir, sha, id string, red bool) error {
	saved := map[string]bool{}
	if raw, err := os.ReadFile(filepath.Join(dir, HeavyReplaysFile)); err == nil {
		if json.Unmarshal(raw, &saved) != nil || saved == nil {
			saved = map[string]bool{}
		}
	}
	saved[sha+" "+id] = red
	return PublishHeavyJSON(dir, HeavyReplaysFile, saved)
}
