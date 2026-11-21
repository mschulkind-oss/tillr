package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/user"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const runtimeConfigSchema = `CREATE TABLE IF NOT EXISTS drain_runtime_config (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('operator', 'migrated')),
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL
)`

const configChangedKind = "drain.config-changed"

func init() {
	if err := event.Register(configChangedKind, Utility); err != nil {
		panic(err)
	}
}

// Scale saves an ordinary-lane target without claiming lanes or signaling workers.
func Scale(ctx context.Context, path string, count int) error {
	if count < 0 {
		return Usagef("scale needs --lanes N (N >= 0)")
	}
	operator, err := user.Current()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, path, false)
	if err != nil {
		return err
	}
	defer s.Close()
	at := time.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(runtimeConfigSchema); err != nil {
			return err
		}
		var previous *int
		if err := tx.QueryRow(`SELECT value_json FROM drain_runtime_config WHERE key='ordinary_lanes'`).Scan(&previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if previous != nil && *previous == count {
			return nil
		}
		if _, err := tx.Exec(`INSERT INTO drain_runtime_config(key,value_json,source,updated_at,updated_by)
VALUES('ordinary_lanes',?,'operator',?,?) ON CONFLICT(key) DO UPDATE SET
value_json=excluded.value_json,source=excluded.source,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, count, at.Format(time.RFC3339Nano), operator.Username); err != nil {
			return err
		}
		return appendEffectOn(tx, configChangedKind, "drain-config", "ordinary_lanes", map[string]any{
			"key": "ordinary_lanes", "previous": previous, "value": count, "source": "operator", "updated_by": operator.Username,
		}, at)
	})
}

// lanes returns the immutable set of currently offered lanes, not the set of all
// lanes with unfinished work. Pending work outside the rotation still settles.
func (l *Loop) lanes() []string {
	if live := l.liveLanes.Load(); live != nil {
		return *live
	}
	return l.cfg.Lanes
}

func (l *Loop) refreshLaneCount(ctx context.Context, startup bool) error {
	if !l.cfg.LiveScale {
		return nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var count int
	var found bool
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='drain_runtime_config')`).Scan(&exists); err != nil || !exists {
			return err
		}
		err := r.QueryRow(`SELECT value_json FROM drain_runtime_config WHERE key='ordinary_lanes'`).Scan(&count)
		found = err == nil
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	s.Close()
	if err != nil || !found {
		return err
	}
	if count < 0 {
		return Usagef("invalid saved ordinary lane count %d", count)
	}
	current := l.lanes()
	target := make([]string, 0)
	var added []string
	for i := 0; i < count; i++ {
		lane := fmt.Sprintf("lane-%d", i+1)
		target = append(target, lane)
		if !slices.Contains(current, lane) {
			added = append(added, lane)
		}
	}
	for _, lane := range l.cfg.Lanes {
		if strings.HasPrefix(lane, "express-") {
			target = append(target, lane)
		}
	}
	if slices.Equal(current, target) {
		return nil
	}
	if !startup && !l.nonLanding && len(added) > 0 {
		// Prepare only the new slots, without moving a worktree used by a run.
		if err := l.stageLaneSet(ctx, added, false); err != nil {
			return err
		}
		if err := l.registerLaneSet(ctx, added); err != nil {
			return err
		}
	}
	// Order publication against a fast-start claim from an older proposal.
	l.laneOfferMu.Lock()
	l.liveLanes.Store(&target)
	l.laneOfferMu.Unlock()
	l.noteStartRotation(l.cfg.Now().UTC())
	if view := l.startView.Load(); view != nil {
		select {
		case view.laneTargetChanged <- struct{}{}:
		default:
		}
	}
	l.reportf("loop: ordinary lane target is now %d\n", count)
	return nil
}
