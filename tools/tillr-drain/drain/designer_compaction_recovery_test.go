package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestInterruptedDesignerCompactionReconcilesCheckpoint(t *testing.T) {
	for _, mode := range []string{"before-checkpoint", "after-checkpoint", "after-receipt", "new-dispatch"} {
		t.Run(mode, func(t *testing.T) {
			f, l, _ := commentFixture(t, "")
			number := 1
			server := stubReview(t, &number)
			defer server.Close()
			id := takeFixtureComment(t, f, server.URL)
			dir := t.TempDir()
			transcript := filepath.Join(dir, "session")
			marker := filepath.Join(dir, "invoked")
			writeForkFile(t, transcript, "{\"type\":\"session\"}\n")
			size, _ := os.Stat(transcript)
			journal := compactionJournal{SessionID: "11111111-1111-4111-8111-111111111111", Checkpoint: "previous-run", Transcript: transcript, Offset: size.Size(), RunID: "first-compacting-run", ItemID: id}
			putDesigner(t, f, lastDesigner{Document: "docs/design/example.md", SessionID: journal.SessionID, RunID: journal.Checkpoint, Transcript: transcript})
			req := run.Request{RunID: journal.RunID, ItemID: id, LaneID: "desk-example", Command: []string{filepath.Join(dir, "adapter")}}
			if mode == "new-dispatch" {
				req.RunID = "second-compacting-run"
			}
			decision := Decision{ItemID: req.ItemID, LaneID: req.LaneID, DesignerSessionID: journal.SessionID, DesignerReason: "resumed-compacted", RunRequest: &req}
			if err := os.MkdirAll(RunDir(f.root, req.RunID), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(dir, "compact"), "#!/bin/sh\ntouch '"+marker+"'\n")
			os.Chmod(filepath.Join(dir, "compact"), 0755)
			if mode != "before-checkpoint" {
				file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					t.Fatal(err)
				}
				file.WriteString("{\"type\":\"compaction\",\"tokensBefore\":50000,\"contextTokenFacts\":{\"after\":{\"tokens\":20000}},\"usage\":{\"totalTokens\":1200,\"cost\":{\"total\":0.12}}}\n")
				file.Close()
			}
			if mode == "after-receipt" {
				before, after, tokens, cost := 50000, 20000, int64(1200), 0.12
				journal.Receipt = &designerCompaction{TokensBefore: &before, TokensAfter: &after, Tokens: &tokens, CostUSD: &cost}
			}
			path := designerCompactionFile(l, journal.SessionID, journal.Checkpoint)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(journal)
			if err := os.WriteFile(path, raw, 0644); err != nil {
				t.Fatal(err)
			}
			err := l.compactDesigner(context.Background(), decision)
			if mode == "before-checkpoint" {
				if err == nil || !strings.Contains(err.Error(), "ambiguous") {
					t.Fatalf("expected ambiguous completion, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(filepath.Join(RunDir(f.root, req.RunID), "compaction.json"))
				if err != nil || !strings.Contains(string(raw), "0.12") {
					t.Fatalf("receipt %s %v", raw, err)
				}
			}
			if mode == "new-dispatch" {
				req.RunID = "third-compacting-run"
				if err := os.MkdirAll(RunDir(f.root, req.RunID), 0755); err != nil {
					t.Fatal(err)
				}
				if err := l.compactDesigner(context.Background(), decision); err != nil {
					t.Fatal(err)
				}
				st, err := store.OpenRead(context.Background(), f.storePath)
				if err != nil {
					t.Fatal(err)
				}
				err = st.ReadTx(context.Background(), func(r *store.Reader) error {
					var count int
					if err := r.QueryRow("SELECT count(*) FROM events WHERE kind=?", designerCompactedKind).Scan(&count); err != nil {
						return err
					}
					if count != 1 {
						t.Fatalf("charged compaction %d times", count)
					}
					return nil
				})
				st.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("repeated interrupted compaction")
			}
		})
	}
}
