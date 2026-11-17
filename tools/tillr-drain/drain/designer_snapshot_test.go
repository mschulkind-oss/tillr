package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRetireDesignerRequiresCommittedDeletion(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	const doc = "docs/design/example.md"
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	writeForkFile(t, transcript, "kept")
	putDesigner(t, f, lastDesigner{Document: doc, Transcript: transcript})
	l.syncedSHA = runGit(t, f.root, "rev-parse", TrunkRef)
	if err := os.Remove(filepath.Join(f.root, doc)); err != nil {
		t.Fatal(err)
	}
	assertRow := func(want bool) {
		t.Helper()
		if err := l.retireDesigners(context.Background()); err != nil {
			t.Fatal(err)
		}
		s, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			d, err := readDesigner(r, doc)
			if err == nil && (d != nil) != want {
				t.Fatalf("designer kept=%t, want %t (pass trunk %s)", d != nil, want, l.syncedSHA)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if raw, err := os.ReadFile(transcript); err != nil || string(raw) != "kept" {
			t.Fatalf("transcript %s %v", raw, err)
		}
	}
	// An uncommitted removal is not deletion from the trunk.
	assertRow(true)
	runGit(t, f.root, "add", "-u")
	runGit(t, f.root, "commit", "-qm", "docs: delete the design")
	// A concurrent landing must not replace the commit already read by this pass.
	assertRow(true)
	// The next pass observes the committed removal and can retire the row.
	l.syncedSHA = runGit(t, f.root, "rev-parse", TrunkRef)
	assertRow(false)
}
