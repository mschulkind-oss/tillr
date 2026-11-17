package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRetireDesignerLeavesTranscript(t *testing.T) {
	for _, mode := range []string{"deleted", "mid-landing", "done", "live"} {
		t.Run(mode, func(t *testing.T) {
			f, l, _ := commentFixture(t, "")
			transcript := filepath.Join(t.TempDir(), "session.jsonl")
			writeForkFile(t, transcript, "kept")
			putDesigner(t, f, lastDesigner{Document: "docs/design/example.md", Transcript: transcript})
			switch mode {
			case "deleted":
				runGit(t, f.root, "rm", "-q", "docs/design/example.md")
				runGit(t, f.root, "commit", "-qm", "docs: delete the design")
			case "mid-landing":
				// git unlinks a file before it writes the landed revision; a pass that
				// looks in that gap sees no file while trunk still holds the document.
				if err := os.Remove(filepath.Join(f.root, "docs/design/example.md")); err != nil {
					t.Fatal(err)
				}
			}
			stage := "DESIGN"
			if mode == "done" {
				stage = "FINISHED"
			}
			index := filepath.Join(t.TempDir(), "index")
			writeForkFile(t, index, "#!/bin/sh\necho '{\"index\":{\"config\":{\"stages\":{\"FINISHED\":\"done\",\"DESIGN\":\"open\"}},\"documents\":[{\"path\":\"docs/design/example.md\",\"stage\":\""+stage+"\",\"questions\":[]}]}}'\n")
			os.Chmod(index, 0755)
			l.cfg.QuestionsCmd = []string{index}
			if err := l.retireDesigners(context.Background()); err != nil {
				t.Fatal(err)
			}
			st, err := store.OpenRead(context.Background(), f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			err = st.ReadTx(context.Background(), func(r *store.Reader) error {
				d, e := readDesigner(r, "docs/design/example.md")
				if (d != nil) != (mode == "live" || mode == "mid-landing") {
					t.Fatalf("row %+v", d)
				}
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			if raw, err := os.ReadFile(transcript); err != nil || string(raw) != "kept" {
				t.Fatalf("transcript %s %v", raw, err)
			}
		})
	}
}
