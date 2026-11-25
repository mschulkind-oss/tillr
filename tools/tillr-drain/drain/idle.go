package drain

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type idleStateCount struct {
	state        string
	count        int
	maxUpdatedAt int64
}

// idlePlanState captures a lightweight fingerprint of every input that could
// turn an idle pass (nothing dispatchable, no live leases or workers) into a
// different plan. While unchanged, the loop skips repeated full passes and
// planDefinition scans so its own reads leave WAL checkpoints a reader-free
// moment.
type idlePlanState struct {
	eventSeq      int64
	itemStates    []idleStateCount
	leasedLanes   int
	runCount      int
	maxFinishedAt int64
	trunkSHA      string
	greenSHA      string
	startSnap     *startSnapshot
	lanes         []string
	policy        *Policy
	held          map[string]string
	nextExpiry    time.Time
	pendingOnly   bool
}

func isIdleTelemetryEvent(kind, objectType string) bool {
	switch kind {
	case CheckpointNotRestartedKind, LoopPassKind, ProcessCPUKind, StoreBusyKind:
		return objectType == "session"
	case "watch.alert.opened", "watch.alert.resolved":
		return objectType == "alert"
	default:
		return false
	}
}

func (l *Loop) hasStageFaults() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.stageFaults) > 0
}

func (l *Loop) heldReasonsSnapshot() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.held) == 0 {
		return nil
	}
	out := make(map[string]string, len(l.held))
	for id, h := range l.held {
		out[id] = h.reason
	}
	return out
}

func (l *Loop) readIdleStoreFingerprint(ctx context.Context, afterSeq int64) (
	latestSeq int64,
	nonTelemetryEvent bool,
	states []idleStateCount,
	leasedLanes int,
	runCount int,
	maxFinishedAt int64,
	hasDesignDocs bool,
	windows map[string]time.Time,
	err error,
) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return 0, false, nil, 0, 0, 0, false, nil, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		if afterSeq < 0 {
			head, qErr := r.EventHeadSeq()
			if qErr != nil {
				return qErr
			}
			latestSeq = head
		} else {
			latestSeq = afterSeq
			rows, qErr := r.Query(`SELECT seq, kind, object_type FROM events WHERE seq > ? ORDER BY seq`, afterSeq)
			if qErr != nil {
				return qErr
			}
			for rows.Next() {
				var seq int64
				var kind, objType string
				if scanErr := rows.Scan(&seq, &kind, &objType); scanErr != nil {
					rows.Close()
					return scanErr
				}
				latestSeq = seq
				if !isIdleTelemetryEvent(kind, objType) {
					nonTelemetryEvent = true
				}
			}
			if closeErr := rows.Close(); closeErr != nil {
				return closeErr
			}
			if rowsErr := rows.Err(); rowsErr != nil {
				return rowsErr
			}
			if nonTelemetryEvent {
				return nil
			}
		}

		itemRows, qErr := r.Query(`SELECT state, COUNT(*), COALESCE(MAX(updated_at), 0) FROM items GROUP BY state ORDER BY state`)
		if qErr != nil {
			return qErr
		}
		for itemRows.Next() {
			var sc idleStateCount
			if scanErr := itemRows.Scan(&sc.state, &sc.count, &sc.maxUpdatedAt); scanErr != nil {
				itemRows.Close()
				return scanErr
			}
			states = append(states, sc)
		}
		if closeErr := itemRows.Close(); closeErr != nil {
			return closeErr
		}
		if rowsErr := itemRows.Err(); rowsErr != nil {
			return rowsErr
		}

		if qErr := r.QueryRow(`SELECT COUNT(*) FROM lanes WHERE state = ?`, LaneStateLeased).Scan(&leasedLanes); qErr != nil {
			return qErr
		}
		if qErr := r.QueryRow(`SELECT COUNT(*), COALESCE(MAX(finished_at), 0) FROM runs`).Scan(&runCount, &maxFinishedAt); qErr != nil {
			return qErr
		}
		if afterSeq < 0 {
			if qErr := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM items WHERE design_doc <> '')`).Scan(&hasDesignDocs); qErr != nil {
				return qErr
			}
			windows, qErr = readBackendWindows(r, l.cfg.Now().UTC())
			if qErr != nil {
				return qErr
			}
		}
		return nil
	})
	return latestSeq, nonTelemetryEvent, states, leasedLanes, runCount, maxFinishedAt, hasDesignDocs, windows, err
}

func (l *Loop) readIdleRefs(ctx context.Context) (trunkSHA, greenSHA string, ok bool) {
	if l.nonLanding || l.cfg.Root == "" {
		return "", "", true
	}
	trunkFull := "refs/heads/" + TrunkRef
	out, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(refname) %(objectname)", trunkFull, GreenRef)
	if err != nil {
		return "", "", false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ref, sha, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		switch ref {
		case trunkFull:
			trunkSHA = sha
		case GreenRef:
			greenSHA = sha
		}
	}
	return trunkSHA, greenSHA, trunkSHA != ""
}

func (l *Loop) captureIdleState(ctx context.Context, holds []Decision, at time.Time, pendingOnly bool) *idlePlanState {
	if l.cfg.Store == "" || l.cfg.ExitWhenDrained || l.cfg.Comments != nil ||
		l.activeCount() > 0 || l.heavyInFlight() || l.infrastructureStreak() > 0 || l.hasStageFaults() {
		return nil
	}
	head, _, states, leasedLanes, runCount, maxFinishedAt, hasDesignDocs, windows, err := l.readIdleStoreFingerprint(ctx, -1)
	if err != nil {
		return nil
	}
	view := l.startView.Load()
	var snap *startSnapshot
	if view != nil {
		snap = view.current.Load()
	} else if hasDesignDocs {
		// Without startView's filesystem watcher, a test or caller may edit a
		// design document on disk between passes without moving trunk or store.
		return nil
	}

	trunkSHA, greenSHA, ok := l.readIdleRefs(ctx)
	if !ok {
		return nil
	}

	var nextExpiry time.Time
	for _, exp := range windows {
		if exp.After(at) && (nextExpiry.IsZero() || exp.Before(nextExpiry)) {
			nextExpiry = exp
		}
	}
	for _, h := range holds {
		if h.BackendHoldCause != "" || h.Reason == ReasonBackendWindow {
			probeAt := at.Add(QuotaProbeInterval)
			if nextExpiry.IsZero() || probeAt.Before(nextExpiry) {
				nextExpiry = probeAt
			}
			break
		}
	}

	return &idlePlanState{
		eventSeq:      head,
		itemStates:    states,
		leasedLanes:   leasedLanes,
		runCount:      runCount,
		maxFinishedAt: maxFinishedAt,
		trunkSHA:      trunkSHA,
		greenSHA:      greenSHA,
		startSnap:     snap,
		lanes:         slices.Clone(l.lanes()),
		policy:        l.currentPolicy(),
		held:          l.heldReasonsSnapshot(),
		nextExpiry:    nextExpiry,
		pendingOnly:   pendingOnly,
	}
}

func (l *Loop) idleUnchanged(ctx context.Context, at time.Time, pendingOnly bool) bool {
	idle := l.idleState
	if idle == nil || idle.pendingOnly != pendingOnly {
		return false
	}
	if l.cfg.ExitWhenDrained || l.cfg.Comments != nil ||
		l.activeCount() > 0 || l.heavyInFlight() || l.infrastructureStreak() > 0 ||
		l.hasStageFaults() || len(l.completions) > 0 {
		return false
	}
	if !idle.nextExpiry.IsZero() && !at.Before(idle.nextExpiry) {
		return false
	}
	if l.currentPolicy() != idle.policy || !slices.Equal(l.lanes(), idle.lanes) {
		return false
	}
	if !maps.Equal(l.heldReasonsSnapshot(), idle.held) {
		return false
	}
	if view := l.startView.Load(); view != nil {
		if view.current.Load() != idle.startSnap {
			return false
		}
	}
	if trunkSHA, greenSHA, ok := l.readIdleRefs(ctx); !ok || trunkSHA != idle.trunkSHA || greenSHA != idle.greenSHA {
		return false
	}
	latestSeq, nonTelemetry, states, leasedLanes, runCount, maxFinishedAt, _, _, err := l.readIdleStoreFingerprint(ctx, idle.eventSeq)
	if err != nil || nonTelemetry {
		return false
	}
	if leasedLanes != idle.leasedLanes || runCount != idle.runCount || maxFinishedAt != idle.maxFinishedAt {
		return false
	}
	if !slices.Equal(states, idle.itemStates) {
		return false
	}
	idle.eventSeq = latestSeq
	return true
}
