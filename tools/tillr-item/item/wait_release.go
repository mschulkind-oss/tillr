package item

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// WaitReleasedKind records why a named wait ended and which finished run it answered.
const WaitReleasedKind = "item.wait-released"

func init() {
	if err := event.Register(WaitReleasedKind, Utility); err != nil {
		panic(err)
	}
}

// LandedBlocker returns the item that fulfilled a named wait, if any. Closed and
// resolved-elsewhere are not landings; an absent blocker supplies no evidence.
func LandedBlocker(r *store.Reader, wait *WaitingOn) (string, error) {
	if wait == nil {
		return "", nil
	}
	id, ok := strings.CutPrefix(wait.Waiter, "item:")
	if !ok || id == "" {
		return "", nil
	}
	var state string
	err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && state != "landed") {
		return "", nil
	}
	return id, err
}

func recordWaitRelease(tx *store.Tx, id, blocker string, at time.Time) error {
	p := struct {
		Blocker string `json:"blocker"`
		RunID   string `json:"run_id,omitempty"`
		Reason  string `json:"reason"`
	}{Blocker: blocker, Reason: "blocker landed"}
	hasRuns, err := hasReaderTable(tx.Reader(), "runs")
	if err != nil {
		return err
	}
	if hasRuns {
		err := tx.QueryRow(`SELECT run_id FROM runs WHERE item_id=? AND state IN ('exited', 'failed-to-start', 'timed-out')
			ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`, id).Scan(&p.RunID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: WaitReleasedKind, ObjectType: "item", ObjectID: &id, Payload: body, At: at})
	return err
}
