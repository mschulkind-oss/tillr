package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// Exercise the actual correction face without importing a writer's schema into
// the reader's fixture. Its process completes before any view read transaction.
func copiedUsageBinary(t *testing.T, h *serveHarness) string {
	t.Helper()
	bin := filepath.Join(h.dir, "tillr-run")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../tillr-run"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build run face: %v\n%s", err, out)
	}
	return bin
}

func copiedUsageCommand(t *testing.T, bin, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"copied-usage", "--store", path}, args...)...)
	cmd.Env = append(os.Environ(), "TILLR_SQLITE_TRACE=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copied usage: %v\n%s", err, out)
	} else {
		return string(out)
	}
	return ""
}

func fixturePassSpend(t *testing.T, h *serveHarness) *passSpend {
	t.Helper()
	s, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var started *passSpend
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		started, _, err = readPassSpend(r, []passDecision{{passID: "copied-pass"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return started
}

func seedCopiedView(t *testing.T, h *serveHarness, copied bool) {
	t.Helper()
	perfExec(t, h, `DELETE FROM runs`)
	perfExec(t, h, `INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_tokens,usage_cost_usd) VALUES
  ('original','item-seed-02','lane-a','pi','exited',1000,1100,1500,.03),
  ('copy','item-seed-02','lane-a','pi','exited',2000,2100,1500,.03),
  ('different','item-seed-04','lane-a','pi','exited',3000,3100,2000,.04)`)
	if !copied {
		perfExec(t, h, `UPDATE runs SET usage_tokens=1600,usage_cost_usd=.05 WHERE run_id='copy'`)
	}
	perfExec(t, h, `CREATE TABLE drain_pass_spend(pass_id TEXT,run_id TEXT,carried_over INTEGER,usage_tokens INTEGER,usage_cost_usd REAL,PRIMARY KEY(pass_id,run_id))`)
	perfExec(t, h, `INSERT INTO drain_pass_spend SELECT 'copied-pass',run_id,0,usage_tokens,usage_cost_usd FROM runs`)
}

func TestCopiedUsageCorrectionLowersItemAndObservatoryTotals(t *testing.T) {
	h := newServeHarness(t)
	seedCopiedView(t, h, true)
	bin := copiedUsageBinary(t, h)
	items := performanceData(t, h, "/api/v1/perf/items")["items"].([]any)
	first := items[0].(map[string]any)
	// HTTP reports recorded tokens, not prices; the correction has not yet
	// invalidated the copied measurement, so this total is not a lower bound.
	if first["item_id"] != "item-seed-02" || first["cost_usd"] != nil || first["usage_tokens"] != float64(3000) || first["lower_bound"] != false {
		t.Fatalf("before: %v", items)
	}
	beforePass := fixturePassSpend(t, h)
	if beforePass.CostUSD != .10 || beforePass.Tokens != 5000 || beforePass.Unreported != 0 {
		t.Fatalf("pass before: %+v", beforePass)
	}
	copiedUsageCommand(t, bin, h.store, "--apply", "--reason", "preceding report reused")
	items = performanceData(t, h, "/api/v1/perf/items")["items"].([]any)
	first = items[0].(map[string]any)
	if first["cost_usd"] != nil || first["usage_tokens"] != float64(1500) || first["lower_bound"] != true || first["unrecorded_runs"] != float64(1) || first["run_count"] != float64(2) {
		t.Fatalf("after: %v", items)
	}
	_, body, raw := h.get("/api/v1/snapshot")
	drain := body["data"].(map[string]any)["drain_summary"].(map[string]any)
	if drain["total_cost_usd"] != nil || drain["unreported_runs"] != float64(1) || drain["cost_unreported_runs"] != nil {
		t.Fatalf("observatory after: %s", raw)
	}
	afterPass := fixturePassSpend(t, h)
	if afterPass.CostUSD != .07 || afterPass.Tokens != 3500 || afterPass.Runs != 3 || afterPass.Unreported != 1 || afterPass.TokensUnreported != 1 || afterPass.CostUnreported != 1 {
		t.Fatalf("pass after: %+v", afterPass)
	}
}

func TestCopiedUsagePassCorrectionMatchesOnlyInvalidatedPair(t *testing.T) {
	h := newServeHarness(t)
	seedCopiedView(t, h, true)
	bin := copiedUsageBinary(t, h)
	perfExec(t, h, `UPDATE drain_pass_spend SET carried_over=1 WHERE run_id='copy'`)
	// The same run can have a different historical measurement from a prior span.
	perfExec(t, h, `INSERT INTO drain_pass_spend VALUES('copied-pass','unmatched',0,900,.02)`)
	copiedUsageCommand(t, bin, h.store, "--apply", "--reason", "preceding report reused")
	// A later measurement on the corrected run must not restore its copied snapshot
	// or replace the unaffected snapshots with the latest measurement.
	perfExec(t, h, `UPDATE runs SET usage_tokens=777,usage_cost_usd=.07 WHERE run_id='copy'`)
	perfExec(t, h, `INSERT INTO drain_pass_spend VALUES('other-pass','copy',0,700,.01)`)
	s, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		started, carried, err := readPassSpend(r, []passDecision{{passID: "copied-pass"}, {passID: "other-pass"}})
		if err != nil {
			return err
		}
		if carried == nil || carried.Runs != 1 || carried.Unreported != 1 || carried.CostUSD != 0 || carried.Tokens != 0 {
			t.Fatalf("copied inherited snapshot: %+v", carried)
		}
		if started == nil || started.Runs != 4 || started.Unreported != 0 || started.Tokens != 5100 || started.CostUSD < .099999 || started.CostUSD > .100001 {
			t.Fatalf("unaffected historical snapshots: %+v", started)
		}
		var tokens int64
		var cost float64
		if err := r.QueryRow(`SELECT usage_tokens,usage_cost_usd FROM drain_pass_spend WHERE pass_id='copied-pass' AND run_id='copy'`).Scan(&tokens, &cost); err != nil {
			return err
		}
		if tokens != 1500 || cost != .03 {
			t.Fatal("historical snapshot was rewritten")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCopiedUsageAbsentLeavesEverySpendViewUnchanged(t *testing.T) {
	h := newServeHarness(t)
	seedCopiedView(t, h, false)
	bin := copiedUsageBinary(t, h)
	beforeItems := performanceData(t, h, "/api/v1/perf/items")
	_, beforeBody, _ := h.get("/api/v1/snapshot")
	beforeDrain := beforeBody["data"].(map[string]any)["drain_summary"]
	beforePass := fixturePassSpend(t, h)
	r, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	beforeEvents, err := r.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := copiedUsageCommand(t, bin, h.store); got != "[]\n" {
		t.Fatalf("detector: %q", got)
	}
	if got := copiedUsageCommand(t, bin, h.store, "--apply", "--reason", "check"); got != "[]\n" {
		t.Fatalf("empty correction: %q", got)
	}
	afterItems := performanceData(t, h, "/api/v1/perf/items")
	_, afterBody, _ := h.get("/api/v1/snapshot")
	afterDrain := afterBody["data"].(map[string]any)["drain_summary"]
	afterEvents, err := r.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeItems, afterItems) || !reflect.DeepEqual(beforeDrain, afterDrain) || !reflect.DeepEqual(beforePass, fixturePassSpend(t, h)) || !reflect.DeepEqual(beforeEvents, afterEvents) {
		t.Fatal("empty correction changed spend views or events")
	}
}
