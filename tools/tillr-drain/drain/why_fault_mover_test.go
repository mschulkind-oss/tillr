package drain

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr/store"
)

// A fault hold names its recorded mover and carries its recorded detail. On 2026-11-18
// `tillr drain why` showed "diagnose the run; tillr drain retry --help" for a gate harness
// error, a frozen definition and a tooling defect alike: a reason with no case of its own in
// whyPresentation fell to that default, although holdrules has its row, and the fault's
// detail, which says what is wrong, was not shown at all.
func TestAFaultHoldShowsItsRecordedMoverAndDetail(t *testing.T) {
	for _, tc := range []struct{ reason, detail string }{
		{StopGateHarnessError, `the gate answered "error": a stale typebox lock in the pi-manage-todo-list fork`},
		{ReasonDefinitionSyncFailed, "briefs/item-a.md:3 item-a: definition-frozen: criterion c1 already carries the state done"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			if err := l.holdItemFault(context.Background(), Candidate{ItemID: "item-a"}, tc.reason, tc.detail); err != nil {
				t.Fatal(err)
			}
			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				answer, err := Why(r, Pass{Now: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
				if err != nil {
					return err
				}
				rule := holdrules.For(tc.reason)
				for _, row := range answer.Rows {
					if row.ItemID != "item-a" {
						continue
					}
					// The row's mover is prefixed with its actor ("operator; ").
					if row.Reason != tc.reason || row.Class != rule.Class || row.Where != rule.Where || !strings.HasSuffix(row.Mover, "; "+rule.Mover) {
						t.Errorf("row %q %q/%q/%q, want the recorded rule %+v", row.Reason, row.Class, row.Where, row.Mover, rule)
					}
					if !strings.Contains(row.Why, tc.detail) {
						t.Errorf("why %q does not carry the fault's detail %q", row.Why, tc.detail)
					}
					return nil
				}
				t.Fatal("item-a has no row")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFaultMoverNamesTheActiveFaultSequence(t *testing.T) {
	for _, reason := range []string{ReasonStageFailed, ReasonAdapterRefused, ReasonSettleFailed, ReasonDecisionCollision, ReasonRunProcessesLive, ReasonPreserveFailed} {
		t.Run(reason, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			ctx := context.Background()
			for _, detail := range []string{"superseded evidence", "current evidence"} {
				if err := l.holdItemFault(ctx, Candidate{ItemID: "item-a"}, reason, detail); err != nil {
					t.Fatal(err)
				}
			}
			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				events, err := r.EventsOfKind(0, itemFaultKind)
				if err != nil {
					return err
				}
				if len(events) != 2 || events[1].Seq <= events[0].Seq {
					t.Fatalf("expected two ordered fault events, got %+v", events)
				}
				want := fmt.Sprintf("tillr drain clear-fault --store '%s' --item 'item-a' --fault %d", f.storePath, events[1].Seq)
				answer, err := Why(r, Pass{StorePath: f.storePath, Now: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
				if err != nil {
					return err
				}
				for _, row := range answer.Rows {
					if row.ItemID == "item-a" {
						if row.Reason != reason || !strings.HasSuffix(row.Mover, want) || strings.Count(row.Mover, "clear-fault") != 1 || !strings.Contains(row.Why, "current evidence") {
							t.Fatalf("held row = %+v, want current %s detail and mover ending in %q", row, reason, want)
						}
						return nil
					}
				}
				t.Fatal("item-a has no row")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A planner hold whose reason has a holdrules row and no case of its own shows that row,
// not a generic "diagnose the run".
func TestAPlannerHoldWithNoCaseShowsItsHoldRule(t *testing.T) {
	class, where, mover := whyPresentation(Decision{Action: ActionHold, From: "held", Reason: ReasonToolingDefect})
	if rule := holdrules.For(ReasonToolingDefect); class != rule.Class || where != rule.Where || mover != rule.Mover {
		t.Fatalf("tooling-defect shown as %q/%q/%q, want %+v", class, where, mover, rule)
	}
}
