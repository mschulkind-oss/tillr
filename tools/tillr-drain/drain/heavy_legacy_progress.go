package drain

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

//go:embed heavy_legacy_progress.py
var heavyLegacyProgressProgram string

// Keep the legacy program's own planning, scheduling, skip and verdict behavior.
// Its Python adapter observes completed checks, not a guessed registry subset.
func heavyLegacyArgv(ctx context.Context, argv []string, laneRoot string) ([]string, error) {
	p, _ := ctx.Value(heavyProgressKey{}).(*heavyProgress)
	if p == nil || len(argv) < 2 || !strings.HasPrefix(filepath.Base(argv[0]), "python") || filepath.Base(argv[1]) != "fork-check.py" {
		return argv, nil
	}
	logs, err := filepath.Abs(filepath.Join(laneRoot, ".rolling", "logs", p.TipSHA))
	if err != nil {
		return nil, err
	}
	return append([]string{argv[0], "-c", heavyLegacyProgressProgram, argv[1], filepath.Join(p.dir, "heavy-legacy-progress.json"), p.TipSHA, filepath.Join(p.dir, "heavy-progress.json"), logs}, argv[2:]...), nil
}

type legacyGoCase struct {
	Package      string `json:"package"`
	Name         string `json:"name"`
	Log          string `json:"log"`
	TimeoutPanic string `json:"timeout_panic"`
}

type legacyFailure struct {
	Log   string         `json:"log"`
	Cases []legacyGoCase `json:"cases"`
}

func readLegacyEvidence(ctx context.Context) (map[string]legacyFailure, error) {
	p, _ := ctx.Value(heavyProgressKey{}).(*heavyProgress)
	if p == nil {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(p.dir, "heavy-legacy-progress.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var saved struct {
		TipSHA   string                   `json:"tip_sha"`
		Reused   []string                 `json:"reused"`
		Failures map[string]legacyFailure `json:"failures"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, err
	}
	if saved.TipSHA != p.TipSHA {
		return nil, nil
	}
	p.reused = append(p.reused, saved.Reused...)
	return saved.Failures, nil
}
