package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

type wakeSource struct {
	wal     string
	watcher *fsnotify.Watcher
	ticks   <-chan time.Time
	ticker  *time.Ticker
}

// Watch the parent directory, not the WAL inode: SQLite may unlink and
// recreate the file. Both callers install this before the first evaluation.
func watchWAL(path string, ticks <-chan time.Time) (*wakeSource, error) {
	wal := filepath.Clean(path + "-wal")
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filepath.Dir(wal)); err != nil {
		w.Close()
		return nil, err
	}
	s := &wakeSource{wal: wal, watcher: w, ticks: ticks}
	if ticks == nil {
		s.ticker = time.NewTicker(60 * time.Second)
		s.ticks = s.ticker.C
	}
	return s, nil
}

func (s *wakeSource) close() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	s.watcher.Close()
}

type wakeReason int

const (
	walWake wakeReason = iota
	timerWake
	deadlineWake
)

func (s *wakeSource) next(ctx context.Context, deadline <-chan time.Time) (wakeReason, error) {
	for {
		select {
		case <-ctx.Done():
			return walWake, ctx.Err()
		case <-deadline:
			return deadlineWake, nil
		case <-s.ticks:
			return timerWake, nil
		case evt, ok := <-s.watcher.Events:
			if !ok {
				return walWake, fmt.Errorf("WAL watcher closed")
			}
			if filepath.Clean(evt.Name) == s.wal {
				return walWake, nil
			}
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return walWake, fmt.Errorf("WAL watcher closed")
			}
			if err != fsnotify.ErrEventOverflow {
				return walWake, err
			}
			// A dropped change could have fired a rule; read a fresh snapshot.
			return walWake, nil
		}
	}
}
