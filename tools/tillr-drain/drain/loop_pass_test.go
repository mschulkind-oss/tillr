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
	assertLoopPassSum(t, fact)

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

	// Hold a write lock right before recordLoopPass writes so it fails with SQLITE_BUSY;
	// logging the failure releases the lock so the loop finishes normally.
	holder, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	held := make(chan error, 1)
	l.beforeRecordLoopPass = func() {
		l.beforeRecordLoopPass = nil
		go func() {
			held <- holder.WriteTx(context.Background(), func(*store.Tx) error {
				close(holding)
				<-release
				return nil
			})
		}()
		<-holding
	}

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

func assertLoopPassSum(t *testing.T, fact LoopPassFact) {
	t.Helper()
	if fact.WallMS < 0 {
		t.Fatalf("wall_ms = %d, want >= 0", fact.WallMS)
	}
	if fact.PhasesMS == nil {
		t.Fatal("phases_ms is nil, want populated map")
	}
	if _, ok := fact.PhasesMS[LoopPassRemainderPhase]; !ok {
		t.Fatalf("phases_ms missing remainder key %q: %+v", LoopPassRemainderPhase, fact.PhasesMS)
	}
	var sum int64
	for name, ms := range fact.PhasesMS {
		if ms < 0 {
			t.Fatalf("phase %q has negative duration %d", name, ms)
		}
		sum += ms
	}
	if sum != fact.WallMS {
		t.Fatalf("sum(phases_ms) = %d, want wall_ms = %d (phases: %+v)", sum, fact.WallMS, fact.PhasesMS)
	}
}

func TestLoopPassTimerRemainderAndAccumulation(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	pt := newLoopPassTimer(func() time.Time { return now })

	// 1.5ms outside any named phase.
	now = now.Add(1500 * time.Microsecond)

	// Repeated phase accumulates sub-millisecond durations before truncating to ms:
	// 1.4ms + 1.4ms = 2.8ms -> 2ms.
	endA := pt.StartPhase("alpha")
	now = now.Add(1400 * time.Microsecond)
	endA()

	// 3.2ms outside any named phase.
	now = now.Add(3200 * time.Microsecond)

	endB := pt.StartPhase("beta")
	now = now.Add(5600 * time.Microsecond)
	endB()

	endA2 := pt.StartPhase("alpha")
	now = now.Add(1400 * time.Microsecond)
	endA2()

	// Total elapsed = 1.5 + 1.4 + 3.2 + 5.6 + 1.4 = 13.1ms -> wallMS = 13.
	// alpha = 2.8ms -> 2, beta = 5.6ms -> 5, other = 13 - (2 + 5) = 6.
	at, wallMS, phasesMS := pt.Finish()
	if !at.Equal(now) {
		t.Fatalf("at = %v, want %v", at, now)
	}
	if wallMS != 13 {
		t.Fatalf("wallMS = %d, want 13", wallMS)
	}
	if phasesMS["alpha"] != 2 {
		t.Fatalf("alpha = %d, want 2", phasesMS["alpha"])
	}
	if phasesMS["beta"] != 5 {
		t.Fatalf("beta = %d, want 5", phasesMS["beta"])
	}
	if phasesMS[LoopPassRemainderPhase] != 6 {
		t.Fatalf("other = %d, want 6", phasesMS[LoopPassRemainderPhase])
	}
	assertLoopPassSum(t, LoopPassFact{SessionID: "test", WallMS: wallMS, PhasesMS: phasesMS})
}

type timedSyncSteps struct {
	recordingSteps
	onSync func()
}

func (s *timedSyncSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	if s.onSync != nil {
		s.onSync()
	}
	return s.recordingSteps.Sync(ctx)
}

func TestLoopPassPayloadCarriesWallAndPhaseTimes(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.sessionID = "session-pass-timing"
	l.sessionInitialized = true
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time {
		// Advance 250µs on each clock read so unattributed gaps exist too.
		now = now.Add(250 * time.Microsecond)
		return now
	}
	l.cfg.Steps = &timedSyncSteps{
		onSync: func() {
			now = now.Add(250 * time.Millisecond)
		},
	}

	var result LoopResult
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("runPasses: %v", err)
	}
	if result.Stop != StopQueueDrained {
		t.Fatalf("stop = %q, want %q", result.Stop, StopQueueDrained)
	}

	events := readLoopPassEvents(t, f.storePath)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(events[0].Payload, &raw); err != nil {
		t.Fatalf("unmarshal raw payload: %v", err)
	}
	for _, key := range []string{"session_id", "wall_ms", "phases_ms"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("payload missing key %q: %s", key, string(events[0].Payload))
		}
	}

	var fact LoopPassFact
	if err := json.Unmarshal(events[0].Payload, &fact); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if fact.SessionID != "session-pass-timing" {
		t.Fatalf("session_id = %q, want %q", fact.SessionID, "session-pass-timing")
	}
	assertLoopPassSum(t, fact)

	if fact.WallMS < 250 {
		t.Fatalf("wall_ms = %d, want >= 250", fact.WallMS)
	}
	if got := fact.PhasesMS["refresh_documents"]; got < 250 {
		t.Fatalf("phases_ms[refresh_documents] = %d, want >= 250 (phases: %+v)", got, fact.PhasesMS)
	}
	for _, phase := range []string{"gate_restart", "observe_trunk", "refresh_documents", "plan", LoopPassRemainderPhase} {
		if _, ok := fact.PhasesMS[phase]; !ok {
			t.Fatalf("phases_ms missing expected phase %q: %+v", phase, fact.PhasesMS)
		}
	}
}

func TestLoopPassRealWallTimeAndWaitForTrigger(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.sessionID = "session-pass-real"
	l.sessionInitialized = true
	l.cfg.Now = time.Now
	l.cfg.ExitWhenDrained = false
	l.cfg.StorePollInterval = 25 * time.Millisecond
	l.cfg.NoNotify = true
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	l.triggerTimer = func(d time.Duration) (<-chan time.Time, func()) {
		calls++
		ch := make(chan time.Time, 1)
		if calls == 1 {
			// Let the first pass wait 25ms and finish cleanly so drain.loop-pass is recorded.
			timer := time.AfterFunc(25*time.Millisecond, func() { ch <- time.Now() })
			return ch, func() { timer.Stop() }
		}
		// Cancel on the second pass so runPasses exits cleanly.
		cancel()
		return ch, func() {}
	}
	l.cfg.Steps = &timedSyncSteps{
		onSync: func() {
			time.Sleep(20 * time.Millisecond)
		},
	}

	var result LoopResult
	if err := l.runPasses(ctx, &result, false, nil); err != nil {
		t.Fatalf("runPasses: %v", err)
	}

	events := readLoopPassEvents(t, f.storePath)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	var fact LoopPassFact
	if err := json.Unmarshal(events[0].Payload, &fact); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	assertLoopPassSum(t, fact)
	if fact.WallMS < 40 {
		t.Fatalf("wall_ms = %d, want >= 40 (phases: %+v)", fact.WallMS, fact.PhasesMS)
	}
	if fact.PhasesMS["refresh_documents"] < 15 {
		t.Fatalf("phases_ms[refresh_documents] = %d, want >= 15 (phases: %+v)", fact.PhasesMS["refresh_documents"], fact.PhasesMS)
	}
	if fact.PhasesMS["wait_for_trigger"] < 20 {
		t.Fatalf("phases_ms[wait_for_trigger] = %d, want >= 20 (phases: %+v)", fact.PhasesMS["wait_for_trigger"], fact.PhasesMS)
	}
}
