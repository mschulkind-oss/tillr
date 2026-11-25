package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

type reworkLandingSteps struct {
	*recoveryLandingSteps
	judge func(context.Context, string, string) ([]HeavyCheck, error)
}

func (s reworkLandingSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if _, err := s.recoveryLandingSteps.Gate(ctx, c); err != nil {
		return GateOutcome{}, err
	}
	return s.ProcessSteps.Gate(ctx, c)
}

func (s reworkLandingSteps) JudgeRequired(ctx context.Context, dir, id, _ string) (HeavyCheck, error) {
	checks, err := s.judge(ctx, dir, id)
	if err != nil {
		return HeavyCheck{}, err
	}
	if len(checks) != 1 || checks[0].CheckID != id {
		return HeavyCheck{}, fmt.Errorf("unexpected required check %q: %+v", id, checks)
	}
	return checks[0], nil
}

func TestRevertedItemReworkRetainsPinAndRelands(t *testing.T) {
	t.Run("still red refuses", func(t *testing.T) { testRevertedItemRework(t, "a", false) })
	t.Run("corrected lands", func(t *testing.T) { testRevertedItemRework(t, "fixed", true) })
}

func testRevertedItemRework(t *testing.T, path string, wantLand bool) {
	ctx := context.Background()
	h := newRevertHarness(t, fileRed("a"))
	culprit := h.land("item-broke", "a", "broken\n")
	h.proof()
	if got := h.reverts(); len(got) != 1 || got[0].Result != "reverted" || got[0].Landing != culprit {
		t.Fatalf("reverts=%+v", got)
	}
	if got := h.query(`SELECT state FROM items WHERE item_id='item-broke'`); len(got) != 1 || got[0] != StateInQueue {
		t.Fatalf("returned state=%v", got)
	}
	base := heavyGit(t, h.root, "rev-parse", TrunkRef)
	heavyGit(t, h.root, "reset", "--hard", base)
	if err := os.WriteFile(filepath.Join(h.root, path), []byte("corrected\n"), 0644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, h.root, "add", path)
	tree := heavyGit(t, h.root, "write-tree")
	head := heavyGit(t, h.root, "commit-tree", tree, "-p", base, "-m", "feat: corrected work")
	const runID = "run-item-broke-rework"
	heavyGit(t, h.root, "update-ref", "refs/tillr/runs/"+runID, head)
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('rework','idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,'item-broke','rework','fixture','finished',10,11,'[]',1)`, runID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Examined retries already reachable from trunk keep the ordinary bounded
	// history. The new rework head is off trunk and must remain pinned.
	for i := 0; i < RetryPinLimit+2; i++ {
		id := fmt.Sprintf("old-attempt-%02d", i)
		heavyGit(t, h.root, "update-ref", "refs/tillr/runs/"+id, base)
		if err := writeFixture(h.path, func(tx *store.Tx) error {
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,'item-broke','rework','fixture','finished',?,11,'[]',1)`, id, i); err != nil {
				return err
			}
			return appendEffect(tx, examinationDoneKind, id, struct{}{}, time.Now())
		}); err != nil {
			t.Fatal(err)
		}
	}
	heavyGit(t, h.root, "update-ref", "refs/tillr/landed/run-item-broke", culprit)
	if _, err := SweepRefs(ctx, RefSweepRequest{Root: h.root, Store: h.path}); err != nil {
		t.Fatal(err)
	}
	if got := heavyGit(t, h.root, "for-each-ref", "--format=%(refname)", "refs/tillr/runs/"+runID); got == "" {
		t.Fatal("sweep deleted new run's needed pin")
	}
	for i := 0; i < RetryPinLimit+2; i++ {
		id := fmt.Sprintf("old-attempt-%02d", i)
		exists := heavyGit(t, h.root, "for-each-ref", "--format=%(refname)", "refs/tillr/runs/"+id) != ""
		if exists != (i >= 2) {
			t.Fatalf("retry retention %s: exists=%v", id, exists)
		}
	}
	if got := heavyGit(t, h.root, "for-each-ref", "--format=%(refname)", "refs/tillr/landed/run-item-broke"); got != "" {
		t.Fatal("recorded per-run marker retained")
	}
	// Exercise a real independent item judge, bound to the corrected tree.
	now := time.Now().UTC()
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('item-broke','c1','the fix works','open','verification-only')`); err != nil {
			return err
		}
		// This is an external evaluator, not a request for drain to dispatch its
		// compatibility review. Record the review here before verifying rework.
		_, err := tx.Exec(`INSERT INTO workflow_selections(item_id,workflow,source,graph)
VALUES('item-broke','external-review@v1','block',json_remove(?, '$.compatibility_review'))`, store.CompatibilityJudgeGraph("external-review@v1", ""))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: "pass", Verdicts: []item.JudgeEntry{{CriterionID: "c1", CriterionHash: item.CriterionHash("the fix works"), Verdict: "done", ClaimClass: item.ClaimVerification, Reason: "fixed", Evidence: []string{"commit:" + head}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		binding, err := item.ReadEvaluatorBinding(tx.Reader(), "item-broke")
		if err != nil {
			return err
		}
		judged, err := item.CheckJudge(ctx, tx.Reader(), output, item.JudgeOptions{ItemID: "item-broke", Head: head, Tree: tree, Actor: "judge-run", JudgedActor: runID, EvaluatorBinding: binding, At: now})
		if err != nil {
			return err
		}
		if judged.Outcome != "pass" || len(judged.Records) != 1 {
			return fmt.Errorf("judge result: %+v", judged)
		}
		_, err = item.RecordVerdict(ctx, tx, judged.Records[0])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	worktree := h.loop.lanePath("rework")
	heavyGit(t, h.root, "worktree", "add", "--detach", worktree, head)
	if err := os.WriteFile(filepath.Join(filepath.Dir(worktree), "fence.txt"), []byte(path+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := Candidate{ItemID: "item-broke", RunID: runID, LaneID: "rework", BaseSHA: base, HeadSHA: head, Worktree: worktree}
	bin := scratchUnderDurable(t)
	config := filepath.Join(bin, "gitconfig")
	writeForkFile(t, config, "[user]\n name = Fixture\n email = fixture@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	declareSiblingInputs(t)
	for _, module := range []string{"tillr-land", "tillr-item"} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(bin, module), ".")
		cmd.Dir = filepath.Join("..", "..", module)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", module, err, out)
		}
	}
	h.loop.cfg.Steps = reworkLandingSteps{recoveryLandingSteps: &recoveryLandingSteps{t: t, ProcessSteps: &ProcessSteps{
		Root: h.root, Store: h.path, LaneRoot: h.loop.cfg.LaneRoot, Now: h.loop.cfg.Now,
		GateProgram: executable(t, `
set -eu
printf '%s' "$TILLR_ORDINARY_GATE_TIMING" > "$(dirname "$TILLR_GATE_SPANS_OUT")/observed-opt-in"
printf '%s' '{"payload":{"checks":[]}}' > "$TILLR_FAST_VERDICT_OUT"
printf '%s' '{"check_spans":[]}' > "$TILLR_GATE_SPANS_OUT"
`),
		ItemBin: filepath.Join(bin, "tillr-item"), LandProgram: "tillr-loop land --store " + h.path + " --tillr-land " + filepath.Join(bin, "tillr-land"), VerdictProgram: "tillr-loop verdict --tillr-item " + filepath.Join(bin, "tillr-item"),
	}}, judge: fileRed("a")}
	if _, landed, err := h.loop.Verify(ctx, c, &LoopResult{}, nil); err != nil || landed != wantLand {
		t.Fatalf("verify landed=%v err=%v\n%s", landed, err, h.report)
	}
	dir := RunDir(h.root, runID)
	if optIn, err := os.ReadFile(filepath.Join(dir, "observed-opt-in")); err != nil || len(optIn) != 0 {
		t.Errorf("relanding timing opt-in = %q, %v; want empty", optIn, err)
	}
	if _, err := os.Stat(filepath.Join(dir, gateTimingFile)); !os.IsNotExist(err) {
		t.Errorf("relanding wrote a timing record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, GateFastVerdictFile)); err != nil {
		t.Errorf("relanding lost its verdict handoff: %v", err)
	}
	if !wantLand {
		if !strings.Contains(h.report.String(), "gate passed item-broke, but a required full-suite check failed") {
			t.Fatalf("verification did not refuse the failing required check:\n%s", h.report)
		}
		if tip := heavyGit(t, h.root, "rev-parse", TrunkRef); tip != base {
			t.Fatalf("failed rework moved trunk to %s", tip)
		}
		return
	}
	if !strings.Contains(h.report.String(), "item-broke required check unit passes") {
		t.Fatalf("verification skipped the previously failing check:\n%s", h.report)
	}
	if got := heavyGit(t, h.root, "rev-parse", TrunkRef+"^{tree}"); got != tree {
		t.Fatalf("published tree=%s want %s", got, tree)
	}
	if rows := h.query(`SELECT run_id FROM landings WHERE item_id='item-broke' AND result='landed' ORDER BY rowid`); len(rows) != 2 || rows[0] != "run-item-broke" || rows[1] != runID {
		t.Fatalf("landing history=%v", rows)
	}
	if _, err := SweepRefs(ctx, RefSweepRequest{Root: h.root, Store: h.path}); err != nil {
		t.Fatal(err)
	}
	// The squash publishes the tree, not the authored commit; the off-trunk
	// pin remains the only copy the sweep can account for.
	if got := heavyGit(t, h.root, "rev-parse", "refs/tillr/runs/"+runID); got != head {
		t.Fatalf("rework pin=%s want %s", got, head)
	}
}
