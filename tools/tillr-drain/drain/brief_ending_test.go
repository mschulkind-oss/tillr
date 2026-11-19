package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestStagePinsWorkerEndingAndPreflightsBothBriefs(t *testing.T) {
	for _, capability := range []string{run.EndingFooter, run.EndingCallSuspends} {
		t.Run(capability, func(t *testing.T) {
			ctx := context.Background()
			f := newRefusalLoopFixture(t, "item-a")
			backends := filepath.Join("..", "..", "tillr-run", "backends")
			stager := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join(backends, "brief-template.md")}
			l := f.loop(t, &continuationSteps{}, stager)
			d := f.decisions()[0]
			adapter := filepath.Join(backends, "pi", "run-pi")
			d.RunRequest.Command = []string{adapter}
			l.endingCapabilities = map[string]string{adapter: capability}
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			staged, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{})
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage: %v %v", staged, err)
			}
			lane := l.laneDirPath(d.LaneID)
			assertEnding := func(dir, want string) {
				t.Helper()
				got, err := os.ReadFile(filepath.Join(dir, endingCapabilityFile))
				if err != nil || string(got) != want+"\n" {
					t.Fatalf("ending at %s: %q %v", dir, got, err)
				}
			}
			assertEnding(lane, capability)
			assertEnding(RunDir(f.root, d.RunRequest.RunID), capability)
			briefPath := filepath.Join(lane, BriefFile)
			raw, err := os.ReadFile(briefPath)
			if err != nil {
				t.Fatal(err)
			}
			brief := string(raw)
			if capability == run.EndingCallSuspends {
				if !strings.Contains(brief, finishing) || !strings.Contains(brief, "| `OBSTACLE:` |") || strings.Contains(brief, "Report shape") || strings.Contains(brief, "Stop conditions") || strings.Contains(brief, "RESULT: done") {
					t.Fatalf("wrong call brief:\n%s", brief)
				}
			} else if !strings.Contains(brief, "## Report shape\n") || !strings.Contains(brief, "## Stop conditions\n") || strings.Contains(brief, "## Finishing\n") {
				t.Fatalf("wrong footer brief:\n%s", brief)
			}
			cmd := exec.Command("sh", "-c", `. "$1"; PROG=test; preflight_brief "$2"`, "preflight", filepath.Join(backends, "preflight.sh"), briefPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("preflight: %v\n%s", err, out)
			}
			id, err := ReadSessionID(lane)
			if err != nil || id == "" {
				t.Fatalf("session: %q %v", id, err)
			}
			// Reverse the probe before restaging the same session. An older footer
			// session didn't even have an ending-capability file.
			next := run.EndingFooter
			if capability == run.EndingFooter {
				next = run.EndingCallSuspends
				if err := os.Remove(filepath.Join(lane, endingCapabilityFile)); err != nil {
					t.Fatal(err)
				}
			}
			spec := StageSpec{Policy: l.currentPolicy(), LaneID: d.LaneID, ItemID: d.ItemID, RunID: "run-resumed", Backend: d.RunRequest.Backend, WorkingDir: WorktreeDir, BaseSHA: base, Continue: true, SessionID: id, EndingCapability: next}
			if err := stager.Stage(ctx, spec); err != nil {
				t.Fatal(err)
			}
			assertEnding(lane, capability)
			assertEnding(RunDir(f.root, spec.RunID), capability)
			spec.SessionID = ""
			spec.RunID = "run-reader"
			spec.EndingCapability = run.EndingCallSuspends
			spec.Node = &WorkflowStage{ID: "scout", Kind: "agent", Seat: "build", Access: "read_only"}
			if err := stager.Stage(ctx, spec); err != nil {
				t.Fatal(err)
			}
			assertEnding(lane, run.EndingFooter)
		})
	}
}
