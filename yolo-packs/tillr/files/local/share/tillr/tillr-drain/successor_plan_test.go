package main_test

import "testing"

func TestPlanPassPrintsEveryPositionAndTransitionWithoutClockOrLanes(t *testing.T) {
	path := fixture(t, itemRecord("ready", "in-queue"), itemRecord("blocked", "blocked"), itemRecord("landed", "landed"))
	input := tickPayload(t, map[string]any{"policy_ref": policy(t, "/bin/true")})
	records := plan(t, path, input)
	if len(records) != 3 {
		t.Fatalf("records: %+v", records)
	}
	// No lanes are registered in this store, so the queued item is deferred.
	for _, one := range records {
		want := map[string]string{"ready": "queued", "blocked": "held", "landed": "landed"}[one.Payload.ItemID]
		if one.Payload.Position != want {
			t.Errorf("%s position=%s, want %s", one.Payload.ItemID, one.Payload.Position, want)
		}
		if one.Payload.Position == "" || one.Payload.Transition != "none" || one.Payload.Reason == "" {
			t.Errorf("position, absent transition, or reason missing: %+v", one.Payload)
		}
	}
}
