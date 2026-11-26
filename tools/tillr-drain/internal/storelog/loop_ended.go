package storelog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// LoopEndedKind records that the loop ended on an error, and why.
const LoopEndedKind = "drain.loop-ended"

func init() {
	if err := event.Register(LoopEndedKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// LoopEnded is the payload of a LoopEndedKind event.
type LoopEnded struct {
	SessionID string `json:"session_id,omitempty"`
	Stop      string `json:"stop"`
	Detail    string `json:"detail"`
	ExitCode  int    `json:"exit_code,omitempty"`
	StepItem  string `json:"item_id,omitempty"`
	Step      string `json:"step,omitempty"`
	EndedAt   string `json:"ended_at"`
}

// RecordLoopEnded appends a LoopEndedKind event. A store that cannot take it
// is reported and is not a second failure: the loop has already ended.
func RecordLoopEnded(storePath string, initialized bool, sessionID, stop, detail string, exitCode int, stepItem, step string, at time.Time, reportf func(string, ...any)) {
	at = at.UTC()
	body, err := json.Marshal(LoopEnded{
		SessionID: sessionID,
		Stop:      stop,
		Detail:    detail,
		ExitCode:  exitCode,
		StepItem:  stepItem,
		Step:      step,
		EndedAt:   at.Format(time.RFC3339Nano),
	})
	if err != nil {
		if reportf != nil {
			reportf("loop: the loop's end was not recorded: %v\n", err)
		}
		return
	}
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, !initialized)
	if err == nil {
		defer s.Close()
		id := sessionID
		if id == "" {
			id = "loop"
		}
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: LoopEndedKind, ObjectType: "session", ObjectID: &id, Payload: body, At: at})
			return err
		})
	}
	if err != nil && reportf != nil {
		reportf("loop: the loop's end was not recorded: %v\n", err)
	}
}
