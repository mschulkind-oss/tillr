package alarms

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	UnrecordedTrunkCommit      = "unrecorded-trunk-commit"
	UnrecordedTrunkCommitKind  = "drain.unrecorded-trunk-commit"
	UnrecordedTrunkClearedKind = "drain.unrecorded-trunk-cleared"
)

func init() {
	if err := event.Register(UnrecordedTrunkCommitKind, "drain"); err != nil {
		panic(err)
	}
	if err := event.Register(UnrecordedTrunkClearedKind, "drain"); err != nil {
		panic(err)
	}
}

func unrecordedTrunkAlarm(r *store.Reader) ([]Alarm, error) {
	var at int64
	var commit string
	err := r.QueryRow(`SELECT ts,
 coalesce(json_extract(payload,'$.commit'), '')
FROM events
WHERE kind = ?
 AND seq > COALESCE((SELECT seq FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1), 0)
ORDER BY seq DESC LIMIT 1`, UnrecordedTrunkCommitKind, UnrecordedTrunkClearedKind).Scan(&at, &commit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if commit == "" {
		return nil, nil
	}
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	return []Alarm{{
		Kind:    UnrecordedTrunkCommit,
		Backend: "trunk",
		Since:   easternTime(time.UnixMilli(at)),
		Cause:   fmt.Sprintf("trunk commit %s was not made by a landing", commit),
		Clears:  "the unrecorded commit is removed from trunk or recorded as a landing",
		Command: fmt.Sprintf("inspect git show %s and reset trunk or run tillr drain accept-rewrite", short),
	}}, nil
}
