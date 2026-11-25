package drain

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyNamesARefusedLaneAndTheExaminationHoldingTheOther(t *testing.T) {
	f, l := passFixture(t, "queued")
	ctx := context.Background()
	at := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id) VALUES ('lane-1','leased','examining-run'), ('lane-4','idle',NULL)`)
		return err
	})
	const detail = "lane lane-4 holds uncommitted work (M file.go)"
	if err := appendLaneEvent(ctx, f.storePath, laneRefusedKind, "lane-4", laneRefusal{LaneID: "lane-4", Reason: ReasonLaneUncommittedWork, Detail: detail}, at); err != nil {
		t.Fatal(err)
	}
	lanes := []string{"lane-1", "lane-4"}
	read := func() WhyAnswer {
		t.Helper()
		var answer WhyAnswer
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			answer, err = Why(r, Pass{AvailableLanes: &lanes, Now: at.Format(time.RFC3339Nano)}, l.cfg.Policy, nil)
			return err
		})
		if len(answer.Rows) != 1 {
			t.Fatalf("want one queue row, got %+v", answer.Rows)
		}
		return answer
	}
	answer := read()
	row := answer.Rows[0]
	if !slices.Equal(answer.Lanes, []string{"lane-1"}) || row.Reason != ReasonAgentLimit || row.TableReason() == ReasonAgentLimit ||
		!strings.Contains(row.Why, "out of rotation") || !strings.Contains(row.Why, detail) || !strings.Contains(row.Mover, "examining-run") {
		t.Fatalf("want a hold naming both the refusal and the holder, got %+v with lanes %v", row, answer.Lanes)
	}
	if err := appendLaneEvent(ctx, f.storePath, laneRestoredKind, "lane-4", struct{}{}, at); err != nil {
		t.Fatal(err)
	}
	answer = read()
	row = answer.Rows[0]
	if !slices.Equal(answer.Lanes, lanes) || row.Decision.Action != ActionDispatch || row.Decision.LaneID != "lane-4" {
		t.Fatalf("want the restored lane back in the plan, got %+v with lanes %v", row, answer.Lanes)
	}
}
