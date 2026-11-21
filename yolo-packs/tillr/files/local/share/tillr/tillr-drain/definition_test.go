package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func setPlannerDefinition(t *testing.T, path string, d definition.Definition) {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
}

func plannerFixtureDefinition(t *testing.T) definition.Definition {
	t.Helper()
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "findings"
	d.States = append(d.States, "ready-to-run", "confirmed", "waiting")
	d.Ready, d.Blocked = "ready-to-run", "waiting"
	d.Finished = []string{"confirmed", "closed"}
	d.Prerequisites = nil
	d.Completes = "hook"
	d.Hooks = &definition.Hooks{Argv: []string{"finding-hook"}, Names: []string{"complete-item"}}
	return d
}

func TestPlannerDefinitionLifecycle(t *testing.T) {
	path := fixture(t, itemRecordRanked("blocker", "landed", "b"), itemRecordFenced("ready", "in-queue", "a", nil, "blocker"), itemRecordRanked("old-ready", "in-queue", "c"))
	d := plannerFixtureDefinition(t)
	setPlannerDefinition(t, path, d)
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=CASE item_id WHEN 'ready' THEN 'ready-to-run' WHEN 'blocker' THEN 'confirmed' ELSE state END`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, "why", "--store", path, "--policy", policy(t, "true"), "--lanes", "lane-1", "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var answer drain.WhyAnswer
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	got := map[string]drain.Decision{}
	for _, row := range answer.Rows {
		got[row.ItemID] = row.Decision
	}
	if got["ready"].Action != drain.ActionDispatch {
		t.Fatalf("no prerequisites, finished blocker: %s", out)
	}
	if got["old-ready"].Reason != drain.ReasonNotReady {
		t.Fatalf("old ready state dispatched: %s", out)
	}
	// A Git terminal state is not a finished state of this definition.
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='blocker'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out = invoke(t, "why", "ready", "--store", path, "--policy", policy(t, "true"), "--lanes", "lane-1", "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var row drain.WhyRow
	if err := json.Unmarshal(out, &row); err != nil {
		t.Fatal(err)
	}
	if row.Reason != drain.ReasonBlocked || strings.Join(row.WaitingOn, ",") != "blocker" {
		t.Fatalf("unfinished dependency: %s", out)
	}
}

func TestWhyLegacyDefinitionReasonsUnchanged(t *testing.T) {
	path := fixture(t, itemRecordRanked("ready", "in-queue", "a"), itemRecordRanked("blocked", "in-queue", "b", "ready"), itemRecordFenced("empty", "in-queue", "c", nil))
	args := []string{"why", "--store", path, "--policy", policy(t, "true"), "--lanes", "lane-1", "--now", "2026-11-16T00:00:00Z"}
	code, out := invoke(t, args...)
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	rows := string(out)
	rows = strings.TrimPrefix(rows, "definition: "+definition.GitLandingName+"\n")
	want := "1\tready\tqueued\tready\tthe next pass\n2\tblocked\theld\tblocked\tdrain; ready\n3\tempty\tqueued\tno-lane\tdrain; lane-1\n"
	if !strings.HasPrefix(rows, want) {
		t.Fatalf("legacy rows: %q", rows)
	}
	// Actor labels and the owner-waiting summary intentionally changed. The
	// item's identity, position and hold reason must still match the old binary.
	coreRows := func(text string) string {
		var core []string
		for _, line := range strings.Split(text, "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) >= 4 {
				core = append(core, strings.Join(fields[:4], "\t"))
			}
		}
		return strings.Join(core, "\n")
	}
	if before := os.Getenv("TILLR_DRAIN_BEFORE_BINARY"); before != "" {
		cmd := exec.Command(before, args...)
		cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "before-state"))
		old, err := cmd.CombinedOutput()
		if err != nil || coreRows(string(old)) != coreRows(rows) {
			t.Fatalf("pre-change binary rows differ: err=%v before=%q after=%q", err, old, rows)
		}
	}
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	setPlannerDefinition(t, path, d)
	code, after := invoke(t, args...)
	if code != 0 || string(after) != string(out) {
		t.Fatalf("explicit default changed rows: %d %s", code, after)
	}
}

func TestWhyDefinitionHeader(t *testing.T) {
	path := fixture(t, itemRecord("a", "in-queue"))
	d := plannerFixtureDefinition(t)
	setPlannerDefinition(t, path, d)
	code, out := invoke(t, "why", "--store", path)
	if code != 0 || !strings.HasPrefix(string(out), "definition: findings\n") {
		t.Fatalf("header: %d %s", code, out)
	}
}
