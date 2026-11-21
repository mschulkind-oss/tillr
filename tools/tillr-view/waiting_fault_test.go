package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestWaitingDefaultShowsTheStageFaultClearCommand(t *testing.T) {
	viewBin, drainBin := buildPair(t)
	dir, path := whyFixture(t, drainBin)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	event, err := db.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'drain.item-fault','item','a','{"item_id":"a","reason":"stage-failed","detail":"fixture staging fault"}')`)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := event.LastInsertId()
	if err != nil || seq <= 0 {
		t.Fatalf("fault sequence: %d, %v", seq, err)
	}
	want := fmt.Sprintf("tillr drain clear-fault --store '%s' --item 'a' --fault %d", path, seq)
	args := []string{"waiting", "--store", path, "--root", dir, "--tillr-drain", drainBin}
	out, err := exec.Command(viewBin, args...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), want) {
		t.Fatalf("waiting table missing %q: %v\n%s", want, err, out)
	}
	out, err = exec.Command(viewBin, append(args, "--json")...).CombinedOutput()
	if err != nil {
		t.Fatalf("waiting JSON: %v\n%s", err, out)
	}
	var rows []waitingRow
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ItemID == "a" {
			if !strings.HasSuffix(row.Mover, want) || !strings.Contains(row.Why, "fixture staging fault") {
				t.Fatalf("waiting row = %+v, want fault detail and mover ending in %q", row, want)
			}
			return
		}
	}
	t.Fatalf("waiting JSON has no held row for a: %s", out)
}

func TestWaitingFaultHasEvidenceAndOneClearCommand(t *testing.T) {
	for _, reason := range []string{"gate-stalled", "gate-harness-error", "host-boundary-violation", "candidate-record-mismatch", "definition-sync-failed"} {
		t.Run(reason, func(t *testing.T) {
			h := newServeHarness(t)
			h.exec(`DELETE FROM dispatches`)
			h.exec(fmt.Sprintf(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'drain.item-fault','item','item-seed-02','{"item_id":"item-seed-02","run_id":"run-fault","lane_id":"lane-a","reason":"%s","detail":"fixture diagnosis"}')`, reason))
			status, body, raw := h.get("/api/v1/waiting")
			if status != 200 {
				t.Fatalf("waiting %d %s", status, raw)
			}
			var held map[string]any
			for _, v := range body["data"].([]any) {
				row := v.(map[string]any)
				if row["item_id"] == "item-seed-02" {
					held = row
				}
			}
			if held == nil || held["class"] != "needs you" {
				t.Fatalf("fault not shown: %s", raw)
			}
			why := held["why"].(string)
			mover := held["mover"].(string)
			for _, want := range []string{reason, "fixture diagnosis", "run-fault", "lane-a"} {
				if !strings.Contains(why, want) {
					t.Errorf("why %q missing %q", why, want)
				}
			}
			if !strings.HasPrefix(mover, "operator; tillr drain clear-fault --store ") || !strings.Contains(mover, "--item 'item-seed-02' --fault ") {
				t.Fatalf("no executable recovery: %q", mover)
			}
			h.exec(fmt.Sprintf(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(4000,'drain.item-fault-cleared','item','item-seed-02','{"reason":"%s"}')`, reason))
			status, _, raw = h.get("/api/v1/waiting")
			if status != 200 || strings.Contains(string(raw), "fixture diagnosis") {
				t.Fatalf("cleared fault still waiting: %d %s", status, raw)
			}
		})
	}
}

func TestWaitingReadsHistoricalBoundaryConvictionWithoutANewPass(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'boundary.check','run','old-run','{"clean":false,"runs":[{"item_id":"item-seed-02","run_id":"old-run","lane_id":"lane-old"}],"first_violation":{"kind":"scope","detail":"escaped.txt"}}')`)
	status, _, raw := h.get("/api/v1/waiting")
	for _, want := range []string{"host-boundary-violation", "old-run", "lane-old", "escaped.txt", "clear-fault"} {
		if status != 200 || !strings.Contains(string(raw), want) {
			t.Fatalf("historical fault missing %s: %d %s", want, status, raw)
		}
	}
}
