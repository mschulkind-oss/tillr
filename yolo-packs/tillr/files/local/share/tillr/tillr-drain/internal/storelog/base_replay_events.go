package storelog

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
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
		if err := event.Register(kind, drainmodel.Utility); err != nil {
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

// FormatBaseReplayOutcome classifies a completed base replay into cancelled, fault, unknown, red, or green.
func FormatBaseReplayOutcome(ctx context.Context, rev string, known bool, outRev string, redCount int, err, restartErr error) string {
	if ctx.Err() != nil || (restartErr != nil && errors.Is(err, restartErr)) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	if err != nil {
		return "fault"
	}
	if !known || (outRev != "" && outRev != rev) {
		return "unknown"
	}
	if redCount > 0 {
		return "red"
	}
	return "green"
}

// RecordBaseReplayEvent appends one base-replay lifecycle event best-effort.
func RecordBaseReplayEvent(ctx context.Context, storePath, kind, runID, rev string, payload any, at time.Time, reportf func(string, ...any)) {
	if storePath == "" {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		if reportf != nil {
			reportf("loop: %s was not marshaled: %v\n", kind, err)
		}
		return
	}
	objectType, objectID := "run", runID
	if objectID == "" {
		objectType, objectID = "commit", rev
	}
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		if ctx.Err() == nil && reportf != nil {
			reportf("loop: %s was not recorded: %v\n", kind, err)
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
	if err := errors.Join(writeErr, closeErr); err != nil && ctx.Err() == nil && reportf != nil {
		reportf("loop: %s was not recorded: %v\n", kind, err)
	}
}
