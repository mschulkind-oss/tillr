package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

const gatherHelperEnv = "TILLR_GATHER_HELPER_STORE"

// TestHelperGatherSecondWriter is the second writer of the fairness test below, run as its
// own process because between processes SQLite's busy wait is the only arbiter. It does
// nothing in an ordinary run.
func TestHelperGatherSecondWriter(t *testing.T) {
	path := os.Getenv(gatherHelperEnv)
	if path == "" {
		t.Skip("helper process only")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		return nil
	})
	if err != nil {
		fmt.Printf("WAITED_ERROR %v\n", err)
		return
	}
	fmt.Println("GOT_LOCK")
}

// One writer that keeps missing must not hold the store's write lock back to back: another
// process that begins a write while it loops gets the lock before the loop ends. Without the pause after a miss each
// attempt began the instant the last rolled back, and the second process, polling for the
// lock, waited out the whole loop (2026-11-20: a run's lane claim, five seconds, SQLITE_BUSY).
func TestAMissingWriteLeavesAnotherProcessATurn(t *testing.T) {
	s := gatheredStore(t)
	const hold = 300 * time.Millisecond
	const misses = 12
	var mu sync.Mutex
	var maxHeld time.Duration
	writeGatheredHooks.Held = func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		if d > maxHeld {
			maxHeld = d
		}
	}
	t.Cleanup(func() { writeGatheredHooks.Held = nil })

	started := make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- writeGathered(ctx, s, func(ctx context.Context, tx *store.Tx) error {
			once.Do(func() { close(started) })
			time.Sleep(hold) // the reads fn makes before it reaches the fact it lacks
			for i := 0; i < misses; i++ {
				if _, err := gather(ctx, "fact "+strconv.Itoa(i), func(context.Context) (string, error) { return "x", nil }); err != nil {
					return err
				}
			}
			return nil
		})
	}()
	<-started

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperGatherSecondWriter$", "-test.v")
	cmd.Env = append(os.Environ(), gatherHelperEnv+"="+s.Path())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the second writer: %v\n%s", err, out)
	}
	// The order is the assertion, not a duration: the second writer got the lock while
	// the looping writer was still looping. Under the bug it waited out the whole loop,
	// so the looping writer had already finished when the second one came back.
	select {
	case err := <-done:
		t.Fatalf("the second writer got the lock only after the looping writer finished all %d misses (%v):\n%s", misses, err, out)
	default:
	}
	if !strings.Contains(string(out), "GOT_LOCK") {
		t.Fatalf("the second writer did not get the lock:\n%s", out)
	}
	mu.Lock()
	held := maxHeld
	mu.Unlock()
	if held == 0 {
		t.Fatal("no attempt reported how long it held the lock")
	}
	cancel()
	<-done
}
