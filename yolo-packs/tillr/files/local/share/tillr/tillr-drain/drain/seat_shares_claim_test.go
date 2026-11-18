package drain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestSeatSharesTwoLoopClaimsAndRestart(t *testing.T) {
	ids := make([]string, 40)
	lanes := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("item-%02d", i)
		lanes[i] = fmt.Sprintf("lane-%d", i+1)
	}
	f, first := passFixture(t, ids...)
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET seat='build'`); return err })
	policy := sharesPolicy(t, `[{"backend":"builder-a","share":3},{"backend":"builder-b","share":1}]`)
	first.cfg.Policy = &policy
	first.cfg.Lanes = lanes
	second := f.loop(t, &recordingSteps{}, &itemContentStager{})
	second.cfg.Policy = &policy
	second.cfg.Lanes = lanes
	plans := mustPlan(t, first)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	counts := map[string]int{}
	for i, plan := range plans {
		if i == 20 {
			first = f.loop(t, &recordingSteps{}, &itemContentStager{})
			first.cfg.Policy = &policy
			first.cfg.Lanes = lanes
		}
		loop := first
		if i%2 == 1 {
			loop = second
		}
		ok, work, err := loop.applyPlan(context.Background(), plan, base, loop.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("claim %d: %v/%v, decision %+v", i, ok, err, plan.decision)
		}
		counts[work.Decision.RunRequest.Backend]++
	}
	if counts["builder-a"] != 30 || counts["builder-b"] != 10 {
		t.Fatalf("40 claims: %v", counts)
	}
}

func TestSeatSharesClaimHonorsReserve(t *testing.T) {
	f, loop := passFixture(t, "item-a", "item-b", "item-c")
	policy := sharesPolicy(t, `[{"backend":"builder-a","share":1},{"backend":"builder-b","share":1}]`)
	backend := policy.Backends["builder-a"]
	backend.Quota, backend.Reserve = []string{"fixture-quota"}, .2
	policy.Backends["builder-a"] = backend
	loop.cfg.Policy = &policy
	loop.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3"}
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET seat='build'`); return err })
	at := loop.cfg.Now()
	reading := func(fraction float64) {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			windows := fmt.Sprintf(`[{"kind":"five_hour","fraction":%g,"reset_at":%q}]`, fraction, at.Add(time.Hour).Format(time.RFC3339))
			_, err := tx.Exec(`INSERT OR REPLACE INTO backend_quota(backend,command,observed_at,reserve,windows,error) VALUES(?,?,?,?,?,'')`, "builder-a", `["fixture-quota"]`, at.UnixMilli(), .2, windows)
			return err
		})
	}
	reading(.79)
	plans := mustPlan(t, loop)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	reading(.8)
	for i, want := range []string{"builder-b", "builder-b", "builder-a"} {
		if i == 2 {
			reading(.79)
		}
		ok, work, err := loop.applyPlan(context.Background(), plans[i], base, at)
		if err != nil || !ok {
			t.Fatalf("claim %d: ok=%v, err=%v", i, ok, err)
		}
		if got := work.Decision.RunRequest.Backend; got != want {
			t.Fatalf("claim %d: got %s, want %s", i, got, want)
		}
	}
}

func TestSeatSharesRoundRobinQuotaFailoverAndRollback(t *testing.T) {
	ctx := context.Background()
	f, _ := passFixture(t, "item-a")
	s, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := sharesPolicy(t, `{"backends":["builder-a","builder-b","designer-a"],"pick":"round-robin"}`)
	choose := func(id string, policy Policy, shut map[string]bool, fresh bool, backend string, rollback bool) string {
		t.Helper()
		d := Decision{DecisionID: id, ItemID: id, Seat: "build", LaneID: "lane", Action: ActionDispatch, SelectSeat: fresh, RunRequest: policy.request(id, "lane", id, backend)}
		err := s.WriteTx(ctx, func(tx *store.Tx) error {
			ok, err := selectSeatDispatch(tx, &d, &policy, shut, time.Now())
			if err != nil {
				return err
			}
			if !ok {
				t.Fatal("no backend")
			}
			if rollback {
				return errSeatWindowClosed
			}
			return recordPassDecision(tx, d, time.Now())
		})
		if err != nil && !(rollback && err == errSeatWindowClosed) {
			t.Fatal(err)
		}
		return d.RunRequest.Backend
	}
	for i, want := range []string{"builder-a", "builder-b", "designer-a", "builder-a", "builder-b", "designer-a"} {
		if got := choose(fmt.Sprint(i), p, nil, true, "builder-a", false); got != want {
			t.Fatalf("round robin %d: %s, want %s", i, got, want)
		}
	}
	if got := choose("rolled-back", p, nil, true, "builder-a", true); got != "builder-a" {
		t.Fatal(got)
	}
	if got := choose("after-rollback", p, map[string]bool{"builder-a": true}, true, "builder-a", false); got != "builder-b" {
		t.Fatal(got)
	}
	// Ordered refusal fail-over stays at the planner-selected successor, never draws again.
	if got := choose("failover", p, nil, false, "designer-a", false); got != "designer-a" {
		t.Fatal(got)
	}
	legacy := sharesPolicy(t, `["builder-a","builder-b"]`)
	if got := choose("legacy", legacy, nil, true, "builder-a", false); got != "builder-a" {
		t.Fatal(got)
	}
	weighted := sharesPolicy(t, `[{"backend":"builder-a","share":100},{"backend":"builder-b","share":3},{"backend":"designer-a","share":1}]`)
	// Counts in a separate seat start at zero; a quota/reserve closure removes its
	// entire weight. Surviving services receive 3:1, not 100:3:1.
	weighted.Seats["review"] = weighted.Seats["build"]
	weighted.SeatLists["review"] = weighted.SeatLists["build"]
	weighted.SeatShares["review"] = weighted.SeatShares["build"]
	counts := map[string]int{}
	for i := 0; i < 40; i++ {
		d := Decision{DecisionID: fmt.Sprintf("quota-%d", i), ItemID: fmt.Sprintf("quota-%d", i), Seat: "review", Action: ActionDispatch, SelectSeat: true, RunRequest: weighted.request("quota", "lane", fmt.Sprintf("quota-%d", i), "builder-a")}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			ok, err := selectSeatDispatch(tx, &d, &weighted, map[string]bool{"builder-a": true}, time.Now())
			if err != nil {
				return err
			}
			if !ok {
				t.Fatal("all closed")
			}
			return recordPassDecision(tx, d, time.Now())
		}); err != nil {
			t.Fatal(err)
		}
		counts[d.RunRequest.Backend]++
	}
	if counts["builder-a"] != 0 || counts["builder-b"] != 30 || counts["designer-a"] != 10 {
		t.Fatalf("closed quota redistribution: %v", counts)
	}
}
