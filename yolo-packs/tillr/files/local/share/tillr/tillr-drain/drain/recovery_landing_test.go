package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Only the gate is a fixture. Landing, criterion verdicts, item-state changes,
// and lane cleanup go through the production processes in both paths.
type recoveryLandingSteps struct {
	*ProcessSteps
	t *testing.T
}

func (s *recoveryLandingSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	tree := runGit(s.t, c.Worktree, "rev-parse", c.HeadSHA+"^{tree}")
	payload := map[string]any{"verdict_id": "fixture-verdict", "plan_id": "fixture-plan", "registry_digest": "sha256:fixture", "base_sha": c.BaseSHA, "tree_sha": tree, "result": "pass", "checks": []any{}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return GateOutcome{}, err
	}
	body, err := record.Encode(record.Envelope{RecordVersion: record.SupportedVersion, Kind: GateVerdictKind, ID: "fixture-verdict", Producer: record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"}, CreatedAt: s.Now(), Payload: raw})
	if err != nil {
		return GateOutcome{}, err
	}
	path := filepath.Join(RunDir(s.Root, c.RunID), GateVerdictRecord)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return GateOutcome{}, err
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		return GateOutcome{}, err
	}
	err = appendJudgment(ctx, s.Store, GateVerdictKind, "run", c.RunID, payload, s.Now())
	return GateOutcome{Outcome: GatePassed}, err
}

func TestExamineLandsRealFixtureWithTheLoopsRecords(t *testing.T) {
	root := scratchUnderDurable(t)
	config := filepath.Join(root, "gitconfig")
	writeForkFile(t, config, "[user]\n name = tillr\n email = tillr@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	for _, module := range []string{"tillr-land", "tillr-item"} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(root, module), ".")
		cmd.Dir = filepath.Join("..", "..", module)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", module, err, out)
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-loop", "tillr-loop"))
	if err != nil {
		t.Fatal(err)
	}
	var baseline []string
	for _, manual := range []bool{false, true} {
		p := newPendingTerminalFixture(t, true, "")
		l := p.replacement(t, &terminalSteps{})
		steps := &recoveryLandingSteps{t: t, ProcessSteps: &ProcessSteps{Root: p.f.root, Store: p.f.storePath, LaneRoot: l.cfg.LaneRoot, Now: l.cfg.Now, ItemBin: filepath.Join(root, "tillr-item"), LandProgram: "python3 " + adapter + " land --store " + p.f.storePath + " --tillr-land " + filepath.Join(root, "tillr-land"), VerdictProgram: "python3 " + adapter + " verdict"}}
		l.cfg.Steps = steps
		var result LoopResult
		if manual {
			result, err = l.Examine(context.Background(), p.runID)
		} else {
			result, err = l.Run(context.Background())
		}
		if err != nil || result.Landed != 1 {
			t.Fatalf("manual=%v: %+v / %v", manual, result, err)
		}
		if content := runGit(t, p.f.root, "show", "main:base.txt"); content != "candidate" {
			t.Fatalf("not landed: %s", content)
		}
		var facts []string
		fixtureWrite(t, p.f, func(tx *store.Tx) error {
			var state, criteria, result, gated, landed string
			if err := tx.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT group_concat(state,',') FROM item_criteria WHERE item_id='item-a'`).Scan(&criteria); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT result,gated_tree,landed_tree FROM landings WHERE run_id=?`, p.runID).Scan(&result, &gated, &landed); err != nil {
				return err
			}
			facts = []string{state, criteria, result, gated, landed}
			return nil
		})
		for _, e := range eventsAfter(t, p.f.storePath, 0) {
			switch e.kind {
			case GateVerdictKind, "land.landing", "item.verdict", examinationDoneKind:
				facts = append(facts, e.kind)
			}
		}
		if !manual {
			baseline = facts
		} else if !reflect.DeepEqual(facts, baseline) {
			t.Fatalf("recovery records %v != loop %v", facts, baseline)
		}
	}
}
