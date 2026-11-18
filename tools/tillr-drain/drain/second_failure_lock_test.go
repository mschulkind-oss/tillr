package drain

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The second-failure routing reads the store's whole history: every event, several
// times, every run of every item, and the reports on disk. On 2026-11-17 it did that
// inside markExamined's write transaction and held the store's one write lock for 4.8
// seconds, so every other writer in the loop and its lanes queued behind it and one
// waited past its busy timeout ("database is locked"). The reading belongs before the
// write: this test writes from a second connection, with no busy timeout, at the moment
// the routing reads, which fails at once if the routing holds the write lock and needs
// no clock to tell.
func TestSecondFailureRoutingReadsOutsideTheWriteLock(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref = 'docs/work.md#work' WHERE item_id = 'item-a'`)
		return err
	})
	first := mustPlan(t, l)[0]
	finishHeldPass(t, f, l, first, run.EndNormalExit)
	if err := l.markExamined(context.Background(), first.decision); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "item-a", RunID: first.decision.RunRequest.RunID, By: "owner", Why: "diagnosed", At: l.cfg.Now()})
		return err
	})
	second := mustPlan(t, l)[0]
	if second.decision.Action != ActionRetry {
		t.Fatalf("not a retry: %+v", second.decision)
	}
	finishHeldPass(t, f, l, second, run.EndNormalExit)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, id := range []string{first.decision.RunRequest.RunID, second.decision.RunRequest.RunID} {
			path := filepath.Join(f.root, id+".report")
			if err := os.WriteFile(path, []byte("report for "+id+"\nRESULT: failed\n"), 0600); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE runs SET artifact_refs = ? WHERE run_id = ?`, mustJSON([]run.ArtifactRef{{Kind: "report", Ref: path}}), id); err != nil {
				return err
			}
			if err := appendEffect(tx, GateVerdictKind, id, map[string]any{"result": "fail"}, l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})

	other, err := sql.Open("sqlite", "file:"+f.storePath+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	read, competing := 0, error(nil)
	secondFailureReading = func() {
		read++
		_, competing = other.Exec(`INSERT INTO meta (key, value) VALUES ('second-failure-competitor', 'written')`)
	}
	defer func() { secondFailureReading = nil }()
	if err := l.markExamined(context.Background(), second.decision); err != nil {
		t.Fatal(err)
	}
	if read == 0 {
		t.Fatal("the second-failure routing never read the history; the fixture does not reach it")
	}
	if competing != nil {
		t.Fatalf("a writer competing with the second-failure routing answered %v: the routing read the history under the write lock", competing)
	}
	var c item.Conflict
	fixtureWrite(t, f, func(tx *store.Tx) error { cs, err := item.ReadConflicts(tx.Reader()); c = cs["item-a"]; return err })
	if c.Status != "raised" || c.WaitingOn != "design" {
		t.Fatalf("the routing no longer raises the design conflict: %+v", c)
	}
}
