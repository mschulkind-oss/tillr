package heavyrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// HeavyProgressKind records started, per-step progress, and ended lifecycle transitions for a trunk heavy proof attempt.
const HeavyProgressKind = "drain.heavy-progress"

func init() {
	if err := event.Register(HeavyProgressKind, "drain"); err != nil {
		panic(err)
	}
}

// HeavyProgressFact is the payload of a HeavyProgressKind event.
type HeavyProgressFact struct {
	TipSHA  string `json:"tip_sha"`
	Phase   string `json:"phase"`
	CheckID string `json:"check_id,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type heavyStoreProgressKey struct{}

type heavyStoreProgress struct {
	runner     HeavyRunner
	baseCtx    context.Context
	trunkID    string
	tipSHA     string
	mu         sync.Mutex
	wholeSteps int
	finished   bool
}

func (r HeavyRunner) StartStoreProgress(ctx context.Context, tipSHA string) (context.Context, *heavyStoreProgress) {
	trunkID := strings.TrimPrefix(r.Trunk, "refs/heads/")
	if trunkID == "" {
		trunkID = "main"
	}
	p := &heavyStoreProgress{
		runner:  r,
		baseCtx: ctx,
		trunkID: trunkID,
		tipSHA:  tipSHA,
	}
	p.mu.Lock()
	p.writeLocked(ctx, HeavyProgressFact{TipSHA: tipSHA, Phase: "started"})
	p.mu.Unlock()
	return context.WithValue(ctx, heavyStoreProgressKey{}, p), p
}

func (p *heavyStoreProgress) step(ctx context.Context, checkID, outcome string, whole bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	if whole {
		p.wholeSteps++
	}
	p.writeLocked(ctx, HeavyProgressFact{
		TipSHA:  p.tipSHA,
		Phase:   "progress",
		CheckID: checkID,
		Outcome: outcome,
	})
}

func (p *heavyStoreProgress) WholeStepCount() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.wholeSteps
}

func (p *heavyStoreProgress) Finish(outcome string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	writeCtx := p.baseCtx
	var cancel context.CancelFunc
	if writeCtx.Err() != nil {
		writeCtx, cancel = context.WithTimeout(context.WithoutCancel(writeCtx), 5*time.Second)
		defer cancel()
	}
	p.writeLocked(writeCtx, HeavyProgressFact{
		TipSHA:  p.tipSHA,
		Phase:   "ended",
		Outcome: outcome,
	})
}

func (p *heavyStoreProgress) writeLocked(ctx context.Context, fact HeavyProgressFact) {
	if p.runner.Store == "" {
		return
	}
	body, err := json.Marshal(fact)
	if err != nil {
		p.runner.progressf("loop: heavy progress was not marshaled: %v\n", err)
		return
	}
	at := time.Now().UTC()
	if p.runner.Now != nil {
		at = p.runner.Now().UTC()
	}
	s, err := store.Open(ctx, p.runner.Store, false)
	if err != nil {
		if ctx.Err() == nil {
			p.runner.progressf("loop: heavy progress was not recorded: %v\n", err)
		}
		return
	}
	trunkID := p.trunkID
	writeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       HeavyProgressKind,
			ObjectType: "trunk",
			ObjectID:   &trunkID,
			At:         at,
			Payload:    body,
		})
		return err
	})
	closeErr := s.Close()
	if err := errors.Join(writeErr, closeErr); err != nil && ctx.Err() == nil {
		p.runner.progressf("loop: heavy progress was not recorded: %v\n", err)
	}
}

func HeavyStoreWholeStepProgress(ctx context.Context, checkID, outcome string) {
	p, _ := ctx.Value(heavyStoreProgressKey{}).(*heavyStoreProgress)
	p.step(ctx, checkID, outcome, true)
}

func HeavyStoreStepProgress(ctx context.Context, checkID, outcome string) {
	p, _ := ctx.Value(heavyStoreProgressKey{}).(*heavyStoreProgress)
	p.step(ctx, checkID, outcome, false)
}

func HasHeavyStoreProgress(ctx context.Context) bool {
	p, _ := ctx.Value(heavyStoreProgressKey{}).(*heavyStoreProgress)
	return p != nil && p.runner.Store != ""
}

func heavyEndedOutcome(ctx context.Context, result string, err error) string {
	if ctx.Err() != nil || errors.Is(err, ErrRestartPending) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	if err != nil {
		return "fault"
	}
	if result == "" {
		return "unknown"
	}
	return result
}
