package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// lockedBuffer is a Report sink a test reads after Run while the loop's goroutines wrote it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// cyclesWaitingSteps holds the Loop's startup sync open until done is closed, moving the
// Loop's clock past one checkpoint interval first.
type cyclesWaitingSteps struct {
	recordingSteps
	advance func()
	done    <-chan struct{}
	t       *testing.T
}

func (s *cyclesWaitingSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	s.advance()
	select {
	case <-s.done:
	case <-ctx.Done():
		return SyncOutcome{}, ctx.Err()
	case <-time.After(hangGuard):
		s.t.Error("the running loop never reached the checkpoint cycle the test waits for")
	}
	return SyncOutcome{Skipped: "a cycle-waiting step set reads no documents"}, nil
}

// checkpointCycleLoop is a Loop whose clock passes one checkpoint interval during its startup
// sync and then stands still, so any checkpoint after the first is a retry, not a new
// interval. first runs before the first cycle; each cycle's answer goes to cycles.
func checkpointCycleLoop(t *testing.T, done chan struct{}, first func(), cycles func(store.Checkpointed)) (*Loop, *lockedBuffer) {
	t.Helper()
	f := newRefusalLoopFixture(t)
	var offset atomic.Int64
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	ticks := make(chan time.Time, 1)
	steps := &cyclesWaitingSteps{
		advance: func() { offset.Add(int64(CheckpointInterval)); ticks <- base.Add(time.Duration(offset.Load())) },
		done:    done,
		t:       t,
	}
	l := f.loop(t, steps, &itemContentStager{})
	l.cfg.Now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	report := &lockedBuffer{}
	l.cfg.Report = report
	var once sync.Once
	l.beforeCheckpoint = func() { once.Do(first) }
	l.heartbeatClock = func() heartbeatTicker { return heartbeatTicker{C: ticks, Stop: func() {}} }
	l.checkpointed = func(c store.Checkpointed) {
		cycles(c)
		select {
		case <-done:
		default:
			ticks <- l.cfg.Now()
		}
	}
	return l, report
}

// A cycle that did not restart the log is tried again at the next heartbeat, not a whole
// interval later, and the loop says so with the log's length. Here a reader's older snapshot
// keeps the first checkpoint from copying the owner's newest frame, so its write cannot
// restart the log; once the reader is gone, the next heartbeat's cycle restarts it.
func TestAMissedRestartIsReportedAndRetriedAtTheNextHeartbeat(t *testing.T) {
	done := make(chan struct{})
	release := make(chan struct{})
	released := make(chan struct{})
	var answers []store.Checkpointed
	var storePath string
	var l *Loop
	var report *lockedBuffer
	l, report = checkpointCycleLoop(t, done,
		func() {
			started := make(chan struct{})
			go func() {
				defer close(released)
				r, err := store.OpenRead(context.Background(), storePath)
				if err != nil {
					t.Error(err)
					close(started)
					return
				}
				defer r.Close()
				r.ReadTx(context.Background(), func(rd *store.Reader) error {
					var n int
					err := rd.QueryRow(`SELECT count(*) FROM meta`).Scan(&n)
					close(started)
					<-release
					return err
				})
			}()
			<-started
		},
		func(c store.Checkpointed) {
			answers = append(answers, c)
			if len(answers) == 1 {
				close(release)
				<-released
			}
			if c.Restarted {
				select {
				case <-done:
				default:
					close(done)
				}
			}
		})
	storePath = l.cfg.Store
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(answers) < 2 {
		t.Fatalf("expected a missed cycle and a retry, saw %d cycles: %+v", len(answers), answers)
	}
	if answers[0].Restarted {
		t.Fatalf("the first cycle restarted the log although a reader held a frame back: %+v", answers[0])
	}
	if !answers[len(answers)-1].Restarted {
		t.Errorf("no retry restarted the log: %+v", answers)
	}
	out := report.String()
	if !strings.Contains(out, "did not restart") || !strings.Contains(out, "log frames") {
		t.Errorf("the loop did not report the missed restart with the log's length:\n%s", out)
	}
}

// When the owner's write waits out SQLite's busy timeout behind another writer, the loop
// skips that cycle, says so, and goes on: a checkpoint is maintenance, and a busy one is not
// a fault.
func TestABusyCheckpointWriteDoesNotFaultTheLoop(t *testing.T) {
	done := make(chan struct{})
	var holder *sql.Conn
	var holderDB *sql.DB
	var storePath string
	var cycles []store.Checkpointed
	l, report := checkpointCycleLoop(t, done,
		func() {
			var err error
			if holderDB, err = sql.Open("sqlite", "file:"+storePath+"?_pragma=busy_timeout(5000)"); err != nil {
				t.Error(err)
				return
			}
			if holder, err = holderDB.Conn(context.Background()); err != nil {
				t.Error(err)
				return
			}
			if _, err := holder.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
				t.Error(err)
			}
		},
		func(c store.Checkpointed) {
			cycles = append(cycles, c)
			if len(cycles) == 1 && holder != nil {
				holder.ExecContext(context.Background(), `ROLLBACK`)
				holder.Close()
				holderDB.Close()
			}
			if c.Restarted {
				select {
				case <-done:
				default:
					close(done)
				}
			}
		})
	storePath = l.cfg.Store
	l.keeperOpen = func(ctx context.Context, path string) (*store.CheckpointOwner, error) {
		return store.CreateAndOwnCheckpoints(ctx, path, store.WithBusyTimeout(time.Millisecond))
	}
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("a busy checkpoint write faulted the loop: %v", err)
	}
	if len(cycles) == 0 || !cycles[0].WriterBusy {
		t.Fatalf("the first cycle did not read as a busy writer: %+v", cycles)
	}
	if out := report.String(); !strings.Contains(out, "busy timeout") {
		t.Errorf("the loop did not report the skipped cycle:\n%s", out)
	}
}

// When the owner's pin waits out SQLite's busy timeout (another connection held the store's
// exclusive lock), the store answers PinBusy and no error (the store package's
// TestABusyPinSkipsTheCycleWithoutAnError), so nothing faults the loop; the loop says why it
// skipped and counts the cycle as a missed restart, and the next heartbeat tries again,
// because restartPending is !Restarted.
func TestABusyPinIsReportedAsASkippedCycle(t *testing.T) {
	report := &lockedBuffer{}
	l := &Loop{}
	l.cfg.Report = report
	l.reportCheckpoint(store.Checkpointed{PinBusy: true})
	if out := report.String(); !strings.Contains(out, "read pin") || !strings.Contains(out, "tried again at the next heartbeat") {
		t.Errorf("the loop did not report the busy pin as a skipped cycle:\n%s", out)
	}
	if l.missedRestarts != 1 {
		t.Errorf("a busy pin counted %d missed restarts, want 1", l.missedRestarts)
	}
}

type recordedCheckpointCycle struct {
	at         time.Time
	checkpoint store.Checkpointed
}

func holdReadSnapshot(t *testing.T, storePath string) func() {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	released := make(chan struct{})
	go func() {
		defer close(released)
		r, err := store.OpenRead(context.Background(), storePath)
		if err != nil {
			t.Error(err)
			close(started)
			return
		}
		defer r.Close()
		r.ReadTx(context.Background(), func(rd *store.Reader) error {
			var n int
			err := rd.QueryRow(`SELECT count(*) FROM meta`).Scan(&n)
			close(started)
			<-release
			return err
		})
	}()
	<-started
	var once sync.Once
	return func() {
		once.Do(func() {
			close(release)
			<-released
		})
	}
}

// When log.not-restarted persists across heartbeats, checkpointIfDue tries once at the next
// heartbeat and then backs off exponentially (2s, 4s, 8s, 16s, 32s) up to CheckpointInterval
// instead of checkpointing on every 1-second heartbeat tick; once a cycle restarts the log,
// the backoff resets.
func TestRepeatedNotRestartedCheckpointsBackOffUpToCheckpointInterval(t *testing.T) {
	f := newRefusalLoopFixture(t)
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	now := base

	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Now = func() time.Time { return now }
	l.keepWALWhileRunning()
	defer l.closeWALKeeper()

	ctx := context.Background()
	if err := l.refreshSession(ctx, now.Add(PassInterval)); err != nil {
		t.Fatal(err)
	}

	var cycles []recordedCheckpointCycle
	var releaseFirst func()
	defer func() {
		if releaseFirst != nil {
			releaseFirst()
		}
	}()
	var firstOnce sync.Once
	l.beforeCheckpoint = func() {
		firstOnce.Do(func() {
			releaseFirst = holdReadSnapshot(t, f.storePath)
		})
	}
	l.checkpointed = func(c store.Checkpointed) {
		cycles = append(cycles, recordedCheckpointCycle{at: now, checkpoint: c})
	}

	step := func(d time.Duration) {
		t.Helper()
		now = now.Add(d)
		if err := l.checkpointIfDue(ctx); err != nil {
			t.Fatalf("checkpointIfDue at +%s: %v", now.Sub(base), err)
		}
	}

	// First scheduled checkpoint at +60s, then 185 consecutive 1-second heartbeat checks
	// (+61s through +245s) while the reader snapshot pins the WAL.
	step(CheckpointInterval)
	for range 185 {
		step(PassInterval)
	}

	// Release the reader and continue 1-second heartbeat checks through +303s (60s + 243s),
	// when the next capped retry is due and restarts the log.
	releaseFirst()
	for range 58 {
		step(PassInterval)
	}

	// After that restart at +303s, normal 60s CheckpointInterval resumes: ticks +304s..+362s
	// do not checkpoint. Hold a new reader only when +363s's cycle starts so +363s misses
	// once, then release it so the immediate next-heartbeat retry at +364s restarts the log.
	var releaseSecond func()
	defer func() {
		if releaseSecond != nil {
			releaseSecond()
		}
	}()
	l.beforeCheckpoint = func() {
		if now.Equal(base.Add(363 * time.Second)) {
			releaseSecond = holdReadSnapshot(t, f.storePath)
		}
	}
	for range 59 {
		step(PassInterval)
	}
	// +363s triggers the scheduled checkpoint (misses once because releaseSecond is held).
	step(PassInterval)
	if releaseSecond != nil {
		releaseSecond()
	}
	// +364s (+1s later) retries immediately because the earlier restart reset the backoff.
	step(PassInterval)

	// Expected cycle timestamps relative to the first checkpoint at +60s:
	//   0s   (+60s):  initial scheduled checkpoint -> log.not-restarted
	//   1s   (+61s):  immediate next-heartbeat retry -> log.not-restarted
	//   3s   (+63s):  +2s backoff -> log.not-restarted
	//   7s   (+67s):  +4s backoff -> log.not-restarted
	//   15s  (+75s):  +8s backoff -> log.not-restarted
	//   31s  (+91s):  +16s backoff -> log.not-restarted
	//   63s  (+123s): +32s backoff -> log.not-restarted
	//   123s (+183s): +60s capped backoff -> log.not-restarted
	//   183s (+243s): +60s capped backoff -> log.not-restarted
	//   243s (+303s): +60s capped backoff (reader released) -> log.restarted
	//   303s (+363s): +60s normal CheckpointInterval -> log.not-restarted
	//   304s (+364s): +1s immediate next-heartbeat retry (backoff reset) -> log.restarted
	firstAt := base.Add(CheckpointInterval)
	wantOffsets := []time.Duration{
		0,
		1 * time.Second,
		3 * time.Second,
		7 * time.Second,
		15 * time.Second,
		31 * time.Second,
		63 * time.Second,
		123 * time.Second,
		183 * time.Second,
		243 * time.Second,
		303 * time.Second,
		304 * time.Second,
	}
	wantRestarted := []bool{
		false, false, false, false, false, false, false, false, false,
		true,
		false,
		true,
	}

	if len(cycles) != len(wantOffsets) {
		var offsets []time.Duration
		for _, c := range cycles {
			offsets = append(offsets, c.at.Sub(firstAt))
		}
		t.Fatalf("ran %d checkpoint cycles at offsets %v from first checkpoint, want %d cycles at %v",
			len(cycles), offsets, len(wantOffsets), wantOffsets)
	}
	for i := range wantOffsets {
		gotOffset := cycles[i].at.Sub(firstAt)
		if gotOffset != wantOffsets[i] {
			t.Errorf("cycle %d ran at +%s from first checkpoint, want +%s", i+1, gotOffset, wantOffsets[i])
		}
		if cycles[i].checkpoint.Restarted != wantRestarted[i] {
			t.Errorf("cycle %d (+%s) Restarted = %v, want %v (%+v)",
				i+1, gotOffset, cycles[i].checkpoint.Restarted, wantRestarted[i], cycles[i].checkpoint)
		}
	}
}

func TestCheckpointRetryDelay(t *testing.T) {
	cases := []struct {
		notRestarted int
		want         time.Duration
	}{
		{0, 0},
		{1, 0},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 32 * time.Second},
		{7, CheckpointInterval},
		{34, CheckpointInterval},
		{35, CheckpointInterval},
		{64, CheckpointInterval},
		{58694, CheckpointInterval},
	}
	for _, tc := range cases {
		if got := checkpointRetryDelay(tc.notRestarted); got != tc.want {
			t.Errorf("checkpointRetryDelay(%d) = %s, want %s", tc.notRestarted, got, tc.want)
		}
	}
}

func TestBusyCycleDuringBackoffRetriesAtNextHeartbeat(t *testing.T) {
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	l := &Loop{walKeeper: &store.CheckpointOwner{}}

	// Three consecutive log.not-restarted cycles reach a 4s backoff.
	l.recordCheckpointLocked(base, store.Checkpointed{})
	l.recordCheckpointLocked(base.Add(1*time.Second), store.Checkpointed{})
	l.recordCheckpointLocked(base.Add(3*time.Second), store.Checkpointed{})
	if l.retryWait != 4*time.Second {
		t.Fatalf("retryWait after 3 misses = %s, want 4s", l.retryWait)
	}
	if l.checkpointDueLocked(base.Add(6 * time.Second)) {
		t.Fatal("checkpoint became due after 3s during a 4s backoff")
	}

	// When the 4s retry hits WriterBusy or PinBusy, the next 1s heartbeat retries at once,
	// without losing the consecutive log.not-restarted count if misses resume.
	l.recordCheckpointLocked(base.Add(7*time.Second), store.Checkpointed{WriterBusy: true})
	if !l.checkpointDueLocked(base.Add(8 * time.Second)) {
		t.Fatal("WriterBusy cycle was not due at the next heartbeat")
	}
	l.recordCheckpointLocked(base.Add(8*time.Second), store.Checkpointed{PinBusy: true})
	if !l.checkpointDueLocked(base.Add(9 * time.Second)) {
		t.Fatal("PinBusy cycle was not due at the next heartbeat")
	}
	l.recordCheckpointLocked(base.Add(9*time.Second), store.Checkpointed{})
	if l.retryWait != 8*time.Second {
		t.Fatalf("retryWait after resumed log.not-restarted = %s, want 8s", l.retryWait)
	}
}

// Each checkpoint cycle that ends log.not-restarted appends one registered
// CheckpointNotRestartedKind event carrying log frames, log bytes and the count of
// consecutive cycles without a restart; a cycle that restarts the log appends nothing new
// and resets the consecutive-cycle count.
func TestNotRestartedCheckpointAppendsRegisteredEvent(t *testing.T) {
	kind, err := event.Lookup(CheckpointNotRestartedKind)
	if err != nil {
		t.Fatalf("event.Lookup(%q): %v", CheckpointNotRestartedKind, err)
	}
	if kind.Owner != Utility {
		t.Fatalf("event kind %q owner = %q, want %q", CheckpointNotRestartedKind, kind.Owner, Utility)
	}

	f := newRefusalLoopFixture(t)
	base := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	now := base

	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Now = func() time.Time { return now }
	l.keepWALWhileRunning()
	defer l.closeWALKeeper()

	ctx := context.Background()
	if err := l.refreshSession(ctx, now.Add(PassInterval)); err != nil {
		t.Fatal(err)
	}

	var cycles []store.Checkpointed
	var releaseFirst func()
	defer func() {
		if releaseFirst != nil {
			releaseFirst()
		}
	}()
	var firstOnce sync.Once
	l.beforeCheckpoint = func() {
		firstOnce.Do(func() {
			releaseFirst = holdReadSnapshot(t, f.storePath)
		})
	}
	l.checkpointed = func(c store.Checkpointed) {
		cycles = append(cycles, c)
	}

	step := func(d time.Duration) {
		t.Helper()
		now = now.Add(d)
		if err := l.checkpointIfDue(ctx); err != nil {
			t.Fatalf("checkpointIfDue at +%s: %v", now.Sub(base), err)
		}
	}

	// Cycle 1 (+60s) and Cycle 2 (+61s): log.not-restarted while reader holds snapshot.
	step(CheckpointInterval)
	step(PassInterval)

	// Release reader; Cycle 3 (+63s, after 2s backoff) restarts the log and appends nothing new.
	releaseFirst()
	step(2 * time.Second)

	// Hold a fresh reader for Cycle 4 (+123s, normal interval): log.not-restarted with reset count 1.
	var releaseSecond func()
	defer func() {
		if releaseSecond != nil {
			releaseSecond()
		}
	}()
	var secondOnce sync.Once
	l.beforeCheckpoint = func() {
		secondOnce.Do(func() {
			releaseSecond = holdReadSnapshot(t, f.storePath)
		})
	}
	step(CheckpointInterval)
	releaseSecond()

	if len(cycles) != 4 {
		t.Fatalf("ran %d checkpoint cycles, want 4: %+v", len(cycles), cycles)
	}
	if cycles[0].Restarted || cycles[1].Restarted || !cycles[2].Restarted || cycles[3].Restarted {
		t.Fatalf("unexpected Restarted outcomes: %+v", cycles)
	}

	type recordedEvent struct {
		objectType string
		objectID   string
		ts         int64
		fact       CheckpointNotRestartedFact
	}
	var got []recordedEvent
	r, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.ReadTx(ctx, func(rd *store.Reader) error {
		rows, err := rd.Query(`SELECT ts, COALESCE(object_type, ''), COALESCE(object_id, ''), payload FROM events WHERE kind = ? ORDER BY seq`, CheckpointNotRestartedKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ev recordedEvent
			var raw string
			if err := rows.Scan(&ev.ts, &ev.objectType, &ev.objectID, &raw); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(raw), &ev.fact); err != nil {
				return err
			}
			got = append(got, ev)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("recorded %d %s events, want 3 (none for the restarted cycle): %+v", len(got), CheckpointNotRestartedKind, got)
	}

	wantCycles := []store.Checkpointed{cycles[0], cycles[1], cycles[3]}
	wantConsecutive := []int{1, 2, 1}
	wantTimes := []time.Time{
		base.Add(CheckpointInterval),
		base.Add(CheckpointInterval + PassInterval),
		base.Add(2*CheckpointInterval + 3*time.Second),
	}
	for i := range got {
		if got[i].objectType != "session" || got[i].objectID != l.sessionID {
			t.Errorf("event %d object = (%q, %q), want (%q, %q)", i+1, got[i].objectType, got[i].objectID, "session", l.sessionID)
		}
		if got[i].ts != wantTimes[i].UnixMilli() {
			t.Errorf("event %d ts = %d, want %d", i+1, got[i].ts, wantTimes[i].UnixMilli())
		}
		fact := got[i].fact
		if fact.LogFrames != wantCycles[i].LogFrames || fact.LogFrames <= 0 {
			t.Errorf("event %d log_frames = %d, want %d (> 0)", i+1, fact.LogFrames, wantCycles[i].LogFrames)
		}
		if fact.LogBytes != wantCycles[i].LogBytes || fact.LogBytes <= 0 {
			t.Errorf("event %d log_bytes = %d, want %d (> 0)", i+1, fact.LogBytes, wantCycles[i].LogBytes)
		}
		if fact.ConsecutiveCycles != wantConsecutive[i] {
			t.Errorf("event %d consecutive_cycles = %d, want %d", i+1, fact.ConsecutiveCycles, wantConsecutive[i])
		}
		if fact.SessionID != l.sessionID {
			t.Errorf("event %d session_id = %q, want %q", i+1, fact.SessionID, l.sessionID)
		}
	}
}
