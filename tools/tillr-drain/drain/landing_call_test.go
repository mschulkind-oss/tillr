package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
		t.Run(map[bool]string{false: "footer", true: "offer"}[offers], func(t *testing.T) {
			script := `set -eu; echo first > candidate.txt; git add candidate.txt; git commit -qm first; echo 'RESULT: done'`
			if offers {
				script = "set -- first\n" + standInLandingOffer
			}
			f, l, steps, d := continuationDispatchFixture(t, script, false)
			l.cfg.Steps = landingCallGateSteps{steps}
			var result LoopResult
			settleContinuation(t, l, &result)
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
				if c.RunID != d.RunRequest.RunID || c.Base != l.laneBase(d.LaneID) || c.Commit != l.headOf(context.Background(), d.LaneID) || c.Summary != "candidate" || c.SessionID != "33b5f2d2-0cd4-4a49-b293-c58f5635561f" || c.Worktree != l.laneTarget(d.LaneID) || c.RunLog != filepath.Join(dir, c.RunID+".log") {
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
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if err := appendLandingCallEvent(tx, callAnswered, "first", landingAnswer{Text: "replacement"}, l.cfg.Now()); err == nil {
					t.Fatal("second answer accepted")
				}
				return nil
			})
		})
	}
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
	refused := state["refused"].([]map[string]string)
	if len(open) != 1 || open[0].CallID != "open" || len(refused) != 1 || refused[0]["commit"] != "old-head" || refused[0]["answer"] != "gate evidence" {
		t.Fatalf("wrong state: %+v", state)
	}
}

func TestReturnedLandingAnswerStaging(t *testing.T) {
	f, l, steps, first := continuationDispatchFixture(t, "set -- first\n"+standInLandingOffer, false)
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
	settleContinuation(t, l, &result)
	p := mustPlan(t, l)[0]
	if p.decision.RunRequest == nil || !p.decision.RunRequest.SessionTurn || p.decision.LaneID != first.LaneID {
		t.Fatalf("not reopening the original session: %+v", p.decision)
	}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	d := applyReturnedTurn(t, l, p, l.laneBase(first.LaneID))
	dir := run.DurableDir(l.laneDirPath(d.LaneID), d.RunRequest.RunID)
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
}

func TestLandingCallLoopRecordsShippedCapabilities(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nprintf '[\"parked-tool-calls\"]\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
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
	outcome := l.settle(ctx, d, run.Record{RunID: req.RunID}, "", base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
	if outcome.err != nil || outcome.stop != "" || steps.gates != 0 {
		t.Fatalf("duplicate was judged: %+v, gates=%d", outcome, steps.gates)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, callOffered).Scan(&count)
		if err == nil && count != 1 {
			t.Fatalf("recorded %d offers", count)
		}
		return err
	})
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
				c := Candidate{RunID: "different-candidate", ItemID: "item-a", Judge: &JudgeReceipt{Writers: []JudgeWriter{{RunID: "writer"}}, Raw: json.RawMessage(`{"verdicts":[{"criterion_id":"c1","verdict":"not-done","reason":"check failed","evidence":["commit:head"]}]}`)}}
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
					if answer.Kind != "refused" || answer.Stage != "judge" || !strings.Contains(answer.Text, "Criterion c1: check failed") || !reflect.DeepEqual(answer.Evidence, []string{"commit:head"}) {
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
