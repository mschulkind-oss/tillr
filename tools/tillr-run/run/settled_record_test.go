package run

import "testing"

func TestSettledRecordRequiresItsObservations(t *testing.T) {
	code := -1
	valid := Record{RunID: "run", LaneID: "lane", Backend: "fixture", State: StateExited, ExitCode: &code, EndCause: EndSettledIdle, SettledAt: "2026-11-17T00:00:01Z", HarnessEndedAt: "2026-11-17T00:00:06Z"}
	if err := checkRecord(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Record){
		func(r *Record) { r.SettledAt = "" },
		func(r *Record) { r.HarnessEndedAt = "not-a-time" },
		func(r *Record) { r.State = StatePaused; r.ExitCode = nil },
		func(r *Record) { r.EndCause = EndNormalExit },
	} {
		r := valid
		change(&r)
		if checkRecord(r) == nil {
			t.Fatalf("accepted malformed settled record: %+v", r)
		}
	}
}
