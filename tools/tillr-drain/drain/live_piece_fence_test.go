package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPlannedPieceUsesCurrentItemFence(t *testing.T) {
	for _, when := range []string{"before dispatch", "during run", "not granted"} {
		t.Run(when, func(t *testing.T) {
			f, l, _ := plannedFixture(t)
			expansionFile(t, f.root, "planned-build", `"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only","outputs":{"proposal":"graph.proposal"},"may_replace":{"work":["build-pieces@v1"]}},{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"plan","to_node":"work"}]`)
			runGit(t, f.root, "commit", "-qm", "test planned workflow without evaluator")
			const added = "new-scope/"
			add := func() {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := item.AddPath(tx, "item-a", added, "operator", "needed by the piece", l.cfg.Now().Add(time.Minute))
					return err
				})
			}
			var staged string
			steps := &obstacleRequestSteps{t: t, f: f, report: func(req run.Request) (string, *landingOffer) {
				switch req.NodeID {
				case "plan":
					proposalArtifact(t, f.root, req.RunID, pieceProposal([]any{piece("one", "build", "write")}))
				case "work/piece-one":
					lane := filepath.Join(f.laneRoot, req.LaneID)
					body, err := os.ReadFile(filepath.Join(lane, FenceFile))
					if err != nil {
						t.Error(err)
						return "", nil
					}
					staged = string(body)
					if when == "during run" {
						add()
					}
					target := filepath.Join(lane, WorktreeDir)
					if err := os.MkdirAll(filepath.Join(target, added), 0o755); err != nil {
						t.Error(err)
						return "", nil
					}
					if err := os.WriteFile(filepath.Join(target, added, "result.txt"), []byte("result\n"), 0o644); err != nil {
						t.Error(err)
						return "", nil
					}
					runGit(t, target, "add", added)
					runGit(t, target, "commit", "-qm", "build the piece")
				default:
					t.Errorf("unexpected node %s", req.NodeID)
				}
				return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
			}}
			l.cfg.Steps = steps
			result := &LoopResult{}
			graphPass(t, l, result)
			graphPass(t, l, result)
			if p := mustPlan(t, l)[0]; p.decision.RunRequest.NodeID != "work/piece-one" {
				t.Fatalf("proposal not installed: %+v", p.decision)
			}
			if when == "before dispatch" {
				add()
			}
			for i := 0; i < 4 && result.Landed == 0 && result.Rejected == 0; i++ {
				graphPass(t, l, result)
			}
			if when == "not granted" {
				if result.Rejected != 1 || !strings.Contains(scopedNodeRefusal(t, f).Detail, added+"result.txt") {
					t.Fatalf("ungranted path not refused: rejected=%d, refusal=%q", result.Rejected, scopedNodeRefusal(t, f).Detail)
				}
			} else if result.Landed != 1 || result.Rejected != 0 {
				t.Fatalf("granted path did not land: landed=%d, rejected=%d, refusal=%q", result.Landed, result.Rejected, scopedNodeRefusal(t, f).Detail)
			}
			if got := strings.Contains(staged, added+"\n"); got != (when == "before dispatch") {
				t.Fatalf("unexpected dispatch fence: %q", staged)
			}
		})
	}
}
