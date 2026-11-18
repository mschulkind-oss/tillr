package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBranchHeldLaneWaitingRowNamesTheHolder(t *testing.T) {
	h := newServeHarness(t)
	rows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil || len(rows) == 0 {
		t.Fatalf("fixture lanes: %v / %v", rows, err)
	}
	lane := rows[0].LaneID
	holder := "/previous-lanes/held/worktree"
	payload, _ := json.Marshal(map[string]string{
		"lane_id": lane, "reason": "lane-branch-held",
		"detail": "lane " + lane + " branch is checked out by the worktree at " + holder,
		"remedy": "remove the holding worktree (git worktree remove " + holder + "), or stage a different lane id",
	})
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (5000,'drain.lane-refused','lane','` + lane + `','` + string(payload) + `')`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("waiting: %d %s", status, raw)
	}
	assertRow := func(rows []any) {
		t.Helper()
		for _, value := range rows {
			row := value.(map[string]any)
			if row["item_id"] != "lane:"+lane {
				continue
			}
			if row["class"] != "needs you" || !strings.Contains(row["why"].(string), "lane-branch-held") || !strings.Contains(row["why"].(string), holder) || !strings.Contains(row["mover"].(string), "git worktree remove "+holder) {
				t.Fatalf("waiting row: %+v", row)
			}
			return
		}
		t.Fatalf("held lane missing from waiting: %+v", rows)
	}
	assertRow(body["data"].([]any))
	bin := filepath.Join(t.TempDir(), "tillr-view")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	cli, err := exec.Command(bin, "waiting", "--history", "--store", h.store, "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("waiting CLI: %v: %s", err, cli)
	}
	var cliRows []any
	if err := json.Unmarshal(cli, &cliRows); err != nil {
		t.Fatalf("CLI JSON: %v: %s", err, cli)
	}
	assertRow(cliRows)
}
