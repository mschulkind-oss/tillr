package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func conflictFixture(t *testing.T, h *serveHarness) {
	t.Helper()
	h.exec(`DELETE FROM dispatches; UPDATE items SET state='blocked' WHERE item_id='item-seed-02';
 INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1000,'item.conflict','item','item-seed-02','{"conflict_id":"conflict-worker","item_id":"item-seed-02","run_id":"worker","text":"full report","status":"raised","waiting_on":"design"}'),
 (2000,'item.conflict','item','item-seed-02','{"conflict_id":"conflict-worker","item_id":"item-seed-02","run_id":"worker","text":"full report","status":"awaiting","design_run_id":"design-a","waiting_on":"design"}')`)
}

func TestWaitingKeepsConflictTransitionsWithOtherHolds(t *testing.T) {
	for _, hold := range []string{"none", "fault", "repair"} {
		t.Run(hold, func(t *testing.T) {
			h := newServeHarness(t)
			conflictFixture(t, h)
			reason := "full report"
			if hold == "fault" {
				reason = "fixture diagnosis"
				h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'drain.item-fault','item','item-seed-02','{"item_id":"item-seed-02","reason":"gate-harness-error","detail":"fixture diagnosis"}')`)
			}
			if hold == "repair" {
				reason = "fixture repair cause"
				h.exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES('repair-a','item-seed-02','lane-a','pi','finished',2500,2600,'[]',11); INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'drain.repair-held','run','repair-a','{"item_id":"item-seed-02","cause":"fixture repair cause","next_action":"retry"}')`)
			}
			status, body, raw := h.get("/api/v1/waiting")
			if status != 200 {
				t.Fatalf("waiting: %d %s", status, raw)
			}
			for _, value := range body["data"].([]any) {
				row := value.(map[string]any)
				if row["item_id"] != "item-seed-02" {
					continue
				}
				why := row["why"].(string)
				for _, want := range []string{reason, "conflict-worker", "raised", "raised → awaiting", "design run design-a"} {
					if !strings.Contains(why, want) {
						t.Errorf("waiting reason %q lacks %q", why, want)
					}
				}
				if strings.Count(why, "conflict-worker") != 1 {
					t.Errorf("not one conflict: %s", why)
				}
				return
			}
			t.Fatal("missing item")
		})
	}
}

func TestLiveWaitingKeepsConflictTransitions(t *testing.T) {
	view, drain := buildPair(t)
	dir, path := whyFixture(t, drain)
	h := &serveHarness{t: t, store: path}
	h.exec(`INSERT INTO items(item_id,title,state,rank,allowed_paths,updated_at) VALUES('item-seed-02','Conflict','in-queue','c','[]',0)`)
	conflictFixture(t, h)
	out, err := exec.Command(view, "waiting", "--store", path, "--root", dir, "--tillr-drain", drain, "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("live waiting: %v %s", err, out)
	}
	for _, want := range []string{"conflict-worker", "raised → awaiting", "design run design-a"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("live waiting lost %q: %s", want, out)
		}
	}
	srv := newServer(context.Background(), path, "", "", filepath.Join(dir, "no-tillr-trace"), dir, "", nil)
	srv.plannerFlag = drain
	defer srv.close()
	h.http = httptest.NewServer(srv.routes())
	defer h.http.Close()
	status, body, response := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, response)
	}
	data := body["data"].(map[string]any)
	for _, value := range data["rows"].([]any) {
		row := value.(map[string]any)
		if row["item_id"] != "item-seed-02" {
			continue
		}
		if !strings.Contains(row["why"].(string), "raised → awaiting") || row["reason"] == nil || row["decision"] == nil || row["class_source"] != "planner" {
			t.Fatalf("why lost transitions or planner fields: %s", response)
		}
		return
	}
	t.Fatal("why lost conflict item")
}

func TestRecordStreamConflictTransitionKeepsOriginBeforeCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	raw := `[{"record_version":1,"kind":"item.conflict","id":"first","producer":{"system":"tillr","utility":"item","version":"0.1.0"},"created_at":"2026-11-17T00:00:00Z","payload":{"conflict_id":"c","item_id":"i","run_id":"worker","text":"report","status":"raised"}},
{"record_version":1,"kind":"item.conflict","id":"second","producer":{"system":"tillr","utility":"item","version":"0.1.0"},"created_at":"2026-11-17T00:01:00Z","payload":{"conflict_id":"c","item_id":"i","run_id":"worker","text":"report","status":"awaiting","design_run_id":"design-a"}}]`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := openRecords(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.events) != 1 || v.events[0].FirstSeq != 1 || v.events[0].Transition != "raised → awaiting; design run design-a" || len(v.conflicts) != 1 || len(v.conflicts[0].Transitions) != 1 || v.conflicts[0].Transitions[0].Report != "" {
		t.Fatalf("incorrect transition after cursor: %+v %+v", v.events, v.conflicts)
	}
}

func TestEventsGroupConflictTransitionsAcrossCursor(t *testing.T) {
	h := newServeHarness(t)
	conflictFixture(t, h)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'item.conflict','item','item-seed-02','{"conflict_id":"conflict-worker","item_id":"item-seed-02","run_id":"worker-b","text":"full report plus evidence","status":"awaiting","design_run_id":"design-a","waiting_on":"design"}')`)
	// The events face carries one conflict summary, while preserving the raw stream's cursor.
	env, code := eventsFace([]string{"--store", h.store, "--since", "0"})
	if code != 0 {
		t.Fatalf("events code %d", code)
	}
	var payload struct {
		Conflicts []struct {
			ConflictID  string `json:"conflict_id"`
			Transitions []struct {
				Label string `json:"label"`
				Seq   int64  `json:"seq"`
			} `json:"transitions"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Conflicts) != 1 || len(payload.Conflicts[0].Transitions) != 3 {
		t.Fatalf("not one conflict with three transitions: %s", env.Payload)
	}
	tr := payload.Conflicts[0].Transitions
	for i, want := range []string{"raised", "raised → awaiting; design run design-a", "evidence added from run worker-b"} {
		if tr[i].Label != want {
			t.Errorf("label %q want %q", tr[i].Label, want)
		}
	}
	env, code = eventsFace([]string{"--store", h.store, "--since", strconv.FormatInt(tr[1].Seq, 10)})
	if code != 0 {
		t.Fatal(code)
	}
	if !bytes.Contains(env.Payload, []byte("evidence added from run worker-b")) {
		t.Fatalf("cursor lost previous transition: %s", env.Payload)
	}
}
