package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestFiledRequestStoredWorkflowResolvesFromInstallation(t *testing.T) {
	installRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "yolo-packs", "tillr", "files", "local", "share", "tillr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)

	f, l, _ := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["plan"] = "build"
	l.cfg.Policy.Seats["verify"] = "build"
	l.cfg.Policy.ReadOnlySeats = append(l.cfg.Policy.ReadOnlySeats, "plan", "verify")
	// Ensure the operated repository has no config/tillr/workflows directory.
	if err := os.RemoveAll(filepath.Join(f.root, "config", "tillr", "workflows")); err != nil {
		t.Fatal(err)
	}

	filing := item.RequestFiling{
		Words:       []byte("Build a change in a consumer repository with no workflow files.\n"),
		Title:       "Consumer repo request",
		Seat:        "build",
		Destination: item.Destination{To: "last"},
		At:          time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		By:          "owner",
		Via:         "cli",
	}
	if err := item.RequestWorkflowCheck(f.root, item.RequestDefaultWorkflow[filing.Seat]); err != nil {
		t.Fatalf("RequestWorkflowCheck: %v", err)
	}
	var itemID, storedWorkflow string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.FileRequest(context.Background(), tx, filing)
		return err
	})
	st, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT item_id, workflow FROM items JOIN request_words USING(item_id)`).Scan(&itemID, &storedWorkflow)
	}); err != nil {
		t.Fatal(err)
	}
	if storedWorkflow != "planned-build@v1" {
		t.Fatalf("stored workflow = %q, want planned-build@v1", storedWorkflow)
	}

	// Stored default workflow resolves from the tillr installation.
	w, err := loadWorkflow(f.root, storedWorkflow, l.cfg.Policy)
	if err != nil {
		t.Fatalf("loadWorkflow(%q) failed: %v", storedWorkflow, err)
	}
	if len(w.Stages) < 3 || w.Stages[0].ID != "plan" {
		t.Fatalf("unexpected planned-build@v1 stages: %+v", w.Stages)
	}
	// Workflow named by planned-build@v1 (build-pieces@v1) also resolves from the installation.
	d := Decision{
		ItemID:     itemID,
		LaneID:     "lane-1",
		Workflow:   w,
		NodeIndex:  0,
		RunRequest: &run.Request{RunID: "run-" + itemID + "-a1-n1"},
	}
	proposal := `{"target":"work","workflow":"build-pieces@v1","args":{"pieces":[{"id":"one","seat":"build","access":"write","prompt":"do it","write_scope":["tools/"]}]}}`
	artifact := proposalArtifact(t, f.root, d.RunRequest.RunID, proposal)
	if err := l.acceptNodeSettlement(context.Background(), &d, []nodeArtifact{artifact}); err != nil {
		t.Fatalf("acceptNodeSettlement(build-pieces@v1): %v", err)
	}
	if len(d.Workflow.Stages) < 3 || !strings.HasPrefix(d.Workflow.Stages[1].ID, "work/") {
		t.Fatalf("expected build-pieces@v1 to expand into work/*, got stages: %+v", d.Workflow.Stages)
	}

	// A workflow file in the operated repository wins over the installed one of the same name.
	repoWF := filepath.Join(f.root, "config", "tillr", "workflows", "planned-build@v1.json")
	if err := os.MkdirAll(filepath.Dir(repoWF), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := `{"name":"planned-build","version":1,"stages":[{"id":"custom-only","kind":"agent","seat":"build","access":"write"}],"edges":[]}`
	writeForkFile(t, repoWF, custom)
	runGit(t, f.root, "add", "config/tillr/workflows/planned-build@v1.json")
	repoW, err := loadWorkflow(f.root, "planned-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatalf("loadWorkflow with repo override failed: %v", err)
	}
	if len(repoW.Stages) != 1 || repoW.Stages[0].ID != "custom-only" {
		t.Fatalf("expected repo workflow to win over installed one, got stages: %+v", repoW.Stages)
	}
}
