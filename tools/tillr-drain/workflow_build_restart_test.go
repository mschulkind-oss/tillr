package main_test

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

func TestInPlaceRestartContinuesThirdWorkflowNode(t *testing.T) {
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
        if [ "$TILLR_SESSION_MODE" = resume ]; then
            git rev-parse HEAD > "$RESTART_TEST_DIR/$TILLR_NODE_ID.resumed"
        else
            printf '%s\n' "$TILLR_RUN_ID" >> "$RESTART_TEST_DIR/$TILLR_NODE_ID.started"
            while [ ! -f "$TILLR_PAUSE_REQUEST" ] && [ ! -f "$RESTART_TEST_DIR/release" ]; do sleep 0.02; done
            exit 0
        fi
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
	copyFile(t, bin, s.exe, nil)
	out, err := os.Create(s.out)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	s.cmd = exec.Command(s.exe, "loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", "1", "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"), "--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0))
	s.cmd.Stdout, s.cmd.Stderr = out, out
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		writeFile(t, filepath.Join(dir, "release"), "done")
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}()
	s.waitFor(t, "the third and fourth nodes to start", func() bool {
		return len(lines(t, filepath.Join(dir, "three.started"))) > 0 && len(lines(t, filepath.Join(dir, "four.started"))) > 0
	})
	target := filepath.Join(f.laneRoot, "lane-1", "worktree")
	head := strings.TrimSpace(git(t, target, "rev-parse", "HEAD"))
	s.replaceBinary(t)
	if answer := s.restart(t); !strings.Contains(answer, "restarted") {
		t.Fatalf("restart answer: %s", answer)
	}
	s.waitFor(t, "same-run continuation or unwanted redispatch", func() bool {
		for _, node := range []string{"three", "four"} {
			if len(lines(t, filepath.Join(dir, node+".started"))) > 1 {
				return true
			}
		}
		return len(lines(t, filepath.Join(dir, "three.resumed"))) > 0 && len(lines(t, filepath.Join(dir, "four.resumed"))) > 0
	})
	for _, node := range []string{"three", "four"} {
		started := lines(t, filepath.Join(dir, node+".started"))
		resumed := lines(t, filepath.Join(dir, node+".resumed"))
		if len(started) != 1 || len(resumed) != 1 || resumed[0] != head {
			t.Fatalf("%s: started=%v resumed=%v, want one run with head %s\n%s", node, started, resumed, head, s.output(t))
		}
		if !strings.Contains(s.output(t), "continuing paused run "+started[0]+" on lane-1 as the same attempt") {
			t.Errorf("no continuation decision for %s:\n%s", started[0], s.output(t))
		}
	}
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		var passed, kept, laterAttempts int
		if err := r.QueryRow(`SELECT count(*) FROM node_runs WHERE item_id='item_041' AND attempt=1 AND node_id IN ('one','two') AND outcome='passed'`).Scan(&passed); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM node_runs WHERE item_id='item_041' AND attempt=1 AND run_id IN (?,?)`, lines(t, filepath.Join(dir, "three.started"))[0], lines(t, filepath.Join(dir, "four.started"))[0]).Scan(&kept); err != nil {
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
