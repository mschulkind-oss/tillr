package drain

import (
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A lane a returned worker's session sits on is reserved for that worker's next turn. On
// 2026-11-19 two such reservations (lane-5 and lane-6) were kept for items that were running
// on other lanes or were being retried without their session, while 16 schedulable items
// were held as no-lane. A reservation whose holder does not start this pass must not keep a
// schedulable item off the lane.
func TestAnUnstartableReturnedWorkerDoesNotKeepItsLane(t *testing.T) {
	f, l, _, _ := returnedSessionFixture(t, RefusalGate)
	db, err := store.Open(t.Context(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, db, "item-earlier")
	db.Close()
	// item-a, whose session the only lane retains, now waits on item-earlier, so it cannot
	// start this pass whatever lane it is offered.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank='0' WHERE item_id='item-earlier'`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_edges(item_id,blocked_by) VALUES('item-a','item-earlier')`)
		return err
	})
	seen := 0
	for _, p := range mustPlan(t, l) {
		d := p.decision
		switch d.ItemID {
		case "item-a":
			seen++
			if d.Action != ActionHold || d.Reason != ReasonBlocked {
				t.Fatalf("the blocked returned worker was not held as blocked: %+v", d)
			}
		case "item-earlier":
			seen++
			if d.Action != ActionDispatch || d.LaneID != "lane-1" {
				t.Fatalf("a schedulable item was kept off the lane an unstartable worker reserved: %s/%s on %q", d.Action, d.Reason, d.LaneID)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("planned %d of the two items", seen)
	}
}

// The loop's own staging refusal holds an item for the rest of the process. Applied after the
// plan, that hold left the lane the planner had already given the item unused for the pass,
// and the next item in order was held as no-lane. The planner must hold the item before it
// assigns lanes.
func TestALoopHeldItemDoesNotTakeTheLaneFromTheNextItem(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	_ = f
	l.cfg.Lanes = []string{"lane-1"}
	l.held["item-a"] = heldItem{reason: ReasonItemUnstagleable, at: l.cfg.Now().Add(time.Hour)}
	seen := 0
	for _, p := range mustPlan(t, l) {
		d := p.decision
		switch d.ItemID {
		case "item-a":
			seen++
			if d.Action != ActionHold || d.Reason != ReasonItemUnstagleable || d.LaneID != "" {
				t.Fatalf("the loop-held item was not held off every lane: %+v", d)
			}
		case "item-b":
			seen++
			if d.Action != ActionDispatch || d.LaneID != "lane-1" {
				t.Fatalf("the next item did not get the lane a loop-held item could not use: %s/%s on %q", d.Action, d.Reason, d.LaneID)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("planned %d of the two items", seen)
	}
}
