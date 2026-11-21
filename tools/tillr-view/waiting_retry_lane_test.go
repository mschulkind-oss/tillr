package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitingShowsOperatorRetryLaneAndHolder(t *testing.T) {
	_, drainBin := buildPair(t)
	dir, path := whyFixture(t, drainBin)
	policy := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policy, []byte(`{"version":1,"backends":{"pi":{"adapter":"pi","model":"m"}},"seats":{"build":["pi"]},"default_backend":"pi"}`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// An examination lease holds the sole lane. Item b's old run exited
	// normally, and nothing has been dispatched since its retry was recorded.
	if _, err := db.Exec(`DELETE FROM item_edges WHERE item_id='b';
UPDATE lanes SET state='leased',run_id='examined-run' WHERE lane_id='lane-1';
INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,event_seq,end_cause)
VALUES ('old-run','b','lane-1','pi','exited',0,1000,2000,'[]',0,'normal-exit');
INSERT INTO retries(retry_id,item_id,supersedes_run_id,requested_by,reason,requested_at)
VALUES ('retry-b','b','old-run','operator','fence widened',3000)`); err != nil {
		t.Fatal(err)
	}

	// The Waiting UI reads /api/v1/why, not the recorded-decisions endpoint.
	srv := newServer(context.Background(), path, "", "", filepath.Join(dir, "no-tillr-trace"), dir, "", nil)
	defer srv.close()
	srv.plannerFlag = drainBin
	srv.policyRef = policy
	srv.offeredLanes = "lane-1"
	httpServer := httptest.NewServer(srv.routes())
	defer httpServer.Close()
	h := &serveHarness{t: t, http: httpServer}
	status, _, raw := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, raw)
	}
	var answer struct {
		Data struct {
			Rows []struct {
				waitingRow
				Reason string `json:"reason"`
			} `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatal(err)
	}
	for _, row := range answer.Data.Rows {
		if row.ItemID != "b" {
			continue
		}
		const holder = "lane-1 held by active lease for examined-run"
		if !strings.Contains(row.Why, "operator retry waiting for a lane: "+holder) || !strings.Contains(row.Mover, holder) {
			t.Errorf("Waiting lost the retry explanation or its mover: %+v", row)
		}
		if strings.Contains(row.Reason+row.Why+row.Mover, "harness-refused") {
			t.Errorf("Waiting reported a refusal for an undispatched operator retry: %+v", row)
		}
		return
	}
	t.Fatalf("retry item missing from Waiting: %s", raw)
}

func TestWaitingResolvesHistoricalHarnessRefusedDecision(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES ('legacy-retry','legacy retry','in-queue',1);
INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at)
VALUES ('old-decision','legacy-retry','retry','harness-refused',1000)`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ItemID == "legacy-retry" {
			if row.Why != "harness-refused" || row.Class != "ready" || row.Where != "queue" || row.Mover != "the next pass" {
				t.Fatalf("legacy retry decision did not resolve to its historical rule: %+v", row)
			}
			return
		}
	}
	t.Fatal("legacy retry decision missing from history")
}
