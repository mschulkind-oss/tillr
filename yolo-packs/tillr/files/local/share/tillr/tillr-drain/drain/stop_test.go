package drain

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestExplicitEndRecordedBeforeStartupPreventsALateRun(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := EndRuns(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	steps := ProcessSteps{Store: f.storePath}
	cmd := exec.Command("sleep", "60")
	if err := steps.startRunFace(cmd, *p.decision.RunRequest); err == nil || cmd.Process != nil {
		t.Fatalf("late run started after explicit end: %v", err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if !works["item-a"].Orphaned {
			t.Fatal("stop was not recorded before startup")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// An interruption refuses the late start of the segment it ended, not a later,
// deliberate continuation of the same run: run.resume reopens it (a0e8450d7).
func TestExplicitEndDoesNotRefuseALaterContinuation(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := EndRuns(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	steps := ProcessSteps{Store: f.storePath}
	req := *p.decision.RunRequest
	req.Resume = true
	cmd := exec.Command("true")
	if err := steps.startRunFace(cmd, req); err != nil {
		t.Fatalf("continuation refused after explicit end: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSlowFaceStartupDoesNotHoldTheStoreWriter(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	steps := ProcessSteps{Store: f.storePath}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- steps.startRun("slow-start", func() error { close(started); <-release; return nil })
	}()
	<-started
	defer func() {
		close(release)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // hang guard: a held writer blocks until release, load only slows Open
	defer cancel()
	s, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatalf("face startup blocked unrelated store migration: %v", err)
	}
	s.Close()
}

func TestRunFaceDiscoveryExcludesAdapterDescendants(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	cmd := exec.Command("sleep", "60")
	cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+p.decision.RunRequest.RunID, "TILLR_LOOP_STORE="+f.storePath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	if owners, err := liveRunFaces(f.storePath, p.decision.RunRequest.RunID, here); err != nil || len(owners) != 0 {
		t.Fatalf("adapter mistaken for run face: %v %v", owners, err)
	}
}
