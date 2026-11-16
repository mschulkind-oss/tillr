package item_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const proseBlock = `### Add the read API

Readers need a way to inspect the current state.

<!-- tillr: item id="item-read-api" paths="tools/view/" prose="2" -->

- **c1:** The route answers.
`

func TestSyncProseRules(t *testing.T) {
	many := proseBlock
	for i := 2; i <= 9; i++ {
		many += fmt.Sprintf("- **c%d:** Another outcome.\n", i)
	}
	cases := []struct{ name, content, rule, message string }{
		{"passes", proseBlock, "", ""},
		{"paragraph-after-list", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "- An outcome\n\nReaders need a way to inspect the current state.", 1), "", ""},
		{"paragraph-after-comment", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "<!-- note -->\n\nReaders need a way to inspect the current state.", 1), "", ""},
		{"unmarked", strings.Replace(proseBlock, ` prose="2"`, "", 1), "prose-unmarked", "mark"},
		{"old-mark", strings.Replace(proseBlock, `prose="2"`, `prose="1"`, 1), "prose-unmarked", "mark"},
		{"imperative", strings.Replace(proseBlock, "Add the read API", "The read API exists", 1), "prose-shape", "title-verb"},
		{"long-title", strings.Replace(proseBlock, "Add the read API", "Add one two three four five six seven eight nine ten eleven twelve", 1), "prose-shape", "title-length"},
		{"no-paragraph", strings.Replace(proseBlock, "Readers need a way to inspect the current state.\n", "", 1), "prose-shape", "description"},
		{"parent-paragraph", "## Parent\n\nThis paragraph belongs to the parent.\n\n" + strings.Replace(proseBlock, "Readers need a way to inspect the current state.\n", "", 1), "prose-shape", "description"},
		{"list-not-paragraph", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "- An outcome", 1), "prose-shape", "description"},
		{"list-continuation-after-blank", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "- An outcome\n\n  Still inside that list.", 1), "prose-shape", "description"},
		{"list-continuation-not-paragraph", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "- An outcome\n  continued here", 1), "prose-shape", "description"},
		{"table-not-paragraph", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "|Outcome|\n|---|\n|An outcome|", 1), "prose-shape", "description"},
		{"fence-not-paragraph", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "```text\nNot prose.\n```", 1), "prose-shape", "description"},
		{"comment-not-paragraph", strings.Replace(proseBlock, "Readers need a way to inspect the current state.", "<!--\nNot prose.\n-->", 1), "prose-shape", "description"},
		{"too-many-criteria", many, "prose-shape", "criteria-count"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := syncStore(t)
			report := runSync(t, s, "", doc("docs/api.md", tc.content))
			if len(report.Findings) != 0 || len(report.SkippedFiles) != 0 || report.CreatedCount != 1 {
				t.Fatalf("warning blocked creation: %+v", report)
			}
			if tc.rule == "" {
				if len(report.Warnings) != 0 {
					t.Fatalf("passing block: %+v", report.Warnings)
				}
				return
			}
			if len(report.Warnings) != 1 {
				t.Fatalf("want one warning, got %+v", report.Warnings)
			}
			n := report.Warnings[0]
			if n.Rule != tc.rule || !strings.Contains(n.Message, tc.message) || n.File != "docs/api.md" || n.ItemID != "item-read-api" || n.Line == 0 || n.Heading == "" {
				t.Fatalf("warning: %+v", n)
			}
			if !strings.Contains(item.SyncText(report, syncAt), "warning:") {
				t.Fatal("text output hides warning")
			}
		})
	}
}

func TestSyncProseMarkStoredAndUpdated(t *testing.T) {
	s := syncStore(t)
	unmarked := strings.Replace(proseBlock, ` prose="2"`, "", 1)
	runSync(t, s, "", doc("docs/api.md", unmarked))
	report := runSync(t, s, "", doc("docs/api.md", proseBlock))
	if report.UpdatedCount != 1 {
		t.Fatalf("mark not updated: %+v", report)
	}
	if got := column(t, s, `SELECT prose FROM items WHERE item_id = 'item-read-api'`); got != "2" {
		t.Fatalf("stored mark %q", got)
	}
	if report := runSync(t, s, "", doc("docs/api.md", proseBlock)); report.UnchangedCount != 1 {
		t.Fatalf("mark not read back: %+v", report)
	}
	if got := storedProsePayload(t, s)["prose"]; got != "2" {
		t.Fatalf("show mark %v", got)
	}
}

func storedProsePayload(t *testing.T, s *store.Store) map[string]any {
	t.Helper()
	var payload map[string]any
	root := scratch(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "api.md"), []byte(proseBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		env, err := item.Show(r, "item-read-api", root)
		if err != nil {
			return err
		}
		return json.Unmarshal(env.Payload, &payload)
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSyncProseBoundaryCases(t *testing.T) {
	for _, verb := range []string{"Add", "Move", "Route", "Prevent", "Isolate", "Hold", "Test"} {
		t.Run(verb, func(t *testing.T) {
			content := strings.Replace(proseBlock, "Add the read API", verb+" one two three four five six seven eight nine ten eleven", 1)
			for i := 2; i <= 8; i++ {
				content += fmt.Sprintf("- **c%d:** Another outcome.\n", i)
			}
			report := runSync(t, syncStore(t), "", doc("docs/api.md", content))
			if len(report.Warnings) != 0 || len(report.Findings) != 0 || report.CreatedCount != 1 {
				t.Fatalf("boundary: %+v", report)
			}
		})
	}
}

func TestStoreBeforeProseMarkGainsColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratch(t), "state.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	runSync(t, s, "", doc("docs/api.md", strings.Replace(proseBlock, ` prose="2"`, "", 1)))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	dropColumn(t, path, "prose")
	s, err = store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := columnDeclaration(t, s, "items", "prose"); got != "TEXT|notnull|''" {
		t.Fatalf("migration: %s", got)
	}
	if got := column(t, s, `SELECT prose FROM items WHERE item_id = 'item-read-api'`); got != "" {
		t.Fatalf("legacy mark: %q", got)
	}
	report := runSync(t, s, "", doc("docs/api.md", proseBlock))
	if report.UpdatedCount != 1 {
		t.Fatalf("legacy update: %+v", report)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, "item-read-api", "blocked", syncAt)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := storedProsePayload(t, s)["prose"]; got != "2" {
		t.Fatalf("state update lost mark: %v", got)
	}
	report = runSync(t, s, "", doc("docs/api.md", strings.Replace(proseBlock, ` prose="2"`, "", 1)))
	if report.UpdatedCount != 1 || len(report.Warnings) != 1 {
		t.Fatalf("removing mark: %+v", report)
	}
	if got := column(t, s, `SELECT prose FROM items WHERE item_id = 'item-read-api'`); got != "" {
		t.Fatalf("removed mark: %q", got)
	}
}

func TestSyncProseWarningDoesNotChangeStoredItem(t *testing.T) {
	clean, warned := syncStore(t), syncStore(t)
	withoutParagraph := strings.Replace(proseBlock, "Readers need a way to inspect the current state.\n", "", 1)
	for _, content := range []string{proseBlock, strings.Replace(proseBlock, "The route answers.", "The route answers twice.", 1)} {
		good := runSync(t, clean, "", doc("docs/api.md", content))
		badContent := strings.Replace(content, "Readers need a way to inspect the current state.\n", "", 1)
		bad := runSync(t, warned, "", doc("docs/api.md", badContent))
		if len(good.Warnings) != 0 || len(bad.Warnings) != 1 || good.CreatedCount != bad.CreatedCount || good.UpdatedCount != bad.UpdatedCount {
			t.Fatalf("warning changed result: %+v / %+v", good, bad)
		}
		// Compare the complete stored record, including the clock and event sequence.
		a, b := storedProsePayload(t, clean), storedProsePayload(t, warned)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("warning changed stored payload: %v / %v", a, b)
		}
	}
	// A dry run reports the same shape warning and writes no item.
	s := syncStore(t)
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		report, err := item.Sync(context.Background(), r, nil, item.SyncInput{Documents: []item.SyncDocument{doc("docs/api.md", withoutParagraph)}, At: syncAt})
		if err == nil && (len(report.Warnings) != 1 || report.CreatedCount != 1) {
			t.Fatalf("dry run: %+v", report)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT count(*) FROM items`) != 0 {
		t.Fatal("dry run wrote item")
	}
}
