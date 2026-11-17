package drain

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestJudgeFailingDispositions(t *testing.T) {
	cases := []struct {
		name     string
		verdicts []string
		outcome  string
	}{
		{"blocked", []string{item.VerdictBlocked}, item.VerdictBlocked},
		{"defective", []string{item.VerdictDefective}, item.VerdictDefective},
		{"done-defective", []string{item.VerdictDone, item.VerdictDefective}, item.VerdictDefective},
		{"done-blocked", []string{item.VerdictDone, item.VerdictBlocked}, item.VerdictBlocked},
		{"not-done-defective", []string{item.VerdictNotDone, item.VerdictDefective}, item.VerdictDefective},
		{"blocked-defective", []string{item.VerdictBlocked, item.VerdictDefective}, item.VerdictDefective},
		{"not-done-blocked", []string{item.VerdictNotDone, item.VerdictBlocked}, item.VerdictBlocked},
	}
	for _, tc := range cases {
		// Exercise both file orders: the first failing entry need not be the controlling kind.
		for _, reverse := range []bool{false, true} {
			name := tc.name
			if reverse {
				name += "-reversed"
			}
			t.Run(name, func(t *testing.T) {
				f, l, s := judgeRouteFixture(t, "pass", true)
				trunk := runGit(t, f.root, "rev-parse", "HEAD")
				verdicts := append([]string(nil), tc.verdicts...)
				if reverse {
					for i, j := 0, len(verdicts)-1; i < j; i, j = i+1, j-1 {
						verdicts[i], verdicts[j] = verdicts[j], verdicts[i]
					}
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					if len(verdicts) > 1 {
						_, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('item-a','c2','second criterion','not-done','verification-only')`)
						return err
					}
					return nil
				})
				controlling := ""
				for i, verdict := range verdicts {
					id, text := "c1", "the first criterion of item-a"
					if i == 1 {
						id, text = "c2", "second criterion"
					}
					reason := id + " judge reason"
					s.entries = append(s.entries, item.JudgeEntry{CriterionID: id, CriterionHash: item.CriterionHash(text), Verdict: verdict, ClaimClass: item.ClaimVerification, Reason: reason, Evidence: []string{"diff:candidate.diff"}, WaitingOn: json.RawMessage(`{"waiter":"plan","reason":"nested reason does not override the entry"}`)})
					if verdict == tc.outcome {
						controlling = id
					}
				}
				result := &LoopResult{}
				for i := 0; i < 4; i++ {
					if _, err := l.pass(context.Background(), result); err != nil {
						t.Fatal(err)
					}
					drainOwned(t, l, result)
				}
				if !reflect.DeepEqual(s.order, []string{"worker", "judge"}) {
					t.Fatalf("gate or landing ran: %v", s.order)
				}
				if got := runGit(t, f.root, "rev-parse", "HEAD"); got != trunk {
					t.Errorf("trunk moved: %s -> %s", trunk, got)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					var state, reason, by, waiting string
					if err := r.QueryRow(`SELECT state,close_reason,closed_by,waiting_on FROM items WHERE item_id='item-a'`).Scan(&state, &reason, &by, &waiting); err != nil {
						return err
					}
					var n int
					if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n); err != nil {
						return err
					}
					if tc.outcome == item.VerdictDefective {
						if state != "closed" || reason != "defective: "+controlling+": "+controlling+" judge reason" || !strings.Contains(by, "-judge;") {
							t.Errorf("closure %s / %s / %s", state, reason, by)
						}
						if n != len(verdicts) {
							t.Errorf("intake recorded %d verdicts, want %d", n, len(verdicts))
						}
						for _, entry := range s.entries {
							var criterionState, payload string
							if err := r.QueryRow(`SELECT state FROM item_criteria WHERE item_id='item-a' AND criterion_id=?`, entry.CriterionID).Scan(&criterionState); err != nil {
								return err
							}
							if criterionState != entry.Verdict {
								t.Errorf("criterion %s state %s, want %s", entry.CriterionID, criterionState, entry.Verdict)
							}
							if err := r.QueryRow(`SELECT payload FROM events WHERE kind='item.verdict' AND json_extract(payload,'$.criterion_id')=?`, entry.CriterionID).Scan(&payload); err != nil {
								return err
							}
							if !strings.Contains(payload, `"actor_kind":"agent"`) || !strings.Contains(payload, s.workerHead) {
								t.Errorf("unbound intake %s", payload)
							}
						}
					} else {
						var got item.WaitingOn
						if err := json.Unmarshal([]byte(waiting), &got); err != nil {
							t.Fatal(err)
						}
						if state != StateBlocked || got.Waiter != "plan" || got.Reason != controlling+" judge reason" {
							t.Errorf("block %s / %+v", state, got)
						}
						if n != 0 {
							t.Errorf("blocked intake recorded %d verdicts", n)
						}
						answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), Build: &l.build, BriefTemplate: &l.cfg.Stage.(*LaneStager).Template}, l.cfg.Policy, &VantageQuestions{Root: f.root})
						if err != nil {
							return err
						}
						found := false
						for _, row := range answer.Rows {
							if row.ItemID == "item-a" {
								found = true
								if row.Class != "waits on work" || row.Mover != "plan" || row.Why != got.Reason {
									t.Errorf("live waiting hides judge blocker: %+v", row)
								}
							}
						}
						if !found {
							t.Error("live waiting lost blocked item")
						}
					}
					var laneState string
					if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-1'`).Scan(&laneState); err != nil {
						return err
					}
					if laneState != "idle" {
						t.Errorf("lane not freed: %s", laneState)
					}
					return nil
				})
				if s.mechanical != 0 || result.Landed != 0 {
					t.Errorf("unexpected retirement: %d / %+v", s.mechanical, result)
				}
			})
		}
	}
}
