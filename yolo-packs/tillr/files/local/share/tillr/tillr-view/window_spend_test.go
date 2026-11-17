package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWaitingMeasuresLast24HoursAndMissingCosts(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM runs`)
	now := time.Now()
	for _, row := range []struct {
		id   string
		at   time.Time
		cost string
	}{
		{"recent", now.Add(-time.Hour), "41.2"},
		{"old", now.Add(-25 * time.Hour), "900"},
		{"retry-with-no-output", now.Add(-2 * time.Hour), "NULL"},
	} {
		h.exec(fmt.Sprintf(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_cost_usd) VALUES('%s', 'item-seed-02', 'lane-a', 'pi', 'exited', %d, %d, %s)`, row.id, row.at.UnixMilli(), row.at.UnixMilli(), row.cost))
	}
	bin := filepath.Join(h.dir, "tillr-view")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	out, err := exec.Command(bin, "waiting", "--history", "--store", h.store).CombinedOutput()
	want := "spend, last 24h: $41.20 over 2 runs; 1 run reported no cost"
	if err != nil || !strings.Contains(string(out), want) {
		t.Fatalf("waiting %v, missing %q:\n%s", err, want, out)
	}
}
