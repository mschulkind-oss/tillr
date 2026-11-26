package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSyncedRoadmapFirstEntryDispatchesFirst(t *testing.T) {
	f, l := passFixture(t)
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s\n' '{"index":{"documents":[{"path":"docs/first.md","stage":"DESIGN","questions":[]},{"path":"docs/second.md","stage":"DESIGN","questions":[]},{"path":"docs/other.md","stage":"DESIGN","questions":[]}]}}'`}
	for _, dir := range []string{"docs/a", "docs/b"} {
		if err := os.MkdirAll(filepath.Join(f.root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	block := func(id string) string {
		return fmt.Sprintf("### %s\n<!-- tillr: item id=\"%s\" paths=\"tools/%s/\" -->\n- **c1:** Works\n", id, id, id)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/a/roadmap.md"), "## Next\n[First](../first.md)\n[Second](../second.md)\n")
	writeForkFile(t, filepath.Join(f.root, "docs/b/roadmap.md"), "## Next\n[Other](../other.md)\n")
	writeForkFile(t, filepath.Join(f.root, "docs/first.md"), block("item-z-first"))
	writeForkFile(t, filepath.Join(f.root, "docs/second.md"), block("item-b-second"))
	writeForkFile(t, filepath.Join(f.root, "docs/other.md"), block("item-a-other"))
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "fixture roadmaps")
	binary := filepath.Join(scratchUnderDurable(t), "tillr-item")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "../../tillr-item"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build item binary: %v\n%s", err, out)
	}
	steps := &ProcessSteps{Root: f.root, Store: f.storePath, ItemBin: binary, Now: l.cfg.Now}
	outcome, err := steps.Sync(context.Background())
	if err != nil || outcome.Created != 3 || len(outcome.Findings) != 0 {
		t.Fatalf("sync: %+v / %v", outcome, err)
	}
	plans := mustPlan(t, l)
	if len(plans) == 0 || plans[0].decision.Action != ActionDispatch || plans[0].decision.ItemID != "item-z-first" {
		t.Fatalf("first dispatch: %+v", plans)
	}
}
