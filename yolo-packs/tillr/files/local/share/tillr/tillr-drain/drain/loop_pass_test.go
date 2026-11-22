package drain

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func readLoopPassEvents(t *testing.T, storePath string) []event.Event {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	var events []event.Event
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT kind, object_type, object_id, payload FROM events WHERE kind = ? ORDER BY seq`, LoopPassKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ev event.Event
			var objID *string
			var payload []byte
			if err := rows.Scan(&ev.Kind, &ev.ObjectType, &objID, &payload); err != nil {
				return err
			}
			ev.ObjectID = objID
			ev.Payload = payload
			events = append(events, ev)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestLoopPassThrottlesToOneMinute(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Steps = &recordingSteps{}
	l.sessionID = "session-pass-1"
	l.sessionInitialized = true
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return now }

	// First pass writes drain.loop-pass.
	var result LoopResult
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("first runPasses: %v", err)
	}
	if result.Stop != StopQueueDrained {
		t.Fatalf("first pass stop = %q, want %q", result.Stop, StopQueueDrained)
	}

	events := readLoopPassEvents(t, f.storePath)
	if len(events) != 1 {
		t.Fatalf("after first pass: got %d events, want 1", len(events))
	}
	if events[0].ObjectType != "session" {
		t.Fatalf("ObjectType = %q, want %q", events[0].ObjectType, "session")
	}
	if events[0].ObjectID == nil || *events[0].ObjectID == "" {
		t.Fatalf("ObjectID = %v, want non-empty", events[0].ObjectID)
	}
	if *events[0].ObjectID != "session-pass-1" {
		t.Fatalf("ObjectID = %q, want %q", *events[0].ObjectID, "session-pass-1")
	}
	var fact LoopPassFact
	if err := json.Unmarshal(events[0].Payload, &fact); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if fact.SessionID != "session-pass-1" {
		t.Fatalf("payload session_id = %q, want %q", fact.SessionID, "session-pass-1")
	}

	// Pass 30 seconds later writes no second drain.loop-pass.
	now = now.Add(30 * time.Second)
	result = LoopResult{}
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("second runPasses: %v", err)
	}
	if events = readLoopPassEvents(t, f.storePath); len(events) != 1 {
		t.Fatalf("after 30s pass: got %d events, want 1", len(events))
	}

	// Pass at 1 minute elapsed writes a second drain.loop-pass.
	now = now.Add(30 * time.Second)
	result = LoopResult{}
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("third runPasses: %v", err)
	}
	if events = readLoopPassEvents(t, f.storePath); len(events) != 2 {
		t.Fatalf("after 1m pass: got %d events, want 2", len(events))
	}
}

type loopPassReportWriter struct {
	buf      *lockedBuffer
	onRecord func()
	once     sync.Once
}

func (w *loopPassReportWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if w.onRecord != nil && strings.Contains(w.buf.String(), "loop: the loop pass was not recorded:") {
		w.once.Do(w.onRecord)
	}
	return n, err
}

func TestLoopPassLogsWriteFailureWithoutStoppingLoop(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Steps = &recordingSteps{}
	l.sessionID = "session-pass-err"
	l.sessionInitialized = true
	l.sessionOptions = []store.OpenOption{store.WithBusyTimeout(0)}
	holding, release := make(chan struct{}), make(chan struct{})
	report := &lockedBuffer{}
	l.cfg.Report = &loopPassReportWriter{
		buf:      report,
		onRecord: func() { close(release) },
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return now }

	// Hold a write lock so recordLoopPass fails with SQLITE_BUSY; logging the failure
	// releases the lock so the pass itself continues normally.
	holder, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	held := make(chan error, 1)
	go func() {
		held <- holder.WriteTx(context.Background(), func(*store.Tx) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	var result LoopResult
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("runPasses during busy store: %v", err)
	}
	if result.Stop != StopQueueDrained {
		t.Fatalf("stop = %q, want %q", result.Stop, StopQueueDrained)
	}
	if !strings.Contains(report.String(), "loop: the loop pass was not recorded:") {
		t.Fatalf("expected failure log in report, got %q", report.String())
	}

	if err := <-held; err != nil {
		t.Fatal(err)
	}

	if events := readLoopPassEvents(t, f.storePath); len(events) != 0 {
		t.Fatalf("expected 0 events after failed write, got %d", len(events))
	}

	// Because lastLoopPassAt was not updated on failure, the next pass records without waiting 1m.
	result = LoopResult{}
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("runPasses after lock release: %v", err)
	}
	if events := readLoopPassEvents(t, f.storePath); len(events) != 1 {
		t.Fatalf("expected 1 event after lock release, got %d", len(events))
	}
}
