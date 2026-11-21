package alarms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const rollFailedKind = "drain.roll-failed"
const storeRollKind = "store-roll-blocked"

type rollFailure struct {
	Commit string `json:"commit"`
	Step   string `json:"step"`
	Detail string `json:"detail"`
	Mover  string `json:"mover"`
	At     int64  `json:"ts"`
}

func (f rollFailure) alarm() Alarm {
	return Alarm{Kind: storeRollKind, Backend: restart.RoleDrain, Since: easternTime(time.UnixMilli(f.At)),
		Cause: fmt.Sprintf("roll of %.12s: %s: %s", f.Commit, f.Step, f.Detail), Clears: "a later drain build starts",
		Command: "resolve the reported store problem, then retry the roll with just tillr-roll"}
}

func init() {
	if err := event.Register(rollFailedKind, "drain"); err != nil {
		panic(err)
	}
}

// RecordStoreRollFailure is the same event the repository roller writes on a
// failed store preflight. Recording it must not itself migrate the store.
func RecordStoreRollFailure(ctx context.Context, path, commit, step string, cause error, at time.Time) error {
	failed := rollFailure{Commit: commit, Step: step, Detail: strings.Join(strings.Fields(cause.Error()), " "), Mover: "operator", At: at.UnixMilli()}
	body, err := json.Marshal(failed)
	if err != nil {
		return err
	}
	if err := appendRollFailure(ctx, path, commit, body, at); err != nil {
		if saveErr := saveStoreRollFallback(path, body); saveErr != nil {
			return errors.Join(err, saveErr)
		}
	}
	return nil
}

func appendRollFailure(ctx context.Context, path, commit string, body []byte, at time.Time) error {
	s, err := store.Open(ctx, path, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: rollFailedKind, ObjectType: "commit", ObjectID: &commit, Payload: body, At: at})
		return err
	})
}

// When SQLite itself cannot record the failure, the roller leaves the same
// alarm in a file beside it. The next successfully published loop clears it.
func storeRollFallbackPath(path string) string { return path + ".roll-alarm.json" }

func saveStoreRollFallback(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".roll-alarm-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(body)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), storeRollFallbackPath(path))
}

func ReadStoreRollFallback(path string) (*Alarm, error) {
	body, err := os.ReadFile(storeRollFallbackPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var failed rollFailure
	if err := json.Unmarshal(body, &failed); err != nil {
		return nil, err
	}
	alarm := failed.alarm()
	return &alarm, nil
}

func ClearStoreRollFallback(path string) error {
	err := os.Remove(storeRollFallbackPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func storeRollAlarm(r *store.Reader) ([]Alarm, error) {
	var failed rollFailure
	err := r.QueryRow(`SELECT ts, object_id, json_extract(payload,'$.step'), json_extract(payload,'$.detail')
FROM events WHERE kind=? AND json_extract(payload,'$.mover')='operator' AND seq > COALESCE(
 (SELECT seq FROM events WHERE kind=? AND json_extract(payload,'$.role')=? ORDER BY seq DESC LIMIT 1), 0)
ORDER BY seq DESC LIMIT 1`, rollFailedKind, restart.BuildKind, restart.RoleDrain).Scan(&failed.At, &failed.Commit, &failed.Step, &failed.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []Alarm{failed.alarm()}, nil
}
