package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestTriageCycleIsRefusedBeforeGateWithoutPersistingSync(t *testing.T) {
	f, l := triageFixture(t)
	declared := strings.Replace(triageBlock, `prose="2"`, `prose="2" blocked_by="item-request"`, 1)
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), declared)
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "declare dependency")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Root: f.root, Documents: []item.SyncDocument{{Path: "docs/request.md", Content: []byte(declared)}}, At: l.cfg.Now()})
		return err
	})
	d := findTriagePlan(t, l, "item-request").decision
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	target := l.laneTarget(d.LaneID)
	runGit(t, target, "reset", "--hard", base)
	requested := strings.Replace(strings.ReplaceAll(triageBlock, "item-declared", "item-request"), `prose="2"`, `prose="2" blocked_by="item-declared"`, 1)
	writeForkFile(t, filepath.Join(target, "docs/request.md"), declared+requested)
	runGit(t, target, "add", "docs")
	runGit(t, target, "commit", "-qm", "cyclic triage block")
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "docs/request.md\n")
	c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD"), BlockConflict: d.BlockConflict}
	before := eventCount(t, f.storePath)
	detail, err := l.validateTriage(context.Background(), c)
	if err != nil || !strings.Contains(detail, "edge-cycle") {
		t.Fatalf("cycle admitted: %q %v", detail, err)
	}
	if after := eventCount(t, f.storePath); after != before {
		t.Fatalf("admission persisted %d events", after-before)
	}
	spy := &triageGateSpy{}
	l.cfg.Steps = spy
	_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
	if err != nil || landed || spy.gates != 0 {
		t.Fatalf("cycle reached gate: %v %v %d", err, landed, spy.gates)
	}
}

func TestTriageAdmissionSupportsNonASCIIReference(t *testing.T) {
	f, l := triageFixture(t)
	d := findTriagePlan(t, l, "item-request").decision
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	target := l.laneTarget(d.LaneID)
	runGit(t, target, "reset", "--hard", base)
	// Git's default quotePath setting quotes this name without -z.
	file := "docs/café.md"
	writeForkFile(t, filepath.Join(target, file), strings.ReplaceAll(triageBlock, "item-declared", "item-request"))
	runGit(t, target, "add", "docs")
	runGit(t, target, "commit", "-qm", "declare non-ASCII document")
	conflict := *d.BlockConflict
	conflict.BodyRef = file + "#add-the-requested-feature"
	c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD"), BlockConflict: &conflict}
	if detail, err := l.validateTriage(context.Background(), c); err != nil || detail != "" {
		t.Fatalf("non-ASCII document rejected: %q %v", detail, err)
	}
}

func TestOlderTriageResumeKeepsItsOriginalDocumentFence(t *testing.T) {
	f, l := triageFixture(t)
	p := findTriagePlan(t, l, "item-request")
	ok, old, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim old: %v %v", ok, err)
	}
	// A newer request's durable dispatch replaces the item's current work, but
	// must not erase the older run's original authority from the run history.
	newer := *old
	req := *old.Decision.RunRequest
	req.RunID += "-new-version"
	newer.Decision.RunRequest = &req
	conflict := *old.Decision.BlockConflict
	conflict.BodyRef = "docs/other.md#request"
	conflict.TriageVersion = "new-version"
	newer.Decision.BlockConflict = &conflict
	fixtureWrite(t, f, func(tx *store.Tx) error { return appendEffect(tx, dispatchStartedKind, req.RunID, &newer, l.cfg.Now()) })
	fence, err := rederiveFence(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath}, resumeRun{ItemID: p.decision.ItemID}, l.laneDirPath(p.decision.LaneID), old.Decision.RunRequest.RunID)
	if err != nil || strings.Join(fence, ",") != "docs/request.md" {
		t.Fatalf("old resume regained build authority: %v %v", fence, err)
	}
}
