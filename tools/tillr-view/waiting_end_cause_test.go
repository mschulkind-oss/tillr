package main

import (
	"strings"
	"testing"
)

func TestWaitingRunOrderUsesRecordedChronology(t *testing.T) {
	older := storeRun{RunID: "run-a9", StartedAtMS: 1000, FinishedAtMS: 2000, EventSeq: 9}
	for _, newer := range []storeRun{
		{RunID: "run-a10", StartedAtMS: 1001, FinishedAtMS: 1001, EventSeq: 1},
		{RunID: "run-a10", StartedAtMS: 1000, FinishedAtMS: 2001, EventSeq: 1},
		{RunID: "run-a10", StartedAtMS: 1000, FinishedAtMS: 2000, EventSeq: 10},
	} {
		if !waitingRunLater(newer, older) || waitingRunLater(older, newer) {
			t.Errorf("chronological order lost: newer %+v, older %+v", newer, older)
		}
	}
}

func TestWaitingHeldLastRunEndCause(t *testing.T) {
	for _, tc := range []struct{ cause, label string }{
		{"normal-exit", "normal exit"},
		{"time-limit", "time limit"},
		{"lease-reclaimed", "lease reclaimed"},
		{"signal", "signal"},
		{"failed-to-start", "failure to start"},
		{"", "end cause not recorded"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h := newServeHarness(t)
			h.exec(`ALTER TABLE runs ADD COLUMN end_cause TEXT`)
			h.exec(`DELETE FROM dispatches`)
			h.exec(`INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at) VALUES ('held','item-seed-02','hold','run-unjudged',9000)`)
			h.exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq,end_cause) VALUES
    ('older','item-seed-02','lane-b','pi','exited',1000,2000,'[]',10,'normal-exit'),
    ('run-a9','item-seed-02','lane-b','pi','exited',3000,4000,'[]',11,'normal-exit'),
    ('run-a10','item-seed-02','lane-b','pi','exited',3000,4000,'[]',12,` + "'" + tc.cause + "'" + `)`)
			check := func(label string) {
				t.Helper()
				status, body, raw := h.get("/api/v1/waiting")
				if status != 200 {
					t.Fatalf("status %d: %s", status, raw)
				}
				rows := body["data"].([]any)
				for _, value := range rows {
					row := value.(map[string]any)
					if row["item_id"] == "item-seed-02" {
						if mover, _ := row["mover"].(string); !strings.Contains(mover, label) {
							t.Errorf("mover %q must show %q", mover, label)
						}
						return
					}
				}
				t.Fatal("held item missing")
			}
			check(tc.label)
			// Neither a missing column nor NULL may be inferred from state/exit code.
			h.exec(`UPDATE runs SET end_cause = NULL WHERE run_id = 'run-a10'`)
			check("end cause not recorded")
			h.exec(`ALTER TABLE runs DROP COLUMN end_cause`)
			check("end cause not recorded")
		})
	}
}
