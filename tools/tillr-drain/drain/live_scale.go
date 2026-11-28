package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const configChangedKind = "drain.config-changed"

func init() {
	if err := event.Register(configChangedKind, Utility); err != nil {
		panic(err)
	}
}

// Scale saves an ordinary-lane target without claiming lanes or signaling workers.
func Scale(ctx context.Context, path string, count int) error {
	if count < 0 {
		return Usagef("scale needs --max-agents N (N >= 0)")
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

func ordinaryLaneCount(lanes []string) int {
	n := 0
	for _, lane := range lanes {
		if !strings.HasPrefix(lane, "express-") && !documentDesk(lane) {
			n++
		}
	}
	return n
}

func (l *Loop) targetLanesForOrdinaryCount(count int) []string {
	target := make([]string, 0, max(0, count)+len(l.cfg.Lanes))
	for i := 0; i < count; i++ {
		target = append(target, fmt.Sprintf("lane-%d", i+1))
	}
	for _, lane := range l.cfg.Lanes {
		if strings.HasPrefix(lane, "express-") {
			target = append(target, lane)
		}
	}
	return target
}

func readSavedOrdinaryLaneTarget(ctx context.Context, path string) (int, bool, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return 0, false, err
	}
	defer s.Close()
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
	return count, found, err
}

func (l *Loop) storeLiveLanes(ctx context.Context, target []string, refreshSession bool) error {
	if slices.Equal(l.lanes(), target) {
		return nil
	}
	l.laneOfferMu.Lock()
	l.liveLanes.Store(&target)
	l.laneOfferMu.Unlock()
	l.signalPassWake()
	if refreshSession {
		return l.refreshSession(ctx, l.cfg.Now().Add(PassInterval))
	}
	return nil
}

// resolveStartupLaneCount reconciles the loop's startup ordinary lane count with
// drain_runtime_config and writes one provenance line before lanes are staged.
func (l *Loop) resolveStartupLaneCount(ctx context.Context, inPlaceRestart bool) error {
	if !l.cfg.LiveScale {
		if l.cfg.ExplicitLanes != nil {
			l.reportf("loop: ordinary lanes: %d (from --lanes flag)\n", *l.cfg.ExplicitLanes)
			return nil
		}
		l.reportf("loop: ordinary lanes: %d (from config)\n", ordinaryLaneCount(l.cfg.Lanes))
		return nil
	}
	if l.cfg.ExplicitLanes != nil && !inPlaceRestart {
		count := *l.cfg.ExplicitLanes
		if count < 0 {
			return Usagef("loop needs --max-agents N (N >= 0)")
		}
		operator, err := user.Current()
		if err != nil {
			return err
		}
		s, err := store.Open(ctx, l.cfg.Store, false)
		if err != nil {
			return err
		}
		at := l.cfg.Now().UTC()
		var previous *int
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			if _, err := tx.Exec(runtimeConfigSchema); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT value_json FROM drain_runtime_config WHERE key='ordinary_lanes'`).Scan(&previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO drain_runtime_config(key,value_json,source,updated_at,updated_by)
VALUES('ordinary_lanes',?,'operator',?,?) ON CONFLICT(key) DO UPDATE SET
value_json=excluded.value_json,source=excluded.source,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, count, at.Format(time.RFC3339Nano), operator.Username); err != nil {
				return err
			}
			if previous != nil && *previous != count {
				return appendEffectOn(tx, configChangedKind, "drain-config", "ordinary_lanes", map[string]any{
					"key": "ordinary_lanes", "previous": previous, "value": count, "source": "operator", "updated_by": operator.Username,
				}, at)
			}
			return nil
		})
		s.Close()
		if err != nil {
			return err
		}
		if err := l.storeLiveLanes(ctx, l.targetLanesForOrdinaryCount(count), true); err != nil {
			return err
		}
		if previous != nil && *previous != count {
			l.reportf("loop: ordinary lanes: %d (from --lanes flag; replaced saved target %d)\n", count, *previous)
		} else {
			l.reportf("loop: ordinary lanes: %d (from --lanes flag)\n", count)
		}
		return nil
	}

	count, found, err := readSavedOrdinaryLaneTarget(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	if found {
		if count < 0 {
			return Usagef("invalid saved ordinary lane count %d", count)
		}
		if err := l.storeLiveLanes(ctx, l.targetLanesForOrdinaryCount(count), true); err != nil {
			return err
		}
		for i := range l.policyState.StartOptions {
			if l.policyState.StartOptions[i].Name == "lanes" || l.policyState.StartOptions[i].Name == "max-agents" {
				l.policyState.StartOptions[i].Value = strconv.Itoa(count)
				l.policyState.StartOptions[i].Source = "saved scale target"
				l.policyState.StartOptions[i].Detail = "drain_runtime_config"
			}
		}
		l.reportf("loop: ordinary lanes: %d (from saved target)\n", count)
		return nil
	}
	if l.cfg.ExplicitLanes != nil {
		l.reportf("loop: ordinary lanes: %d (from --lanes flag)\n", *l.cfg.ExplicitLanes)
		return nil
	}
	if l.cfg.LanesSource != "" {
		l.reportf("loop: ordinary lanes: %d (from %s)\n", ordinaryLaneCount(l.cfg.Lanes), l.cfg.LanesSource)
		return nil
	}
	l.reportf("loop: ordinary lanes: %d (from default)\n", ordinaryLaneCount(l.cfg.Lanes))
	return nil
}

// FormatStartOptionsLine formats the resolved start options and their sources
// for the loop's startup log line.
func FormatStartOptionsLine(opts []StartOption) string {
	parts := make([]string, 0, len(opts))
	for _, opt := range opts {
		src := opt.Source
		if opt.Detail != "" {
			src = fmt.Sprintf("%s %s", opt.Source, opt.Detail)
		}
		parts = append(parts, fmt.Sprintf("%s=%s (%s)", opt.Name, opt.Value, src))
	}
	return "loop: start-options: " + strings.Join(parts, ", ")
}

func (l *Loop) refreshLaneCount(ctx context.Context, startup bool) error {
	if !l.cfg.LiveScale {
		return nil
	}
	count, found, err := readSavedOrdinaryLaneTarget(ctx, l.cfg.Store)
	if err != nil || !found {
		return err
	}
	if count < 0 {
		return Usagef("invalid saved ordinary lane count %d", count)
	}
	current := l.lanes()
	target := l.targetLanesForOrdinaryCount(count)
	var added []string
	for _, lane := range target {
		if !strings.HasPrefix(lane, "express-") && !slices.Contains(current, lane) {
			added = append(added, lane)
		}
	}
	if slices.Equal(current, target) {
		return nil
	}
	if !startup && !l.nonLanding && !l.cfg.ItemWorktrees && len(added) > 0 {
		// Prepare only the new slots, without moving a worktree used by a run.
		if err := l.stageLaneSet(ctx, added, false); err != nil {
			return err
		}
		if err := l.registerLaneSet(ctx, added); err != nil {
			return err
		}
	}
	// Order publication against a fast-start claim from an older proposal.
	if err := l.storeLiveLanes(ctx, target, startup); err != nil {
		return err
	}
	l.reportf("loop: ordinary lane target is now %d\n", count)
	return nil
}
