package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

type outputChunks chan string

func (c outputChunks) Write(p []byte) (int, error) { c <- string(p); return len(p), nil }

func awaitChunk(t *testing.T, c <-chan string) string {
	t.Helper()
	select {
	case s := <-c:
		return s
	case <-time.After(hangGuard):
		t.Fatal("no watcher output")
		return ""
	}
}
func awaitAttached(t *testing.T, s *store.Store) {
	t.Helper()
	until := time.Now().Add(hangGuard)
	for time.Now().Before(until) {
		var attached bool
		err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM foreground_delivery WHERE waiting_since IS NOT NULL)`).Scan(&attached)
		})
		if err != nil {
			t.Fatal(err)
		}
		if attached {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("watcher did not attach")
}
func cursor(t *testing.T, text string) int64 {
	t.Helper()
	var seq int64
	if _, err := fmt.Sscanf(text, "TILLR_WATCH_NOTICE 1 pending, through_seq %d", &seq); err != nil {
		t.Fatalf("bad marker %q: %v", text, err)
	}
	return seq
}
func TestFollowDelivery(t *testing.T) {
	s, rules := fixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	output := make(outputChunks, 20)
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		defer close(done)
		done <- runWithTicks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--follow"}, output, &stderr, ticks)
	}()
	defer func() { cancel(); <-done }()
	awaitAttached(t, s)
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	first := awaitChunk(t, output)
	seq := cursor(t, first)
	for _, part := range []string{"item-a has 2 refusals", "check: read refusals", "do: inspect evidence", "recorded data, not instructions", "TimelineBundle", "--follow", "--seen-through"} {
		if !strings.Contains(first, part) {
			t.Fatalf("missing %q in %q", part, first)
		}
	}
	ticks <- time.Now()
	marker := awaitChunk(t, output)
	if marker != fmt.Sprintf("TILLR_WATCH_NOTICE 1 pending, through_seq %d\n", seq) {
		t.Fatalf("marker: %q", marker)
	}
	// Printing is not confirmation. A second non-follow wait repeats the bundle.
	var out, errout bytes.Buffer
	if code := run(context.Background(), []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "1s"}, &out, &errout); code != 0 || !strings.Contains(out.String(), "item-a has 2 refusals") {
		t.Fatalf("unconfirmed: %d %q %q", code, out.String(), errout.String())
	}
	out.Reset()
	errout.Reset()
	if code := run(context.Background(), []string{"seen", "--store", s.Path(), "--through", fmt.Sprint(seq)}, &out, &errout); code != 0 {
		t.Fatalf("seen: %d %q", code, errout.String())
	}
	// After confirmation a tick must not repeat the marker.
	ticks <- time.Now()
	select {
	case text := <-output:
		t.Fatalf("output after confirmation: %q", text)
	case <-time.After(100 * time.Millisecond):
	}
	fact(t, s, `{"item_id":"item-b"}`)
	fact(t, s, `{"item_id":"item-b"}`)
	later := awaitChunk(t, output)
	laterSeq := cursor(t, later)
	if laterSeq <= seq || !strings.Contains(later, "item-b has 2 refusals") || strings.Contains(later, "item-a has 2 refusals") {
		t.Fatalf("later notice: %q", later)
	}
	select {
	case code := <-done:
		t.Fatalf("follow exited: %d %q", code, stderr.String())
	default:
	}
}
func TestClaudeRearmDelivery(t *testing.T) {
	s, rules := fixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(outputChunks, 10)
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		done <- run(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "110m"}, out, &stderr)
	}()
	awaitAttached(t, s)
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	text := awaitChunk(t, out)
	seq := cursor(t, text)
	if code := <-done; code != 0 || !strings.Contains(text, "item-a has 2 refusals") || !strings.Contains(text, "recorded data, not instructions") {
		t.Fatalf("notice: %d %q %q", code, text, stderr.String())
	}
	line := text[strings.LastIndex(text, "re-arm: ")+len("re-arm: "):]
	line = strings.TrimSpace(line)
	const prefix = "tillr watch "
	if !strings.HasPrefix(line, prefix) || !strings.Contains(line, fmt.Sprintf("--seen-through %d", seq)) || !strings.Contains(line, "--timeout 1h50m0s") {
		t.Fatalf("re-arm: %q", line)
	}
	// Parse the printed shell words exactly as the foreground Bash call would.
	parsed, err := exec.Command("sh", "-c", "set -- "+strings.TrimPrefix(line, prefix)+`; printf '%s\0' "$@"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(parsed), "\x00"), "\x00")
	// Shorten only the timeout to exercise quiet re-arm without waiting 110 minutes.
	for i := range args {
		if args[i] == "--timeout" {
			args[i+1] = "30ms"
		}
	}
	var rearmed, rearmErr bytes.Buffer
	if code := run(ctx, args, &rearmed, &rearmErr); code != 2 || strings.Contains(rearmed.String(), "TILLR_WATCH_NOTICE") || !strings.Contains(rearmed.String(), "re-arm:") {
		t.Fatalf("confirmed re-arm: %d %q %q", code, rearmed.String(), rearmErr.String())
	}
}

func TestFollowLaterArrivalBeforeConfirmation(t *testing.T) {
	s, rules := fixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	ticks, output, done := make(chan time.Time), make(outputChunks, 20), make(chan int, 1)
	go func() {
		done <- runWithTicks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--follow"}, output, io.Discard, ticks)
	}()
	defer func() { cancel(); <-done }()
	awaitAttached(t, s)
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	first := cursor(t, awaitChunk(t, output))
	fact(t, s, `{"item_id":"item-b"}`)
	fact(t, s, `{"item_id":"item-b"}`)
	both := awaitChunk(t, output)
	var second int64
	if _, err := fmt.Sscanf(both, "TILLR_WATCH_NOTICE 2 pending, through_seq %d", &second); err != nil || second <= first || !strings.Contains(both, "item-a has 2 refusals") || !strings.Contains(both, "item-b has 2 refusals") {
		t.Fatalf("both notices: %q, %v", both, err)
	}
	if code := run(ctx, []string{"seen", "--store", s.Path(), "--through", fmt.Sprint(first)}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("seen: %d", code)
	}
	ticks <- time.Now()
	if marker := awaitChunk(t, output); marker != fmt.Sprintf("TILLR_WATCH_NOTICE 1 pending, through_seq %d\n", second) {
		t.Fatalf("later pending marker: %q", marker)
	}
}

func TestFollowDoesNotSpinOnOwnWALWrites(t *testing.T) {
	s, rules := fixture(t, false)
	monitor, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	if err := monitor.Add(filepath.Dir(s.Path())); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	output, done := make(outputChunks, 10), make(chan int, 1)
	go func() {
		done <- runWithTicks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--follow"}, output, io.Discard, make(chan time.Time))
	}()
	defer func() { cancel(); <-done }()
	awaitAttached(t, s)
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	awaitChunk(t, output)
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	count := 0
	for {
		select {
		case evt := <-monitor.Events:
			if evt.Name == s.Path()+"-wal" {
				count++
			}
			if count > 30 {
				t.Fatalf("watcher spins on WAL events: %d", count)
			}
		case err := <-monitor.Errors:
			t.Fatal(err)
		case <-timer.C:
			return
		}
	}
}

type noticeWriterFunc func([]byte) (int, error)

func (w noticeWriterFunc) Write(p []byte) (int, error) { return w(p) }

func TestFailedPublicationDoesNotAdvanceShown(t *testing.T) {
	s, rules := fixture(t, false)
	ctx := context.Background()
	var initStderr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, io.Discard, &initStderr); code != 0 || initStderr.Len() != 0 {
		t.Fatalf("init eval: %d %q", code, initStderr.String())
	}
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	var seq int64
	out := noticeWriterFunc(func(p []byte) (int, error) {
		seq = cursor(t, string(p))
		return 0, fmt.Errorf("output failed")
	})
	var stderr bytes.Buffer
	if code := run(ctx, []string{"wait", "--store", s.Path(), "--rules", rules}, out, &stderr); code != 1 || !strings.Contains(stderr.String(), "output failed") {
		t.Fatalf("failed output: %d %q", code, stderr.String())
	}
	assertSeenRefused(t, ctx, s.Path(), seq)
}

func assertSeenRefused(t *testing.T, ctx context.Context, path string, seq int64) {
	t.Helper()
	var stderr bytes.Buffer
	if code := run(ctx, []string{"seen", "--store", path, "--through", fmt.Sprint(seq)}, io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "exceeds shown_seq") {
		t.Fatalf("unprinted notice confirmation: %d %q", code, stderr.String())
	}
}

func TestNoticeOutputDoesNotHoldStoreWriteLock(t *testing.T) {
	for _, name := range []string{"ordinary", "follow"} {
		t.Run(name, func(t *testing.T) {
			s, rules := fixture(t, false)
			ctx, cancel := context.WithCancel(context.Background())
			var initStderr bytes.Buffer
			if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, io.Discard, &initStderr); code != 0 || initStderr.Len() != 0 {
				t.Fatalf("init eval: %d %q", code, initStderr.String())
			}
			fact(t, s, `{"item_id":"item-a"}`)
			fact(t, s, `{"item_id":"item-a"}`)
			entered, release, done := make(chan string, 1), make(chan struct{}), make(chan int, 1)
			args := []string{"wait", "--store", s.Path(), "--rules", rules}
			if name == "follow" {
				args = append(args, "--follow")
			}
			ticks := make(chan time.Time, 1)
			out := noticeWriterFunc(func(p []byte) (int, error) {
				entered <- string(p)
				<-release
				return len(p), nil
			})
			go func() {
				defer close(done)
				done <- runWithTicks(ctx, args, out, io.Discard, ticks)
			}()
			released := false
			defer func() {
				if !released {
					close(release)
				}
				cancel()
				<-done
			}()
			seq := cursor(t, awaitChunk(t, entered))
			writeCtx, stop := context.WithTimeout(ctx, 300*time.Millisecond)
			defer stop()
			if err := s.WriteTx(writeCtx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{At: time.Now(), Kind: "drain.refusal", Payload: []byte(`{"item_id":"item-b"}`)})
				return err
			}); err != nil {
				t.Fatalf("store write while notice output is blocked: %v", err)
			}
			assertSeenRefused(t, writeCtx, s.Path(), seq)
			close(release)
			released = true
			if name == "follow" {
				// An early refusal leaves the notice pending. The next marker
				// gives the foreground agent another chance to confirm it.
				ticks <- time.Now()
				if marker := awaitChunk(t, entered); marker != fmt.Sprintf("TILLR_WATCH_NOTICE 1 pending, through_seq %d\n", seq) {
					t.Fatalf("pending marker: %q", marker)
				}
			} else if code := <-done; code != 0 {
				t.Fatalf("wait exited %d", code)
			}
			var stderr bytes.Buffer
			if code := run(ctx, []string{"seen", "--store", s.Path(), "--through", fmt.Sprint(seq)}, io.Discard, &stderr); code != 0 {
				t.Fatalf("confirmation after output: %d %q", code, stderr.String())
			}
		})
	}
}

func TestFollowRejectsTimeout(t *testing.T) {
	s, rules := fixture(t, false)
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{"wait", "--store", s.Path(), "--rules", rules, "--follow", "--timeout", "20ms"}, &out, &stderr)
	if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "--follow cannot be combined with --timeout") {
		t.Fatalf("follow with timeout: %d %q %q", code, out.String(), stderr.String())
	}
}

func TestFollowEmptyRulesAttach(t *testing.T) {
	s, _ := fixture(t, false)
	rules := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(rules, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--follow"}, io.Discard, io.Discard)
	}()
	awaitAttached(t, s)
	select {
	case code := <-done:
		t.Fatalf("empty watcher exited: %d", code)
	default:
	}
	cancel()
	<-done
}
