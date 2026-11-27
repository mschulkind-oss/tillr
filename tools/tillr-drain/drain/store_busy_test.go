package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

type busyPassReportWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	once    sync.Once
	onRetry func()
}

func (w *busyPassReportWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	s := w.buf.String()
	w.mu.Unlock()
	if strings.Contains(s, "loop: store busy during pass; will retry on a later pass:") {
		w.once.Do(w.onRetry)
	}
	return n, err
}

func (w *busyPassReportWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestPassBusyStoreRetriesInsteadOfFaulting(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.ExitWhenDrained = true
	l.cfg.BackoffBase = time.Millisecond
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	holder, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holding, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- holder.WriteTx(context.Background(), func(*store.Tx) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	report := &busyPassReportWriter{onRetry: func() { close(release) }}
	l.cfg.Report = report

	var result LoopResult
	if err := l.runPasses(context.Background(), &result); err != nil {
		t.Fatalf("runPasses returned error on busy store: %v", err)
	}
	if result.Stop != StopQueueDrained {
		t.Fatalf("stop = %q, want %q (detail=%q)", result.Stop, StopQueueDrained, result.Detail)
	}
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "loop: store busy during pass; will retry on a later pass:") {
		t.Fatalf("expected busy pass retry log, got %q", report.String())
	}
	got := readStoreBusyEvents(t, f.storePath)
	if len(got) == 0 || got[0].Operation != "pass" {
		t.Fatalf("expected pass store-busy event, got %+v", got)
	}
}

func TestSweepRefsDoesNotHoldWriteLockDuringGitDelete(t *testing.T) {
	f, l := passFixture(t, "item-a")
	seedRefRun(t, f, "run-1", "item-a", 1)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateLanded)
		return err
	})
	trunkOut, err := exec.Command("git", "-C", f.root, "rev-parse", TrunkRef).Output()
	if err != nil {
		t.Fatal(err)
	}
	trunk := strings.TrimSpace(string(trunkOut))
	if out, err := exec.Command("git", "-C", f.root, "update-ref", "refs/tillr/runs/run-1", trunk).CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v: %s", err, out)
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	entered := filepath.Join(shimDir, "entered")
	proceed := filepath.Join(shimDir, "proceed")
	shim := filepath.Join(shimDir, "git")
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"update-ref\" ]; then\n" +
		"    : > \"" + entered + "\"\n" +
		"    i=0\n" +
		"    while [ ! -f \"" + proceed + "\" ] && [ \"$i\" -lt 200 ]; do\n" +
		"      sleep 0.05\n" +
		"      i=$((i + 1))\n" +
		"    done\n" +
		"    break\n" +
		"  fi\n" +
		"done\n" +
		"exec \"" + realGit + "\" \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := context.Background()
	sweepDone := make(chan error, 1)
	go func() {
		_, err := sweepRefs(ctx, RefSweepRequest{Root: f.root, Store: f.storePath, At: l.cfg.Now()}, sweepIfUnlocked)
		sweepDone <- err
	}()

	deadline := time.After(hangGuard)
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		select {
		case err := <-sweepDone:
			t.Fatalf("sweepRefs finished before git update-ref was invoked: err=%v", err)
		case <-deadline:
			t.Fatal("timed out waiting for git update-ref")
		case <-time.After(10 * time.Millisecond):
		}
	}

	concurrent, err := store.Open(ctx, f.storePath, false, store.WithBusyTimeout(0))
	if err != nil {
		_ = os.WriteFile(proceed, nil, 0o644)
		t.Fatal(err)
	}
	defer concurrent.Close()
	if err := concurrent.WriteTx(ctx, func(*store.Tx) error { return nil }); err != nil {
		_ = os.WriteFile(proceed, nil, 0o644)
		t.Fatalf("concurrent WriteTx during git update-ref failed (write lock still held): %v", err)
	}

	if err := os.WriteFile(proceed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-sweepDone; err != nil {
		t.Fatalf("sweepRefs failed: %v", err)
	}
}
