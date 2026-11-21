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
	ok, work, err := loop.applyPlan(context.Background(), plans[0], base, at)
	if err != nil || ok || work != nil {
		t.Fatalf("claim on the drawn, closed backend: ok=%v work=%+v err=%v", ok, work, err)
	}
	reading(.79)
	ok, work, err = loop.applyPlan(context.Background(), plans[0], base, at)
	if err != nil || !ok || work.Decision.RunRequest.Backend != "builder-a" {
		t.Fatalf("claim after reopening: ok=%v work=%+v err=%v", ok, work, err)
	}
}

func TestSeatSharesClosedDrawSurvivesRollbackAndReopen(t *testing.T) {
	for _, pick := range []string{"in-order", "round-robin", "random"} {
		t.Run(pick, func(t *testing.T) {
			ctx := context.Background()
			f, _ := passFixture(t, "item-a")
			s, err := store.Open(ctx, f.storePath, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			p := sharesPolicy(t, fmt.Sprintf(`{"backends":[{"backend":"builder-a","share":1},{"backend":"builder-b","share":1},{"backend":"designer-a","share":1}],"pick":%q}`, pick))
			if err := p.Check(); err != nil {
				t.Fatal(err)
			}
			p.seatDraw = func() float64 { return 0 }
			choose := func(id string, shut map[string]bool, rollback bool) string {
				t.Helper()
				d := Decision{DecisionID: id, ItemID: id, Seat: "build", LaneID: "lane", Action: ActionDispatch, SelectSeat: true, RunRequest: p.request(id, "lane", id, "builder-a")}
				var result string
				err := s.WriteTx(ctx, func(tx *store.Tx) error {
					return keepSeatWait(tx, func() error {
						ok, err := selectSeatDispatch(tx, &d, &p, shut, time.Now())
						if err != nil || !ok {
							return err
						}
						result = d.RunRequest.Backend
						if rollback {
							return errSeatWindowClosed
						}
						return recordPassDecision(tx, d, time.Now())
					})
				})
				if err != nil && !(rollback && err == errSeatWindowClosed) {
					t.Fatal(err)
				}
				return result
			}
			if got := choose("rolled-back", nil, true); got != "builder-a" {
				t.Fatalf("rolled-back draw: %s", got)
			}
			shut := map[string]bool{"builder-a": true}
			if got := choose("waiting", shut, false); got != "" {
				t.Fatalf("closed draw dispatched on %s", got)
			}
			// Change the next selection: a new draw would no longer give A.
			if got := choose("another", nil, false); got != "builder-a" {
				t.Fatalf("wait counted as a dispatch: %s", got)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(ctx, f.storePath, true)
			if err != nil {
				t.Fatal(err)
			}
			s = reopened
			p.seatDraw = func() float64 { return .99 }
			if got := choose("waiting", shut, false); got != "" {
				t.Fatalf("waiting item redrew onto %s", got)
			}
			if got := choose("waiting", nil, false); got != "builder-a" {
				t.Fatalf("reopened draw = %s, want builder-a", got)
			}
			if pick == "round-robin" {
				for i, want := range []string{"builder-b", "designer-a", "builder-a"} {
					if got := choose(fmt.Sprintf("turn-%d", i), nil, false); got != want {
						t.Errorf("round-robin turn %d = %s, want %s", i, got, want)
					}
				}
			}
		})
	}
}
