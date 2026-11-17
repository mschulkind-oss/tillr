package item_test

import (
	"github.com/mschulkind-oss/tillr-item/item"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProseRewriteSyncOptInAndIdempotence(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	bad := strings.Replace(proseBlock, ` prose="2"`, "", 1)
	runSync(t, s, root, doc("docs/api.md", bad))
	if got := column(t, s, `SELECT count(*) FROM items WHERE item_id='rewrite-item-read-api'`); got != "0" {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[prose]\nrewrite = true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report := runSync(t, s, root, doc("docs/api.md", bad))
	if report.CreatedCount != 1 {
		t.Fatalf("rewrite not created: %+v", report)
	}
	for query, want := range map[string]string{
		`SELECT seat FROM items WHERE item_id='rewrite-item-read-api'`:             "build",
		`SELECT allowed_paths FROM items WHERE item_id='rewrite-item-read-api'`:    `["docs/api.md"]`,
		`SELECT count(*) FROM item_criteria WHERE item_id='rewrite-item-read-api'`: "4",
	} {
		if got := column(t, s, query); got != want {
			t.Fatalf("%s: %s != %s", query, got, want)
		}
	}
	runSync(t, s, root, doc("docs/api.md", bad))
	if got := column(t, s, `SELECT count(*) FROM events WHERE kind='item.add' AND object_id='rewrite-item-read-api'`); got != "1" {
		t.Fatalf("duplicate rewrite: %s", got)
	}
}

func TestProseRewriteProtectedAttributesWithDirectiveWhitespace(t *testing.T) {
	for _, opening := range []string{"<!-- tillr: item", "<!-- tillr:  item", "<!-- tillr: item\n"} {
		for _, key := range []string{"paths", "blocked_by", "seat"} {
			t.Run(opening+key, func(t *testing.T) {
				before := strings.Replace(proseBlock, "<!-- tillr: item", opening, 1)
				after := before
				switch key {
				case "paths":
					after = strings.Replace(after, `paths="tools/view/"`, `paths="tools/other/"`, 1)
				case "blocked_by":
					after = strings.Replace(after, `prose="2"`, `prose="2" blocked_by="item-other"`, 1)
				case "seat":
					after = strings.Replace(after, `prose="2"`, `prose="2" seat="design"`, 1)
				}
				got := item.ValidateProseRewrite("docs/api.md", "item-read-api", []byte(before), []byte(after))
				if !strings.HasPrefix(got, key+" changed:") {
					t.Fatalf("protected %s escaped: %q", key, got)
				}
			})
		}
	}
}

func TestProseRewriteSkipsLanded(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	runSync(t, s, root, doc("docs/api.md", proseBlock))
	exec(t, s, `UPDATE items SET state='landed' WHERE item_id='item-read-api'`)
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[prose]\nrewrite = true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report := runSync(t, s, root, doc("docs/api.md", strings.Replace(proseBlock, ` prose="2"`, "", 1)))
	if report.CreatedCount != 0 {
		t.Fatalf("landed rewrite: %+v", report)
	}
}
