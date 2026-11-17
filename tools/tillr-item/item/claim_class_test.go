package item_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

var classes = []string{"verification-only", "red-gate-triage", "direction", "owner-only"}

func claimBlock(id, class string) string {
	attr := ""
	if class != "" {
		attr = ` claims="c1:` + class + `"`
	}
	return "# Claims\n\n<!-- tillr: item id=\"" + id + "\" paths=\"tools/thing/\"" + attr + " -->\n\n- **c1:** The criterion.\n- **c2:** A default criterion.\n"
}

func TestSyncCriterionClaimClasses(t *testing.T) {
	s := syncStore(t)
	report := runSync(t, s, "", doc("docs/claims.md", claimBlock("item-claims", "owner-only")))
	if len(report.Findings) != 0 {
		t.Fatalf("findings: %+v", report.Findings)
	}
	for id, want := range map[string]string{"c1": "owner-only", "c2": "verification-only"} {
		if got := column(t, s, `SELECT claim_class FROM item_criteria WHERE criterion_id=?`, id); got != want {
			t.Fatalf("%s: %s", id, got)
		}
	}
}

func TestSyncCriterionClaimMoves(t *testing.T) {
	for _, from := range classes {
		for _, to := range classes {
			t.Run(from+"/"+to, func(t *testing.T) {
				s := syncStore(t)
				runSync(t, s, "", doc("docs/claims.md", claimBlock("item-claims", from)))
				// A presented criterion still permits class hardening, but not text edits.
				exec(t, s, `UPDATE item_criteria SET state='done' WHERE criterion_id='c1'`)
				report := runSync(t, s, "", doc("docs/claims.md", claimBlock("item-claims", to)))
				allowed := from == to || from == "verification-only" || to == "owner-only" && (from == "direction" || from == "red-gate-triage")
				want := from
				if allowed {
					want = to
					if len(report.Findings) != 0 {
						t.Fatalf("findings: %+v", report.Findings)
					}
				} else {
					if len(report.Findings) != 1 || report.Findings[0].Rule != "claim-class-weakened" {
						t.Fatalf("findings: %+v", report.Findings)
					}
				}
				if got := column(t, s, `SELECT claim_class FROM item_criteria WHERE criterion_id='c1'`); got != want {
					t.Fatalf("got %s want %s; %+v", got, want, report)
				}
			})
		}
	}
}

func TestSyncCriterionClaimWeakeningIsFileAtomicAndDryRun(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/claims.md", claimBlock("item-claims", "owner-only")))
	documents := []item.SyncDocument{doc("docs/claims.md", claimBlock("item-new", "")+"\n"+claimBlock("item-claims", "direction"))}
	var preview item.SyncReport
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		preview, err = item.Sync(context.Background(), r, nil, item.SyncInput{Documents: documents, At: syncAt})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	written := runSync(t, s, "", documents...)
	for _, report := range []item.SyncReport{preview, written} {
		if len(report.Findings) != 1 || report.Findings[0].Rule != "claim-class-weakened" || report.Findings[0].ItemID != "item-claims" {
			t.Fatalf("findings: %+v", report.Findings)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM items`); n != 1 {
		t.Fatalf("file wrote new sibling: %d items", n)
	}
}

func TestSyncCriterionClaimFindingsWriteNothing(t *testing.T) {
	for _, tc := range []struct{ attr, text, rule string }{
		{`claims="c1:unknown"`, "The criterion.", "invalid-claim-class"},
		{`claims="c3:direction"`, "The criterion.", "claim-criterion-missing"},
		{`claims="c1:owner-only,c1:direction"`, "The criterion.", "duplicate-claim"},
		{"", "Owner decision recorded: yes", "owner-criterion-untyped"},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			s := syncStore(t)
			bad := strings.Replace(claimBlock("item-bad", ""), `paths="tools/thing/"`, `paths="tools/thing/" `+tc.attr, 1)
			bad = strings.Replace(bad, "The criterion.", tc.text, 1)
			report := runSync(t, s, "", doc("docs/bad.md", claimBlock("item-good", "")+"\n"+bad))
			if len(report.Findings) != 1 || report.Findings[0].Rule != tc.rule || report.Findings[0].ItemID != "item-bad" {
				t.Fatalf("findings: %+v", report.Findings)
			}
			if n := count(t, s, `SELECT count(*) FROM items`); n != 0 {
				t.Fatalf("file wrote %d items", n)
			}
		})
	}
}

func TestCriterionClaimLegacyMigration(t *testing.T) {
	path := filepath.Join(scratch(t), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE items (item_id TEXT PRIMARY KEY, title TEXT NOT NULL, body_ref TEXT, state TEXT NOT NULL, design_doc TEXT, updated_at INTEGER NOT NULL, event_seq INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE item_criteria (item_id TEXT NOT NULL REFERENCES items(item_id), criterion_id TEXT NOT NULL, text TEXT NOT NULL, state TEXT NOT NULL, PRIMARY KEY(item_id,criterion_id));
 INSERT INTO items(item_id,title,state,updated_at) VALUES('item-legacy','Legacy','not-started',0);`)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{"Owner decision recorded: yes", "OWNER DECISION RECORDED", "Owner decision recordedness", "The owner decision recorded"}
	for i, text := range texts {
		if _, err = raw.Exec(`INSERT INTO item_criteria VALUES('item-legacy',?,?,'')`, fmt.Sprint(i), text); err != nil {
			t.Fatal(err)
		}
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	for opening := 0; opening < 2; opening++ {
		s, err := store.Open(context.Background(), path, true)
		if err != nil {
			t.Fatal(err)
		}
		for i := range texts {
			want := "verification-only"
			if i < 2 {
				want = "owner-only"
			}
			if got := column(t, s, `SELECT claim_class FROM item_criteria WHERE criterion_id=?`, fmt.Sprint(i)); got != want {
				t.Fatalf("opening %d criterion %d: %s want %s", opening, i, got, want)
			}
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
