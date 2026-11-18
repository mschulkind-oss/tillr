package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func setDesignFence(t *testing.T, f refusalLoopFixture, id string, paths ...string) {
	t.Helper()
	raw, _ := json.Marshal(paths)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET seat='design',allowed_paths=? WHERE item_id=?", string(raw), id)
		return err
	})
}

func TestDesignDeskRouting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		off   bool
		want  string
	}{
		{"single", []string{"docs/design/example.md", "tools/"}, false, "desk-example"},
		{"duplicate", []string{"docs/design/example.md", "docs/design/example.md"}, false, "desk-example"},
		{"two", []string{"docs/design/example.md", "docs/design/other.md"}, false, "lane-1"},
		{"none", []string{"tools/"}, false, "lane-1"},
		{"directory", []string{"docs/design/"}, false, "lane-1"},
		{"prefix boundary", []string{"docs/design-other/example.md"}, false, "lane-1"},
		{"off", []string{"docs/design/example.md"}, true, "lane-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t, "item-write")
			l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
			if !tc.off {
				l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
			}
			setDesignFence(t, f, "item-write", tc.paths...)
			d := mustPlan(t, l)[0].decision
			if d.Action != ActionDispatch || d.LaneID != tc.want {
				t.Fatalf("dispatch %+v; want %s", d, tc.want)
			}
			if tc.want == "lane-1" && d.DeskDocument != "" {
				t.Fatalf("pool dispatch records a document: %+v", d)
			}
		})
	}
}

func TestDeskCapacityAndDocumentWait(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
	for i := 0; i < 4; i++ {
		writeForkFile(t, filepath.Join(f.root, fmt.Sprintf("docs/design/design-%d.md", i)), "# Design\n")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i := 0; i < 4; i++ {
			doc := fmt.Sprintf("docs/design/design-%d.md", i)
			_, err := item.TakeComments(context.Background(), tx, f.root, doc, []item.ReviewComment{{ID: fmt.Sprintf("c%d", i), Comment: "Why?"}}, time.Now())
			if err != nil {
				return err
			}
		}
		return nil
	})
	dispatched, waiting := 0, 0
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == "item-other" {
			continue
		}
		if d.Action == ActionDispatch {
			dispatched++
			if d.LaneID == "lane-1" {
				t.Fatal("desk consumed pool lane")
			}
		} else if d.Reason == "waits on a desk" && d.DeferredBy != "" {
			waiting++
			var doc string
			fixtureWrite(t, f, func(tx *store.Tx) error {
				return tx.QueryRow("SELECT design_doc FROM items WHERE item_id=?", d.ItemID).Scan(&doc)
			})
			if d.DeferredBy != doc {
				t.Fatalf("capacity mover %q; want %q", d.DeferredBy, doc)
			}
			class, _, mover := whyPresentation(d)
			if class != "waits on work" || mover != doc {
				t.Fatalf("capacity presentation %s %s", class, mover)
			}
		} else {
			t.Fatalf("unexpected decision %+v", d)
		}
	}
	if dispatched != 3 || waiting != 1 {
		t.Fatalf("dispatches=%d waits=%d", dispatched, waiting)
	}
	// Changing the configured limit and persisting a lease exercises occupied
	// desks from another pass as well as assignments within this pass.
	first := mustPlan(t, l)
	for _, p := range first {
		if p.decision.Action == ActionDispatch {
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := run.ClaimLease(context.Background(), tx, *p.decision.RunRequest, p.decision.LaneID, l.cfg.Now())
				return err
			})
			break
		}
	}
	l.cfg.Comments.MaxRunning = 1
	for _, p := range mustPlan(t, l) {
		if item.IsCommentItem(p.decision.ItemID) && p.decision.Action == ActionDispatch {
			t.Fatalf("occupied desk ignored: %+v", p.decision)
		}
	}
}

func TestDocumentDeskWait(t *testing.T) {
	// A writing run reserves the same desk for comments, including across a new planner snapshot.
	f2, l2, _ := commentFixture(t, "")
	l2.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 1}
	fixtureWrite(t, f2, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state='in-queue' WHERE item_id='item-other'")
		return err
	})
	setDesignFence(t, f2, "item-other", "docs/design/example.md")
	writer := mustPlan(t, l2)[0].decision
	fixtureWrite(t, f2, func(tx *store.Tx) error {
		_, err := run.ClaimLease(context.Background(), tx, *writer.RunRequest, writer.LaneID, l2.cfg.Now())
		return err
	})
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	id := takeFixtureComment(t, f2, server.URL)
	for _, p := range mustPlan(t, l2) {
		if p.decision.ItemID == id {
			d := p.decision
			if d.Reason != "waits on the document's desk" || d.DeferredBy != "item-other" {
				t.Fatalf("busy desk %+v", d)
			}
			class, _, mover := whyPresentation(d)
			if class != "waits on work" || mover != "item-other" {
				t.Fatalf("presentation %s %s", class, mover)
			}
		}
	}
}

func TestDesignDeskLeavesPoolLaneFree(t *testing.T) {
	f, l := passFixture(t, "item-write", "item-build")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	setDesignFence(t, f, "item-write", "docs/design/example.md")
	seen := map[string]Decision{}
	for _, p := range mustPlan(t, l) {
		seen[p.decision.ItemID] = p.decision
	}
	if seen["item-write"].LaneID != "desk-example" || seen["item-build"].LaneID != "lane-1" {
		t.Fatalf("pool and desk assignments %+v", seen)
	}
}

func TestTwoDesignWritersWaitOnSameDeskInOnePass(t *testing.T) {
	f, l := passFixture(t, "item-first", "item-second")
	l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	for _, id := range []string{"item-first", "item-second"} {
		setDesignFence(t, f, id, "docs/design/example.md")
	}
	plans := mustPlan(t, l)
	if plans[0].decision.Action != ActionDispatch || plans[1].decision.Reason != ReasonDocumentDeskBusy || plans[1].decision.DeferredBy != plans[0].decision.ItemID {
		t.Fatalf("same-pass reservations %+v %+v", plans[0].decision, plans[1].decision)
	}
}

func TestWritingSessionAnswersFirstComment(t *testing.T) {
	f, l, steps := commentFixture(t, "")
	l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state='in-queue' WHERE item_id='item-other'")
		return err
	})
	setDesignFence(t, f, "item-other", "docs/design/example.md")
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	if result, err := l.Run(ctx); err != nil {
		t.Fatalf("writing %+v: %v", result, err)
	}
	if len(steps.sessions) != 1 || steps.requests[0].LaneID != "desk-example" {
		t.Fatalf("writer requests %+v", steps.requests)
	}
	writingSession := steps.sessions[0]
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	id := takeFixtureComment(t, f, server.URL)
	// A new supervisor must recover the writer from the store, not local memory.
	stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	l = f.loop(t, steps, stage)
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "fixture", Model: "fixture-model"}
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == id && p.decision.DesignerSessionID != writingSession {
			t.Fatalf("comment did not choose writer: %+v", p.decision)
		}
	}
	if result, err := l.Run(ctx); err != nil {
		t.Fatalf("comment %+v: %v", result, err)
	}
	if len(steps.sessions) != 2 || steps.sessions[1] != writingSession {
		t.Fatalf("staged sessions %v", steps.sessions)
	}
}
