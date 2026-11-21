package drain

import (
	"context"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

const startViewRefreshInterval = 15 * time.Second

type startQueueRow struct{ id, rank string }

// A snapshot and its collections are immutable after publication. The rows use
// planner order, but carry no decision about whether any item can start.
type startSnapshot struct {
	items    []startQueueRow
	occupied map[string]bool
}

type startView struct {
	current atomic.Pointer[startSnapshot]
	updated chan struct{} // Coalesced notification; nothing in dispatch waits on it.
}

func newStartView() *startView {
	return &startView{updated: make(chan struct{}, 1)}
}

func (v *startView) snapshot() startSnapshot {
	if current := v.current.Load(); current != nil {
		return *current
	}
	return startSnapshot{}
}

// watch seeds once and then refreshes on the timer, all off the dispatch path.
// It owns publication; a failed read leaves the last good snapshot in place.
// The returned function cancels and joins the watcher.
func (v *startView) watch(parent context.Context, interval time.Duration, read func(context.Context) (startSnapshot, error)) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for ctx.Err() == nil {
			next, err := read(ctx)
			if err == nil && ctx.Err() == nil {
				prev := v.current.Load()
				if prev == nil || !slices.Equal(prev.items, next.items) || !maps.Equal(prev.occupied, next.occupied) {
					v.current.Store(&next)
					select {
					case v.updated <- struct{}{}:
					default:
					}
				}
			}
			timer.Reset(interval)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func readStartSnapshot(ctx context.Context, path string, at time.Time) (startSnapshot, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return startSnapshot{}, err
	}
	defer s.Close()
	var next startSnapshot
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id, COALESCE(rank, '') FROM items ` + queueOrderSQL)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row startQueueRow
			if err := rows.Scan(&row.id, &row.rank); err != nil {
				return err
			}
			next.items = append(next.items, row)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		next.occupied, err = readLanes(r, at)
		return err
	})
	return next, err
}
