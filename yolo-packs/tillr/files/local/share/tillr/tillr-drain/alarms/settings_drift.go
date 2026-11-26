package alarms

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	SettingsDrift       = "settings-drift"
	SettingsDriftKind   = "drain.settings-drift"
	SettingsCurrentKind = "drain.settings-current"
)

func init() {
	if err := event.Register(SettingsDriftKind, "drain"); err != nil {
		panic(err)
	}
	if err := event.Register(SettingsCurrentKind, "drain"); err != nil {
		panic(err)
	}
}

func settingsDriftAlarm(r *store.Reader) ([]Alarm, error) {
	var at int64
	var commit, detail string
	err := r.QueryRow(`SELECT ts,
 coalesce(object_id, ''),
 coalesce(json_extract(payload,'$.detail'), '')
FROM events
WHERE kind = ?
 AND seq > COALESCE((
   SELECT MAX(seq) FROM events
   WHERE kind = ?
      OR (kind = ? AND json_extract(payload,'$.role') = ?)
 ), 0)
ORDER BY seq DESC LIMIT 1`, SettingsDriftKind, SettingsCurrentKind, restart.BuildKind, restart.RoleDrain).Scan(&at, &commit, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cause := "running settings differ from .tillr.toml"
	if commit != "" {
		cause = fmt.Sprintf("landed .tillr.toml at %.12s differs from running settings", commit)
	}
	if detail != "" {
		cause += ": " + detail
	}
	return []Alarm{{
		Kind:    SettingsDrift,
		Backend: restart.RoleDrain,
		Since:   easternTime(time.UnixMilli(at)),
		Cause:   cause,
		Clears:  "the loop restarts or a landing matches the running settings",
		Command: "run tillr-drain restart to apply the updated .tillr.toml settings",
	}}, nil
}
