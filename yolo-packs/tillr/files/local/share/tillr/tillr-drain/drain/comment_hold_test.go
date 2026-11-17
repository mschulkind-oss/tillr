package drain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCommentHoldScopeAndLiveRun(t *testing.T) {
	for _, mode := range []string{"unstarted", "running", "other-document"} {
		t.Run(mode, func(t *testing.T) {
			f, l, _ := commentFixture(t, "")
			number := 1
			server := stubReview(t, &number)
			defer server.Close()
			id := takeFixtureComment(t, f, server.URL)
			doc := "docs/design/example.md"
			if mode == "other-document" {
				doc = "docs/design/other.md"
				writeForkFile(t, filepath.Join(f.root, doc), "# Another design\n")
				runGit(t, f.root, "add", doc)
				runGit(t, f.root, "commit", "-qm", "docs: another fixture design")
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec("UPDATE items SET state='in-queue',design_doc=? WHERE item_id='item-other'", doc)
				return err
			})
			if mode == "running" {
				// Record the dispatch before taking another snapshot with the comment open.
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec("UPDATE items SET state='closed' WHERE item_id=?", id)
					return err
				})
				for _, p := range mustPlan(t, l) {
					if p.decision.ItemID == "item-other" {
						ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
						if err != nil || !ok {
							t.Fatalf("dispatch: %v / %v", ok, err)
						}
					}
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec("UPDATE items SET state='in-queue' WHERE item_id=?", id)
					return err
				})
			}
			found := false
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID != "item-other" {
					continue
				}
				found = true
				d := p.decision
				switch mode {
				case "unstarted":
					if d.Action != ActionHold || d.Reason != "design-under-comment" || d.DeferredBy != id {
						t.Fatalf("hold: %+v", d)
					}
				case "running":
					if d.Action != ActionHold || d.Reason != ReasonRunLive || d.RunRequest == nil {
						t.Fatalf("live run changed: %+v", d)
					}
				case "other-document":
					if d.Action != ActionDispatch {
						t.Fatalf("other document held: %+v", d)
					}
				}
			}
			if !found {
				t.Fatal("build item absent from plan")
			}
		})
	}
}

type commentReleaseStager struct{ attempted bool }

func (s *commentReleaseStager) Stage(_ context.Context, spec StageSpec) error {
	if spec.ItemID == "item-other" {
		s.attempted = true
	}
	return ItemContentf(spec.ItemID, "fixture", "stop before starting a fixture run")
}

func TestCommentClosureReleasesBuildInSamePass(t *testing.T) {
	for _, mode := range []string{"resolved", "deleted", "owner-closed", "landed", "resolved-elsewhere", "answered"} {
		t.Run(mode, func(t *testing.T) {
			f, l, _ := commentFixture(t, "")
			comments := []item.ReviewComment{{ID: "a", Comment: "Why?", CreatedAt: 1}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"comments": comments})
			}))
			defer server.Close()
			id := takeFixtureComment(t, f, server.URL)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec("UPDATE items SET state='in-queue',design_doc='docs/design/example.md' WHERE item_id='item-other'")
				return err
			})
			switch mode {
			case "resolved":
				comments[0].Resolved = true
			case "deleted":
				comments = nil
			case "landed", "resolved-elsewhere":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec("UPDATE items SET state=? WHERE item_id=?", mode, id)
					return err
				})
			default:
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := item.Close(context.Background(), tx, id, mode, "fixture", l.cfg.Now())
					return err
				})
			}
			l.cfg.VantageURL, l.cfg.VantageRepo = server.URL, "fixture"
			stage := &commentReleaseStager{}
			l.cfg.Stage = stage
			result := &LoopResult{}
			if stop, err := l.pass(context.Background(), result); err != nil {
				t.Fatalf("pass %s: %v", stop, err)
			}
			if !stage.attempted {
				t.Fatal("build stayed held in the pass that closed its comment item")
			}
		})
	}
}
