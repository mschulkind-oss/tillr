package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A real runner and Pi adapter execute a harness fixture in a staged worktree.
// Two pauses interrupt sleep 60; the third segment makes the one candidate.
func TestTwoPausesContinueOneAttemptAndLandOnce(t *testing.T) {
	harness := harnessCount + `prev=
for arg in "$@"; do
    if [ "$prev" = "--session-id" ] || [ "$prev" = "--session" ]; then printf 'session-id %s\n' "$arg" >> "$STUB_ARGV"; fi
    prev=$arg
done
n=0; [ ! -f "$STUB_SEGMENT" ] || n=$(cat "$STUB_SEGMENT")
n=$((n+1))
if [ "$n" -lt 3 ]; then
    trap 'exit 143' TERM
    echo "$n" > "$STUB_SEGMENT"
    sleep 60
else
    cat
    echo candidate > candidate.txt
    git add candidate.txt
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
    printf 'RESULT: done\n'
fi
`
	f := newWalledFixture(t, harness)
	var report strings.Builder
	loop := f.newLoop(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0), &report)
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		result, err := loop.Run(ctx)
		if err == nil && (result.Landed != 1 || result.Candidates != 1) {
			err = run.Internalf("landed=%d candidates=%d", result.Landed, result.Candidates)
		}
		finished <- err
	}()
	for segment := 1; segment <= 2; segment++ {
		deadline := time.Now().Add(hangGuard)
		for {
			raw, _ := os.ReadFile(os.Getenv("STUB_SEGMENT"))
			if strings.TrimSpace(string(raw)) == string(rune('0'+segment)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("segment never entered sleep 60")
			}
			select {
			case err := <-finished:
				t.Fatalf("loop stopped early: %v", err)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		// The initial dispatch's item state remains unchanged. Earlier pauses wrote
		// no verdict, refusal or retry, even though the runner has a terminal row.
		s, err := store.Open(ctx, f.loop.store, true)
		if err != nil {
			t.Fatal(err)
		}
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			decisions, err := drain.Plan(r, drain.Pass{}, f.loop.policy, nil)
			if err != nil {
				return err
			}
			for _, env := range decisions {
				var d drain.Decision
				if err := json.Unmarshal(env.Payload, &d); err != nil {
					return err
				}
				if d.ItemID == "item_041" && d.Position != "running" {
					return run.Internalf("position=%s", d.Position)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if segment == 2 {
			events, e := s.Events(ctx, 0)
			if e != nil {
				t.Fatal(e)
			}
			for _, event := range events {
				if strings.Contains(event.Kind, "verdict") || strings.Contains(event.Kind, "refusal") || strings.Contains(event.Kind, "retry") {
					t.Fatalf("pause wrote %s", event.Kind)
				}
			}
		}
		err = run.RequestPause(ctx, s, "run-item_041-a1", 2*time.Second, time.Now())
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-finished; err != nil {
		t.Fatalf("loop failed: %v\n%s", err, &report)
	}
	rows := lines(t, f.argv)
	if len(rows) != 3 || rows[0] != rows[1] || rows[1] != rows[2] {
		t.Fatalf("session not continued: %v", rows)
	}
	s, err := store.OpenRead(context.Background(), f.loop.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, e := range events {
		counts[e.Kind]++
	}
	if counts[run.AllocateKind] != 1 || counts[run.ResumeKind] != 2 || counts["drain.candidate"] != 1 {
		t.Fatalf("event counts: %v", counts)
	}
	calls := lines(t, f.loop.count)
	if countMatching(calls, "gate --item item_041") != 1 || countMatching(calls, "land --item item_041") != 1 || countMatching(calls, "verdict") != 1 {
		t.Fatalf("judged more than once: %v", calls)
	}
	log, err := os.ReadFile(filepath.Join(f.laneDir, "run-item_041-a1.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "interrupted by a restart") {
		t.Fatal("continuation did not receive restart notice")
	}
}
