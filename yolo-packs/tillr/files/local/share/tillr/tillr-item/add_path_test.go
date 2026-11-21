package main_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func addPathCommand(t *testing.T, db, path, by, why string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, "add-path", "--store", db, "--item", "0", "--path", path, "--by", by, "--why", why)
	out, err := cmd.CombinedOutput()
	return exitOf(err), string(out)
}

func TestAddPathRecordsAnOperatorReceipt(t *testing.T) {
	t.Setenv("TILLR_RUN_ID", "")
	db, s := placeFixture(t, "same-rank")
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["tools/old/"]' WHERE item_id='0'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code, out := addPathCommand(t, db, "docs/new, notes.md", "operator", "repair the link"); code != 0 || !strings.Contains(out, `"allowed_paths":["tools/old/","docs/new, notes.md"]`) {
			t.Fatalf("add-path exited %d: %s", code, out)
		}
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var fence, rank, state string
		var seq int64
		if err := r.QueryRow(`SELECT allowed_paths, rank, state, event_seq FROM items WHERE item_id='0'`).Scan(&fence, &rank, &state, &seq); err != nil {
			return err
		}
		if fence != `["tools/old/","docs/new, notes.md"]` || rank != "same-rank" || state != "in-queue" {
			t.Fatalf("unexpected item after add-path: fence=%s rank=%s state=%s", fence, rank, state)
		}
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		if len(events) != 1 || events[0].Kind != "item.update" || events[0].Seq != seq {
			t.Fatalf("want one item.update receipt at sequence %d, got %+v", seq, events)
		}
		for _, want := range []string{`"path_added":"docs/new, notes.md"`, `"by":"operator"`, `"why":"repair the link"`} {
			if !strings.Contains(string(events[0].Payload), want) {
				t.Errorf("receipt lacks %s: %s", want, events[0].Payload)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAddPathAfterDesignLanding(t *testing.T) {
	t.Setenv("TILLR_RUN_ID", "")
	for _, state := range []string{"in-queue", "blocked"} {
		t.Run(state, func(t *testing.T) {
			db, s := placeFixture(t, "rank")
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='0'`, state); err != nil {
					return err
				}
				_, err := tx.Exec(`CREATE TABLE landings(item_id TEXT, result TEXT, run_id TEXT, trunk_after TEXT);
					INSERT INTO landings VALUES ('0', 'landed', 'run-design', 'design-commit')`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if code, out := addPathCommand(t, db, "docs/new.md", "owner", "needed for build"); code != 0 || !strings.Contains(out, `"allowed_paths":["docs/new.md"]`) || !strings.Contains(out, `"state":"`+state+`"`) {
				t.Fatalf("add-path after design landing: %d %s", code, out)
			}
		})
	}
}

func TestAddPathRefusesWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name, state, runID, path, by, why, diagnostic string
	}{
		{"closed", "closed", "", "docs/new.md", "owner", "needed", "state-terminal"},
		{"landed", "landed", "", "docs/new.md", "owner", "needed", "state-terminal"},
		{"resolved elsewhere", "resolved-elsewhere", "", "docs/new.md", "owner", "needed", "state-terminal"},
		{"worker's own item", "in-queue", "run-0-a1", "docs/new.md", "owner", "needed", "operator-only"},
		{"missing actor", "in-queue", "", "docs/new.md", " ", "needed", "--by"},
		{"missing reason", "in-queue", "", "docs/new.md", "owner", " ", "--why"},
		{"absolute", "in-queue", "", "/docs", "owner", "needed", "repo-relative"},
		{"traversal", "in-queue", "", "docs/../tools", "owner", "needed", "repo-relative"},
		{"multiline", "in-queue", "", "docs/new.md\nother.txt", "owner", "needed", "repo-relative"},
		{"wildcard", "in-queue", "", "docs/*", "owner", "needed", "fence-wildcard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TILLR_RUN_ID", tc.runID)
			t.Setenv("TILLR_ITEM_ID", "0")
			db, s := placeFixture(t, "rank")
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='0'`, tc.state)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if code, out := addPathCommand(t, db, tc.path, tc.by, tc.why); code == 0 || !strings.Contains(out, tc.diagnostic) {
				t.Fatalf("wanted %q refusal, got %d: %s", tc.diagnostic, code, out)
			}
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				var fence string
				var count int
				if err := r.QueryRow(`SELECT COALESCE(allowed_paths,''), (SELECT COUNT(*) FROM events) FROM items WHERE item_id='0'`).Scan(&fence, &count); err != nil {
					return err
				}
				if fence != "" || count != 0 {
					t.Fatalf("refusal wrote fence %q and %d events", fence, count)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
