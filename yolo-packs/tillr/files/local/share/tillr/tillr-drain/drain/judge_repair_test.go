package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func prepareJudgeCandidate(t *testing.T, mode string) (refusalLoopFixture, *Loop, *judgeRouteSteps, Candidate) {
	t.Helper()
	f, l, s := judgeRouteFixture(t, mode)
	persistFixtureJudgeSelection(t, f)
	lane := l.laneDirPath("lane-1")
	writeForkFile(t, filepath.Join(lane, BriefFile), "worker brief\n")
	session, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, SessionIDFile), session+"\n")
	writeForkFile(t, filepath.Join(lane, FenceFile), "tools/item-a/\n")
	base := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD")
	req := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture"}
	if _, _, err := s.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	c := Candidate{ItemID: req.ItemID, LaneID: req.LaneID, RunID: req.RunID, Worktree: l.lanePath("lane-1"), BaseSHA: base, HeadSHA: s.workerHead, Request: &req}
	return f, l, s, c
}

func TestReplacementRestoresJudgeCommitBeforeHeadGuard(t *testing.T) {
	for _, mode := range []string{"terminal", "face-lost"} {
		t.Run(mode, func(t *testing.T) {
			f, l, s, c := prepareJudgeCandidate(t, "pass")
			checked, ok, err := l.judgeCandidate(context.Background(), c, &LoopResult{})
			if err != nil || !ok {
				t.Fatalf("judge %v %v", ok, err)
			}
			// Replay the state a canceled supervisor leaves: its handed-off judge has
			// terminated after committing; the intake has not yet run.
			checked.Judge.Raw = nil
			if mode == "face-lost" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`DELETE FROM runs WHERE run_id=?`, c.RunID+"-judge")
					return err
				})
			}
			raw, err := json.Marshal(checked.Judge)
			if err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(RunDir(f.root, c.RunID), judgeReceiptFile), string(raw))
			writeForkFile(t, filepath.Join(c.Worktree, "tools/item-a/result.txt"), "judge mutation\n")
			runGit(t, c.Worktree, "add", ".")
			runGit(t, c.Worktree, "commit", "-qm", "judge mutation")
			replacement := f.loop(t, s, l.cfg.Stage)
			replacement.cfg.Policy = l.cfg.Policy
			_, landed, err := replacement.Verify(context.Background(), c, &LoopResult{}, nil)
			if err != nil || landed {
				t.Fatalf("replacement must refuse the judge, not fault the worker: %v %v", landed, err)
			}
			if head := runGit(t, c.Worktree, "rev-parse", "HEAD"); head != c.HeadSHA {
				t.Fatalf("worker head not restored %s", head)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var n int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='drain.fault'`).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Errorf("worker faults %d", n)
				}
				var id string
				if err := r.QueryRow(`SELECT object_id FROM events WHERE kind='drain.refusal' ORDER BY seq DESC LIMIT 1`).Scan(&id); err != nil {
					return err
				}
				if id != c.RunID+"-judge" {
					t.Errorf("refusal %s", id)
				}
				return nil
			})
		})
	}
}

func TestLegacyCheckedJudgeRecoversPublicationOnce(t *testing.T) {
	f, l, s, c := prepareJudgeCandidate(t, "pass")
	ctx := context.Background()
	checked, accepted, err := l.judgeCandidate(ctx, c, &LoopResult{})
	if err != nil || !accepted {
		t.Fatalf("judge: accepted=%t err=%v", accepted, err)
	}
	receiptPath := filepath.Join(RunDir(f.root, c.RunID), judgeReceiptFile)
	var legacy map[string]json.RawMessage
	encoded, err := json.Marshal(checked.Judge)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	options, err := json.Marshal(struct {
		ItemID, Head, Tree, Actor, JudgedActor, GrantID string
	}{c.ItemID, c.HeadSHA, checked.Judge.Options.Tree, checked.Judge.Options.Actor, checked.Judge.Options.JudgedActor, "retired-id"})
	if err != nil {
		t.Fatal(err)
	}
	legacy["options"] = options
	encoded, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, receiptPath, string(encoded))
	s.afterLand = func() {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`CREATE TRIGGER test_refuse_verdict BEFORE INSERT ON events WHEN NEW.kind='item.verdict'
BEGIN SELECT RAISE(ABORT,'test failure before settlement'); END;`)
			return err
		})
	}
	if _, landed, err := l.Verify(ctx, c, &LoopResult{}, nil); err == nil || landed {
		t.Fatalf("expected failed retirement after publication: landed=%t err=%v", landed, err)
	}
	landedHead := runGit(t, f.root, "rev-parse", "HEAD")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER test_refuse_verdict`)
		return err
	})
	replacement := f.loop(t, s, l.cfg.Stage)
	replacement.cfg.Policy = l.cfg.Policy
	if _, landed, err := replacement.Verify(ctx, c, &LoopResult{}, nil); err != nil || !landed {
		t.Fatalf("recover publication: landed=%t err=%v", landed, err)
	}
	_, _, _ = replacement.Verify(ctx, c, &LoopResult{}, nil)
	if want := []string{"worker", "judge", "gate", "landing"}; !reflect.DeepEqual(s.order, want) {
		t.Fatalf("repeated work: got %v, want %v", s.order, want)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='item.verdict' AND json_extract(payload,'$.evaluated_head')=? AND json_extract(payload,'$.landed[0]')=? AND json_type(payload,'$.grant_id') IS NULL`, c.HeadSHA, landedHead).Scan(&count)
		if err == nil && count != 1 {
			t.Errorf("expected one verdict on %s landing %s, got %d", c.HeadSHA, landedHead, count)
		}
		return err
	})
}

func TestStandaloneJudgeCannotSubstituteForAnExplicitEvaluator(t *testing.T) {
	f, l, s, c := prepareJudgeCandidate(t, "pass")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE workflow_selections SET workflow='explicit@v1', graph=? WHERE item_id=?`,
			`{"stages":[{"id":"verify","kind":"agent","access":"read_only","judge_classes":["verification-only"]}],"exit":{"evaluator_slot":"verify","requires_nodes":["verify"]}}`, c.ItemID)
		return err
	})
	result := &LoopResult{}
	_, accepted, err := l.judgeCandidate(context.Background(), c, result)
	if err != nil || accepted || result.RefusedSteps[ReasonJudgeUnreadable] != 1 {
		t.Fatalf("expected evaluator binding hold: accepted=%t result=%+v err=%v", accepted, result, err)
	}
	if want := []string{"worker"}; !reflect.DeepEqual(s.order, want) {
		t.Fatalf("unexpected standalone judge: got %v, want %v", s.order, want)
	}
}

func TestGatedLegacyJudgeResumesOnlyWithMatchingEvaluator(t *testing.T) {
	for _, tc := range []struct {
		name, node string
		land       bool
	}{
		{"legacy compatibility transport", "", true},
		{"inconsistent node", "wrong-node", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, l, s, c := prepareJudgeCandidate(t, "pass")
			ctx := context.Background()
			checked, accepted, err := l.judgeCandidate(ctx, c, &LoopResult{})
			if err != nil || !accepted {
				t.Fatalf("judge: accepted=%t err=%v", accepted, err)
			}
			// Resume directly from a gate verdict with an old receipt.
			checked.Judge.Options.EvaluatorBinding = item.EvaluatorBinding{}
			checked.Judge.legacy = true
			checked.Judge.Request.NodeID = tc.node
			result := &LoopResult{}
			_, landed, err := l.landCandidate(ctx, checked, result, nil)
			if err != nil || landed != tc.land {
				t.Fatalf("landed=%t, want %t; err=%v", landed, tc.land, err)
			}
			want := []string{"worker", "judge"}
			if tc.land {
				want = append(want, "landing")
			} else if result.RefusedSteps[ReasonJudgeUnreadable] != 1 {
				t.Fatalf("expected evaluator binding hold: %+v", result)
			}
			if !reflect.DeepEqual(s.order, want) {
				t.Fatalf("unexpected steps: got %v, want %v", s.order, want)
			}
		})
	}
}

// An unreadable judge file is the review run's, not the worker's: the item is held as
// `judge-unreadable` in the queue, and since the owner's ruling of 2026-11-17 a changed
// backend policy re-dispatches nothing.
func TestAnUnreadableJudgeIsNotChargedToTheWorker(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "missing")
	result := &LoopResult{}
	for attempt := 0; attempt < 2; attempt++ {
		for i := 0; i < 4; i++ {
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)
		}
		l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "fixture", Model: "changed-model-" + string(rune('a'+attempt))}
	}
	if len(s.runs) != 2 {
		t.Fatalf("expected one worker and one judge: %v", s.runs)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
			return err
		}
		if state != StateInQueue {
			t.Errorf("judge failures blocked worker as %s", state)
		}
		return nil
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" && p.decision.Reason != ReasonJudgeUnreadable {
			t.Errorf("an unreadable judge is held as %q, want %q", p.decision.Reason, ReasonJudgeUnreadable)
		}
	}
}

func TestJudgeLandingDoesNotDependOnAClock(t *testing.T) {
	for _, phase := range []string{"gate", "landing"} {
		t.Run(phase, func(t *testing.T) {
			f, l, s := judgeRouteFixture(t, "pass")
			at := l.cfg.Now()
			expire := func() { l.cfg.Now = func() time.Time { return at.Add(2 * time.Hour) } }
			if phase == "gate" {
				s.afterGate = expire
			} else {
				s.afterLand = expire
			}
			result := &LoopResult{}
			for i := 0; i < 4; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if !reflect.DeepEqual(s.order, []string{"worker", "judge", "gate", "landing"}) {
				t.Errorf("repeated steps %v", s.order)
			}
			if result.Landed != 1 {
				t.Errorf("settled landings %d", result.Landed)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var n int
				err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n)
				if n != 1 {
					t.Errorf("verdict count %d", n)
				}
				return err
			})
		})
	}
}

// The exact diff is kept outside adapter-preflighted text, not altered to make
// literal brace pairs look like a different patch.
func TestJudgeDiffArtifactKeepsLiteralBraces(t *testing.T) {
	_, l, _, c := prepareJudgeCandidate(t, "preflight")
	checked, ok, err := l.judgeCandidate(context.Background(), c, &LoopResult{})
	if err != nil || !ok {
		t.Fatalf("judge %v %v", ok, err)
	}
	raw, err := os.ReadFile(filepath.Join(run.DurableDir(l.laneDirPath(c.LaneID), checked.Judge.Request.RunID), "candidate.diff"))
	if err != nil {
		t.Fatal(err)
	}
	diff, err := l.git(context.Background(), c.Worktree, "diff", c.BaseSHA, c.HeadSHA)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != diff {
		t.Fatal("candidate diff artifact changed")
	}
}
