package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// TestJudgeGrantAbsentShowsHeldAndNamesExpiredGrant reproduces the 2026-11-19 failure:
// items waiting on judge-grant-absent (queued before dispatch, mid-workflow at an
// evaluator_slot node, or post-candidate before agent review) must be reported as held
// rather than running/moving, and when a covering grant existed and expired, Why must
// name the grant, when it expired, and the command that issues a new one.
func TestJudgeGrantAbsentShowsHeldAndNamesExpiredGrant(t *testing.T) {
	f, l, _ := judgeRouteFixture(t, "mechanical", true)
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")

	issuedAt := l.cfg.Now().Add(-23 * time.Hour)
	expiresAt := issuedAt.Add(24 * time.Hour) // 1 hour from initial Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, q := range []string{
			`DELETE FROM item_grants`,
			`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`,
			`UPDATE items SET state='in-queue', judge_mode='agent', allowed_paths='["tools/item-b/"]' WHERE item_id='item-b'`,
			`INSERT OR IGNORE INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('item-b','c1','verify item-b','','verification-only')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		_, err := tx.Exec(
			`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('grant-verif-24h','verification-only','owner',?,?)`,
			issuedAt.UnixMilli(), expiresAt.UnixMilli(),
		)
		return err
	})

	// Run two passes so item-a finishes its two writer nodes and reaches the verify evaluator node,
	// while item-b has not yet been judged. Then advance the clock past grant expiry.
	l.cfg.Lanes = []string{"lane-1"}
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	afterExpiry := expiresAt.Add(2 * time.Minute)
	l.cfg.Now = func() time.Time { return afterExpiry }

	// Both item-a (mid-workflow at verify evaluator_slot) and item-b (queued with judge_mode=agent)
	// now wait as judge-grant-absent. Neither may appear as running or moving.
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID != "item-a" && p.decision.ItemID != "item-b" {
			continue
		}
		if p.decision.Action != ActionHold || p.decision.Reason != ReasonJudgeGrantAbsent {
			t.Fatalf("%s decision = %+v, want hold/%s", p.decision.ItemID, p.decision, ReasonJudgeGrantAbsent)
		}
		if p.decision.From != "held" || p.decision.Position != "held" {
			t.Errorf("%s listed as from=%q position=%q, want held", p.decision.ItemID, p.decision.From, p.decision.Position)
		}
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{
			Now:            afterExpiry.UTC().Format(time.RFC3339),
			StorePath:      f.storePath,
			Build:          &l.build,
			AvailableLanes: &l.cfg.Lanes,
		}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		if answer.Summary.Running != 0 {
			t.Errorf("summary.Running = %d, want 0 (%s)", answer.Summary.Running, answer.Summary)
		}
		if answer.Summary.Held[ReasonJudgeGrantAbsent] != 2 {
			t.Errorf("summary.Held[%s] = %d, want 2 (%s)", ReasonJudgeGrantAbsent, answer.Summary.Held[ReasonJudgeGrantAbsent], answer.Summary)
		}
		found := 0
		wantExpiry := expiresAt.UTC().Format(time.RFC3339)
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" && row.ItemID != "item-b" {
				continue
			}
			found++
			if row.Position != "held" {
				t.Errorf("%s row.Position = %q, want held", row.ItemID, row.Position)
			}
			if row.Class != "needs you" {
				t.Errorf("%s row.Class = %q, want needs you", row.ItemID, row.Class)
			}
			for _, field := range []struct {
				name, val string
			}{
				{"why", row.Why},
				{"mover", row.Mover},
			} {
				for _, want := range []string{"grant-verif-24h", wantExpiry, "tillr item grant issue --store " + f.storePath} {
					if !strings.Contains(field.val, want) {
						t.Errorf("%s %s = %q, missing %q", row.ItemID, field.name, field.val, want)
					}
				}
			}
		}
		if found != 2 {
			t.Fatalf("found %d rows for item-a/item-b, want 2: %+v", found, answer.Rows)
		}
		return nil
	})

	// When no covering grant ever existed, items still show as held and name the issue
	// command, without claiming a grant expired.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`DELETE FROM item_grants`)
		return err
	})
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{
			Now:            afterExpiry.UTC().Format(time.RFC3339),
			StorePath:      f.storePath,
			Build:          &l.build,
			AvailableLanes: &l.cfg.Lanes,
		}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" && row.ItemID != "item-b" {
				continue
			}
			if row.Position != "held" || row.Class != "needs you" {
				t.Errorf("%s without prior grant: position=%q class=%q", row.ItemID, row.Position, row.Class)
			}
			if strings.Contains(row.Why, "expired at") || strings.Contains(row.Mover, "expired at") {
				t.Errorf("%s falsely claimed expiry: why=%q mover=%q", row.ItemID, row.Why, row.Mover)
			}
			if !strings.Contains(row.Why, "tillr item grant issue --store "+f.storePath) || !strings.Contains(row.Mover, "tillr item grant issue --store "+f.storePath) {
				t.Errorf("%s missing issue command: why=%q mover=%q", row.ItemID, row.Why, row.Mover)
			}
		}
		return nil
	})

	// Issuing a new covering grant resumes item-a at its verify evaluator node without
	// re-running the completed writer nodes.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('grant-verif-renewed','verification-only','owner',?,?)`,
			afterExpiry.UnixMilli(), afterExpiry.Add(24*time.Hour).UnixMilli(),
		)
		return err
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" && p.decision.Action != ActionDispatch {
			t.Fatalf("after renewal item-a action = %s (reason %s), want dispatch", p.decision.Action, p.decision.Reason)
		}
	}
}
