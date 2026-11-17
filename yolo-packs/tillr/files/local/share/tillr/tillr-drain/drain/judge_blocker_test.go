package drain

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestJudgeMalformedBlockerDoesNotChangeDisposition(t *testing.T) {
	for _, payload := range []string{"", "null", `"owner"`, `{}`, `{"waiter":"nobody"}`, `{"waiter":"item:missing"}`} {
		t.Run(payload, func(t *testing.T) {
			f, l, s := judgeRouteFixture(t, "pass", true)
			s.entries = []item.JudgeEntry{{CriterionID: "c1", CriterionHash: item.CriterionHash("the first criterion of item-a"), Verdict: item.VerdictBlocked, ClaimClass: item.ClaimVerification, Reason: "needs a ruling", Evidence: []string{"diff:candidate.diff"}, WaitingOn: json.RawMessage(payload)}}
			result := &LoopResult{}
			for i := 0; i < 4; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if !reflect.DeepEqual(s.order, []string{"worker", "judge"}) {
				t.Fatalf("order %v", s.order)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var state, waiting string
				if err := r.QueryRow(`SELECT state,waiting_on FROM items WHERE item_id='item-a'`).Scan(&state, &waiting); err != nil {
					return err
				}
				if state != StateInQueue || waiting != "" {
					t.Errorf("malformed block changed item: %s / %s", state, waiting)
				}
				var n int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Errorf("malformed block wrote %d verdicts", n)
				}
				return nil
			})
			if result.RefusedSteps[ReasonJudgeUnreadable] != 1 {
				t.Errorf("unreadable hold missing: %+v", result)
			}
		})
	}
}
