package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTriageResumeCannotBroadenToTheBuildFence(t *testing.T) {
	f, l := triageFixture(t)
	p := findTriagePlan(t, l, "item-request")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, w, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	id := w.Decision.RunRequest.RunID
	fence, err := rederiveFence(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: l.cfg.LaneRoot}, resumeRun{ItemID: p.decision.ItemID}, l.laneDirPath(p.decision.LaneID), id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fence, ",") != "docs/request.md" {
		t.Fatalf("resumed build fence: %v", fence)
	}
	for _, dir := range []string{l.laneDirPath(p.decision.LaneID), RunDir(f.root, id)} {
		raw, err := os.ReadFile(filepath.Join(dir, FenceFile))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "docs/request.md\n" {
			t.Fatalf("persisted broader fence: %s", raw)
		}
	}
}
