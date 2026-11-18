package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// One tip's successful checks survive process replacement. The heavy lock owns
// this file; attribution uses a context without it and always executes afresh.
type heavyProgress struct {
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
type heavyProgressKey struct{}

func loadHeavyProgress(dir, tip string) (*heavyProgress, error) {
	p := &heavyProgress{TipSHA: tip, Checks: map[string]HeavyCheck{}, dir: dir}
	raw, err := os.ReadFile(filepath.Join(dir, "heavy-progress.json"))
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var prior heavyProgress
	if err := json.Unmarshal(raw, &prior); err != nil {
		return nil, err
	}
	if prior.TipSHA == tip && prior.Checks != nil {
		p.Checks = prior.Checks
		p.PassedAt = prior.PassedAt
	}
	return p, nil
}
func reuseHeavyCheck(ctx context.Context, id string) (HeavyCheck, bool) {
	p, _ := ctx.Value(heavyProgressKey{}).(*heavyProgress)
	if p == nil {
		return HeavyCheck{}, false
	}
	check, ok := p.Checks[id]
	if !ok || check.Result != "pass" {
		return HeavyCheck{}, false
	}
	p.reused = append(p.reused, id)
	return check, true
}
func saveHeavyCheck(ctx context.Context, check HeavyCheck) error {
	p, _ := ctx.Value(heavyProgressKey{}).(*heavyProgress)
	if p == nil || check.Result != "pass" {
		return nil
	}
	p.Checks[check.CheckID] = check
	if p.PassedAt == nil {
		p.PassedAt = map[string]time.Time{}
	}
	if _, known := p.PassedAt[check.CheckID]; !known {
		p.PassedAt[check.CheckID] = time.Now().UTC()
	}
	return publishHeavyJSON(p.dir, "heavy-progress.json", p)
}
func publishHeavyJSON(dir, name string, value any) error {
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
