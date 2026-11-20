package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestAnsweredObstacleRepeat(t *testing.T) {
	for _, tc := range []struct{ kind, first, next, waiter string }{
		{"definition", "CRITERIA: c1\nEVIDENCE: first count", "CRITERIA: c1\nEVIDENCE: second count", "operator"},
		{"definition", "CRITERIA: c1\nEVIDENCE: first count", "CRITERIA: c2\nEVIDENCE: second count", "design"},
		{"owner", "QUESTION: publish the branch?", "QUESTION: publish the branch?", "owner"},
		{"owner", "QUESTION: publish the branch?", "QUESTION: publish the other branch?", "design"},
	} {
		t.Run(tc.kind+"/"+tc.waiter, func(t *testing.T) {
			f, l, work := obstacleFixture(t, &obstacleSteps{}, "item-a")
			ctx := context.Background()
			report := func(fields string) string { return "OBSTACLE: " + tc.kind + "\n" + fields + "\nRESULT: blocked" }
			settleReport(t, f, l, work, report(tc.first))
			c, ok := conflictOf(t, f, "item-a")
			if !ok || c.WaitingOn != "design" {
				t.Fatalf("first obstacle: %+v", c)
			}
			path := filepath.Join(f.root, c.BodyRef)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, path, "# Keep the criterion\n\nThe original criterion still applies.\n\n"+string(original))
			runGit(t, f.root, "add", c.BodyRef)
			runGit(t, f.root, "commit", "-qm", "explain the criterion")
			head := runGit(t, f.root, "rev-parse", TrunkRef)
			if err := l.recordBlockAnswer(ctx, Candidate{ItemID: "item-a", HeadSHA: head, BlockConflict: &c}); err != nil {
				t.Fatal(err)
			}
			p := decisionFor(t, l, "item-a")
			claimed, nextWork, err := l.applyPlan(ctx, p, head, l.cfg.Now())
			if err != nil || !claimed {
				t.Fatalf("claim worker after answer: %v %v (%+v)", claimed, err, p.decision)
			}
			settleReport(t, f, l, nextWork, report(tc.next))
			next, _ := conflictOf(t, f, "item-a")
			if tc.waiter == "design" {
				if next.WaitingOn != "design" {
					t.Fatalf("new obstacle held: %+v", next)
				}
			} else if next.Status != "owner" || next.WaitingOn != "owner" || !strings.Contains(next.Text, "Held for the "+tc.waiter) || !strings.Contains(next.Text, report(tc.first)) || !strings.Contains(next.Text, report(tc.next)) {
				t.Fatalf("repeat not held with both reports: %+v", next)
			}
			if tc.waiter != "design" {
				d := decisionFor(t, l, "item-a").decision
				if d.Action != ActionHold || d.Seat != tc.waiter {
					t.Fatalf("repeat planner decision: %+v", d)
				}
			}
			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
				if err != nil {
					return err
				}
				mover := answer.Rows[0].Mover
				if !strings.HasSuffix(mover, "; answered obstacles: 1") {
					t.Fatalf("answer count missing from why: %s", mover)
				}
				if tc.waiter != "design" && (!strings.HasPrefix(mover, tc.waiter+";") || !strings.Contains(mover, collapseSingleLine(report(tc.first))) || !strings.Contains(mover, collapseSingleLine(report(tc.next)))) {
					t.Fatalf("why does not show both reports and the next actor: %s", mover)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
