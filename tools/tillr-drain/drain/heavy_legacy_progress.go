package drain

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

//go:embed heavy_legacy_progress.py
var heavyLegacyProgressProgram string

var heavyLegacyNonceSeq atomic.Uint64

// Keep the legacy program's own planning, scheduling, skip and verdict behavior.
// Its Python adapter observes completed checks, not a guessed registry subset.
func heavyLegacyArgv(ctx context.Context, argv []string, laneRoot string) ([]string, string, string, bool, error) {
	p, _ := ctx.Value(heavyProgressKey{}).(*heavyProgress)
	if p == nil || len(argv) < 2 || !strings.HasPrefix(filepath.Base(argv[0]), "python") || filepath.Base(argv[1]) != "fork-check.py" {
		return argv, "", "", false, nil
	}
	logs, err := filepath.Abs(filepath.Join(laneRoot, ".rolling", "logs", p.TipSHA))
	if err != nil {
		return nil, "", "", false, err
	}
	progressPath := filepath.Join(p.dir, "heavy-legacy-progress.json")
	return append([]string{argv[0], "-c", heavyLegacyProgressProgram, argv[1], progressPath, p.TipSHA, filepath.Join(p.dir, "heavy-progress.json"), logs}, argv[2:]...), progressPath, p.TipSHA, true, nil
}

func nextHeavyLegacyNonce() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), heavyLegacyNonceSeq.Add(1))
}

type legacyCompletedStep struct {
	CheckID string `json:"check_id"`
	Outcome string `json:"outcome"`
}

func startHeavyLegacyProgressWatcher(ctx context.Context, progressPath, tipSHA, nonce string) func() {
	if progressPath == "" || !hasHeavyStoreProgress(ctx) {
		return func() {}
	}
	seen := 0
	pollOnce := func() {
		raw, err := os.ReadFile(progressPath)
		if err != nil {
			return
		}
		var saved struct {
			TipSHA    string                `json:"tip_sha"`
			RunNonce  string                `json:"run_nonce"`
			Completed []legacyCompletedStep `json:"completed"`
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			return
		}
		if saved.TipSHA != tipSHA || saved.RunNonce != nonce {
			return
		}
		for ; seen < len(saved.Completed); seen++ {
			item := saved.Completed[seen]
			if item.Outcome == "running" {
				heavyStoreStepProgress(ctx, item.CheckID, item.Outcome)
				continue
			}
			heavyStoreWholeStepProgress(ctx, item.CheckID, item.Outcome)
		}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollOnce()
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		if ctx.Err() == nil {
			pollOnce()
		}
	}
}

func startHeavyReplayProgressWatcher(ctx context.Context, progressPath, checkID string) func() {
	if progressPath == "" || checkID == "" || !hasHeavyStoreProgress(ctx) {
		return func() {}
	}
	var lastSeq, lastUnits int
	pollOnce := func() {
		raw, err := os.ReadFile(progressPath)
		if err != nil {
			return
		}
		var snap struct {
			Seq            int `json:"seq"`
			CompletedUnits int `json:"completed_units"`
		}
		if err := json.Unmarshal(raw, &snap); err != nil {
			return
		}
		if snap.Seq > lastSeq && snap.CompletedUnits > lastUnits {
			lastSeq = snap.Seq
			lastUnits = snap.CompletedUnits
			heavyStoreStepProgress(ctx, checkID, "running")
		}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollOnce()
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		if ctx.Err() == nil {
			pollOnce()
		}
	}
}

type legacyGoCase struct {
	Package      string `json:"package"`
	Name         string `json:"name"`
	Log          string `json:"log"`
	TimeoutPanic string `json:"timeout_panic"` // First proof's timeout panic or runtime deadlock line.
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
