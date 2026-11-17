package drain

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestParallelReadersRestartOnlyInterruptedNodes(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.interrupt = true
	steps.writerAfter = 6
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, loopOrphanedKind, "run-item-a-a1-n1", struct{}{}, l.cfg.Now())
	})
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.started != 6 || steps.builders != 1 {
		t.Fatalf("started=%d builders=%d, want only two re-executions", steps.started, steps.builders)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		for i := 1; i <= 4; i++ {
			var id, outcome string
			err := r.QueryRow(`SELECT run_id,outcome FROM node_runs WHERE node_id=?`, fmt.Sprintf("scout%d", i)).Scan(&id, &outcome)
			if err != nil {
				return err
			}
			if strings.HasSuffix(id, "-restart") != (i >= 3) || outcome != "passed" {
				t.Errorf("scout%d: %s %s", i, id, outcome)
			}
		}
		return nil
	})
	if result.Tokens != 60 {
		t.Fatalf("spent interrupted runs disappeared: %d tokens", result.Tokens)
	}
}

func TestParallelReadersEndRunsFindsSiblingsAfterPrimaryFinished(t *testing.T) {
	f, l, _ := parallelFixture(t)
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`DELETE FROM runs WHERE run_id IN ('run-item-a-a1-n3','run-item-a-a1-n4')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=0 WHERE run_id IN ('run-item-a-a1-n3','run-item-a-a1-n4')`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state='leased',run_id='run-item-a-a1-n1' WHERE lane_id='lane-1'`)
		return err
	})
	if skipped, err := EndRuns(context.Background(), f.storePath); err != nil || len(skipped) != 0 {
		t.Fatalf("end-runs: %v %v", skipped, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if !works["item-a"].Orphaned {
			t.Error("finished primary hid its unfinished siblings")
		}
		var count int
		err = r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, loopOrphanedKind).Scan(&count)
		if count != 1 {
			t.Errorf("interruption recorded %d times", count)
		}
		return err
	})
}

func TestWorkflowParallelLimits(t *testing.T) {
	for _, test := range []struct {
		field   string
		want    int
		invalid bool
	}{{"", 4, false}, {`,"max_parallel":1`, 1, false}, {`,"max_parallel":8`, 8, false}, {`,"max_parallel":0`, 0, true}, {`,"max_parallel":null`, 0, true}, {`,"max_parallel":9`, 0, true}, {`,"max_parallel":-1`, 0, true}} {
		t.Run(test.field, func(t *testing.T) {
			raw := strings.TrimSuffix(strings.TrimSpace(workflowFixture), "}") + test.field + "}"
			w, err := decodeWorkflow([]byte(raw), "scout-then-build@v1")
			if test.invalid {
				if err == nil || !strings.Contains(err.Error(), "workflow-max-parallel") {
					t.Fatalf("expected limit refusal: %v", err)
				}
				return
			}
			if err != nil || w.MaxParallel != test.want {
				t.Fatalf("workflow=%+v error=%v", w, err)
			}
		})
	}
}
