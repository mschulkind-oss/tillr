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
	PiBuildStale       = "pi-build-stale"
	PiBuildStaleKind   = "drain.pi-build-stale"
	PiBuildCurrentKind = "drain.pi-build-current"
)

func init() {
	if err := event.Register(PiBuildStaleKind, "drain"); err != nil {
		panic(err)
	}
	if err := event.Register(PiBuildCurrentKind, "drain"); err != nil {
		panic(err)
	}
}

func piBuildStaleAlarm(r *store.Reader) ([]Alarm, error) {
	var at int64
	var staleKey, wantedKey, reason, detail string
	err := r.QueryRow(`SELECT ts,
 coalesce(json_extract(payload,'$.stale_key'), ''),
 coalesce(json_extract(payload,'$.wanted_key'), ''),
 coalesce(json_extract(payload,'$.reason'), ''),
 coalesce(json_extract(payload,'$.detail'), '')
FROM events
WHERE kind = ?
 AND seq > COALESCE((SELECT seq FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1), 0)
ORDER BY seq DESC LIMIT 1`, PiBuildStaleKind, PiBuildCurrentKind).Scan(&at, &staleKey, &wantedKey, &reason, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if staleKey == "" {
		staleKey = "none"
	}
	if reason == "" {
		reason = "failed"
	}
	cause := fmt.Sprintf("pi build is stale: active key %s, wanted key %s (%s", staleKey, wantedKey, reason)
	if detail != "" {
		cause += ": " + detail
	}
	cause += ")"
	return []Alarm{{
		Kind:    PiBuildStale,
		Backend: "pi",
		Since:   easternTime(time.UnixMilli(at)),
		Cause:   cause,
		Clears:  "a verified build exists for the wanted key",
		Command: "fix the build error and run bin/pi-forks build-pi to build the wanted pi fork tip",
	}}, nil
}
