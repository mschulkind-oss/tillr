package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestInPlaceRestartKeepsLiveWorkflowReaders(t *testing.T) {
	dir := scratch(t, "workflow-build-restart")
	t.Setenv("RESTART_TEST_DIR", dir)
	adapter := stub(t, "adapter", `set -eu
case "$TILLR_NODE_ID" in
    one|two)
        printf '%s\n' "$TILLR_NODE_ID" >> candidate.txt
        git add candidate.txt
        git commit -qm "$TILLR_NODE_ID"
        ;;
    three|four)
        printf '%s %s\n' "$TILLR_RUN_ID" "$TILLR_SESSION_MODE" >> "$RESTART_TEST_DIR/$TILLR_NODE_ID.started"
        while [ ! -f "$RESTART_TEST_DIR/release" ]; do sleep 0.02; done
        ;;
    last)
        while [ ! -f "$RESTART_TEST_DIR/release" ]; do sleep 0.02; done
        printf 'last\n' >> candidate.txt; git add candidate.txt; git commit -qm last
        ;;
    *) exit 7 ;;
esac
printf 'RESULT: done\n'
`)
	writeFile(t, filepath.Join(filepath.Dir(adapter), "pause-kind"), run.PauseAtGap+"\n")
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "candidate.txt"))
	ctx := context.Background()
	db, err := store.Open(ctx, f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='build' WHERE item_id='item_041'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "config/tillr/workflows/restart@v1.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, `{"name":"restart","version":1,"stages":[
		{"id":"one","kind":"agent","seat":"build","access":"write"},
		{"id":"two","kind":"agent","seat":"build","access":"write"},
		{"id":"three","kind":"agent","seat":"scout","access":"read_only"},
		{"id":"four","kind":"agent","seat":"scout","access":"read_only"},
		{"id":"last","kind":"agent","seat":"build","access":"write"}],"edges":[
		{"from_node":"one","to_node":"two"},{"from_node":"two","to_node":"three"},
		{"from_node":"two","to_node":"four"},{"from_node":"three","to_node":"last"},
		{"from_node":"four","to_node":"last"}]}`)
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[workflows.default]\nbuild = 'restart@v1'\n")
	git(t, f.root, "add", ".")
	git(t, f.root, "commit", "-qm", "workflow fixture")
	f.policy.Seats = map[string]string{"build": "fixture", "scout": "fixture"}
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(dir, "policy.json")
	writeFile(t, policy, string(raw))
	s := &rollScenario{f: f, exe: filepath.Join(dir, "tillr-drain"), out: filepath.Join(dir, "loop.out")}
	copyFile(t, drainBin(t), s.exe, nil)
	out, err := os.Create(s.out)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	s.cmd = exec.Command(s.exe, "loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", "2", "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), "--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0))
	s.cmd.Stdout, s.cmd.Stderr = out, out
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.recordLoopOwner()
	t.Cleanup(func() { s.stop(t) })
	s.waitFor(t, "the third and fourth nodes to start", func() bool {
		return len(lines(t, filepath.Join(dir, "three.started"))) > 0 && len(lines(t, filepath.Join(dir, "four.started"))) > 0
	})
	first := map[string]string{}
	for _, node := range []string{"three", "four"} {
		first[node] = strings.Fields(lines(t, filepath.Join(dir, node+".started"))[0])[0]
	}
	s.replaceBinary(t)
	if answer := s.restart(t); !strings.Contains(answer, "restarted") {
		t.Fatalf("restart answer: %s", answer)
	}
	writeFile(t, filepath.Join(dir, "release"), "done")
	s.waitFor(t, "the replacement loop to advance past both readers", func() bool {
		var advanced bool
		if err := db.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM node_runs WHERE item_id='item_041' AND attempt=1 AND node_id='last')`).Scan(&advanced)
		}); err != nil {
			t.Fatal(err)
		}
		return advanced
	})
	for _, node := range []string{"three", "four"} {
		if started := lines(t, filepath.Join(dir, node+".started")); len(started) != 1 || strings.Fields(started[0])[0] != first[node] {
			t.Fatalf("%s restarted: %v\n%s", node, started, s.output(t))
		}
	}
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		var passed, kept, laterAttempts int
		if err := r.QueryRow(`SELECT count(*) FROM node_runs WHERE item_id='item_041' AND attempt=1 AND node_id IN ('one','two') AND outcome='passed'`).Scan(&passed); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM node_runs WHERE item_id='item_041' AND attempt=1 AND outcome='passed' AND run_id IN (?,?)`, first["three"], first["four"]).Scan(&kept); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM node_runs WHERE item_id='item_041' AND attempt>1`).Scan(&laterAttempts); err != nil {
			return err
		}
		if passed != 2 || kept != 2 || laterAttempts != 0 {
			t.Errorf("passed predecessors=%d retained readers=%d later attempts=%d", passed, kept, laterAttempts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInPlaceRestartWorkflowReadersEndsFixtureProcesses(t *testing.T) {
	assertNoMarkedProcessAliveAfter(t, "workflow_build_restart", TestInPlaceRestartKeepsLiveWorkflowReaders)
}
