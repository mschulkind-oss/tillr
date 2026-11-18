package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestFixerLegacyReadsIncludeUntouchedTest(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	if err := os.MkdirAll(filepath.Join(root, "tools/gate/checks"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, ".rolling"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools/gate/checks/fast.json"), `{"id":"fast","scope":"tree","tier":"fast"}`)
	writeForkFile(t, filepath.Join(root, "tools/fork-check.py"), `import json,sys
assert sys.argv[1:]==['--registry-json']
print(json.dumps({'declarations':[{'id':'storybook/roster','kind':'storybook','label':'regenerate the pi-subagents roster storybook','watches':[{'path':'src/'},{'path':'tests/untouched_test.go'},{'path':'tools/gate/'}]}]}))
`)
	legacy := executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"storybook/regenerate-the-pi-subagents-roster-storybook","kind":"storybook","label":"regenerate the pi-subagents roster storybook","status":"FAIL","attributes_json":"{\"writes\":[\"src/changed.go\"]}"}]}
JSON
exit 1`)
	heavyCommit(t, root, "bad")
	gate := executable(t, `printf '%s' '{"kind":"gate.verdict","payload":{"checks":[]}}'`)
	steps := &ProcessSteps{Root: root, LaneRoot: work, RollingProgram: legacy, HeavyGateBin: gate}
	runner := heavyFixture(t, root, steps.JudgeHeavy)
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checks := out.Runs[0].Checks
	f, l := passFixture(t)
	id, err := l.fileTrunkFixer(context.Background(), runGit(t, f.root, "rev-parse", "HEAD"), []string{"unit"}, GateOutcome{SuspectPaths: []string{"src/changed.go"}, CheckWatches: map[string][]string{"unit": checks[0].Reads}})
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var raw string
		if err := tx.QueryRow(`SELECT allowed_paths FROM items WHERE item_id=?`, id).Scan(&raw); err != nil {
			return err
		}
		if !strings.Contains(raw, "tests/untouched_test.go") {
			t.Fatalf("untouched test missing from fence: %s", raw)
		}
		if strings.Contains(raw, "tools/gate/") {
			t.Fatalf("gate admitted: %s", raw)
		}
		return nil
	})
}

func TestLegacyDynamicWatchesIncludeWholeSelection(t *testing.T) {
	root, _ := heavyRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tests/untouched_test.go"), "test")
	writeForkFile(t, filepath.Join(root, "tests/other_test.go"), "test")
	writeForkFile(t, filepath.Join(root, "outside.go"), "outside")
	heavyCommit(t, root, "watched sources")
	writeForkFile(t, filepath.Join(root, "tools/fork-check.py"), `import json
print(json.dumps({'declarations':[{'id':'dynamic','selected_by':[], 'selects':[{'regex':r'^tests/.*_test\.go$'}], 'watches':[{'paths':True}]}]}))
`)
	watches, err := legacyCheckWatches(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(watches["dynamic"], ",")
	for _, path := range []string{"tests/untouched_test.go", "tests/other_test.go"} {
		if !strings.Contains(got, path) {
			t.Fatalf("dynamic selection lost %s: %v", path, watches)
		}
	}
	if strings.Contains(got, "outside.go") {
		t.Fatalf("dynamic selection admitted unrelated file: %v", watches)
	}
}

func TestLegacyRegistryIncludesInProcessChecks(t *testing.T) {
	root, _ := heavyRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools/fork-check.py"), `import json
print(json.dumps({'declarations':[], 'in_process':[{'id':'file-size','watches':[]}]}))
`)
	watches, err := legacyCheckWatches(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := watches["file-size"]; !ok {
		t.Fatalf("in-process check absent: %v", watches)
	}
}

func TestFixerBroadWatchRetainsSafeSources(t *testing.T) {
	f, l := passFixture(t)
	for _, path := range []string{"tools/tillr-item/sync_test.go", "tools/gate/checks/unit.json", "tools/tillr-gate/run.go", "yolo-packs/tillr/files/local/share/tillr/tillr-gate/run.go"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(f.root, path)), 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(f.root, path), "fixture")
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "watched sources")
	red := runGit(t, f.root, "rev-parse", "HEAD")
	for i, watch := range []string{"tools/", "."} {
		id, err := l.fileTrunkFixer(context.Background(), red, []string{"unit"}, GateOutcome{SuspectPaths: []string{"README.md"}, CheckWatches: map[string][]string{"unit": {watch}}})
		if err != nil {
			t.Fatal(err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			var raw string
			if err := tx.QueryRow(`SELECT allowed_paths FROM items WHERE item_id=?`, id).Scan(&raw); err != nil {
				return err
			}
			if !strings.Contains(raw, "tools/tillr-item/sync_test.go") || strings.Contains(raw, "tools/gate/") || strings.Contains(raw, "tools/tillr-gate/") || strings.Contains(raw, "yolo-packs/tillr/files/local/share/tillr/tillr-gate/") {
				t.Fatalf("watch %d (%s) lost safe sources or admitted gate: %s", i, watch, raw)
			}
			return nil
		})
	}
}

func TestFixerBroadWatchUsesRedRevision(t *testing.T) {
	for _, watch := range []string{".", "tools/"} {
		t.Run(watch, func(t *testing.T) {
			f, l := passFixture(t)
			if err := os.MkdirAll(filepath.Join(f.root, "tools"), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, "tools/red.go"), "red source")
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "red sources")
			red := runGit(t, f.root, "rev-parse", "HEAD")
			writeForkFile(t, filepath.Join(f.root, "tools/later.go"), "later source")
			runGit(t, f.root, "rm", "tools/red.go")
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "later sources")
			id, err := l.fileTrunkFixer(context.Background(), red, []string{"unit"}, GateOutcome{CheckWatches: map[string][]string{"unit": {watch}}})
			if err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var raw string
				if err := tx.QueryRow(`SELECT allowed_paths FROM items WHERE item_id=?`, id).Scan(&raw); err != nil {
					return err
				}
				if !strings.Contains(raw, "tools/red.go") || strings.Contains(raw, "tools/later.go") {
					t.Fatalf("fence must use red revision, not the later checkout: %s", raw)
				}
				return nil
			})
		})
	}
}

func TestFixerRangeIncludesEveryCommitEvenRevertedPaths(t *testing.T) {
	f, l := passFixture(t)
	base := runGit(t, f.root, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(f.root, "culprit.go"), "bad")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "culprit")
	runGit(t, f.root, "rm", "culprit.go")
	runGit(t, f.root, "commit", "-qm", "remove culprit")
	writeForkFile(t, filepath.Join(f.root, "tip.go"), "tip")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "tip")
	tip := runGit(t, f.root, "rev-parse", "HEAD")
	paths := l.rangePaths(context.Background(), base, tip)
	id, err := l.fileTrunkFixer(context.Background(), tip, []string{"unit"}, GateOutcome{SuspectPaths: paths, CheckWatches: map[string][]string{"unit": {"tests/unit_test.go", "tools/", "tools/gate/", "tools/tillr-gate/run.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var raw string
		if err := tx.QueryRow(`SELECT allowed_paths FROM items WHERE item_id=?`, id).Scan(&raw); err != nil {
			return err
		}
		if !strings.Contains(raw, "culprit.go") {
			t.Fatalf("culprit two commits below tip missing from fence: %s", raw)
		}
		if strings.Contains(raw, `"tools/"`) || strings.Contains(raw, "tools/gate/") || strings.Contains(raw, "tools/tillr-gate/") {
			t.Fatalf("gate admitted to fence: %s", raw)
		}
		return nil
	})
}

func TestFixerAttachesThreeTipsAndEscalatesFenceReport(t *testing.T) {
	f, l := passFixture(t, "ordinary")
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	var first string
	for i, sha := range []string{"red1", "red2", "red3"} {
		id, err := l.fileTrunkFixer(context.Background(), sha, []string{"unit"}, GateOutcome{SuspectPaths: []string{sha + ".go"}, CheckWatches: map[string][]string{"unit": {"tests/unit_test.go"}}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		} else if id != first {
			t.Fatalf("tip %s filed %s, want attached %s", sha, id, first)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM items WHERE item_id LIKE 'fixer-main-%'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("%d fixers, want 1", count)
		}
		var raw string
		if err := tx.QueryRow(`SELECT allowed_paths FROM items WHERE item_id=?`, first).Scan(&raw); err != nil {
			return err
		}
		var paths []string
		if err := json.Unmarshal([]byte(raw), &paths); err != nil {
			return err
		}
		for _, p := range []string{"red1.go", "red2.go", "red3.go"} {
			if !strings.Contains(raw, p) {
				t.Fatalf("attachment lost %s: %v", p, paths)
			}
		}
		// Even a fixer with a body reference must not strand its fence refusal at design.
		_, err := tx.Exec(`UPDATE items SET body_ref='docs/repair.md' WHERE item_id=?`, first)
		return err
	})
	d := Decision{ItemID: first, RunRequest: &run.Request{RunID: "fixer-run"}}
	// The fixer reaches the owner only by naming him (blocked.go, FixerNextActorRule).
	report := "Fix needs `tests/outside_test.go`, outside the fence.\nNEXT-ACTOR: owner\nRESULT: blocked"
	if err := l.reportedBlock(context.Background(), d, report); err != nil {
		t.Fatal(err)
	}
	attached, err := l.fileTrunkFixer(context.Background(), "red4", []string{"unit"}, GateOutcome{SuspectPaths: []string{"red4.go"}})
	if err != nil || attached != first {
		t.Fatalf("attachment after owner hold: %s %v", attached, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		cs, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		c := cs[first]
		if c.WaitingOn != "owner" || c.Status != "owner" || !strings.Contains(c.Text, "tests/outside_test.go") {
			t.Fatalf("unanswered fence refusal: %+v", c)
		}
		answer, err := Why(tx.Reader(), Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "ordinary" {
				if row.Class != "needs you" || !strings.Contains(row.Mover, first) || !strings.Contains(row.Why, "check: unit") || !strings.Contains(row.Mover, "waiting on owner") {
					t.Fatalf("live waiting lost fixer context: %+v", row)
				}
				return nil
			}
		}
		t.Fatal("ordinary item missing from waiting")
		return nil
	})
}
