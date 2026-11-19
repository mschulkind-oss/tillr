package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// liveStream is a pi stream two turns into a run: partial usage on message_update (which
// must not count), then two complete turn_end usages.
const liveStream = `{"type":"session","id":"s"}
{"type":"message_update","message":{"role":"assistant","usage":{"totalTokens":999,"cost":{"total":9}}}}
{"type":"turn_end","message":{"role":"assistant","usage":{"totalTokens":1000,"cost":{"total":0.03}}}}
{"type":"tool_execution_start","toolCallId":"t1","toolName":"read","args":{"path":"a"}}
{"type":"turn_end","message":{"role":"assistant","usage":{"totalTokens":500,"cost":{"total":0.045}}}}
`

func TestLiveSpendCountsOnlyFinishedTurns(t *testing.T) {
	got := sumLiveSpend(strings.NewReader(liveStream))
	if got.Turns == nil || *got.Turns != 2 || got.Tokens == nil || *got.Tokens != 1500 || got.CostUSD == nil || *got.CostUSD < 0.0749 || *got.CostUSD > 0.0751 {
		t.Fatalf("spend %+v, want 2 turns, 1500 tokens, $0.075", got)
	}
	none := sumLiveSpend(strings.NewReader(`{"type":"session"}` + "\n"))
	if none.Turns == nil || *none.Turns != 0 || none.CostUSD != nil || none.Note == "" {
		t.Fatalf("a stream with no turn: %+v, want 0 turns and no cost, with a note", none)
	}
	if missing := readLiveSpend(filepath.Join(t.TempDir(), "absent.log")); missing.Turns != nil || missing.Note == "" {
		t.Fatalf("a missing log read as %+v, want an absence with a note", missing)
	}
}

func TestRunningShowsALiveRunsTurnsAndSpend(t *testing.T) {
	h := newRunningHarness(t)
	if err := os.WriteFile(filepath.Join(h.laneRoot, "lane-1", "run-live.log"), []byte(liveStream), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatal(err)
	}
	spend := payload.Active[0].Spend
	if spend.Turns == nil || *spend.Turns != 2 || spend.CostUSD == nil {
		t.Fatalf("live spend %+v", spend)
	}
	var out bytes.Buffer
	renderRunning(&out, payload)
	for _, want := range []string{"TURNS", "SPENT", "$0.07"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the table is missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunningNamesARepeatingCallEnding(t *testing.T) {
	h := newRunningHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seq := h.addEvent(h.now, "run.finish", "run-loop", `{"end_cause":"repeating-call","repeating_call":{"tool":"read","args":"{\"path\":\"pass.go\"}","args_digest":"d","streak":40}}`)
	for _, stmt := range []string{
		`ALTER TABLE runs ADD COLUMN end_cause TEXT`,
		`UPDATE runs SET end_cause = 'normal-exit'`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, event_seq, end_cause)
		 VALUES ('run-loop', 'item-done', 'lane-2', 'pi', 'exited', 0, 3000, 4000, ` + strconv.FormatInt(seq, 10) + `, 'repeating-call')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatal(err)
	}
	var loop, done finishedRun
	for _, f := range payload.Finished {
		switch f.RunID {
		case "run-loop":
			loop = f
		case "run-done":
			done = f
		}
	}
	if loop.EndCause != "repeating-call" || loop.Outcome != "repeating-call: read ×40" || loop.RepeatingCall == nil || loop.RepeatingCall.Args != `{"path":"pass.go"}` {
		t.Fatalf("the repeating-call run reads %+v", loop)
	}
	if done.Outcome != "landed landed-sha" || done.EndCause != "normal-exit" {
		t.Fatalf("a landed run's outcome changed: %+v", done)
	}
	var out bytes.Buffer
	renderRunning(&out, payload)
	if !strings.Contains(out.String(), "repeating-call: read ×40") {
		t.Fatalf("the table does not name the cause:\n%s", out.String())
	}
}
