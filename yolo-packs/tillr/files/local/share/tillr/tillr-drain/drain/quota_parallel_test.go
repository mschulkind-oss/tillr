package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A retained group can have a completed primary and unfinished sibling requests.
// Planning and claim must judge only the executions that will actually start.
func TestRetainedParallelQuotaAdmission(t *testing.T) {
	for _, mode := range []string{"retry", "restart"} {
		for _, closed := range []string{"sibling", "completed-primary", "unfinished-primary"} {
			t.Run(mode+"/"+closed, func(t *testing.T) {
				f, l, _ := parallelFixture(t)
				l.cfg.Policy.Backends["secondary"] = Backend{Adapter: "fixture", Quota: fixtureQuotaCommand(), Reserve: .2}
				l.cfg.Policy.SeatLists = map[string][]string{"secondary": {"secondary"}}
				enableFixtureQuota(l, "fixture")
				b := l.cfg.Policy.Backends["fixture"]
				b.Reserve = .2
				l.cfg.Policy.Backends["fixture"] = b
				d := mustPlan(t, l)[0].decision
				for i := 1; i <= 3; i++ {
					d.Workflow.Stages[i].Seat = "secondary"
				}
				fixtureWrite(t, f, func(tx *store.Tx) error { return l.claimParallel(tx, &d) })
				base := runGit(t, f.root, "rev-parse", TrunkRef)
				fixtureWrite(t, f, func(tx *store.Tx) error {
					if closed != "unfinished-primary" {
						code := 0
						if err := applyTerminal(tx, run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: &code, StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339)}); err != nil {
							return err
						}
					}
					work := pendingWork{Decision: d, BaseSHA: base, Ready: mode == "retry"}
					if err := appendEffect(tx, nodeAdvancedKind, d.RunRequest.RunID, work, l.cfg.Now()); err != nil {
						return err
					}
					if mode == "restart" {
						return appendEffect(tx, loopOrphanedKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now())
					}
					return nil
				})
				// The open plan precedes a new reading. Claim must independently
				// reject it if an unfinished execution has become unavailable.
				p := mustPlan(t, l)[0]
				backend := "fixture"
				if closed == "sibling" {
					backend = "secondary"
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE backend_quota SET windows='[{"kind":"five_hour","fraction":0.8,"reset_at":"2099-11-18T00:00:00Z"}]',reserve=0.2 WHERE backend=?`, backend)
					return err
				})
				wantOpen := closed == "completed-primary"
				planned := mustPlan(t, l)[0].decision
				if (planned.Action == ActionDispatch) != wantOpen {
					t.Errorf("plan action=%s reason=%s; want open=%v", planned.Action, planned.Reason, wantOpen)
				}
				ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
				if err != nil {
					t.Fatal(err)
				}
				if ok != wantOpen {
					t.Fatalf("claimed=%v; want open=%v", ok, wantOpen)
				}
			})
		}
	}
}

func TestParallelQuotaAdmissionChecksSiblingBackends(t *testing.T) {
	for _, state := range []string{"reserve", "stale", "error"} {
		for _, open := range []bool{false, true} {
			name := state
			if open {
				name += "-open-seat"
			}
			t.Run(name, func(t *testing.T) {
				f, l, _ := parallelFixture(t)
				l.cfg.Policy.Backends["blocked"] = Backend{Adapter: "fixture", Quota: fixtureQuotaCommand(), Reserve: .2}
				l.cfg.Policy.Backends["open"] = Backend{Adapter: "fixture"}
				l.cfg.Policy.Seats["secondary"] = "blocked"
				if open {
					l.cfg.Policy.Seats["secondary"] = "open"
				}
				d := mustPlan(t, l)[0].decision
				d.Workflow.Stages[1].Seat = "secondary"
				fixtureWrite(t, f, func(tx *store.Tx) error {
					switch state {
					case "reserve":
						_, err := tx.Exec(`UPDATE backend_quota SET windows='[{"kind":"five_hour","fraction":0.8,"reset_at":"2099-11-18T00:00:00Z"}]' WHERE backend='blocked'`)
						return err
					case "stale":
						_, err := tx.Exec(`UPDATE backend_quota SET observed_at=? WHERE backend='blocked'`, l.cfg.Now().Add(-QuotaProbeInterval-time.Second).UnixMilli())
						return err
					default:
						_, err := tx.Exec(`UPDATE backend_quota SET windows='[]',error='quota command failed' WHERE backend='blocked'`)
						return err
					}
				})
				fixtureWrite(t, f, func(tx *store.Tx) error { return l.claimParallel(tx, &d) })
				found := false
				for _, sibling := range d.Parallel {
					if sibling.RunRequest.Backend == "blocked" {
						t.Fatalf("claimed %s sibling on closed backend", state)
					}
					if sibling.Workflow.Stages[sibling.NodeIndex].ID == "scout2" {
						found = true
						if sibling.RunRequest.Backend != "open" {
							t.Fatalf("wrong explicit backend: %+v", sibling.RunRequest)
						}
					}
				}
				if found != open {
					t.Fatalf("secondary claimed=%v, open=%v", found, open)
				}
			})
		}
	}
}
