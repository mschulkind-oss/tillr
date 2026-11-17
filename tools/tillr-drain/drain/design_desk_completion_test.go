package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCommentAfterWriterWithoutDocumentBlob(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	id := takeFixtureComment(t, f, server.URL)
	putDesigner(t, f, lastDesigner{Document: "docs/design/example.md", SessionID: "writing-session", Backend: "fixture", Model: "fixture-model"})
	stage := l.cfg.Stage.(*LaneStager)
	task, err := stage.commentTask(context.Background(), StageSpec{ItemID: id, SessionID: "writing-session", BaseSHA: runGit(t, f.root, "rev-parse", "HEAD")}, f.laneRoot)
	if err != nil || !strings.Contains(task, "no recorded document blob; read the document") {
		t.Fatalf("task=%q err=%v", task, err)
	}
}

func TestDesignDeskRecordsOnlyDeskRuns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		paths   []string
		off     bool
		missing bool
		want    bool
	}{
		{name: "single", paths: []string{"docs/design/example.md"}, want: true},
		{name: "two", paths: []string{"docs/design/example.md", "docs/design/other.md"}},
		{name: "none", paths: []string{"tools/"}},
		{name: "off", paths: []string{"docs/design/example.md"}, off: true},
		{name: "failed before writing", paths: []string{"docs/design/new.md"}, missing: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, _ := commentFixture(t, "")
			if !tc.off {
				l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec("UPDATE items SET state='in-queue' WHERE item_id='item-other'")
				return err
			})
			setDesignFence(t, f, "item-other", tc.paths...)
			ctx := context.Background()
			d := mustPlan(t, l)[0].decision
			staged, err := l.stageDispatches(ctx, []Decision{d}, runGit(t, f.root, "rev-parse", "HEAD"), &LoopResult{})
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage %v: %v", staged, err)
			}
			d = staged[0]
			exit := 0
			if tc.missing {
				exit = 1
			}
			rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", Route: "fixture-model", State: run.StateExited, ExitCode: countPtr(exit)}
			if err := l.recordDesigner(ctx, d, rec); err != nil {
				t.Fatal(err)
			}
			st, err := store.OpenRead(ctx, f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			err = st.ReadTx(ctx, func(r *store.Reader) error {
				var count int
				if err := r.QueryRow("SELECT count(*) FROM last_designers").Scan(&count); err != nil {
					return err
				}
				if !tc.want {
					if count != 0 {
						t.Fatalf("pool run wrote %d rows", count)
					}
					return nil
				}
				recorded, err := readDesigner(r, tc.paths[0])
				if err != nil {
					return err
				}
				session, err := ReadSessionID(filepath.Join(f.laneRoot, d.LaneID))
				if err != nil {
					return err
				}
				if recorded == nil || recorded.SessionID != session || recorded.RunID != rec.RunID || recorded.Desk != d.LaneID {
					t.Fatalf("last designer %+v", recorded)
				}
				if tc.missing && recorded.BlobID != "" {
					t.Fatalf("invented blob %q", recorded.BlobID)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
