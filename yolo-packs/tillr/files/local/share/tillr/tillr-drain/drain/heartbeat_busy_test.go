package drain

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// The 2026-11-18 drain stops: the session heartbeat's write waited out the busy timeout
// behind another writer, and the failed heartbeat cancelled the loop's work and stopped
// it. A heartbeat that meets a held write lock now reports that its beat was not
// recorded and keeps beating; the next tick records the session again, and nothing was
// cancelled. The lock is held on another connection and the heartbeat's open has a zero
// busy timeout, so the beat meets the held lock at once rather than after a wait.
func TestABusyStoreDoesNotStopTheHeartbeat(t *testing.T) {
	f, l := passFixture(t, "item-a")
	var offset atomic.Int64
	base := l.cfg.Now()
	l.cfg.Now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	if err := l.refreshSession(context.Background(), l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	l.sessionOptions = []store.OpenOption{store.WithBusyTimeout(0)}
	report := &lockedBuffer{}
	l.cfg.Report = report
	ticks := make(chan time.Time)
	l.heartbeatClock = func() heartbeatTicker { return heartbeatTicker{C: ticks, Stop: func() {}} }

	holder, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holding, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- holder.WriteTx(context.Background(), func(*store.Tx) error { close(holding); <-release; return nil })
	}()
	<-holding

	ctx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	var cancelled atomic.Bool
	stop := l.keepSessionAlive(ctx, func() { cancelled.Store(true); cancelWork() })
	offset.Add(int64(time.Second))
	ticks <- l.cfg.Now()
	waitUntil(t, func() bool {
		return cancelled.Load() || strings.Contains(report.String(), "the session heartbeat was not recorded")
	})
	if cancelled.Load() {
		t.Fatal("a busy store's heartbeat cancelled the loop's work")
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	offset.Add(int64(time.Second))
	want := l.cfg.Now().UnixMilli()
	ticks <- l.cfg.Now()
	waitUntil(t, func() bool { return refreshedAt(t, f.storePath, l.sessionID) == want })
	if err := stop(); err != nil {
		t.Fatalf("the heartbeat answered %v after a busy beat", err)
	}
	if cancelled.Load() {
		t.Fatal("the heartbeat cancelled the loop's work")
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("the awaited condition never held")
		}
		time.Sleep(time.Millisecond)
	}
}

func refreshedAt(t *testing.T, path, session string) int64 {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var at int64
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT refreshed_at FROM drain_sessions WHERE session_id = ?`, session).Scan(&at)
	}); err != nil {
		t.Fatal(err)
	}
	return at
}
