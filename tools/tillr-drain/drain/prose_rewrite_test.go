package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func proseRewriteFixture(t *testing.T) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := triageFixture(t)
	bad := strings.Replace(triageBlock, ` prose="2"`, "", 1)
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), bad)
	writeForkFile(t, filepath.Join(f.root, ".tillr.toml"), "[prose]\nrewrite = true\n")
	if err := os.MkdirAll(filepath.Join(f.root, ".agents/skills/tillr-prose-style"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, ".agents/skills/tillr-prose-style/SKILL.md"), "Whole prose guide\nKeep the contract.\n")
	runGit(t, f.root, "add", "docs", ".tillr.toml", ".agents")
	runGit(t, f.root, "commit", "-qm", "unmarked request")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Root: f.root, Documents: []item.SyncDocument{{Path: "docs/request.md", Content: []byte(bad)}}, At: l.cfg.Now()})
		return err
	})
	return f, l
}

func TestProseRewriteHoldAndRelease(t *testing.T) {
	f, l := proseRewriteFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET rank='0' WHERE item_id='item-declared'`)
		return err
	})
	plans := mustPlan(t, l)
	if plans[0].decision.ItemID != "rewrite-item-declared" {
		t.Fatalf("rewrite not ahead of lowest ordinary rank: %+v", plans[0].decision)
	}
	d := findTriagePlan(t, l, "item-declared").decision
	if d.Reason != "awaits-rewrite" || d.DeferredBy != "rewrite-item-declared" || d.Action != ActionHold {
		t.Fatalf("not held: %+v", d)
	}
	rewrite := findTriagePlan(t, l, "rewrite-item-declared").decision
	if rewrite.Action != ActionDispatch || rewrite.Seat != "build" || rewrite.Reason == ReasonTriageNoBlock {
		t.Fatalf("rewrite not dispatched: %+v", rewrite)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='rewrite-item-declared'`)
		return err
	})
	if d := findTriagePlan(t, l, "item-declared").decision; d.Reason == "awaits-rewrite" {
		t.Fatalf("landed rewrite still holds: %+v", d)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue' WHERE item_id='rewrite-item-declared'`)
		return err
	})
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), triageBlock)
	if d := findTriagePlan(t, l, "item-declared").decision; d.Reason == "awaits-rewrite" {
		t.Fatalf("passing block still holds: %+v", d)
	}
}

func TestProseRewriteImpossibleCriteriaWaitOnOwner(t *testing.T) {
	f, l := proseRewriteFixture(t)
	raw := triageBlock
	for i := 2; i <= 9; i++ {
		raw += fmt.Sprintf("- **c%d:** Keep outcome %d.\n", i, i)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), raw)
	d := findTriagePlan(t, l, "rewrite-item-declared").decision
	if d.Action != ActionHold || !strings.HasPrefix(d.Reason, "prose-criteria-count:") {
		t.Fatalf("impossible rewrite dispatched: %+v", d)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, l.questions)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "rewrite-item-declared" {
				if row.Class != "needs you" || !strings.Contains(row.Mover, "owner") || !strings.Contains(row.Why, "preserving them cannot pass") {
					t.Fatalf("owner cause not shown: %+v", row)
				}
				return nil
			}
		}
		t.Fatal("missing impossible rewrite")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Owner reconciles the excess criteria and leaves prose for the cheap rewrite.
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), strings.Replace(triageBlock, ` prose="2"`, "", 1))
	if d := findTriagePlan(t, l, "rewrite-item-declared").decision; d.Action != ActionDispatch {
		t.Fatalf("owner repair did not release rewrite: %+v", d)
	}
}

func TestProseRewriteWhyShowsCurrentMover(t *testing.T) {
	f, l := proseRewriteFixture(t)
	s := openFixture(t, f.storePath)
	defer s.Close()
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, l.questions)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "item-declared" {
				if row.Class != "waits on work" || row.Mover != "rewrite-item-declared" || row.Why != "awaits-rewrite" {
					t.Fatalf("wrong current face: %+v", row)
				}
				return nil
			}
		}
		t.Fatal("missing original from current face")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProseRewriteBriefCarriesWholeGuideAndFindings(t *testing.T) {
	f, l := proseRewriteFixture(t)
	s := &LaneStager{Root: f.root, Store: f.storePath}
	text, err := s.proseRewriteTask(context.Background(), "rewrite-item-declared")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Whole prose guide\nKeep the contract.\n", "prose-unmarked", "item-declared", "id, paths, blocked_by and seat"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q: %s", want, text)
		}
	}
	_ = l
}

func TestProseRewriteContractRefusedBeforeGate(t *testing.T) {
	for _, change := range []string{"id", "paths", "blocked_by", "seat", "add", "remove", "number", "unmarked", "valid"} {
		t.Run(change, func(t *testing.T) {
			f, l := proseRewriteFixture(t)
			p := findTriagePlan(t, l, "rewrite-item-declared")
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			target := l.laneTarget(p.decision.LaneID)
			runGit(t, target, "reset", "--hard", base)
			raw := triageBlock
			switch change {
			case "id":
				raw = strings.Replace(raw, "item-declared", "item-renamed", 1)
			case "paths":
				raw = strings.Replace(raw, `paths="docs/"`, `paths="tools/"`, 1)
			case "blocked_by":
				raw = strings.Replace(raw, `prose="2"`, `prose="2" blocked_by="item-declared"`, 1)
			case "seat":
				raw = strings.Replace(raw, `prose="2"`, `prose="2" seat="design"`, 1)
			case "add":
				raw += "- **c2:** New criterion.\n"
			case "remove":
				raw = strings.Replace(raw, "- **c1:** The feature answers the request.\n", "", 1)
			case "number":
				raw = strings.Replace(raw, "**c1:", "**c2:", 1)
			case "unmarked":
				raw = strings.Replace(raw, ` prose="2"`, "", 1)
			}
			writeForkFile(t, filepath.Join(target, "docs/request.md"), raw)
			runGit(t, target, "add", "docs")
			runGit(t, target, "commit", "--allow-empty", "-qm", "rewrite contract")
			writeForkFile(t, filepath.Join(l.laneDirPath(p.decision.LaneID), FenceFile), "docs/request.md\n")
			c := Candidate{ItemID: "rewrite-item-declared", RunID: p.decision.RunRequest.RunID, LaneID: p.decision.LaneID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD")}
			spy := &triageGateSpy{}
			l.cfg.Steps = spy
			_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
			if change == "valid" {
				if err != nil || !landed || spy.gates != 1 {
					t.Fatalf("valid rewrite did not land: gates=%d landed=%v err=%v", spy.gates, landed, err)
				}
				return
			}
			if err != nil || landed || spy.gates != 0 {
				t.Fatalf("reached gate: gates=%d landed=%v err=%v", spy.gates, landed, err)
			}
			if c.RefusalDetail == "" {
				t.Fatal("missing changed field detail")
			}
		})
	}
}
