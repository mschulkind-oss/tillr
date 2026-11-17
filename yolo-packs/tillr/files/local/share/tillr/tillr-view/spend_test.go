package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestEndedRunsRemainInItemSpendAndMissingRunMakesLowerBound(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `ALTER TABLE runs ADD COLUMN end_cause TEXT`)
	perfExec(t, h, `DELETE FROM runs`)
	perfExec(t, h, `INSERT INTO runs(run_id,item_id,lane_id,backend,state,end_cause,started_at,finished_at,usage_tokens,usage_cost_usd) VALUES
 ('ended-1','item-seed-02','lane-a','pi','exited','signal',1200,1300,1500,.03),
 ('ended-2','item-seed-02','lane-a','pi','exited','signal',1400,1500,1500,.03),
 ('missing','item-seed-02','lane-a','pi','timed-out','lease-reclaimed',1600,1700,NULL,NULL)`)
	items := performanceData(t, h, "/api/v1/perf/items")["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items: %v", items)
	}
	row := items[0].(map[string]any)
	if row["usage_tokens"] != float64(3000) || row["unrecorded_runs"] != float64(1) || row["lower_bound"] != true {
		t.Fatalf("ended/unreported spend: %v", row)
	}
	// The snapshot keeps the missing-run count but never exposes a price.
	_, body, raw := h.get("/api/v1/snapshot")
	if body["data"] == nil {
		t.Fatalf("snapshot: %s", raw)
	}
	drain, ok := body["data"].(map[string]any)["drain_summary"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot has no drain summary: %s", raw)
	}
	if drain["unreported_runs"] != float64(1) || drain["total_cost_usd"] != nil || drain["cost_unreported_runs"] != nil {
		t.Fatalf("header hides missing run: %v", drain)
	}
}

func TestPassSpendSeparatesInheritedCostAndPrintsMissingCounts(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `CREATE TABLE IF NOT EXISTS drain_pass_spend(pass_id TEXT,run_id TEXT,carried_over INTEGER,usage_tokens INTEGER,usage_cost_usd REAL,PRIMARY KEY(pass_id,run_id))`)
	perfExec(t, h, `INSERT INTO drain_pass_spend VALUES('pass-2','inherited',1,1500,.03),('pass-2','missing',1,NULL,NULL),('pass-2','new',0,100,.01)`)
	s, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var started, carried *passSpend
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		started, carried, err = readPassSpend(r, []passDecision{{passID: "pass-2"}, {passID: "pass-2"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if started == nil || carried == nil || started.CostUSD != .01 || carried.CostUSD != .03 || carried.Unreported != 1 {
		t.Fatalf("pass attribution: %+v %+v", started, carried)
	}
	var out bytes.Buffer
	if err := printPasses(&out, []passRow{{SpendStarted: started, SpendCarried: carried}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"spend started in this loop: $0.010000", "spend carried over from earlier loops: lower bound: $0.030000", "1 unreported run(s)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "ceiling") {
		t.Fatal("spend line is not measurement-only")
	}
}
