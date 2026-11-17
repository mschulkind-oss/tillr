package main_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func spendStopFixture(t *testing.T) (loopFixture, string) {
	t.Helper()
	markers := scratch(t, "spend-stop")
	t.Setenv("SPEND_STOP", markers)
	adapter := stub(t, "spend-worker", counting+`printf 'tokens: 1500\ncost usd: 0.030000\n---\n'
echo ready > "$SPEND_STOP/$TILLR_RUN_ID.ready"
while [ ! -f "$SPEND_STOP/release" ]; do sleep 0.01; done
`+committingAdapterBody+`printf 'RESULT: done\n'`)
	spendBackends(t, adapter, "")
	writeFile(t, filepath.Join(filepath.Dir(adapter), "extract-report"), "#!/bin/sh\nfor a in \"$@\"; do [ \"$prev\" != --out ] || out=$a; prev=$a; log=$a; done\ncp \"$log\" \"$out\"\n")
	// writeFile retains the extractor's executable mode.
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "release"), "finish") })
	return f, markers
}

func waitForSpendRuns(t *testing.T, f loopFixture, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(runsForItem(t, f.store, "item_a")) < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(runsForItem(t, f.store, "item_a")) != n {
		t.Fatal("worker did not record its end")
	}
}

func storedSpendRun(t *testing.T, f loopFixture, id string) run.Record {
	t.Helper()
	raw, err := exec.Command(sibling(t, "run"), "status", "--store", f.store, "--run", id).Output()
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Payload struct {
			Run run.Record `json:"run"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatal(err)
	}
	return answer.Payload.Run
}

func TestStoppedLoopLeavesSpendToFinishingWorkerAndReplacementCarry(t *testing.T) {
	f, markers := spendStopFixture(t)
	cmd, out := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "run-item_a-a1.ready"), 10*time.Second)
	stopFixture(t, f, cmd, out, false)
	if len(terminalRunRows(t, f.store)) != 0 || len(matchingPIDs(t, f.store)) != 1 {
		t.Fatal("ordinary stop signaled the worker")
	}
	writeFile(t, filepath.Join(markers, "release"), "finish")
	waitForSpendRuns(t, f, 1)
	rec := storedSpendRun(t, f, "run-item_a-a1")
	if rec.Usage == nil || rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != .03 {
		t.Fatalf("surviving worker lost spend: %+v", rec)
	}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	result, err := f.newLoop(t, 1, steps).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.StartedSpend.CostUSD != 0 || result.CarriedSpend.CostUSD != .03 || result.RecordedSpend.CostUSD != .03 || result.Dispatched != 0 {
		t.Fatalf("inherited spend attributed to new work: %+v", result)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var carried bool
		var cost float64
		if err := r.QueryRow(`SELECT carried_over,usage_cost_usd FROM drain_pass_spend WHERE run_id=?`, rec.RunID).Scan(&carried, &cost); err != nil {
			return err
		}
		if !carried || cost != .03 {
			t.Fatalf("pass charged inherited run as new: %v %f", carried, cost)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body := readJSONFile(t, drain.LoopResultFile(f.root, result.FirstGeneration, result.LastGeneration))
	payload := body["payload"].(map[string]any)
	if payload["spend_carried_over"].(map[string]any)["cost_usd"] != .03 || payload["spend_started_in_loop"].(map[string]any)["cost_usd"] != float64(0) {
		t.Fatalf("record lost spend origin: %v", payload)
	}
}

func TestTwoExplicitEndsRemainInNextLoopSpend(t *testing.T) {
	f, markers := spendStopFixture(t)
	for attempt := 1; attempt <= 2; attempt++ {
		cmd, out := startStopFixture(t, f, 1)
		id := "run-item_a-a" + fmtInt(attempt)
		waitForFile(t, filepath.Join(markers, id+".ready"), 10*time.Second)
		stopFixture(t, f, cmd, out, true)
		waitForSpendRuns(t, f, attempt)
		rec := storedSpendRun(t, f, id)
		if rec.Usage == nil || rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != .03 {
			t.Fatalf("explicit end lost spend: %+v", rec)
		}
	}
	cmd, out := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "run-item_a-a3.ready"), 10*time.Second)
	stopFixture(t, f, cmd, out, false)
	if !strings.Contains(out.String(), "spend all recorded runs: $0.060000, 3000 tokens") || strings.Contains(out.String(), "ceiling") {
		t.Fatalf("next loop hides ended runs: %s", out.String())
	}
	writeFile(t, filepath.Join(markers, "release"), "finish")
	waitForSpendRuns(t, f, 3)
}
