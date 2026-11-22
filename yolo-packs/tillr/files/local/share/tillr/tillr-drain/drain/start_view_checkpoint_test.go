package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

const startIndexCheckpointStore = "TILLR_TEST_START_INDEX_CHECKPOINT_STORE"

func TestStartIndexCheckpointHelper(t *testing.T) {
	path := os.Getenv(startIndexCheckpointStore)
	if path == "" {
		return
	}
	ctx := context.Background()
	owner, err := store.OwnCheckpoints(ctx, path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cycle, err := owner.Checkpoint(ctx)
	if err != nil || !cycle.Restarted {
		fmt.Fprintf(os.Stderr, "checkpoint during question indexing did not restart: %+v, %v\n", cycle, err)
		os.Exit(1)
	}
	fmt.Println(`{"index":{"documents":[{"path":"docs/design.md","questions":[{"id":"OQ-1","state":"open","title":"Which?"}]}]}}`)
	os.Exit(0) // The external scanner's stdout must contain only the index.
}

func TestStartViewReleasesStoreBeforeIndexingNewDesignDocument(t *testing.T) {
	for _, seed := range []bool{true, false} {
		name := "new_document"
		if seed {
			name = "initial_snapshot"
		}
		t.Run(name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			t.Setenv(startIndexCheckpointStore, f.storePath)
			ctx := context.Background()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := []string{exe, "-test.run=^TestStartIndexCheckpointHelper$"}
			var refresh startRefresh
			t.Cleanup(refresh.store.close)
			read := func(prev startSnapshot) startSnapshot {
				t.Helper()
				next, err := refresh.read(ctx, l.cfg.Now(), l.currentPolicy(), l.startViewPass(), f.root, command, "", prev, startWatchStore)
				if err != nil {
					t.Fatal(err)
				}
				return next
			}
			var prev startSnapshot
			if !seed {
				prev = read(prev)
			}
			doc := "docs/design.md"
			if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, doc), "# Design\n")
			runGit(t, f.root, "add", doc)
			runGit(t, f.root, "commit", "-qm", "design doc")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET design_doc=? WHERE item_id='item-a'`, doc)
				return err
			})
			next := read(prev)
			if _, err := refresh.docs.questions.Document(doc); err != nil {
				t.Fatal(err)
			}
			if got := next.holds["item-a"]; got != ReasonQuestionOpen {
				t.Fatalf("new document's open question did not hold the proposal: %q", got)
			}
		})
	}
}
