package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// A mirrored design item (the item-design-<slug> row `tillr item sync` creates for a
// design the roadmap links) names its whole design document as its body_ref, with no
// anchor and no item block. On 2026-11-27 such an item's first candidate edited that
// document, as its criterion asked, and the loop refused it at the fence step as a
// worker editing its own item block, so no mirrored design item could land. The
// document is the work product, so the edit reaches the gate; an ordinary item that
// edits its own item block is still refused.
func TestAMirroredDesignItemThatEditsItsDesignReachesTheGate(t *testing.T) {
	cases := []struct {
		name, id, ref, doc, edit string
		refused                  bool
	}{
		{
			name: "mirrored design item edits its design",
			// No record is seeded: the loop's own sync mirrors the routed design.
			id:   "item-design-design-fixture",
			doc:  "---\nstage: DESIGN\n---\n\n# Fixture design\n\nAn open choice.\n",
			edit: "---\nstage: DECIDED\n---\n\n# Fixture design\n\nThe choice, settled.\n",
		},
		{
			name: "ordinary item edits its own item block",
			id:   "item-target", ref: "docs/design/fixture.md#fixture",
			doc:     "# Fixture\n<!-- tillr: item id=\"item-target\" paths=\"candidate.txt,docs/design/fixture.md\" -->\n- **c1:** Make the candidate.\n",
			edit:    "# Fixture\n<!-- tillr: item id=\"item-target\" paths=\"candidate.txt,docs/design/fixture.md\" -->\n- **c1:** Make something easier.\n",
			refused: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := counting + committingAdapterBody + `printf '%s' "$FIXTURE_EDIT" > docs/design/fixture.md
git add docs/design/fixture.md
git -c user.name=stub -c user.email=stub@example.invalid commit -q --amend --no-edit
printf 'RESULT: done\n'
`
			t.Setenv("FIXTURE_EDIT", tc.edit)
			f := newLoopFixture(t, stub(t, "adapter", adapter))
			if err := os.MkdirAll(filepath.Join(f.root, "docs", "design"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(f.root, "docs", "design", "fixture.md"), tc.doc)
			git(t, f.root, "add", "docs/design/fixture.md")
			if tc.ref == "" {
				// The roadmap routes the design, so the loop's own sync mirrors it as an item.
				writeFile(t, filepath.Join(f.root, "docs", "roadmap.md"), "# Roadmap\n\n- [Fixture](design/fixture.md)\n")
				git(t, f.root, "add", "docs/roadmap.md")
				// An open design mirrors to the design seat.
				f.policy.Seats = map[string]string{"design": "fixture", "build": "fixture"}
			}
			git(t, f.root, "commit", "-m", "fixture design")
			if tc.ref == "" {
				fixtureAt(t, f.store)
			} else {
				fixtureAt(t, f.store, envelope("item.record", "item-"+tc.id, map[string]any{
					"item_id": tc.id, "title": "Fixture design", "state": "in-queue", "seat": "build",
					"body_ref": tc.ref, "allowed_paths": []string{"candidate.txt", "docs/design/fixture.md"},
					"blocked_by": []string{}, "criteria": []map[string]string{{"criterion_id": "c1", "text": "Advance the design", "state": ""}},
				}))
			}
			fence := []string{"candidate.txt", "docs/design/fixture.md"}
			report := &strings.Builder{}
			steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
			questions := strings.Fields(indexStub(t, map[string]any{"path": "docs/design/fixture.md", "questions": []any{}}))
			steps.QuestionsCmd = questions
			loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
				Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"},
				Policy: f.policy, Steps: &evidenceProcessSteps{steps}, Stage: passStager{laneRoot: f.laneRoot, fence: fence},
				Now: steps.Now, Report: report,
				QuestionsCmd: questions,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := loop.Run(context.Background())
			if err != nil {
				t.Fatalf("%v; %s", err, report)
			}
			edited := strings.Contains(report.String(), "worker edited its own item block")
			gated := countMatching(lines(t, f.count), "gate")
			if tc.refused {
				if !edited || gated != 0 || result.Landed != 0 {
					t.Fatalf("an edit to the item's own block must be refused before the gate: result %+v, gate runs %d; %s", result, gated, report)
				}
				return
			}
			if edited || gated == 0 || result.Landed != 1 {
				t.Fatalf("the design edit must reach the gate and land: result %+v, gate runs %d; %s", result, gated, report)
			}
		})
	}
}
