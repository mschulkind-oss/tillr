package heavyrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var heavyLegacyNonceSeq atomic.Uint64

func NextHeavyLegacyNonce() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), heavyLegacyNonceSeq.Add(1))
}

func StartHeavyReplayProgressWatcher(ctx context.Context, progressPath, checkID string) func() {
	if progressPath == "" || checkID == "" || !HasHeavyStoreProgress(ctx) {
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
			HeavyStoreStepProgress(ctx, checkID, "running")
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

type LegacyGoCase struct {
	Package      string `json:"package"`
	Name         string `json:"name"`
	Log          string `json:"log"`
	TimeoutPanic string `json:"timeout_panic"` // First proof's timeout panic or runtime deadlock line.
}

type LegacyFailure struct {
	Log   string         `json:"log"`
	Cases []LegacyGoCase `json:"cases"`
}

func ReadLegacyEvidence(ctx context.Context) (map[string]LegacyFailure, error) {
	p, _ := ctx.Value(HeavyProgressKey{}).(*HeavyProgress)
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
		Failures map[string]LegacyFailure `json:"failures"`
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
