package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// seedFile is the repository's real seed: the queue a burn drains, read exactly as
// `tillr item seed --from config/tillr/seed-items.json` reads it.
const seedFile = "../../config/tillr/seed-items.json"

// The seed a burn starts from dispatches the items the owner ruled for, and every item it
// carries names a fence.
//
// Both halves are the ones that stop a burn before its first tick rather than during it.
// An item with no `allowed_paths` is refused by staging (`briefValues`: a brief with no
// fence is not dispatchable), and three staging refusals in a row trip
// `StopInfraCircuitBreaker` in under a second — so an unfenced seed spends the loop's
// whole stop budget on the file rather than on work. An item whose target paths live only
// on `patch/pi` used to fail the same way: a lane worktree is cut from `main`, so those
// files are not in it. That is no longer the end of the story. A fork-series item whose
// fence names `.forks/lanes/<lane>/<fork>` is staged against that checkout, the boundary
// excuses its `refs/heads/patch/<fork>`, and the land moves the patch branch
// (fork_lane_test.go pins the whole path). The three seed items held below still fence
// neither, so their files are not in the lane worktree and they remain blocked.
//
// The dispatch set moved on 2026-11-13: [`D-D2-7`] ruled OQ-QS1 **A and B together**
// (docs/design/tillr-day2.md), so the two dispatchable survivors are joined by the plan's
// unbuilt chunks and one observatory verification row. This case pinned "exactly the two
// main-tree items" while the seed held five; the set is pinned by id below so the next
// change to it has to change this list on purpose rather than inherit a count.
//
// Negative control: the seed as it stood before this change — five `in-queue` items and no
// `allowed_paths` — fails both halves, so a planner that answered "dispatch" for whatever
// it was given, and a store that dropped the fence, each fail here.
func TestTheSeedFileDispatchesTheRuledItems(t *testing.T) {
	doc, err := os.ReadFile(seedFile)
	if err != nil {
		t.Fatalf("reading %s failed: %v", seedFile, err)
	}
	ctx := context.Background()
	path := filepath.Join(scratch(t, "seed-plan"), "board.sqlite3")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	// The real seed goes in through the real face, so a wildcard or a field the item record
	// does not have is refused here rather than at staging time.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Seed(ctx, tx, seedFile, doc, time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC))
		return err
	}); err != nil {
		t.Fatalf("seeding %s failed: %v", seedFile, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}

	// Every item carries a fence, dispatched tonight or held. The count is the seed's own
	// size and is bumped by hand when an item is filed, on purpose: it is the tripwire that
	// a seed write dropped or duplicated rows, and deriving it from the same file would make
	// it agree with any mistake that file already contains. D-QP6's immediate prune removed
	// the 15 terminal rows (7 `landed`, 7 `resolved-elsewhere`, 1 `closed`) from the
	// 79-row file, so the count moved from 79 to 64. The bar's two rows then moved to an
	// item block in the status-bar plan, so the count moved from 64 to 62: the block is the
	// definition from here and the seed only ever shrinks. Every pruned row was terminal or
	// superseded, so none was ever dispatchable.
	fences := allowedPaths(t, path)
	if len(fences) != 62 {
		t.Fatalf("the seed stored %d item(s), want 62: %v", len(fences), fences)
	}
	for id, paths := range fences {
		if len(paths) == 0 {
			t.Fatalf("item %s names no allowed_paths, so staging would refuse it and three such refusals would halt the loop", id)
		}
	}

	// Seven lanes and a dispatch budget of seven, so the seven highest-ranked items go and the
	// rest are held by rank or by their own state: the assertion below is about the seed's
	// order and states, not about capacity as a policy.
	tickPath := tick(t, "2026-11-13T00:00:00Z",
		[]string{"lane-1", "lane-2", "lane-3", "lane-4", "lane-5", "lane-6", "lane-7"}, 7, policy(t, "/bin/true"))
	records := plan(t, path, tickPath)

	dispatched := []string{}
	for _, record := range records {
		decision := record.Payload
		if decision.Action == drain.ActionDispatch {
			dispatched = append(dispatched, decision.ItemID)
			if len(fences[decision.ItemID]) == 0 {
				t.Fatalf("item %s was dispatched with no fence", decision.ItemID)
			}
			continue
		}
		// A held item is held by its own state — `blocked` for the fork items and the rulings
		// waiting on the owner, `not-ready` for the rows the seed keeps as history after they
		// landed or closed — by the fence of an earlier item in this tick, or by rank against
		// a budget of seven. All are facts about the seed; none is a lane shortage.
		if decision.Action != drain.ActionHold {
			t.Fatalf("item %s was %q, want a hold", decision.ItemID, decision.Action)
		}
		switch decision.Reason {
		case drain.ReasonBlocked, drain.ReasonNotReady, drain.ReasonNoLane, drain.ReasonForkHeld:
		default:
			t.Fatalf("item %s was held for %q, want %s, %s or %s", decision.ItemID,
				decision.Reason, drain.ReasonBlocked, drain.ReasonNoLane, drain.ReasonForkHeld)
		}
		if decision.Reason == drain.ReasonForkHeld && decision.DeferredBy == "" {
			t.Fatalf("item %s was deferred behind nothing: a fork-held hold names the item that holds the branch", decision.ItemID)
		}
	}
	// The seven highest-ranked dispatchable items, in rank order. The rank is the queue's
	// priority (`items.rank`) and the lane count is the only bound: two items with disjoint
	// fences run together now, because each has its own worktree and the lander serializes its
	// own ref swap. A plan that still serialized on fence overlap would dispatch fewer and
	// change this list. The list is the seed's real dispatch set, so a change to a rank or a
	// fence moves it and has to change this list on purpose rather than inherit a count.
	//
	// The budget is exactly seven, so the seven names here are the whole dispatch set: the
	// rows after them hold on `capacity`, and `item-obs-questions-view` (`04`) is held by its
	// own `blocked_by` edge. `00` (`item-obs-filter`) left the queue by landing
	// (`253d1e4437`).
	want := []string{
		"item-obs-archive", "item-obs-unranked-section-v2", "item-pipe-real-registry",
		"item-drain-fence-overlap", "item-run-isolated-durable-dir",
		"item-verdict-writes-the-seed-back", "item-turn-stopwatch-length",
	}
	if len(dispatched) != len(want) {
		t.Fatalf("the seed dispatched %v, want exactly %v", dispatched, want)
	}
	for i, id := range want {
		if dispatched[i] != id {
			t.Fatalf("the seed dispatched %v, want exactly %v", dispatched, want)
		}
	}
	// The dispatching items target files `main` actually has: a fence that names a path the
	// trunk does not carry is a dispatch that cannot succeed, whatever the model does.
	for _, id := range want {
		for _, fence := range fences[id] {
			target := filepath.Join("..", "..", fence)
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("item %s fences %q, which is not in the tree: %v", id, fence, err)
			}
		}
	}
}

// allowedPaths answers every stored item's fence, keyed by item id, read from the column
// staging reads rather than from the seed document: a loader that dropped the field would
// pass a comparison against the file it just read.
func allowedPaths(t *testing.T, path string) map[string][]string {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatalf("opening %s failed: %v", path, err)
	}
	defer s.Close()
	fences := map[string][]string{}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id, COALESCE(allowed_paths, '') FROM items ORDER BY item_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, raw string
			if err := rows.Scan(&id, &raw); err != nil {
				return err
			}
			// The column is the list as JSON text, which is the store's spelling of it.
			var decoded []string
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
					return err
				}
			}
			fences[id] = decoded
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("reading the fences failed: %v", err)
	}
	return fences
}
