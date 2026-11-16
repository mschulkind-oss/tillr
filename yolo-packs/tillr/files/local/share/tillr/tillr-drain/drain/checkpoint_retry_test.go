package drain

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	case <-time.After(30 * time.Second):
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
	steps := &cyclesWaitingSteps{
		advance: func() { offset.Add(int64(CheckpointInterval)) },
		done:    done,
		t:       t,
	}
	l := f.loop(t, steps, &itemContentStager{})
	l.cfg.Now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	report := &lockedBuffer{}
	l.cfg.Report = report
	var once sync.Once
	l.beforeCheckpoint = func() { once.Do(first) }
	l.checkpointed = cycles
	return l, report
}

// A cycle that did not restart the log is tried again at the next heartbeat, not a whole
// interval later, and the loop says so with the log's length. Here a reader's older snapshot
// keeps the first checkpoint from copying the owner's newest frame, so its write cannot
// restart the log; once the reader is gone, the next heartbeat's cycle restarts it.
func TestAMissedRestartIsReportedAndRetriedAtTheNextHeartbeat(t *testing.T) {
	done := make(chan struct{})
	release := make(chan struct{})
	var answers []store.Checkpointed
	var storePath string
	var l *Loop
	var report *lockedBuffer
	l, report = checkpointCycleLoop(t, done,
		func() {
			started := make(chan struct{})
			go func() {
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
