package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

type noOrderCandidateSteps struct {
	recordingSteps
	t *testing.T
}

func (s *noOrderCandidateSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.t.Fatal("queue order reached the gate")
	return GateOutcome{}, nil
}

func (s *noOrderCandidateSteps) Land(context.Context, Candidate) (LandOutcome, error) {
	s.t.Fatal("queue order reached the lander")
	return LandOutcome{}, nil
}

func TestHandReorderNeverChangesTrunk(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	l := f.loop(t, &noOrderCandidateSteps{t: t}, &itemContentStager{})
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at.Add(time.Hour) }
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec("UPDATE items SET rank=CASE item_id WHEN 'item-a' THEN 'a' ELSE 'b' END, state='blocked'"); err != nil {
			return err
		}
		_, err := item.Place(context.Background(), tx, "item-b", item.Destination{To: "first"}, at, "owner", "cli")
		return err
	})
	before := runGit(t, f.root, "rev-parse", "refs/heads/main")
	if _, err := l.pass(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/heads/main"); got != before {
		t.Fatalf("reorder changed trunk: %s -> %s", before, got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var first string
		if err := tx.QueryRow("SELECT item_id FROM items ORDER BY rank LIMIT 1").Scan(&first); err != nil {
			return err
		}
		if first != "item-b" {
			t.Fatalf("hand reorder not kept: first=%s", first)
		}
		return nil
	})
}

func TestLegacyRecorderCheckoutMayDisappearButNotAppear(t *testing.T) {
	root := "/lanes"
	ordinary := Worktree{Path: "/repo", Branch: "refs/heads/main"}
	legacy := Worktree{Path: filepath.Join(root, ".order-record", "run-order-record-1-2", WorktreeDir)}
	without := &Boundary{LaneRoot: root, Worktrees: []Worktree{ordinary}}
	with := &Boundary{LaneRoot: root, Worktrees: []Worktree{ordinary, legacy}}
	if lanes, observed := compareRunWorktrees(with, without); len(lanes)+len(observed) != 0 {
		t.Fatalf("retirement was attributed to a run: %v %v", lanes, observed)
	}
	if lanes, observed := compareRunWorktrees(without, with); len(lanes) != 0 || len(observed) != 1 || observed[0].Where != legacy.Path {
		t.Fatalf("unexpected checkout was not observed: %v %v", lanes, observed)
	}
}

func TestInFlightOrderRecordRetiresWithoutLanding(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &noOrderCandidateSteps{t: t}, &itemContentStager{})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state='blocked'")
		return err
	})
	before := runGit(t, f.root, "rev-parse", "refs/heads/main")
	id := "run-order-record-1-2"
	worktree := filepath.Join(l.cfg.LaneRoot, ".order-record", id, WorktreeDir)
	runGit(t, f.root, "worktree", "add", "--detach", worktree, before)
	writeForkFile(t, filepath.Join(worktree, "order.txt"), "item-a\n")
	runGit(t, worktree, "add", "order.txt")
	runGit(t, worktree, "commit", "-qm", "docs: record the queue's order")
	candidate := Candidate{RunID: id, Worktree: worktree, BaseSHA: before, HeadSHA: runGit(t, worktree, "rev-parse", "HEAD")}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, "drain.order-record", id, map[string]any{"phase": "gated", "candidate": candidate}, l.cfg.Now())
	})
	for range 2 {
		// A replacement must also leave a retired receipt alone.
		l = f.loop(t, &noOrderCandidateSteps{t: t}, &itemContentStager{})
		if _, err := l.pass(context.Background(), &LoopResult{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := runGit(t, f.root, "rev-parse", "refs/heads/main"); got != before {
		t.Fatalf("old candidate landed: %s -> %s", before, got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var raw []byte
		if err := tx.QueryRow("SELECT payload FROM events WHERE kind='drain.order-record' ORDER BY seq DESC LIMIT 1").Scan(&raw); err != nil {
			return err
		}
		var terminal struct {
			Phase   string `json:"phase"`
			Cleaned bool   `json:"cleaned"`
		}
		if err := json.Unmarshal(raw, &terminal); err != nil {
			return err
		}
		if terminal.Phase != "done" || !terminal.Cleaned {
			t.Fatalf("old work not retired: %s", raw)
		}
		var historical, total int
		if err := tx.QueryRow("SELECT sum(json_extract(payload, '$.phase')='gated'), count(*) FROM events WHERE kind='drain.order-record'").Scan(&historical, &total); err != nil {
			return err
		}
		if historical != 1 || total != 2 {
			t.Fatalf("historical events=%d, total=%d; want one historical and one retirement", historical, total)
		}
		return nil
	})
	if strings.Contains(runGit(t, f.root, "worktree", "list", "--porcelain"), "worktree "+worktree) {
		t.Fatalf("legacy worktree still registered: %s", worktree)
	}
}
