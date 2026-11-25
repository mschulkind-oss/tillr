package drain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// BaseReplayStartedKind records that the drain started a base replay of
	// one or more checks on a commit for a candidate.
	BaseReplayStartedKind = "drain.base-replay-started"
	// BaseReplayEndedKind records that a base replay finished, with its
	// outcome and elapsed milliseconds.
	BaseReplayEndedKind = "drain.base-replay-ended"
)

func init() {
	for _, kind := range []string{BaseReplayStartedKind, BaseReplayEndedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

// BaseReplayStartedPayload is the payload of a BaseReplayStartedKind event.
type BaseReplayStartedPayload struct {
	BaseSHA       string   `json:"base_sha"`
	ItemID        string   `json:"item_id"`
	RunID         string   `json:"run_id"`
	Checks        []string `json:"checks"`
	ActiveReplays int      `json:"active_replays"`
}

// BaseReplayEndedPayload is the payload of a BaseReplayEndedKind event.
type BaseReplayEndedPayload struct {
	BaseSHA       string   `json:"base_sha"`
	ItemID        string   `json:"item_id"`
	RunID         string   `json:"run_id"`
	Checks        []string `json:"checks"`
	Outcome       string   `json:"outcome"`
	ElapsedMS     int64    `json:"elapsed_ms"`
	ActiveReplays int      `json:"active_replays"`
}

func (l *Loop) runReplay(ctx context.Context, replay ReplayStep, candidate Candidate, rev string, checks []string) (ReplayOutcome, error) {
	if _, ok := replay.(unknownReplay); ok {
		return replay.Replay(ctx, candidate, rev, checks)
	}
	checkIDs := append([]string{}, checks...)
	l.baseReplayMu.Lock()
	l.activeBaseReplays++
	activeAtStart := l.activeBaseReplays
	started := l.cfg.Now().UTC()
	l.recordBaseReplayEvent(ctx, BaseReplayStartedKind, candidate, rev, BaseReplayStartedPayload{
		BaseSHA:       rev,
		ItemID:        candidate.ItemID,
		RunID:         candidate.RunID,
		Checks:        checkIDs,
		ActiveReplays: activeAtStart,
	}, started)
	l.baseReplayMu.Unlock()

	out, err := replay.Replay(ctx, candidate, rev, checks)

	writeCtx := ctx
	var cancel context.CancelFunc
	if writeCtx.Err() != nil {
		writeCtx, cancel = context.WithTimeout(context.WithoutCancel(writeCtx), 5*time.Second)
		defer cancel()
	}
	l.baseReplayMu.Lock()
	l.activeBaseReplays--
	if l.activeBaseReplays < 0 {
		l.activeBaseReplays = 0
	}
	activeAfterEnd := l.activeBaseReplays
	ended := l.cfg.Now().UTC()
	elapsedMS := ended.Sub(started).Milliseconds()
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	l.recordBaseReplayEvent(writeCtx, BaseReplayEndedKind, candidate, rev, BaseReplayEndedPayload{
		BaseSHA:       rev,
		ItemID:        candidate.ItemID,
		RunID:         candidate.RunID,
		Checks:        checkIDs,
		Outcome:       baseReplayOutcome(ctx, rev, out, err),
		ElapsedMS:     elapsedMS,
		ActiveReplays: activeAfterEnd,
	}, ended)
	l.baseReplayMu.Unlock()
	return out, err
}

func baseReplayOutcome(ctx context.Context, rev string, out ReplayOutcome, err error) string {
	if ctx.Err() != nil || errors.Is(err, errRestartPending) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	if err != nil {
		return "fault"
	}
	if !out.Known || (out.Rev != "" && out.Rev != rev) {
		return "unknown"
	}
	if len(out.Red) > 0 {
		return "red"
	}
	return "green"
}

func (l *Loop) recordBaseReplayEvent(ctx context.Context, kind string, candidate Candidate, rev string, payload any, at time.Time) {
	if l.cfg.Store == "" {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		l.reportf("loop: %s was not marshaled: %v\n", kind, err)
		return
	}
	objectType, objectID := "run", candidate.RunID
	if objectID == "" {
		objectType, objectID = "commit", rev
	}
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		if ctx.Err() == nil {
			l.reportf("loop: %s was not recorded: %v\n", kind, err)
		}
		return
	}
	writeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       kind,
			ObjectType: objectType,
			ObjectID:   &objectID,
			Payload:    body,
			At:         at,
		})
		return err
	})
	closeErr := s.Close()
	if err := errors.Join(writeErr, closeErr); err != nil && ctx.Err() == nil {
		l.reportf("loop: %s was not recorded: %v\n", kind, err)
	}
}
