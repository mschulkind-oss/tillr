package drain_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

const landingItemDefinition = `### Landing work
<!-- tillr: item id="item-landing" paths="candidate.txt,base.txt" -->
- **c1:** first requirement
- **c2:** second requirement
`

func syncLandingItem(t *testing.T, f loopFixture, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "docs", "work.md"), body)
	git(t, f.root, "add", "docs/work.md")
	git(t, f.root, "commit", "-m", "item definition")
	cmd := exec.Command(sibling(t, "item"), "sync", "--store", f.store, "--root", f.root, "--json")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("item sync: %v: %s", err, out)
	}
}

type landingCriterion struct{ ID, Text, State string }

func landingCriteria(t *testing.T, path string) []landingCriterion {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got []landingCriterion
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT criterion_id,text,state FROM item_criteria WHERE item_id='item-landing' ORDER BY criterion_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c landingCriterion
			if err := rows.Scan(&c.ID, &c.Text, &c.State); err != nil {
				return err
			}
			got = append(got, c)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestVerdictCountsIgnoreScratchNamesAndArguments(t *testing.T) {
	invocations := []string{
		"/scratch/no-verdict/gate --item item-land --root /scratch/no-verdict/repo",
		"/scratch/no-verdict/land --item item-land --root /scratch/no-verdict/repo",
		"/scratch/no-verdict/verdict --item item-land --verdict pass",
		"/scratch/no-verdict/adapter --model verdict",
	}
	if got := countMatching(invocations, "verdict"); got != 1 {
		t.Fatalf("counted scratch names or arguments as verdict calls: %d", got)
	}
	if got := countMatching(invocations, "gate --item"); got != 1 {
		t.Fatalf("argv matching changed: %d", got)
	}
	if got := countMatching(invocations, "--verdict fail"); got != 0 {
		t.Fatalf("counted a failing verdict that was not invoked: %d", got)
	}
}

func TestNonLandingPreservesCriteriaAndAllowsSync(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict=%v", conflict), func(t *testing.T) {
			adapter := committingAdapter
			if conflict {
				adapter = conflictAdapter
			}
			f := newLoopFixture(t, stub(t, "adapter", adapter))
			syncLandingItem(t, f, landingItemDefinition)
			before := landingCriteria(t, f.store)
			verdictAdapter, err := filepath.Abs("../../tillr-loop/tillr-loop")
			if err != nil {
				t.Fatal(err)
			}
			verdict := fmt.Sprintf("python3 %q verdict --root %q --tillr-item %q", verdictAdapter, f.root, sibling(t, "item"))
			land := always(t, "land", 1)
			// A refused landing goes back to its worker with the evidence; the second,
			// identical refusal is a repeat, and with no design seat in the fixture's
			// policy it waits on the owner (the owner's ruling of 2026-11-17).
			step, reason := drain.RefusalLand, drain.ReasonConflictOwner
			if conflict {
				envelope := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
				land = conflictLandScript(t, f.root, envelope, envelope, 1)
				step, reason = drain.RefusalConflict, drain.ReasonConflictOwner
			}
			stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt", "base.txt"}}}
			steps := f.steps(t, always(t, "gate", 0), land, verdict)
			loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
				Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"},
				Policy: f.policy, Steps: steps, Stage: stage, Now: steps.Now, Report: os.Stderr,
				QuestionsCmd: strings.Fields(indexStub(t, map[string]any{"path": "docs/work.md", "questions": []any{}})),
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := loop.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, landingCriteria(t, f.store)) {
				t.Fatalf("landing changed criteria: before=%+v after=%+v", before, landingCriteria(t, f.store))
			}
			events := eventsOf(t, f.store)
			for _, e := range events {
				if e.Kind == "item.verdict" {
					t.Fatalf("non-landing recorded verdict: %+v", e)
				}
			}
			refusals := refusalsOf(t, events)
			want := 2
			if conflict {
				// Original run, conflict-fix run on the moved trunk, returned run, then
				// the fresh restart from the current trunk (D-JL12) which conflicts again
				// and parks for the owner.
				want = 4
			}
			if len(refusals) != want {
				t.Fatalf("refusals=%+v", refusals)
			}
			for _, r := range refusals {
				if r.step != step {
					t.Fatalf("refusal=%+v want step %s", r, step)
				}
			}
			if result.Dispatched != want || !strings.Contains(result.Detail, "item-landing ("+reason+")") {
				t.Fatalf("missing landing hold: %+v", result)
			}
			if conflict && (result.ConflictFixes != 1 || len(stage.specs) != 4 || stage.specs[1].Conflict == nil || stage.specs[3].Conflict != nil || refusals[0].fixRunID != stage.specs[1].RunID) {
				t.Fatalf("conflict repair changed: %+v %+v", result, stage.specs)
			}
			syncLandingItem(t, f, strings.ReplaceAll(landingItemDefinition, "requirement", "revised requirement"))
			after := landingCriteria(t, f.store)
			for i, c := range after {
				if c.State != before[i].State || !strings.Contains(c.Text, "revised requirement") {
					t.Fatalf("sync did not revise unjudged criterion: %+v", after)
				}
			}
		})
	}
}

func TestOpeningStorePreservesLegacyNotDoneCriteria(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, envelope("item.record", "legacy", map[string]any{
		"item_id": "item-landing", "title": "legacy failed landing", "state": "in-queue", "allowed_paths": []string{"candidate.txt"},
		"criteria": []any{map[string]any{"criterion_id": "c1", "text": "legacy requirement", "state": "not-done"}},
	}))
	before := landingCriteria(t, f.store)
	if len(before) != 1 || before[0].State != "not-done" {
		t.Fatalf("bad legacy fixture: %+v", before)
	}
	s, err := store.Open(context.Background(), f.store, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if after := landingCriteria(t, f.store); !reflect.DeepEqual(before, after) {
		t.Fatalf("opening rewrote legacy criteria: before=%+v after=%+v", before, after)
	}
}
