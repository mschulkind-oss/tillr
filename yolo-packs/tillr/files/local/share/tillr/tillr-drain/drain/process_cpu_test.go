package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

type recordedProcessCPUEvent struct {
	Kind       string
	ObjectType string
	ObjectID   string
	AtMS       int64
	Fact       ProcessCPUFact
	Raw        map[string]any
}

func readProcessCPUEvents(t *testing.T, storePath string) []recordedProcessCPUEvent {
	t.Helper()
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var out []recordedProcessCPUEvent
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT kind, object_type, COALESCE(object_id, ''), ts, payload FROM events WHERE kind = ? ORDER BY seq`, ProcessCPUKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ev recordedProcessCPUEvent
			var payload []byte
			if err := rows.Scan(&ev.Kind, &ev.ObjectType, &ev.ObjectID, &ev.AtMS, &payload); err != nil {
				return err
			}
			if err := json.Unmarshal(payload, &ev.Fact); err != nil {
				return err
			}
			dec := json.NewDecoder(strings.NewReader(string(payload)))
			dec.UseNumber()
			if err := dec.Decode(&ev.Raw); err != nil {
				return err
			}
			out = append(out, ev)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeFakeProcEntry(t *testing.T, procRoot string, pid int, comm string, state string, utime, stime, startTime uint64, argv ...string) {
	t.Helper()
	dir := filepath.Join(procRoot, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var cmdline []byte
	if len(argv) > 0 {
		cmdline = []byte(strings.Join(argv, "\x00") + "\x00")
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), cmdline, 0o644); err != nil {
		t.Fatal(err)
	}
	// Fields after "(comm)":
	// [0]=state [1..10]=ppid+9 zeros [11]=utime [12]=stime [13..18]=6 fields [19]=starttime
	stat := fmt.Sprintf(
		"%d (%s) %s 1 1 1 0 -1 0 0 0 0 0 %d %d 0 0 20 0 1 0 %d 0 0\n",
		pid, comm, state, utime, stime, startTime,
	)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
}

func removeFakeProcEntry(t *testing.T, procRoot string, pid int) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(procRoot, fmt.Sprintf("%d", pid))); err != nil {
		t.Fatal(err)
	}
}

func TestProcessCPUEventShapeAndMinuteCadenceFromFakeProc(t *testing.T) {
	if !slices.ContainsFunc(event.Kinds(), func(k event.Kind) bool {
		return k.Name == ProcessCPUKind && k.Owner == Utility
	}) {
		t.Fatalf("%s is not registered under owner %q", ProcessCPUKind, Utility)
	}

	f, l := passFixture(t, "item-a")
	l.cfg.Steps = &recordingSteps{}
	l.sessionID = "session-cpu-1"
	l.sessionInitialized = true
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	fakeProc := filepath.Join(t.TempDir(), "proc")
	if err := os.MkdirAll(fakeProc, 0o755); err != nil {
		t.Fatal(err)
	}
	l.procRoot = fakeProc
	l.selfPID = 1001

	now := time.Date(2026, 11, 23, 5, 50, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return now }

	// Baseline /proc state at t0 (including comm with spaces/parens and negative controls).
	writeFakeProcEntry(t, fakeProc, 1001, "tillr-drain (loop)", "R", 100, 20, 5000, "/usr/local/bin/tillr-drain", "loop", "--store", ".tillr/state.db")
	writeFakeProcEntry(t, fakeProc, 2001, "tillr-watch", "R", 400, 50, 6000, "/usr/local/bin/tillr-watch", "wait", "--owner", "whale", "--follow")
	writeFakeProcEntry(t, fakeProc, 2002, "tillr", "S", 10, 5, 6100, "/home/user/.cache/tillr/bin/current-tillr-0123456789abcdef", "watch", "wait")
	writeFakeProcEntry(t, fakeProc, 3001, "tillr-view", "R", 1000, 200, 7000, "/home/user/.cache/tillr/bin/tillr-view-deadbeef01234567", "serve", "--store", ".tillr/state.db")

	// Processes that must be ignored: other faces, notify relay, shell wrappers, zombies, non-PID entries.
	writeFakeProcEntry(t, fakeProc, 4001, "tillr-watch", "S", 9000, 0, 8000, "/usr/local/bin/tillr-watch", "daemon")
	writeFakeProcEntry(t, fakeProc, 4002, "tillr-view", "S", 9000, 0, 8100, "/usr/local/bin/tillr-view", "status")
	writeFakeProcEntry(t, fakeProc, 4003, "sh", "S", 9000, 0, 8200, "/bin/sh", "-c", "tillr watch wait --follow")
	writeFakeProcEntry(t, fakeProc, 4004, "tillr-view", "Z", 9000, 0, 8300, "/usr/local/bin/tillr-view", "serve")
	writeFakeProcEntry(t, fakeProc, 4005, "tillr", "S", 9000, 0, 8400, "/usr/local/bin/tillr", "notify", "serve", "--store", ".tillr/state.db")
	if err := os.WriteFile(filepath.Join(fakeProc, "uptime"), []byte("12345.67 23456.78\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pass at t0 primes the baseline and appends no event yet.
	var result LoopResult
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("first runPasses: %v", err)
	}
	if events := readProcessCPUEvents(t, f.storePath); len(events) != 0 {
		t.Fatalf("expected 0 %s events at baseline t0, got %+v", ProcessCPUKind, events)
	}

	// Pass at t0 + 30s still appends nothing (< 1 minute).
	now = now.Add(30 * time.Second)
	writeFakeProcEntry(t, fakeProc, 2001, "tillr-watch", "R", 4000, 100, 6000, "/usr/local/bin/tillr-watch", "wait", "--owner", "whale", "--follow")
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("30s runPasses: %v", err)
	}
	if events := readProcessCPUEvents(t, f.storePath); len(events) != 0 {
		t.Fatalf("expected 0 %s events at +30s, got %+v", ProcessCPUKind, events)
	}

	// At t0 + 1m (6000 clock ticks of wall time at 100 Hz):
	//   1001 (self):             +180 ticks  ->   3%
	//   2001 (tillr watch wait): +7980 ticks -> 133%
	//   2002 (tillr watch wait): +0 ticks    ->   0%
	//   3001 (tillr-view serve): +7200 ticks -> 120%
	now = now.Add(30 * time.Second)
	writeFakeProcEntry(t, fakeProc, 1001, "tillr-drain (loop)", "R", 250, 50, 5000, "/usr/local/bin/tillr-drain", "loop", "--store", ".tillr/state.db")
	writeFakeProcEntry(t, fakeProc, 2001, "tillr-watch", "R", 8000, 430, 6000, "/usr/local/bin/tillr-watch", "wait", "--owner", "whale", "--follow")
	writeFakeProcEntry(t, fakeProc, 3001, "tillr-view", "R", 7500, 900, 7000, "/home/user/.cache/tillr/bin/tillr-view-deadbeef01234567", "serve", "--store", ".tillr/state.db")
	writeFakeProcEntry(t, fakeProc, 4001, "tillr-watch", "S", 18000, 0, 8000, "/usr/local/bin/tillr-watch", "daemon")

	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("60s runPasses: %v", err)
	}
	events := readProcessCPUEvents(t, f.storePath)
	if len(events) != 1 {
		t.Fatalf("expected 1 %s event at +1m, got %d: %+v", ProcessCPUKind, len(events), events)
	}
	first := events[0]
	if first.ObjectType != "session" || first.ObjectID != "session-cpu-1" {
		t.Fatalf("unexpected envelope: object_type=%q object_id=%q", first.ObjectType, first.ObjectID)
	}
	if wantMS := time.Date(2026, 11, 23, 5, 51, 0, 0, time.UTC).UnixMilli(); first.AtMS != wantMS {
		t.Fatalf("unexpected timestamp %d, want %d", first.AtMS, wantMS)
	}
	wantFirst := ProcessCPUFact{
		SessionID:           "session-cpu-1",
		WindowMS:            60_000,
		SelfPID:             1001,
		SelfCPUPercent:      3,
		WatchWaitPID:        2001,
		WatchWaitCPUPercent: 133,
		ViewServePID:        3001,
		ViewServeCPUPercent: 120,
		MaxCPUPercent:       133,
		MaxRole:             ProcessRoleWatchWait,
		MaxPID:              2001,
		Processes: []ProcessCPUEntry{
			{PID: 1001, Role: ProcessRoleDrainLoop, CPUPercent: 3},
			{PID: 2001, Role: ProcessRoleWatchWait, CPUPercent: 133},
			{PID: 2002, Role: ProcessRoleWatchWait, CPUPercent: 0},
			{PID: 3001, Role: ProcessRoleViewServe, CPUPercent: 120},
		},
	}
	if !reflect.DeepEqual(first.Fact, wantFirst) {
		t.Fatalf("first %s payload mismatch:\n got: %+v\nwant: %+v", ProcessCPUKind, first.Fact, wantFirst)
	}
	// Numeric fields must serialize as JSON integers (no decimal points) so tillr-watch
	// integer predicates can compare them directly.
	for _, field := range []string{"window_ms", "self_pid", "self_cpu_percent", "watch_wait_pid", "watch_wait_cpu_percent", "view_serve_pid", "view_serve_cpu_percent", "max_cpu_percent", "max_pid"} {
		num, ok := first.Raw[field].(json.Number)
		if !ok {
			t.Fatalf("field %q missing or not json.Number: %T (%v)", field, first.Raw[field], first.Raw[field])
		}
		if _, err := num.Int64(); err != nil {
			t.Fatalf("field %q = %q is not an integer: %v", field, num, err)
		}
	}

	// Second minute (+2m): PID 2001 exited, PID 3002 started mid-minute (1500 ticks = 25%
	// over the 60s window), self used 60 ticks (1%), and PID 3001 cooled to 120 ticks (2%).
	removeFakeProcEntry(t, fakeProc, 2001)
	writeFakeProcEntry(t, fakeProc, 1001, "tillr-drain (loop)", "S", 300, 60, 5000, "/usr/local/bin/tillr-drain", "loop", "--store", ".tillr/state.db")
	writeFakeProcEntry(t, fakeProc, 3001, "tillr-view", "S", 7600, 920, 7000, "/home/user/.cache/tillr/bin/tillr-view-deadbeef01234567", "serve", "--store", ".tillr/state.db")
	writeFakeProcEntry(t, fakeProc, 3002, "tillr", "R", 1200, 300, 9500, "tillr", "view", "serve", "--store", ".tillr/state.db")

	now = now.Add(time.Minute)
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("120s runPasses: %v", err)
	}
	events = readProcessCPUEvents(t, f.storePath)
	if len(events) != 2 {
		t.Fatalf("expected 2 %s events at +2m, got %d", ProcessCPUKind, len(events))
	}
	wantSecond := ProcessCPUFact{
		SessionID:           "session-cpu-1",
		WindowMS:            60_000,
		SelfPID:             1001,
		SelfCPUPercent:      1,
		WatchWaitPID:        2002,
		WatchWaitCPUPercent: 0,
		ViewServePID:        3002,
		ViewServeCPUPercent: 25,
		MaxCPUPercent:       25,
		MaxRole:             ProcessRoleViewServe,
		MaxPID:              3002,
		Processes: []ProcessCPUEntry{
			{PID: 1001, Role: ProcessRoleDrainLoop, CPUPercent: 1},
			{PID: 2002, Role: ProcessRoleWatchWait, CPUPercent: 0},
			{PID: 3001, Role: ProcessRoleViewServe, CPUPercent: 2},
			{PID: 3002, Role: ProcessRoleViewServe, CPUPercent: 25},
		},
	}
	if !reflect.DeepEqual(events[1].Fact, wantSecond) {
		t.Fatalf("second %s payload mismatch:\n got: %+v\nwant: %+v", ProcessCPUKind, events[1].Fact, wantSecond)
	}
}

func TestIdleLoopHeartbeatAppendsProcessCPUOnceAMinute(t *testing.T) {
	f, l := passFixture(t, "item-a")
	var offset atomic.Int64
	base := time.Date(2026, 11, 23, 6, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return base.Add(time.Duration(offset.Load())) }

	fakeProc := filepath.Join(t.TempDir(), "proc")
	l.procRoot = fakeProc
	l.selfPID = 500
	writeFakeProcEntry(t, fakeProc, 500, "tillr-drain", "S", 10, 10, 1000, "tillr-drain", "loop")
	writeFakeProcEntry(t, fakeProc, 600, "tillr-watch", "R", 0, 0, 2000, "tillr-watch", "wait")

	if err := l.refreshSession(context.Background(), l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	l.heartbeatClock = func() heartbeatTicker { return heartbeatTicker{C: ticks, Stop: func() {}} }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := l.keepSessionAlive(ctx, cancel)

	// Sub-minute heartbeat at +59s records no CPU event.
	writeFakeProcEntry(t, fakeProc, 600, "tillr-watch", "R", 3000, 0, 2000, "tillr-watch", "wait")
	offset.Store(int64(59 * time.Second))
	ticks <- l.cfg.Now()
	waitUntil(t, func() bool { return refreshedAt(t, f.storePath, l.sessionID) == l.cfg.Now().UnixMilli() })
	if got := readProcessCPUEvents(t, f.storePath); len(got) != 0 {
		t.Fatalf("expected 0 events at +59s, got %+v", got)
	}

	// Heartbeat at +60s records one event with 6000 ticks = 100% CPU.
	writeFakeProcEntry(t, fakeProc, 500, "tillr-drain", "S", 70, 10, 1000, "tillr-drain", "loop")
	writeFakeProcEntry(t, fakeProc, 600, "tillr-watch", "R", 6000, 0, 2000, "tillr-watch", "wait")
	offset.Store(int64(time.Minute))
	ticks <- l.cfg.Now()
	waitUntil(t, func() bool { return len(readProcessCPUEvents(t, f.storePath)) == 1 })

	// Heartbeat at +90s still leaves count at 1.
	offset.Store(int64(90 * time.Second))
	ticks <- l.cfg.Now()
	waitUntil(t, func() bool { return refreshedAt(t, f.storePath, l.sessionID) == l.cfg.Now().UnixMilli() })
	if got := readProcessCPUEvents(t, f.storePath); len(got) != 1 {
		t.Fatalf("expected 1 event at +90s, got %+v", got)
	}

	if err := stop(); err != nil {
		t.Fatalf("keepSessionAlive returned %v", err)
	}
	got := readProcessCPUEvents(t, f.storePath)
	if len(got) != 1 || got[0].Fact.WatchWaitCPUPercent != 100 || got[0].Fact.WatchWaitPID != 600 || got[0].Fact.SelfCPUPercent != 1 || got[0].Fact.SelfPID != 500 {
		t.Fatalf("unexpected idle heartbeat event: %+v", got)
	}
}

type processCPUReportWriter struct {
	buf      *lockedBuffer
	onRecord func()
	once     sync.Once
}

func (w *processCPUReportWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if w.onRecord != nil && strings.Contains(w.buf.String(), "loop: process cpu was not recorded:") {
		w.once.Do(w.onRecord)
	}
	return n, err
}

func TestProcessCPULogsWriteFailureWithoutStoppingLoop(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Steps = &recordingSteps{}
	l.sessionID = "session-cpu-busy"
	l.sessionInitialized = true
	l.sessionOptions = []store.OpenOption{store.WithBusyTimeout(0)}

	fakeProc := filepath.Join(t.TempDir(), "proc")
	l.procRoot = fakeProc
	l.selfPID = 700
	writeFakeProcEntry(t, fakeProc, 700, "tillr-drain", "S", 0, 0, 1000, "tillr-drain", "loop")

	now := time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return now }
	l.primeProcessCPU()

	holding, release := make(chan struct{}), make(chan struct{})
	report := &lockedBuffer{}
	l.cfg.Report = &processCPUReportWriter{
		buf:      report,
		onRecord: func() { close(release) },
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	// Advance 1 minute with a busy store; recordProcessCPUIfDue logs and lets the pass continue.
	now = now.Add(time.Minute)
	l.lastLoopPassAt = now // isolate the busy write to recordProcessCPUIfDue
	writeFakeProcEntry(t, fakeProc, 700, "tillr-drain", "R", 3000, 0, 1000, "tillr-drain", "loop")

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
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if got := readProcessCPUEvents(t, f.storePath); len(got) != 0 {
		t.Fatalf("expected 0 events after failed busy write, got %+v", got)
	}

	// Next pass retries because baseline was preserved on write failure.
	now = now.Add(time.Second)
	writeFakeProcEntry(t, fakeProc, 700, "tillr-drain", "R", 3050, 0, 1000, "tillr-drain", "loop")
	if err := l.runPasses(context.Background(), &result, false, nil); err != nil {
		t.Fatalf("retry runPasses: %v", err)
	}
	got := readProcessCPUEvents(t, f.storePath)
	if len(got) != 1 || got[0].Fact.SelfCPUPercent != 50 {
		t.Fatalf("expected retried event at 50%% CPU (3050 ticks / 61s), got %+v", got)
	}
}
