package drain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestProposalNodeUsesCurrentItemFenceOnRestart(t *testing.T) {
	for _, change := range []string{"item apply", "fence obstacle answer"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			const id = "item-a"
			f := newRefusalLoopFixture(t, id)
			stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
			const added = "new-scope/"
			steps := &proposalSteps{workflowSteps: &workflowSteps{t: t, f: f}, writePath: added + "result.txt"}
			l := f.loop(t, steps, stage)
			l.cfg.Lanes = []string{"lane-1"}
			l.cfg.Policy.Seats = map[string]string{"scout": "fixture", "build": "fixture"}
			holdWALKeeper(t, f.storePath)
			if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0755); err != nil {
				t.Fatal(err)
			}
			expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"plan","kind":"agent","seat":"scout","access":"read_only","outputs":{"proposal":"graph.proposal"},"may_replace":{"three":["replacement@v1"]}},{"id":"three","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"plan","to_node":"three"}]`)
			expansionFile(t, f.root, "replacement", `"stages":[{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[]`)
			runGit(t, f.root, "commit", "-qm", "proposal workflow")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id=?`, id)
				return err
			})

			result := &LoopResult{}
			graphPass(t, l, result)
			graphPass(t, l, result)
			p := mustPlan(t, l)[0]
			if got := p.decision.RunRequest.NodeID; got != "three/work" || p.decision.Workflow.Revision != 1 {
				t.Fatalf("proposal did not install its writer: %s", got)
			}
			deadOwnerClaim(t, f, l, p)
			work := pendingForTest(t, f, id)
			if staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, work.BaseSHA, result); err != nil || len(staged) != 1 {
				t.Fatalf("stage original writer: %v / %v", staged, err)
			}
			original := work.Decision.RunRequest.RunID
			oldFence, err := os.ReadFile(filepath.Join(RunDir(f.root, original), FenceFile))
			if err != nil || string(oldFence) != "tools/"+id+"/\n" {
				t.Fatalf("original fence: %q / %v", oldFence, err)
			}

			if change == "fence obstacle answer" {
				// Use the same synthetic answer document as the landed-answer tests.
				// The answer synchronizes the widened definition from the fixture trunk.
				if err := os.MkdirAll(filepath.Join(f.root, "briefs"), 0755); err != nil {
					t.Fatal(err)
				}
				doc, _, _ := strings.Cut(siblingDoc, "\n### Build item b")
				head := commitBriefs(t, f, strings.Replace(doc, "tools/item-a/", "tools/item-a/,"+added, 1))
				c := item.Conflict{ConflictID: "fence-conflict", ItemID: id, RunID: original,
					Status: "raised", WaitingOn: "design", BodyRef: "briefs/item-a.md#build-item-a",
					Text: "OBSTACLE: fence\nPATHS: " + added + "\nEVIDENCE: the work needs this directory\nRESULT: blocked\n"}
				fixtureWrite(t, f, func(tx *store.Tx) error { return item.RecordConflict(tx, c, l.cfg.Now()) })
				if err := l.recordBlockAnswer(ctx, Candidate{ItemID: id, HeadSHA: head, BlockConflict: &c}); err != nil {
					t.Fatal(err)
				}
			} else {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					var state string
					if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
						return err
					}
					_, err := item.Apply(ctx, tx, record.Envelope{RecordVersion: record.SupportedVersion, Kind: item.RecordKind, ID: "item-" + id,
						CreatedAt: l.cfg.Now(), Producer: record.Producer{System: record.System, Utility: Utility, Version: Version},
						Payload: []byte(mustJSON(map[string]any{"item_id": id, "title": "the item " + id, "state": state, "workflow": "scout-then-build@v1",
							"body_ref": "briefs/" + id + ".md", "allowed_paths": []string{"tools/" + id + "/", added},
							"criteria": []map[string]string{{"criterion_id": "c1", "text": "the first criterion of " + id, "state": ""}}})),
					})
					return err
				})
			}

			next := replaceGraphLoop(t, f, l, steps)
			for i := 0; i < 4 && result.Landed == 0; i++ {
				graphPass(t, next, result)
			}
			if len(steps.runs) != 2 || steps.runs[1].RunID == original || steps.runs[1].NodeID != work.Decision.RunRequest.NodeID {
				t.Fatalf("expected a fresh run of the same node: %+v", steps.runs)
			}
			runDir := RunDir(f.root, steps.runs[1].RunID)
			for _, file := range []string{ItemFenceFile, FenceFile} {
				body, err := os.ReadFile(filepath.Join(runDir, file))
				if err != nil || !slices.Contains(fenceEntries(string(body)), added) {
					t.Errorf("restarted %s misses %s: %q / %v", file, added, body, err)
				}
			}
			if result.Landed != 1 || steps.gates != 1 {
				t.Fatalf("widened change: landed=%d gates=%d refusals=%v", result.Landed, steps.gates, result.RefusedSteps)
			}
		})
	}
}
