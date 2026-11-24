package drain_test

import (
	"context"
	"encoding/json"
	"os"
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
	for {
		if got := len(runsForItem(t, f.store, "item_a")); got >= n {
			if got != n {
				t.Fatalf("recorded %d runs, want %d", got, n)
			}
			return
		}
		if len(matchingPIDs(t, f.store)) == 0 {
			// Re-check once after observing no live worker process in case the
			// worker committed its terminal row and exited between the two reads.
			if got := len(runsForItem(t, f.store, "item_a")); got == n {
				return
			} else if got > n {
				t.Fatalf("recorded %d runs, want %d", got, n)
			}
			t.Fatal("worker did not record its end")
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("worker did not record its end before test cancellation: %v", t.Context().Err())
		case <-time.After(25 * time.Millisecond):
		}
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
	waitForFile(t, filepath.Join(markers, "run-item_a-a1.ready"), hangGuard)
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
	if result.StartedSpend.CostUSD != 0 || result.CarriedSpend.CostUSD != .03 || result.Dispatched != 0 {
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
	if _, ok := payload["spend_all_recorded_runs"]; ok {
		t.Fatalf("exit record included a historical spend total: %v", payload)
	}
}

func TestTwoExplicitEndsRemainInNextLoopSpend(t *testing.T) {
	f, markers := spendStopFixture(t)
	for attempt := 1; attempt <= 2; attempt++ {
		cmd, out := startStopFixture(t, f, 1)
		id := "run-item_a-a" + fmtInt(attempt)
		waitForFile(t, filepath.Join(markers, id+".ready"), hangGuard)
		stopFixture(t, f, cmd, out, true)
		waitForSpendRuns(t, f, attempt)
		rec := storedSpendRun(t, f, id)
		if rec.Usage == nil || rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != .03 {
			t.Fatalf("explicit end lost spend: %+v", rec)
		}
	}
	cmd, out := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "run-item_a-a3.ready"), hangGuard)
	stopFixture(t, f, cmd, out, false)
	if !strings.Contains(out.String(), "loop: spend, last 24h: $0.06 over 2 runs") {
		t.Fatalf("startup spend hides ended runs: %s", out.String())
	}
	if !strings.Contains(out.String(), "loop: spend carried over from earlier loops:") ||
		strings.Contains(out.String(), "spend all recorded runs:") {
		t.Fatalf("expected a loop-only exit spend report: %s", out.String())
	}
	writeFile(t, filepath.Join(markers, "release"), "finish")
	waitForSpendRuns(t, f, 3)
}

func TestWaitForSpendRunsWaitsForDelayedWorkerEnd(t *testing.T) {
	f, markers := spendStopFixture(t)
	extractor := filepath.Join(filepath.Dir(f.policy.Backends["fixture"].Adapter), "extract-report")
	writeFile(t, extractor, `#!/bin/sh
echo extracting > "$SPEND_STOP/extracting"
while [ ! -f "$SPEND_STOP/allow-record" ]; do sleep 0.01; done
for a in "$@"; do [ "$prev" != --out ] || out=$a; prev=$a; log=$a; done
cp "$log" "$out"
`)
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "allow-record"), "finish") })

	cmd, out := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "run-item_a-a1.ready"), hangGuard)
	stopFixture(t, f, cmd, out, true)
	waitForFile(t, filepath.Join(markers, "extracting"), hangGuard)
	if n := len(runsForItem(t, f.store, "item_a")); n != 0 {
		t.Fatalf("worker recorded its end before release: %d", n)
	}
	const injectedDelay = 300 * time.Millisecond
	type releaseResult struct {
		at  time.Time
		err error
	}
	released := make(chan releaseResult, 1)
	go func() {
		time.Sleep(injectedDelay)
		at := time.Now()
		err := os.WriteFile(filepath.Join(markers, "allow-record"), []byte("finish"), 0o600)
		released <- releaseResult{at: at, err: err}
	}()
	waitForSpendRuns(t, f, 1)
	res := <-released
	if res.err != nil {
		t.Fatalf("releasing delayed extractor: %v", res.err)
	}
	if time.Now().Before(res.at) {
		t.Fatal("waitForSpendRuns returned before delayed worker end was released")
	}
	rec := storedSpendRun(t, f, "run-item_a-a1")
	if rec.Usage == nil || rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != .03 {
		t.Fatalf("delayed explicit end lost spend: %+v", rec)
	}
}
