package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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

func TestLiveStreamCountsRecoveredLoopsOnly(t *testing.T) {
	stream := liveStream +
		`{"type":"loop_detected","action":"recovered","shape":"identical","tokensBefore":9000,"tokensAfter":2500}` + "\n" +
		`{"type":"loop_detected","action":"recovered","shape":"rotation","tokensBefore":12000,"tokensAfter":3500}` + "\n" +
		`{"type":"loop_detected","action":"ended","shape":"identical","toolName":"bash","streak":12}` + "\n" +
		`{"type":"loop_detected","toolName":"read","streak":40}` + "\n"

	spend, rec := sumLiveRunStream(strings.NewReader(stream))
	if spend.Turns == nil || *spend.Turns != 2 {
		t.Fatalf("spend %+v, want 2 turns", spend)
	}
	if rec == nil || rec.Count != 2 || rec.Shape != "rotation" || rec.TokensBefore != 12000 || rec.TokensAfter != 3500 {
		t.Fatalf("recovery = %+v, want count=2 shape=rotation 12000->3500", rec)
	}
}

func TestRunningShowsLiveAndFinishedLoopRecoveries(t *testing.T) {
	h := newRunningHarness(t)
	liveLog := liveStream +
		`{"type":"loop_detected","action":"recovered","shape":"identical","tokensBefore":12000,"tokensAfter":3500}` + "\n"
	if err := os.WriteFile(filepath.Join(h.laneRoot, "lane-1", "run-live.log"), []byte(liveLog), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seqNormal := h.addEvent(h.now, "run.finish", "run-rec-ok",
		`{"end_cause":"normal-exit","loop_recovery":{"count":1,"shape":"rotation","tokens_before":18000,"tokens_after":4200}}`)
	seqLoop := h.addEvent(h.now, "run.finish", "run-rec-loop",
		`{"end_cause":"repeating-call","repeating_call":{"tool":"","shape":"no-tool","args":"{}","args_digest":"d","streak":10},"loop_recovery":{"count":2,"shape":"no-tool","tokens_before":25000,"tokens_after":6000}}`)
	for _, stmt := range []string{
		`ALTER TABLE runs ADD COLUMN end_cause TEXT`,
		`UPDATE runs SET end_cause = 'normal-exit'`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, event_seq, end_cause)
		 VALUES ('run-rec-ok', 'item-done', 'lane-2', 'pi', 'exited', 0, 3000, 4000, ` + strconv.FormatInt(seqNormal, 10) + `, 'normal-exit')`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, event_seq, end_cause)
		 VALUES ('run-rec-loop', 'item-done', 'lane-2', 'pi', 'exited', 0, 5000, 6000, ` + strconv.FormatInt(seqLoop, 10) + `, 'repeating-call')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Active) != 1 || payload.Active[0].LoopRecovery == nil {
		t.Fatalf("active run missing loop recovery: %+v", payload.Active)
	}
	liveRec := payload.Active[0].LoopRecovery
	if liveRec.Count != 1 || liveRec.Shape != "identical" || liveRec.TokensBefore != 12000 || liveRec.TokensAfter != 3500 {
		t.Fatalf("live recovery = %+v", liveRec)
	}

	var recOK, recLoop finishedRun
	for _, f := range payload.Finished {
		switch f.RunID {
		case "run-rec-ok":
			recOK = f
		case "run-rec-loop":
			recLoop = f
		}
	}
	if recOK.LoopRecovery == nil || recOK.LoopRecovery.Count != 1 || recOK.LoopRecovery.Shape != "rotation" || recOK.LoopRecovery.TokensBefore != 18000 || recOK.LoopRecovery.TokensAfter != 4200 {
		t.Fatalf("finished normal-exit recovery = %+v", recOK)
	}
	if recLoop.Outcome != "repeating-call: no-tool ×10" || recLoop.LoopRecovery == nil || recLoop.LoopRecovery.Count != 2 || recLoop.LoopRecovery.Shape != "no-tool" || recLoop.LoopRecovery.TokensBefore != 25000 || recLoop.LoopRecovery.TokensAfter != 6000 {
		t.Fatalf("finished repeating-call recovery = %+v", recLoop)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"loop_recovery":{"count":1,"shape":"identical","tokens_before":12000,"tokens_after":3500}`,
		`"loop_recovery":{"count":1,"shape":"rotation","tokens_before":18000,"tokens_after":4200}`,
		`"loop_recovery":{"count":2,"shape":"no-tool","tokens_before":25000,"tokens_after":6000}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("JSON missing %s:\n%s", want, string(raw))
		}
	}

	var out bytes.Buffer
	renderRunning(&out, payload)
	text := out.String()
	for _, want := range []string{
		"RECOVERIES",
		"1× (identical, 12000→3500 tok)",
		"1× (rotation, 18000→4200 tok)",
		"2× (no-tool, 25000→6000 tok)",
		"repeating-call: no-tool ×10",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("table missing %q:\n%s", want, text)
		}
	}
}
