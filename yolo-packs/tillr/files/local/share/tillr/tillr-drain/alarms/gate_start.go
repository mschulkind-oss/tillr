package alarms

import (
	"database/sql"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	GateStartFailed      = "gate-start-failed"
	GateStartFailedKind  = "drain.gate-start-failed"
	GateStartClearedKind = "drain.gate-start-cleared"
)

func init() {
	if err := event.Register(GateStartFailedKind, "drain"); err != nil {
		panic(err)
	}
	if err := event.Register(GateStartClearedKind, "drain"); err != nil {
		panic(err)
	}
}

func gateStartAlarm(r *store.Reader) ([]Alarm, error) {
	var at int64
	var detail string
	err := r.QueryRow(`SELECT ts,
 coalesce(json_extract(payload,'$.detail'), '')
FROM events
WHERE kind = ?
 AND seq > COALESCE((SELECT seq FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1), 0)
ORDER BY seq DESC LIMIT 1`, GateStartFailedKind, GateStartClearedKind).Scan(&at, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if detail == "" {
		detail = "the gate program could not start"
	}
	return []Alarm{{
		Kind:    GateStartFailed,
		Backend: "gate",
		Since:   easternTime(time.UnixMilli(at)),
		Cause:   detail,
		Clears:  "a gate starts",
		Command: "repair the missing gate runner or failed gate start, then tillr drain clear-fault",
	}}, nil
}
