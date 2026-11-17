package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func prepareJudgeCandidate(t *testing.T, mode string) (refusalLoopFixture, *Loop, *judgeRouteSteps, Candidate) {
	t.Helper()
	f, l, s := judgeRouteFixture(t, mode, true)
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

func TestExpiredPostLandGrantResumesSettlementOnly(t *testing.T) {
	f, l, s, c := prepareJudgeCandidate(t, "pass")
	at := l.cfg.Now()
	s.afterLand = func() { l.cfg.Now = func() time.Time { return at.Add(2 * time.Hour) } }
	_, landed, err := l.Verify(context.Background(), c, &LoopResult{}, nil)
	if err == nil || landed {
		t.Fatalf("expired grant must retain pending settlement: %v %v", landed, err)
	}
	landedSHA := runGit(t, f.root, "rev-parse", "HEAD")
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n)
		if n != 0 {
			t.Errorf("verdict without current authority")
		}
		return err
	})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('grant-new','verification-only','owner',?,?)`, l.cfg.Now().Add(-time.Minute).UnixMilli(), l.cfg.Now().Add(time.Hour).UnixMilli())
		return err
	})
	replacement := f.loop(t, s, l.cfg.Stage)
	replacement.cfg.Policy = l.cfg.Policy
	replacement.cfg.Now = l.cfg.Now
	_, landed, err = replacement.Verify(context.Background(), c, &LoopResult{}, nil)
	if err != nil || !landed {
		t.Fatalf("new covering grant must settle published work: %v %v", landed, err)
	}
	if !reflect.DeepEqual(s.order, []string{"worker", "judge", "gate", "landing"}) {
		t.Errorf("repeated work: %v", s.order)
	}
	if head := runGit(t, f.root, "rev-parse", "HEAD"); head != landedSHA {
		t.Errorf("landing repeated %s", head)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var payload string
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind='item.verdict'`).Scan(&payload); err != nil {
			return err
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(payload), &v); err != nil {
			return err
		}
		if v["grant_id"] != "grant-new" || v["landed"].([]any)[0] != landedSHA {
			t.Errorf("wrong authority or landed binding %s", payload)
		}
		return nil
	})
}

func TestTwoUnreadableJudgesDoNotSpendWorkerReadmission(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "missing", true)
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
	if len(s.runs) != 4 {
		t.Fatalf("expected two workers and two judges: %v", s.runs)
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
		if p.decision.ItemID == "item-a" && p.decision.Action != ActionDispatch {
			t.Errorf("worker allowance consumed: %s", p.decision.Reason)
		}
	}
}

func TestJudgeGrantExpiryRetainsLoopSettlement(t *testing.T) {
	for _, phase := range []string{"gate", "landing"} {
		t.Run(phase, func(t *testing.T) {
			f, l, s := judgeRouteFixture(t, "pass", true)
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
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID == "item-a" && p.decision.Reason != ReasonJudgeGrantAbsent {
					t.Errorf("grant expiry hold %s", p.decision.Reason)
				}
			}
			if result.Landed != 0 {
				t.Errorf("item counted settled before authority %d", result.Landed)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('replacement-grant','verification-only','owner',?,?)`, l.cfg.Now().Add(-time.Minute).UnixMilli(), l.cfg.Now().Add(time.Hour).UnixMilli())
				return err
			})
			for i := 0; i < 2; i++ {
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
