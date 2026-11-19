package drain

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr/store"
)

func TestUnexpectedPatchMovesAreDurablyHeldWithoutConvictingRun(t *testing.T) {
	f, l := passFixture(t, "item-a")
	own := Worktree{Path: l.lanePath("lane-1"), Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
	snap := func() *Boundary {
		return &Boundary{Repo: f.root, Targets: []string{own.Path}, Refs: map[string]string{}, Worktrees: []Worktree{own}}
	}
	before, after := snap(), snap()
	// More than the diagnostic observation cap: no publication hold may be dropped.
	for i := 0; i < 25; i++ {
		ref := fmt.Sprintf("refs/heads/patch/fork-%02d", i)
		before.Refs[ref] = "old"
		after.Refs[ref] = "new"
	}
	violations, observed, err := CompareRunObserved(before, after)
	if err != nil || len(violations) != 0 {
		t.Fatalf("comparison: %v / %v", violations, err)
	}
	refs := []runRef{{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1"}}
	if err := l.recordBoundaryCheck(context.Background(), BoundaryScopeRun, "tick", refs, before.Targets, violations, l.cfg.Now(), observed...); err != nil {
		t.Fatal(err)
	}
	payload := lastBoundaryCheck(t, f, refs[0].RunID)
	moves, _ := payload["patch_moves"].([]any)
	if payload["clean"] != true || len(moves) != 25 {
		t.Fatalf("want clean bracket and 25 durable publication holds, got %v", payload)
	}
	move := moves[0].(map[string]any)
	if move["ref"] != "refs/heads/patch/fork-00" || move["old"] != "old" || move["new"] != "new" {
		t.Fatalf("move loses identity: %v", move)
	}
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var holds []patchmoves.Hold
	pending := func() {
		t.Helper()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error { var err error; holds, err = patchmoves.Pending(r); return err }); err != nil {
			t.Fatal(err)
		}
	}
	pending()
	if len(holds) != 25 || len(holds[0].Runs) != 1 || holds[0].Runs[0] != refs[0].RunID {
		t.Fatalf("holds: %+v", holds)
	}
	h := holds[0]
	at := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	ack := func(fork, by string) error {
		return s.WriteTx(context.Background(), func(tx *store.Tx) error { return patchmoves.Acknowledge(tx, h.Observation, fork, by, at) })
	}
	for _, invalid := range [][2]string{{h.Fork(), " "}, {"missing", "owner"}} {
		if err := ack(invalid[0], invalid[1]); err == nil {
			t.Fatal("accepted invalid acknowledgment")
		}
	}
	if err := ack(h.Fork(), "owner"); err != nil {
		t.Fatal(err)
	}
	if err := ack(h.Fork(), "owner"); err == nil {
		t.Fatal("accepted repeated acknowledgment")
	}
	pending()
	if len(holds) != 24 {
		t.Fatalf("acknowledgment cleared wrong holds: %+v", holds)
	}
	var by, timestamp string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT json_extract(payload,'$.by'), json_extract(payload,'$.acknowledged_at') FROM events WHERE kind=?`, patchmoves.AcknowledgedKind).Scan(&by, &timestamp)
	}); err != nil {
		t.Fatal(err)
	}
	if by != "owner" || timestamp != at.Format(time.RFC3339Nano) {
		t.Fatalf("missing audit: %s %s", by, timestamp)
	}
	// Retrying the bracket after acknowledgment does not recreate the hold.
	if err := l.recordBoundaryCheck(context.Background(), BoundaryScopeRun, "tick", refs, before.Targets, violations, l.cfg.Now(), observed...); err != nil {
		t.Fatal(err)
	}
	pending()
	if len(holds) != 24 {
		t.Fatalf("replay recreated hold: %v", holds)
	}
	if !strings.Contains(h.Remedy(f.storePath), "--observation") {
		t.Fatal(h.Remedy(f.storePath))
	}
}

func TestLegitimatePatchMovesNeverHoldPublication(t *testing.T) {
	for _, mode := range []string{"marked-landing", "assigned-marked-landing"} {
		t.Run(mode, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			own := Worktree{Path: l.lanePath("lane-1"), Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
			before := &Boundary{Repo: f.root, Targets: []string{own.Path}, Worktrees: []Worktree{own}, Refs: map[string]string{"refs/heads/patch/pi": "old"}}
			after := &Boundary{Repo: f.root, Targets: before.Targets, Worktrees: before.Worktrees, Refs: map[string]string{"refs/heads/patch/pi": "new"}}
			if mode == "assigned-marked-landing" {
				before.PatchRefs = []string{"refs/heads/patch/pi"}
				after.PatchRefs = before.PatchRefs
			}
			after.Refs["refs/tillr/landed/sibling"] = "new"
			violations, observed, err := CompareRunObserved(before, after)
			if err != nil || len(violations) != 0 || len(observed) != 0 {
				t.Fatalf("legitimate move (CompareRunObserved): %v %v %v", violations, observed, err)
			}
			compareViolations, err := Compare(before, after)
			if err != nil || len(compareViolations) != 0 {
				t.Fatalf("legitimate move (Compare): %v %v", compareViolations, err)
			}
			if err := l.recordBoundaryCheck(context.Background(), BoundaryScopeRun, "tick", []runRef{{RunID: "run-item-a-a1"}}, before.Targets, violations, l.cfg.Now(), observed...); err != nil {
				t.Fatal(err)
			}
			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				holds, err := patchmoves.Pending(r)
				if len(holds) != 0 {
					t.Fatalf("legitimate move held: %v", holds)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
