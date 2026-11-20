package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// loopEndedKind records that the loop ended on an error, and why. On 2026-11-20 a loop ended
// three minutes after it started on one run's SQLITE_BUSY, its keeper restarted it, and
// nothing in the store's events said the loop had died: the run was recorded timed out. The
// loop's own result file says it, but only to whoever reads that directory; the event puts
// the end in the same log as the runs it affected. A clean stop (a drained pile, an operator
// stop) is not an error and records nothing here.
const loopEndedKind = "drain.loop-ended"

func init() {
	if err := event.Register(loopEndedKind, Utility); err != nil {
		panic(err)
	}
}

type loopEnded struct {
	SessionID string `json:"session_id,omitempty"`
	Stop      string `json:"stop"`
	Detail    string `json:"detail"`
	ExitCode  int    `json:"exit_code,omitempty"`
	StepItem  string `json:"item_id,omitempty"`
	Step      string `json:"step,omitempty"`
	EndedAt   string `json:"ended_at"`
}

// recordLoopEnded appends the event when result is a fault stop. A store that cannot take it
// is reported and is not a second failure: the loop has already ended.
func (l *Loop) recordLoopEnded(result LoopResult) {
	if result.Stop != StopFault {
		return
	}
	at := l.cfg.Now().UTC()
	body, err := json.Marshal(loopEnded{SessionID: l.sessionID, Stop: result.Stop, Detail: result.Detail, ExitCode: result.ExitCode, StepItem: result.StopItemID, Step: result.StopStep, EndedAt: at.Format(time.RFC3339Nano)})
	if err != nil {
		l.reportf("loop: the loop's end was not recorded: %v\n", err)
		return
	}
	ctx := context.Background()
	s, err := store.Open(ctx, l.cfg.Store, !l.sessionInitialized)
	if err == nil {
		defer s.Close()
		id := l.sessionID
		if id == "" {
			id = "loop"
		}
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: loopEndedKind, ObjectType: "session", ObjectID: &id, Payload: body, At: at})
			return err
		})
	}
	if err != nil {
		l.reportf("loop: the loop's end was not recorded: %v\n", err)
	}
}
