package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func (s *exitSteps) ItemState(ctx context.Context, id, runID, state string, waiting ...item.WaitingOn) error {
	if s.stateFault {
		s.stateFault = false
		return fmt.Errorf("stop before state move")
	}
	if s.realState {
		st, err := store.Open(ctx, s.f.storePath, true)
		if err != nil {
			return err
		}
		defer st.Close()
		return st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := item.SetState(ctx, tx, id, state, s.tillrNow(), waiting...)
			return err
		})
	}
	return s.workflowSteps.ItemState(ctx, id, runID, state, waiting...)
}

func (s *exitSteps) tillrNow() time.Time { return time.Now().UTC() }

func assertNoCommitExit(t *testing.T, f refusalLoopFixture, steps *exitSteps, base, wantJudge string) {
	t.Helper()
	if runGit(t, f.root, "rev-parse", "HEAD") != base || steps.gates != 0 || steps.verdicts != 0 {
		t.Fatal("no-commit exit called gate, land or verdict")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var payload []byte
		if err := tx.QueryRow(`SELECT payload FROM events WHERE kind='item.verdict' AND json_extract(payload,'$.criterion_id')='verify'`).Scan(&payload); err != nil {
			return err
		}
		var v struct {
			Evidence      []string `json:"evidence"`
			Landed        []string `json:"landed"`
			EvaluatedHead string   `json:"evaluated_head"`
			Verdict       string   `json:"verdict"`
			ActorKind     string   `json:"actor_kind"`
		}
		if err := json.Unmarshal(payload, &v); err != nil {
			return err
		}
		if v.EvaluatedHead != base || len(v.Landed) != 0 || v.Verdict != "done" || v.ActorKind != "mechanical" || len(v.Evidence) < 2 {
			t.Errorf("verdict: %s", payload)
		}
		raw, err := os.ReadFile(v.Evidence[0])
		if err != nil {
			return err
		}
		var record struct {
			Workflow string `json:"workflow"`
			BaseSHA  string `json:"base_sha"`
			Landed   bool   `json:"landed"`
			Nodes    []struct {
				NodeID  string `json:"node_id"`
				RunID   string `json:"run_id"`
				Outcome string `json:"outcome"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if record.Workflow != "scout-then-build@v1" || record.BaseSHA != base || record.Landed || len(record.Nodes) != 1 || record.Nodes[0].RunID != steps.runs[0].RunID || record.Nodes[0].Outcome != "passed" {
			t.Errorf("exit record: %s", raw)
		}
		var state, judge string
		if err := tx.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT state FROM item_criteria WHERE item_id='item-a' AND criterion_id='judge'`).Scan(&judge); err != nil {
			return err
		}
		if state != StateLanded || judge != wantJudge {
			t.Errorf("item %s judge %s", state, judge)
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='land.landing'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("landings %d", n)
		}
		return nil
	})
}

func TestWorkflowExitRecordRestart(t *testing.T) {
	f, l, steps := exitFixture(t, fmt.Sprintf(exitOne, "no_commit"), false, 0)
	base := runGit(t, f.root, "rev-parse", "HEAD")
	result := &LoopResult{}
	graphPass(t, l, result)
	// An owner criterion added after dispatch remains untouched when the
	// already-completed graph is settled and its record is replayed.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET state='' WHERE item_id='item-a' AND criterion_id='judge'`)
		return err
	})
	steps.stateFault = true
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	c := <-l.completions
	if _, err := l.takeCompletion(c, result); err == nil {
		t.Fatal("expected interrupted state move")
	}
	path := filepath.Join(runDurableForTest(f, steps.runs[0].RunID), "graph-exit.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// The exit record, not a backend report that may be gone on restart,
	// owns completion once published.
	if err := os.Remove(filepath.Join(f.laneRoot, "lane-1", "report.md")); err != nil {
		t.Fatal(err)
	}
	next := replaceGraphLoop(t, f, l, steps)
	for i := 0; i < 4; i++ {
		graphPass(t, next, result)
	}
	if len(steps.runs) != 1 || result.Landed != 0 {
		t.Fatalf("restart ran nodes again: %v %+v", steps.runs, result)
	}
	assertNoCommitExit(t, f, steps, base, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict' AND json_extract(payload,'$.criterion_id')='verify'`).Scan(&n)
		if n != 1 {
			t.Errorf("duplicate verdicts: %d", n)
		}
		return err
	})
}
func runDurableForTest(f refusalLoopFixture, id string) string {
	return filepath.Join(f.laneRoot, "lane-1", "scratch", id)
}
