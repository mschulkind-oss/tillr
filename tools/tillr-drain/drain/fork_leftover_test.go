package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func readForkLeftoverEvents(t *testing.T, f refusalLoopFixture) []ForkLeftover {
	t.Helper()
	var out []ForkLeftover
	fixtureRead(t, f, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, ForkLeftoverKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var ev ForkLeftover
			if err := json.Unmarshal([]byte(raw), &ev); err != nil {
				return err
			}
			out = append(out, ev)
		}
		return rows.Err()
	})
	return out
}

func TestForkCheckoutLeftoverReportedWhenRunEnds(t *testing.T) {
	t.Run("uncommitted work left by ended run", func(t *testing.T) {
		f, l, c := heldForkFixture(t)
		ctx := context.Background()
		// Start at the delivered tip so HEAD is clean, then leave uncommitted files.
		runGit(t, c.Worktree, "reset", "--hard", c.BaseSHA)
		writeForkFile(t, filepath.Join(c.Worktree, "leftover-a.txt"), "dirty a\n")
		writeForkFile(t, filepath.Join(c.Worktree, "leftover-b.txt"), "dirty b\n")

		// Simulate an ended run whose settlement fails without resetting the fork checkout.
		runID := c.RunID + "-ended"
		req := run.Request{RunID: runID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", CorrelationID: "tick-1"}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, Action: ActionSettle, Attempt: 1, RunRequest: &req}
		work := &pendingWork{Decision: d, BaseSHA: c.BaseSHA, LoopID: "loop-test"}
		timing := &PassTiming{TickID: "tick-1", AtMS: l.cfg.Now().UnixMilli()}

		_ = l.examineClaimed(ctx, d, work, l.cfg.Now(), timing, nil)

		events := readForkLeftoverEvents(t, f)
		if len(events) != 1 {
			t.Fatalf("got %d %s events, want 1: %+v", len(events), ForkLeftoverKind, events)
		}
		got := events[0]
		absCheckout, err := filepath.Abs(c.Worktree)
		if err != nil {
			t.Fatal(err)
		}
		if got.Checkout != absCheckout || got.LaneID != c.LaneID || got.RunID != runID || got.ItemID != c.ItemID || got.Ref != c.BaseRef {
			t.Fatalf("unexpected identity fields: %+v", got)
		}
		if got.UncommittedCount != 2 || !slices.Equal(got.UncommittedFiles, []string{"leftover-a.txt", "leftover-b.txt"}) {
			t.Fatalf("unexpected uncommitted fields: %+v", got)
		}
		if got.UnretainedHead != "" {
			t.Fatalf("unexpected unretained_head %q for clean HEAD", got.UnretainedHead)
		}
		if !strings.Contains(got.Holds, "uncommitted work") || !strings.Contains(got.Holds, "leftover-a.txt") {
			t.Fatalf("holds = %q, want uncommitted work summary", got.Holds)
		}
		// Nothing is cleared automatically.
		for _, name := range []string{"leftover-a.txt", "leftover-b.txt"} {
			if _, err := os.Stat(filepath.Join(c.Worktree, name)); err != nil {
				t.Fatalf("uncommitted file %s was cleared automatically: %v", name, err)
			}
		}
	})

	t.Run("unretained commits left by ended run", func(t *testing.T) {
		f, l, c := heldForkFixture(t)
		ctx := context.Background()
		// heldForkFixture leaves c.HeadSHA committed in c.Worktree without a run pin
		// and not in refs/heads/patch/fixture-fork (c.BaseSHA).
		runID := c.RunID + "-ended"
		req := run.Request{RunID: runID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", CorrelationID: "tick-2"}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, Action: ActionSettle, Attempt: 1, RunRequest: &req}
		work := &pendingWork{Decision: d, BaseSHA: c.BaseSHA, LoopID: "loop-test"}
		timing := &PassTiming{TickID: "tick-2", AtMS: l.cfg.Now().UnixMilli()}

		_ = l.examineClaimed(ctx, d, work, l.cfg.Now(), timing, nil)

		events := readForkLeftoverEvents(t, f)
		if len(events) != 1 {
			t.Fatalf("got %d %s events, want 1: %+v", len(events), ForkLeftoverKind, events)
		}
		got := events[0]
		if got.UnretainedHead != c.HeadSHA || got.DeliveryTip != c.BaseSHA {
			t.Fatalf("unretained = (%q, tip %q), want (%q, tip %q)", got.UnretainedHead, got.DeliveryTip, c.HeadSHA, c.BaseSHA)
		}
		if got.UncommittedCount != 0 || len(got.UncommittedFiles) != 0 {
			t.Fatalf("unexpected uncommitted fields: %+v", got)
		}
		if !strings.Contains(got.Holds, "unretained commits at "+c.HeadSHA[:8]) {
			t.Fatalf("holds = %q, want unretained commits summary", got.Holds)
		}
		// Nothing is cleared automatically: HEAD is still the unretained commit.
		if head := runGit(t, c.Worktree, "rev-parse", "HEAD"); head != c.HeadSHA {
			t.Fatalf("HEAD = %s, want unretained %s preserved", head, c.HeadSHA)
		}
	})

	t.Run("both uncommitted work and unretained commits produce one event", func(t *testing.T) {
		f, l, c := heldForkFixture(t)
		ctx := context.Background()
		writeForkFile(t, filepath.Join(c.Worktree, "extra.txt"), "uncommitted\n")

		runID := c.RunID + "-ended"
		req := run.Request{RunID: runID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", CorrelationID: "tick-3"}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, Action: ActionSettle, Attempt: 1, RunRequest: &req}
		work := &pendingWork{Decision: d, BaseSHA: c.BaseSHA, LoopID: "loop-test"}
		timing := &PassTiming{TickID: "tick-3", AtMS: l.cfg.Now().UnixMilli()}

		_ = l.examineClaimed(ctx, d, work, l.cfg.Now(), timing, nil)

		events := readForkLeftoverEvents(t, f)
		if len(events) != 1 {
			t.Fatalf("got %d %s events, want 1: %+v", len(events), ForkLeftoverKind, events)
		}
		got := events[0]
		if got.UncommittedCount != 1 || got.UnretainedHead != c.HeadSHA {
			t.Fatalf("expected both leftovers in one event, got %+v", got)
		}
		if !strings.Contains(got.Holds, "uncommitted work") || !strings.Contains(got.Holds, "unretained commits") {
			t.Fatalf("holds = %q, want both uncommitted work and unretained commits", got.Holds)
		}
		if _, err := os.Stat(filepath.Join(c.Worktree, "extra.txt")); err != nil {
			t.Fatalf("uncommitted file was cleared: %v", err)
		}
		if head := runGit(t, c.Worktree, "rev-parse", "HEAD"); head != c.HeadSHA {
			t.Fatalf("HEAD = %s, want %s preserved", head, c.HeadSHA)
		}
	})

	t.Run("settled candidate leaving uncommitted work reports it", func(t *testing.T) {
		f, l, c := heldForkFixture(t)
		ctx := context.Background()
		// A worker committed candidate.txt and also left an untracked file behind.
		writeForkFile(t, filepath.Join(c.Worktree, "scratch.tmp"), "left behind\n")

		req := run.Request{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", CorrelationID: "tick-4"}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, Action: ActionSettle, Attempt: 1, RunRequest: &req}
		work := &pendingWork{Decision: d, BaseSHA: c.BaseSHA, LoopID: "loop-test"}
		timing := &PassTiming{TickID: "tick-4", AtMS: l.cfg.Now().UnixMilli()}

		_ = l.examineClaimed(ctx, d, work, l.cfg.Now(), timing, nil)

		events := readForkLeftoverEvents(t, f)
		if len(events) != 1 {
			t.Fatalf("got %d %s events, want 1: %+v", len(events), ForkLeftoverKind, events)
		}
		got := events[0]
		if got.UncommittedCount != 1 || !slices.Equal(got.UncommittedFiles, []string{"scratch.tmp"}) || got.UnretainedHead != "" {
			t.Fatalf("unexpected leftover after settled candidate: %+v", got)
		}
		if _, err := os.Stat(filepath.Join(c.Worktree, "scratch.tmp")); err != nil {
			t.Fatalf("uncommitted file was cleared: %v", err)
		}
	})

	t.Run("clean or retained checkout emits no event", func(t *testing.T) {
		for _, mode := range []string{"at-tip", "pinned"} {
			t.Run(mode, func(t *testing.T) {
				f, l, c := heldForkFixture(t)
				ctx := context.Background()
				if mode == "at-tip" {
					runGit(t, c.Worktree, "reset", "--hard", c.BaseSHA)
				} else {
					if err := l.pinLane(ctx, c.LaneID, c.ItemID, c.RunID, c.HeadSHA); err != nil {
						t.Fatal(err)
					}
				}
				runID := c.RunID + "-ended"
				req := run.Request{RunID: runID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", CorrelationID: "tick-clean"}
				d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, Action: ActionSettle, Attempt: 1, RunRequest: &req}
				work := &pendingWork{Decision: d, BaseSHA: c.BaseSHA, LoopID: "loop-test"}
				timing := &PassTiming{TickID: "tick-clean", AtMS: l.cfg.Now().UnixMilli()}
				_ = l.examineClaimed(ctx, d, work, l.cfg.Now(), timing, nil)

				if events := readForkLeftoverEvents(t, f); len(events) != 0 {
					t.Fatalf("%s: got %d %s events, want 0: %+v", mode, len(events), ForkLeftoverKind, events)
				}
			})
		}
	})
}
