package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
)

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
	return storelog.FormatBaseReplayOutcome(ctx, rev, out.Known, out.Rev, len(out.Red), err, errRestartPending)
}

func (l *Loop) recordBaseReplayEvent(ctx context.Context, kind string, candidate Candidate, rev string, payload any, at time.Time) {
	storelog.RecordBaseReplayEvent(ctx, l.cfg.Store, kind, candidate.RunID, rev, payload, at, l.reportf)
}
