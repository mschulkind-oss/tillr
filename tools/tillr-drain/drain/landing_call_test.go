package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type landingCallGateSteps struct{ *continuationSteps }

func (s landingCallGateSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	if c.HeadSHA != runGit(s.t, c.Worktree, "rev-parse", "HEAD") || c.HeadSHA == c.BaseSHA {
		s.t.Fatalf("unexpected gate candidate: %+v", c)
	}
	return GateOutcome{Outcome: GateFailed}, nil
}

const standInLandingOffer = `
set -eu
printf '%s\n' "$1" > candidate.txt
git add candidate.txt
git commit -qm "$1"
printf '{"call_id":"%s","commit":"%s","base":"%s","summary":"candidate"}\n' "$1" "$(git rev-parse HEAD)" "$(cat ../base_sha)" > "$YOLO_DURABLE_DIR/offer.tmp"
mv "$YOLO_DURABLE_DIR/offer.tmp" "$YOLO_DURABLE_DIR/landing-offer.json"
exit 29
`

func TestLandingCallOfferAndFooterSettlement(t *testing.T) {
	for _, offers := range []bool{false, true} {
		t.Run(map[bool]string{false: "footer", true: "killed-after-offer-is-written-run-ends-submitted"}[offers], func(t *testing.T) {
			script := `set -eu; echo first > candidate.txt; git add candidate.txt; git commit -qm first; echo 'RESULT: done'`
			if offers {
				script = "set -- first\n" + strings.Replace(standInLandingOffer, "exit 29", "kill -KILL $$", 1)
			}
			f, l, steps, d := continuationDispatchFixture(t, script, false)
			l.cfg.Steps = landingCallGateSteps{steps}
			var result LoopResult
			settleContinuation(t, l, &result)
			if steps.gates != 1 || result.RefusedSteps[RefusalGate] != 1 {
				t.Fatalf("did not judge commit: gates=%d result=%+v", steps.gates, result)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var count int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, callOffered).Scan(&count); err != nil {
					return err
				}
				if (count == 1) != offers {
					t.Fatalf("offers=%d", count)
				}
				if !offers {
					return nil
				}
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=?`, callOffered).Scan(&raw); err != nil {
					return err
				}
				var c landingCall
				if err := json.Unmarshal(raw, &c); err != nil {
					return err
				}
				dir := l.laneDirPath(d.LaneID)
				if c.RunID != d.RunRequest.RunID || c.Base != l.laneBase(d.LaneID) || c.Commit != l.headOf(context.Background(), d.LaneID) || c.SessionID != "33b5f2d2-0cd4-4a49-b293-c58f5635561f" || c.Worktree != l.laneTarget(d.LaneID) || c.RunLog != filepath.Join(dir, c.RunID+".log") {
					t.Fatalf("incorrect call: %+v", c)
				}
				return nil
			})
			if !offers {
				return
			}
			if ok, err := l.intakeLandingOffer(context.Background(), d, run.Record{RunID: d.RunRequest.RunID}, l.laneBase(d.LaneID)); ok != landingOfferSettled || err != nil {
				t.Fatalf("offer replay: %v %v", ok, err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var count int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, callOffered).Scan(&count); err != nil {
					return err
				}
				if count != 1 {
					t.Fatalf("replay inserted %d call offers", count)
				}
				return nil
			})
		})
	}
}

func TestLandingCallRefusesSecondAnswer(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendLandingCallEvent(tx, callOffered, "first", landingCall{landingOffer: landingOffer{CallID: "first"}, ItemID: "item-a", RunID: "writer"}, l.cfg.Now()); err != nil {
			return err
		}
		if err := appendLandingCallEvent(tx, callAnswered, "first", landingAnswer{Kind: "refused", Text: "first"}, l.cfg.Now()); err != nil {
			return err
		}
		if err := appendLandingCallEvent(tx, callAnswered, "first", landingAnswer{Kind: "refused", Text: "replacement"}, l.cfg.Now()); err == nil {
			t.Fatal("second answer accepted")
		}
		return nil
	})
}

func TestLandingStateIncludesPriorRefusalsAndOpenCalls(t *testing.T) {
	f, l := passFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, c := range []landingCall{
			{landingOffer: landingOffer{CallID: "old", Commit: "old-head"}, ItemID: "item-a", SessionID: "session"},
			{landingOffer: landingOffer{CallID: "open", Commit: "new-head"}, ItemID: "item-a", SessionID: "session"},
			{landingOffer: landingOffer{CallID: "other", Commit: "other-head"}, ItemID: "item-a", SessionID: "other-session"},
		} {
			if err := appendLandingCallEvent(tx, callOffered, c.CallID, c, l.cfg.Now()); err != nil {
				return err
			}
		}
		if err := appendLandingCallEvent(tx, callAnswered, "old", landingAnswer{CallID: "old", Kind: "refused", Text: "gate evidence"}, l.cfg.Now()); err != nil {
			return err
		}
		return appendLandingCallEvent(tx, callDelivered, "old", landingCallDelivery{Form: "tool-result", RunID: "previous"}, l.cfg.Now())
	})
	state, err := l.landingState(context.Background(), "item-a", "session")
	if err != nil {
		t.Fatal(err)
	}
	open := state["open"].([]landingOffer)
	refused := state["refused"].([]landingRefusal)
	if len(open) != 1 || open[0].CallID != "open" || len(refused) != 1 || refused[0] != (landingRefusal{Commit: "old-head", Answer: "gate evidence"}) {
		t.Fatalf("wrong state: %+v", state)
	}
}

func returnedLandingFixture(t *testing.T, transcript string) (refusalLoopFixture, *Loop, Decision, string) {
	t.Helper()
	script := "set -- first\n" + standInLandingOffer
	if transcript != "" {
		script = strings.Replace(script, `"summary":"candidate"`, `"summary":"candidate","transcript":"`+transcript+`"`, 1)
	}
	f, l, steps, first := continuationDispatchFixture(t, script, false)
	for _, dir := range []string{l.laneDirPath(first.LaneID), RunDir(l.cfg.Root, first.RunRequest.RunID)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, endingCapabilityFile), []byte(run.EndingCallSuspends+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	declareWorkerResumeFixture(t, l)
	l.cfg.Steps = landingCallGateSteps{steps}
	var result LoopResult
	settleContinuation(t, l, &result)
	p := mustPlan(t, l)[0]
	if p.decision.RunRequest == nil || !p.decision.RunRequest.SessionTurn || p.decision.LaneID != first.LaneID {
		t.Fatalf("not reopening the original session: %+v", p.decision)
	}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	d := applyReturnedTurn(t, l, p, l.laneBase(first.LaneID))
	dir := run.DurableDir(l.laneDirPath(d.LaneID), d.RunRequest.RunID)
	return f, l, d, dir
}

func TestReturnedLandingAnswerStaging(t *testing.T) {
	for _, transcript := range []string{"", "/session/file.jsonl"} {
		t.Run(map[bool]string{true: "path", false: "no-path"}[transcript != ""], func(t *testing.T) {
			f, l, d, dir := returnedLandingFixture(t, transcript)
			fixtureRead(t, f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=?`, callOffered).Scan(&raw); err != nil {
					return err
				}
				var c struct {
					Transcript string `json:"transcript"`
				}
				if err := json.Unmarshal(raw, &c); err != nil {
					return err
				}
				if c.Transcript != transcript {
					t.Fatalf("record transcript = %q, want %q", c.Transcript, transcript)
				}
				return nil
			})
			path, pathErr := os.ReadFile(filepath.Join(dir, "landing-transcript"))
			if transcript == "" {
				if !os.IsNotExist(pathErr) {
					t.Fatalf("unexpected transcript: %q, %v", path, pathErr)
				}
			} else if pathErr != nil || string(path) != transcript+"\n" {
				t.Fatalf("staged transcript = %q, %v", path, pathErr)
			}
			raw, err := os.ReadFile(filepath.Join(dir, landingAnswerFile))
			if err != nil {
				t.Fatal(err)
			}
			var answer landingAnswer
			if err := json.Unmarshal(raw, &answer); err != nil {
				t.Fatal(err)
			}
			if answer.CallID != "first" || answer.Kind != "refused" || answer.Stage != "gate" || !strings.Contains(answer.Text, d.RunRequest.RunID) {
				t.Fatalf("wrong staged answer: %+v", answer)
			}
			if err := l.reconcileLandingDelivery(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var count int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, callDelivered).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					t.Fatal("staging alone recorded a delivery")
				}
				return nil
			})
		})
	}
}

func TestLandingCallDeliversRefusalsAsToolResults(t *testing.T) {
	ctx := context.Background()
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"gate", "judge", "fence", "missing-transcript", "open-refusal"} {
		t.Run(scenario, func(t *testing.T) {
			stage := scenario
			if scenario == "missing-transcript" || scenario == "open-refusal" {
				stage = "gate"
			}
			bin := t.TempDir()
			transcript := filepath.Join(bin, "session.jsonl")
			// Pi is the only stand-in at the reopening seam: run-pi must choose
			// promptless delivery from the held call before invoking it.
			if err := writeForkSafe(filepath.Join(bin, "pi"), []byte(`#!/usr/bin/env python3
import json, os, pathlib, sys
scr = pathlib.Path(os.environ['YOLO_DURABLE_DIR'])
prompt = sys.stdin.buffer.read()
if os.environ['TILLR_SESSION_MODE'] == 'new':
    assert b'document lint: invalid heading' in prompt, prompt
    (scr / 'fresh-prompt').write_bytes(prompt)
    sys.exit(0)
if os.environ.get('LANDING_OPEN_REFUSAL') == '1':
    print('Pi refused to open the recorded session', file=sys.stderr)
    sys.exit(1)
(scr / 'received-prompt').write_bytes(prompt)
assert os.environ['TILLR_SESSION_MODE'] == 'resume'
session = sys.argv[sys.argv.index('--session') + 1]
path = pathlib.Path((scr / 'landing-transcript').read_text().rstrip('\n'))
header, call = [json.loads(line) for line in path.read_text().splitlines()]
assert header['id'] == session
answer = json.loads((scr / 'landing-answer.json').read_text())
assert call['message']['content'][0]['id'] == answer['call_id']
with path.open('a') as out:
    out.write(json.dumps(dict(type='message', id='result', parentId=call['id'], message=dict(
        role='toolResult', toolName='land', toolCallId=answer['call_id'], isError=True,
        content=[dict(type='text', text=answer['text'])], details=answer))) + '\n')
`), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			script := "set -- first\n" + strings.Replace(standInLandingOffer, `"summary":"candidate"`, `"summary":"candidate","transcript":`+strconv.Quote(transcript), 1)
			if stage == "fence" {
				script = strings.ReplaceAll(script, "candidate.txt", "outside.txt")
			}
			f, l, steps, first := continuationDispatchFixture(t, script, false)
			lane := l.laneDirPath(first.LaneID)
			firstDir := RunDir(f.root, first.RunRequest.RunID)
			for _, dir := range []string{lane, firstDir} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(dir, endingCapabilityFile), run.EndingCallSuspends+"\n")
			}
			session, err := ReadSessionID(lane)
			if err != nil || session == "" {
				t.Fatalf("original session: %q %v", session, err)
			}
			parked := `{"type":"session","id":` + strconv.Quote(session) + "}\n" +
				`{"type":"message","id":"offered","parentId":null,"message":{"role":"assistant","content":[{"type":"toolCall","name":"land","id":"first"}]}}` + "\n"
			writeForkFile(t, transcript, parked)
			base := l.laneBase(first.LaneID)
			head := runGit(t, l.laneTarget(first.LaneID), "rev-parse", "HEAD")
			var evidence []string
			var detail []string
			switch stage {
			case "gate":
				l.cfg.Steps = &designRepairSteps{returnedLandingSteps: &returnedLandingSteps{continuationSteps: steps, root: f.root}, failures: 1, output: triageFixtureBlock}
				writeForkFile(t, filepath.Join(firstDir, GatePlanRecord), `{"record_version":1,"kind":"gate.plan","id":"plan","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":{"checks":[{"check_id":"documents","command":["lint-docs","--strict"]}]}}`)
				evidence = []string{filepath.Join(firstDir, "documents.log")}
				detail = []string{"Check: documents", `Command: ["lint-docs" "--strict"]`, "document lint: invalid heading",
					"check go-drain", "case: TestGatheredWriteFairness", "at: tools/tillr-drain/drain/gathered_fairness_test.go:113",
					"assertion: a check asserts elapsed time", "reproduce only this case: " + triageFixtureOneCase, "not the whole suite"}
			case "judge":
				l.cfg.Policy.Seats = map[string]string{"review": "reviewer"}
				l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture", Model: "review-model"}
				// This fixture selects review after staging the worker. Model the
				// selection migration rather than relying on a late mode toggle.
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE workflow_selections SET graph=? WHERE item_id='item-a'`, store.CompatibilityJudgeGraph(singleWorkerWorkflow, ""))
					return err
				})
				l.cfg.Steps = &judgeRouteSteps{workflowSteps: &workflowSteps{t: t, f: f}, mode: "not-done", workerHead: head}
				evidence = []string{"commit:" + head}
				detail = []string{"Criterion c1 (content): the first judge reason"}
			case "fence":
				detail = []string{"outside.txt is outside allowed_paths"}
			}
			declareWorkerResumeFixture(t, l)
			var result LoopResult
			settleContinuation(t, l, &result)
			plans := mustPlan(t, l)
			if len(plans) != 1 || plans[0].decision.RunRequest == nil || !plans[0].decision.RunRequest.SessionTurn || plans[0].decision.LaneID != first.LaneID {
				t.Fatalf("%s refusal did not plan a turn in the original session: %+v (settlement: %+v)", stage, plans, result)
			}
			p := plans[0]
			p.decision.RunRequest.Command = []string{adapter, "--backend", "pi", "--model", "fixture", "--effort", "low"}
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
			d := applyReturnedTurn(t, l, p, base)
			dir := run.DurableDir(lane, d.RunRequest.RunID)
			if scenario == "missing-transcript" {
				if err := os.Remove(transcript); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "open-refusal" {
				t.Setenv("LANDING_OPEN_REFUSAL", "1")
				writeForkFile(t, transcript, parked+`{"type":"compaction","id":"compacted","parentId":"offered"}`+"\n")
			}
			rec, log, err := steps.Run(ctx, *d.RunRequest)
			if stage != scenario {
				if err != nil || rec.ExitCode == nil || *rec.ExitCode != 1 {
					t.Fatalf("expected failed session open: %+v / %v", rec, err)
				}
				outcome := l.settle(ctx, d, rec, log, base, "tick", l.cfg.Now(), &result, nil, nil, false)
				if outcome.err != nil || outcome.stop != "" {
					t.Fatalf("fresh fallback: %+v", outcome)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					var count int
					if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, callDelivered).Scan(&count); err != nil {
						return err
					}
					if count != 0 {
						t.Fatal("failed open recorded a delivery to the original session")
					}
					var freshRun string
					if err := r.QueryRow(`SELECT object_id FROM events WHERE kind=? AND json_extract(payload,'$.decision.worker_session_reason')='resume-failed'`, dispatchStartedKind).Scan(&freshRun); err != nil {
						return err
					}
					prompt, err := os.ReadFile(filepath.Join(run.DurableDir(lane, freshRun), "fresh-prompt"))
					if err != nil || !strings.Contains(string(prompt), "document lint: invalid heading") {
						t.Fatalf("fresh session did not receive the answer: %v", err)
					}
					return nil
				})
				return
			}
			if err != nil || rec.ExitCode == nil || *rec.ExitCode != 0 {
				stderr, _ := os.ReadFile(filepath.Join(lane, "stream.err"))
				t.Fatalf("reopen: %+v / %v; %s", rec, err, stderr)
			}
			prompt, err := os.ReadFile(filepath.Join(dir, "received-prompt"))
			if err != nil || len(prompt) != 0 {
				t.Fatalf("reopened prompt = %q / %v", prompt, err)
			}
			body, err := os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
			if len(lines) != 3 || !strings.HasPrefix(string(body), parked) {
				t.Fatalf("expected session, parked call, and result only: %s", body)
			}
			var resultEntry struct {
				Message struct {
					Role    string
					Details landingAnswer
				}
			}
			if err := json.Unmarshal([]byte(lines[2]), &resultEntry); err != nil {
				t.Fatal(err)
			}
			answer := resultEntry.Message.Details
			if resultEntry.Message.Role != "toolResult" || answer.CallID != "first" || answer.Kind != "refused" || answer.Stage != stage || !reflect.DeepEqual(answer.Evidence, evidence) || !landingResultPersisted(body, session, answer) {
				t.Fatalf("not the expected persisted error result: %+v", resultEntry)
			}
			if !strings.HasPrefix(answer.Text, "ANSWER: refused\n") {
				t.Fatalf("wrong answer heading: %q", answer.Text)
			}
			for _, want := range append(detail, "Offer commit: "+head, "Branch head: "+head, "Last base: "+base, "Turn: "+d.RunRequest.RunID, "Scratch: "+dir) {
				if !strings.Contains(answer.Text, want) {
					t.Errorf("answer lacks %q: %s", want, answer.Text)
				}
			}
			if stage == "gate" {
				// The failing case opens the answer: before the offer lines and the logs.
				if at, offer := strings.Index(answer.Text, "check go-drain"), strings.Index(answer.Text, "Offer commit:"); at < 0 || offer < at || !strings.Contains(answer.Text[:offer], failureHeading) {
					t.Errorf("the failing case is not at the top of the answer: %s", answer.Text)
				}
			}
			outcome := l.settle(ctx, d, rec, log, base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
			if outcome.err != nil || outcome.stop != "" {
				t.Fatalf("settle reopened run: %+v", outcome)
			}
			assertDelivered := func() {
				fixtureRead(t, f, func(r *store.Reader) error {
					var count int
					var id, form, runID string
					if err := r.QueryRow(`SELECT count(*), COALESCE(MAX(object_id),''), COALESCE(MAX(json_extract(payload,'$.form')),''), COALESCE(MAX(json_extract(payload,'$.run_id')),'') FROM events WHERE kind=?`, callDelivered).Scan(&count, &id, &form, &runID); err != nil {
						return err
					}
					if count != 1 || id != "first" || form != "tool-result" || runID != d.RunRequest.RunID {
						t.Fatalf("delivery: count=%d call=%q form=%q run=%q", count, id, form, runID)
					}
					return nil
				})
			}
			assertDelivered()
			if err := l.reconcileLandingDelivery(ctx, d); err != nil {
				t.Fatal(err)
			}
			assertDelivered()
		})
	}
}

func TestLandingTranscriptIsWrittenBeforeAnswer(t *testing.T) {
	_, l, d, dir := returnedLandingFixture(t, "/session/file.jsonl")
	if err := os.Remove(filepath.Join(dir, landingAnswerFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "landing-transcript")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "landing-transcript"), 0700); err != nil {
		t.Fatal(err)
	}
	spec := StageSpec{SessionID: d.Returned.WorkerSession.SessionID}
	if err := l.stageLandingAnswer(context.Background(), d, &spec); err == nil {
		t.Fatal("staging should fail when the transcript path cannot be written")
	}
	if _, err := os.Stat(filepath.Join(dir, landingAnswerFile)); !os.IsNotExist(err) {
		t.Fatalf("answer published before transcript: %v", err)
	}
}

func TestLandingCallAnswerCannotExistBeforeOfferProcessExits(t *testing.T) {
	ctx := context.Background()
	f, l, _, d := continuationDispatchFixture(t, "set -- first\n"+standInLandingOffer, false)
	rec := run.Record{RunID: d.RunRequest.RunID}
	child := exec.Command("sleep", "60")
	child.Env = append(os.Environ(), "TILLR_RUN_ID="+rec.RunID, "TILLR_LOOP_STORE="+f.storePath)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if got, err := l.intakeLandingOffer(ctx, d, rec, l.laneBase(d.LaneID)); got != landingOfferAbsent || err == nil || !strings.Contains(err.Error(), "processes survive") {
		t.Fatalf("offer with surviving child: %v / %v", got, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind IN (?,?)`, callOffered, callAnswered).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("recorded %d call events while the child survived", count)
		}
		return nil
	})
}

func TestLandingMessageWaitsForRunEnd(t *testing.T) {
	f, l, d, dir := returnedLandingFixture(t, "")
	if err := os.WriteFile(filepath.Join(dir, "landing-delivery"), []byte("message\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "60")
	cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+d.RunRequest.RunID, "TILLR_LOOP_STORE="+f.storePath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err := l.reconcileLandingDelivery(context.Background(), d); err == nil || !strings.Contains(err.Error(), "processes survive") {
		t.Fatalf("live run: %v", err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, callDelivered).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("delivered while live: %d", count)
		}
		return nil
	})
}

func TestLandingDeliveryForms(t *testing.T) {
	for _, form := range []string{"message", "tool-result", "absent"} {
		t.Run(form, func(t *testing.T) {
			f, l, d, dir := returnedLandingFixture(t, "")
			if form != "absent" {
				if err := os.WriteFile(filepath.Join(dir, "landing-delivery"), []byte(form+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// A directory is not a readable transcript: message must not inspect it.
			if err := os.WriteFile(filepath.Join(dir, "landing-transcript"), []byte(dir+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if form == "message" {
				if err := l.reconcileLandingDelivery(context.Background(), d); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := l.reconcileLandingDelivery(context.Background(), d); err == nil {
					t.Fatal("tool-result must inspect transcript")
				}
				var answer landingAnswer
				raw, err := os.ReadFile(filepath.Join(dir, landingAnswerFile))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &answer); err != nil {
					t.Fatal(err)
				}
				details, err := json.Marshal(answer)
				if err != nil {
					t.Fatal(err)
				}
				text, err := json.Marshal(answer.Text)
				if err != nil {
					t.Fatal(err)
				}
				session := d.Returned.WorkerSession.SessionID
				transcript := `{"type":"session","id":"` + session + `"}` + "\n" + `{"type":"message","id":"r","parentId":null,"message":{"role":"toolResult","toolName":"land","toolCallId":"first","isError":true,"content":[{"type":"text","text":` + string(text) + `}],"details":` + string(details) + `}}` + "\n"
				path := filepath.Join(dir, "session.jsonl")
				if err := os.WriteFile(path, []byte(transcript), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "landing-transcript"), []byte(path+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := l.reconcileLandingDelivery(context.Background(), d); err != nil {
					t.Fatal(err)
				}
			}
			if err := l.reconcileLandingDelivery(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var count int
				var got string
				if err := r.QueryRow(`SELECT count(*), COALESCE(MAX(json_extract(payload,'$.form')),'') FROM events WHERE kind=?`, callDelivered).Scan(&count, &got); err != nil {
					return err
				}
				want := "tool-result"
				if form == "message" {
					want = form
				}
				if count != 1 || got != want {
					t.Fatalf("delivery count=%d form=%q, want 1 %q", count, got, want)
				}
				return nil
			})
		})
	}
}

func TestLandingCallLoopRecordsShippedCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name           string
		script         string
		wantPi         string
		wantFeatures   []string
		wantCapability string
	}{
		{
			name:           "supported",
			script:         "printf '[\"parked-tool-calls\"]\\n'",
			wantPi:         run.EndingCallSuspends,
			wantFeatures:   []string{"parked-tool-calls"},
			wantCapability: run.EndingCallSuspends,
		},
		{
			name:           "rejects",
			script:         "exit 2",
			wantPi:         run.EndingFooter,
			wantCapability: run.EndingFooter,
		},
		{
			name:           "unsupported",
			script:         "printf '[\"other\"]\\n'",
			wantPi:         run.EndingFooter,
			wantFeatures:   []string{"other"},
			wantCapability: run.EndingFooter,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			piPath := filepath.Join(bin, "pi")
			if err := writeForkSafe(piPath, []byte("#!/bin/sh\n"+tc.script+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			fakeClaude(t, bin, "echo 'No conversation found to continue' >&2; exit 1")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			f, l := passFixture(t)
			l.cfg.Policy.Backends = map[string]Backend{}
			want := map[string]string{}
			for _, name := range []string{"agy", "claude", "muse", "pi"} {
				adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", name, "run"))
				if err != nil {
					t.Fatal(err)
				}
				l.cfg.Policy.Backends[name] = Backend{Adapter: adapter}
				want[adapter] = run.EndingFooter
				if name == "pi" {
					want[adapter] = tc.wantPi
				}
			}
			if _, err := l.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=?`, endingCapabilitiesKind).Scan(&raw); err != nil {
					return err
				}
				got := map[string]string{}
				if err := json.Unmarshal(raw, &got); err != nil {
					return err
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("effective capabilities %v; want %v", got, want)
				}
				var count int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, piFeaturesKind).Scan(&count); err != nil {
					return err
				}
				if count != 1 {
					t.Fatalf("pi features events=%d, want 1", count)
				}
				var featRaw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=?`, piFeaturesKind).Scan(&featRaw); err != nil {
					return err
				}
				var feat struct {
					Path       string   `json:"path"`
					Capability string   `json:"capability"`
					Features   []string `json:"features"`
				}
				if err := json.Unmarshal(featRaw, &feat); err != nil {
					return err
				}
				if feat.Path != piPath || feat.Capability != tc.wantCapability || !reflect.DeepEqual(feat.Features, tc.wantFeatures) {
					t.Fatalf("pi features payload=%+v, want path=%q capability=%q features=%v", feat, piPath, tc.wantCapability, tc.wantFeatures)
				}
				return nil
			})
		})
	}
}

func TestLandingCallLoopWithoutPiBackendSkipsProbe(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "pi-started")
	piPath := filepath.Join(bin, "pi")
	if err := writeForkSafe(piPath, []byte("#!/bin/sh\n: > "+marker+"\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	fakeClaude(t, bin, "echo 'No conversation found to continue' >&2; exit 1")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f, l := passFixture(t)
	l.cfg.Policy.Backends = map[string]Backend{}
	want := map[string]string{}
	for _, name := range []string{"agy", "claude", "muse"} {
		adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", name, "run"))
		if err != nil {
			t.Fatal(err)
		}
		l.cfg.Policy.Backends[name] = Backend{Adapter: adapter}
		want[adapter] = run.EndingFooter
	}
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("loop without pi backend started pi (stat err=%v)", err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=?`, endingCapabilitiesKind).Scan(&raw); err != nil {
			return err
		}
		got := map[string]string{}
		if err := json.Unmarshal(raw, &got); err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("effective capabilities %v; want %v", got, want)
		}
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, piFeaturesKind).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("pi features events=%d, want 0", count)
		}
		return nil
	})
}

func TestLandingCallClosesOnProvenSupersededOrWithdrawn(t *testing.T) {
	for _, outcome := range []string{"proven", "superseded", "withdrawn"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a")
			at := l.cfg.Now()
			tip := runGit(t, f.root, "rev-parse", TrunkRef)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if err := appendLandingCallEvent(tx, callOffered, "call", landingCall{landingOffer: landingOffer{CallID: "call", Commit: tip}, ItemID: "item-a", RunID: "run"}, at); err != nil {
					return err
				}
				switch outcome {
				case "superseded":
					_, err := tx.Exec(`UPDATE items SET state='resolved-elsewhere' WHERE item_id='item-a'`)
					return err
				case "withdrawn":
					_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`)
					return err
				default:
					return recordFixtureLanding(tx, "item-a", "run", tip, 1)
				}
			})
			if outcome == "proven" {
				if err := l.reconcileLandingCalls(ctx); err != nil {
					t.Fatal(err)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					var landed, closed int
					err := r.QueryRow(`SELECT count(*) FILTER (WHERE kind=?),count(*) FILTER (WHERE kind=?) FROM events WHERE object_type='call'`, callLanded, callClosed).Scan(&landed, &closed)
					if err == nil && (landed != 1 || closed != 0) {
						t.Fatalf("before proof: %d landings and %d closes", landed, closed)
					}
					return err
				})
				runGit(t, f.root, "update-ref", GreenRef, tip)
			}
			for range 2 {
				if err := l.reconcileLandingCalls(ctx); err != nil {
					t.Fatal(err)
				}
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var reason string
				var count int
				err := r.QueryRow(`SELECT count(*),COALESCE(MAX(json_extract(payload,'$.reason')),'') FROM events WHERE object_type='call' AND kind=?`, callClosed).Scan(&count, &reason)
				if err == nil && (count != 1 || reason != outcome) {
					t.Fatalf("closed %d times as %q; want %q", count, reason, outcome)
				}
				return err
			})
		})
	}
}

func TestLandingCallReplayDistinguishesOpenSettledAndDuplicate(t *testing.T) {
	ctx := context.Background()
	f, l, steps, d := continuationDispatchFixture(t, "set -- first\n"+standInLandingOffer, false)
	req := *d.RunRequest
	base := l.laneBase(d.LaneID)
	rec := run.Record{RunID: req.RunID}
	for range 2 {
		got, err := l.intakeLandingOffer(ctx, d, rec, base)
		if err != nil || got != landingOfferSubmitted {
			t.Fatalf("open replay: %v, %v", got, err)
		}
	}
	original, err := os.ReadFile(filepath.Join(run.DurableDir(l.laneDirPath(d.LaneID), req.RunID), landingOfferFile))
	if err != nil {
		t.Fatal(err)
	}
	req.RunID = "later-run"
	d.RunRequest = &req
	dir := run.DurableDir(l.laneDirPath(d.LaneID), req.RunID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(dir, landingOfferFile), string(original))
	got, err := l.intakeLandingOffer(ctx, d, run.Record{RunID: req.RunID}, base)
	if err != nil || got != landingOfferDuplicate {
		t.Fatalf("later replay: %v, %v", got, err)
	}
	var report bytes.Buffer
	l.cfg.Report = &report
	outcome := l.settle(ctx, d, run.Record{RunID: req.RunID}, "", base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
	if outcome.err != nil || outcome.stop != "" || steps.gates != 0 {
		t.Fatalf("duplicate was judged: %+v, gates=%d", outcome, steps.gates)
	}
	if got := report.String(); !strings.Contains(got, req.RunID) || !strings.Contains(got, "repeated a landing call already recorded") {
		t.Fatalf("duplicate report = %q, want run ID %q and repeat notice", got, req.RunID)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		err := r.QueryRow(`SELECT count(*) FROM events WHERE object_type='call'`).Scan(&count)
		if err == nil && count != 1 {
			t.Fatalf("recorded %d call events, want 1", count)
		}
		return err
	})
	if c := itemConflict(t, f); c.ConflictID != "" {
		t.Fatalf("duplicate raised conflict: %+v", c)
	}
}

func TestLandingCallReplayRejectsChangedHeadOrBase(t *testing.T) {
	ctx := context.Background()
	f, l, _, d := continuationDispatchFixture(t, "set -- first\n"+standInLandingOffer, false)
	rec := run.Record{RunID: d.RunRequest.RunID}
	base := l.laneBase(d.LaneID)
	if got, err := l.intakeLandingOffer(ctx, d, rec, base); err != nil || got != landingOfferSubmitted {
		t.Fatalf("initial offer: %v, %v", got, err)
	}
	baseFile := filepath.Join(l.laneDirPath(d.LaneID), BaseSHAFile)
	writeForkFile(t, baseFile, "different-base\n")
	if _, err := l.intakeLandingOffer(ctx, d, rec, base); err == nil {
		t.Fatal("unfinished replay accepted a different base")
	}
	writeForkFile(t, baseFile, base+"\n")
	target := l.laneTarget(d.LaneID)
	writeForkFile(t, filepath.Join(target, "candidate.txt"), "changed after offer\n")
	runGit(t, target, "add", "candidate.txt")
	runGit(t, target, "commit", "-qm", "advance after offer")
	if _, err := l.intakeLandingOffer(ctx, d, rec, base); err == nil {
		t.Fatal("unfinished replay accepted a different HEAD")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendLandingCallEvent(tx, callAnswered, "first", landingAnswer{CallID: "first", Kind: "refused"}, l.cfg.Now())
	})
	if got, err := l.intakeLandingOffer(ctx, d, rec, base); err != nil || got != landingOfferSettled {
		t.Fatalf("settled replay: %v, %v", got, err)
	}
}

func TestLandingCallAnswersJudgeAndRevertFromTheirDurableRecords(t *testing.T) {
	for _, outcome := range []string{"judge", "reverted"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				return appendLandingCallEvent(tx, callOffered, "call", landingCall{landingOffer: landingOffer{CallID: "call", Commit: "offered", Base: "base"}, ItemID: "item-a", RunID: "writer"}, l.cfg.Now())
			})
			switch outcome {
			case "judge":
				c := Candidate{RunID: "different-candidate", ItemID: "item-a", Judge: &JudgeReceipt{Writers: []JudgeWriter{{RunID: "writer"}}, Raw: json.RawMessage(`{"verdicts":[{"criterion_id":"c1","verdict":"not-done","reason":"check failed","evidence":["commit:head"]},{"criterion_id":"c2","verdict":"not-done","reason":"missing candidate test","evidence":["node:work"]}]}`)}}
				if err := l.holdJudge(ctx, c, "judge", ReasonEvalFailed, "check failed", &LoopResult{}); err != nil {
					t.Fatal(err)
				}
			case "reverted":
				p := revertPayload{Landing: "landed", RunID: "writer", Result: "reverted", Revert: "revert", Tip: "tip"}
				if err := l.appendRevert(ctx, p, &refusalPayload{Step: RefusalReverted, Detail: "check failed"}); err != nil {
					t.Fatal(err)
				}
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id='call'`, callAnswered).Scan(&raw); err != nil {
					return err
				}
				var answer landingAnswer
				if err := json.Unmarshal(raw, &answer); err != nil {
					return err
				}
				if outcome == "judge" {
					want := []landingFinding{{CriterionID: "c1", Scope: "content"}, {CriterionID: "c2", Scope: "content"}}
					if answer.Kind != "refused" || answer.Stage != "judge" || !reflect.DeepEqual(answer.Findings, want) ||
						!strings.Contains(answer.Text, "Criterion c1 (content): check failed") || !strings.Contains(answer.Text, "Criterion c2 (content): missing candidate test") ||
						!reflect.DeepEqual(answer.Evidence, []string{"commit:head", "node:work"}) {
						t.Fatalf("judge answer: %+v", answer)
					}
				} else {
					var landed string
					if err := r.QueryRow(`SELECT json_extract(payload,'$.commit') FROM events WHERE kind=? AND object_id='call'`, callLanded).Scan(&landed); err != nil {
						return err
					}
					if answer.Kind != "reverted" || landed != "landed" || !strings.Contains(answer.Text, "Branch head: revert") {
						t.Fatalf("revert answer: %+v; landed=%s", answer, landed)
					}
				}
				return nil
			})
		})
	}
}

func TestLandingCallIdenticalRefusalWaitsForDesign(t *testing.T) {
	f, l := designSeatFixture(t)
	for _, id := range []string{"first", "second"} {
		p := mustPlan(t, l)[0]
		runID := p.decision.RunRequest.RunID
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendLandingCallEvent(tx, callOffered, id, landingCall{landingOffer: landingOffer{CallID: id}, ItemID: p.decision.ItemID, RunID: runID}, l.cfg.Now())
		})
		finishRefused(t, f, l, p, RefusalGate, "same refusal", "unit", "same failing output")
	}
	if c := itemConflict(t, f); !strings.HasPrefix(c.Text, IdenticalRepeatPrefix) || c.WaitingOn != "design" {
		t.Fatalf("not routed to design: %+v", c)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		var id string
		err := r.QueryRow(`SELECT count(*),COALESCE(MAX(object_id),'') FROM events WHERE kind=?`, callAnswered).Scan(&count, &id)
		if err == nil && (count != 1 || id != "first") {
			t.Fatalf("recorded %d answers, last ID %q, want first only", count, id)
		}
		return err
	})
}

func TestLandingCallFooterLandingDoesNotTakeWriter(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(_ *store.Tx) error {
		// A footer-only landing must not wait on the writer already held here.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return l.recordLandingCallLanded(ctx, "footer-run", "landed")
	})
}

func TestLandingResultPersisted(t *testing.T) {
	answer := landingAnswer{CallID: "c", Kind: "refused", Text: "ANSWER: refused"}
	header := `{"type":"session","id":"s"}` + "\n"
	result := `{"type":"message","id":"r","parentId":null,"message":{"role":"toolResult","toolName":"land","toolCallId":"c","isError":true,"content":[{"type":"text","text":"ANSWER: refused"}],"details":{"call_id":"c","kind":"refused","text":"ANSWER: refused"}}}` + "\n"
	for _, tc := range []struct {
		name, transcript string
		want             bool
	}{
		{"matching", header + result, true},
		{"later turn", header + result + `{"type":"message","id":"m","parentId":"r"}` + "\n", true},
		{"another branch", header + result + `{"type":"message","id":"m","parentId":null}` + "\n", false},
		{"different session", strings.Replace(header, `"s"`, `"other"`, 1) + result, false},
		{"different result", header + strings.Replace(result, `"c"`, `"other"`, 1), false},
		{"truncated", header + result[:len(result)/2], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := landingResultPersisted([]byte(tc.transcript), "s", answer); got != tc.want {
				t.Fatalf("persisted = %t; want %t", got, tc.want)
			}
		})
	}
}

// fakeClaude keeps a loop test from starting the real harness when it probes for the capability.
func fakeClaude(t *testing.T, dir, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"+script+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestLandingCallLoopRecordsClaudeCallSuspendsOnlyWhenTheBinaryProbesSo(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"binary knows deferred resume", "echo 'No deferred tool marker found in the resumed session' >&2; exit 1", run.EndingCallSuspends},
		{"binary predates it", "echo 'No conversation found to continue' >&2; exit 1", run.EndingFooter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			fakeClaude(t, bin, tc.script)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME", "")
			f, l := passFixture(t)
			adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "claude", "run"))
			if err != nil {
				t.Fatal(err)
			}
			l.cfg.Policy.Backends = map[string]Backend{"claude": {Adapter: adapter}}
			if _, err := l.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=?`, endingCapabilitiesKind).Scan(&raw); err != nil {
					return err
				}
				got := map[string]string{}
				if err := json.Unmarshal(raw, &got); err != nil {
					return err
				}
				if !reflect.DeepEqual(got, map[string]string{adapter: tc.want}) {
					t.Fatalf("effective capabilities %v; want %s", got, tc.want)
				}
				return nil
			})
		})
	}
}
