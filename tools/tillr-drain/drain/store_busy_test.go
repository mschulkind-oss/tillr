package drain

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestBusyWriteRecordedAtNextSuccessfulWrite(t *testing.T) {
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
	stop := l.keepSessionAlive(ctx, cancelWork)

	offset.Add(int64(time.Second))
	ticks <- l.cfg.Now()
	waitUntil(t, func() bool {
		return strings.Contains(report.String(), "the session heartbeat was not recorded")
	})

	// While the store is busy, no drain.store-busy event can be written yet.
	if got := readStoreBusyEvents(t, f.storePath); len(got) != 0 {
		t.Fatalf("expected no store-busy event while store is busy, got %+v", got)
	}

	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}

	// On the next successful write, the refused busy write is recorded.
	offset.Add(int64(time.Second))
	want := l.cfg.Now().UnixMilli()
	ticks <- l.cfg.Now()
	waitUntil(t, func() bool { return refreshedAt(t, f.storePath, l.sessionID) == want })
	if err := stop(); err != nil {
		t.Fatalf("keepSessionAlive returned %v", err)
	}

	got := readStoreBusyEvents(t, f.storePath)
	if len(got) != 1 {
		t.Fatalf("expected 1 store-busy event, got %d: %+v", len(got), got)
	}
	if got[0].Operation != "session-heartbeat" {
		t.Fatalf("expected operation %q, got %q", "session-heartbeat", got[0].Operation)
	}
	if got[0].WaitedMS < 0 {
		t.Fatalf("expected non-negative waited_ms, got %d", got[0].WaitedMS)
	}
}

func readStoreBusyEvents(t *testing.T, path string) []StoreBusyFact {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var facts []StoreBusyFact
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, StoreBusyKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var fact StoreBusyFact
			if err := json.Unmarshal(raw, &fact); err != nil {
				return err
			}
			facts = append(facts, fact)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return facts
}
