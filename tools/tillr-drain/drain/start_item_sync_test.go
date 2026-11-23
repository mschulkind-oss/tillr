package drain

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

type recordedSQLQuery struct {
	query string
	args  []any
}

type recordingSQLReader struct {
	r       *store.Reader
	queries []recordedSQLQuery
}

func (rec *recordingSQLReader) Query(query string, args ...any) (*sql.Rows, error) {
	rec.queries = append(rec.queries, recordedSQLQuery{query: query, args: args})
	return rec.r.Query(query, args...)
}

func (rec *recordingSQLReader) QueryRow(query string, args ...any) *sql.Row {
	rec.queries = append(rec.queries, recordedSQLQuery{query: query, args: args})
	return rec.r.QueryRow(query, args...)
}

func assertNoSQLiteTempSortOrEphemeral(t *testing.T, storePath string, queries []recordedSQLQuery) {
	t.Helper()
	if len(queries) == 0 {
		t.Fatal("expected at least one recorded SQL query")
	}
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		for _, q := range queries {
			planRows, err := r.Query("EXPLAIN QUERY PLAN "+q.query, q.args...)
			if err != nil {
				return err
			}
			var plans []string
			for planRows.Next() {
				var id, parent, unused int
				var detail string
				if err := planRows.Scan(&id, &parent, &unused, &detail); err != nil {
					planRows.Close()
					return err
				}
				plans = append(plans, detail)
			}
			if err := planRows.Err(); err != nil {
				planRows.Close()
				return err
			}
			planRows.Close()
			joinedPlan := strings.Join(plans, "; ")
			if strings.Contains(joinedPlan, "TEMP B-TREE") {
				t.Errorf("query uses SQLite temp B-tree:\n  sql:  %s\n  plan: %s", q.query, joinedPlan)
			}

			vdbeRows, err := r.Query("EXPLAIN "+q.query, q.args...)
			if err != nil {
				return err
			}
			var forbidden []string
			for vdbeRows.Next() {
				var addr int
				var opcode string
				var p1, p2, p3 int
				var p4 sql.NullString
				var p5 int
				var comment sql.NullString
				if err := vdbeRows.Scan(&addr, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
					vdbeRows.Close()
					return err
				}
				switch opcode {
				case "SorterOpen", "SorterInsert", "SorterSort", "SorterNext", "OpenEphemeral", "OpenAutoindex":
					forbidden = append(forbidden, opcode)
				}
				if strings.Contains(p4.String, "json_group_array") {
					forbidden = append(forbidden, "AggStep("+p4.String+")")
				}
			}
			if err := vdbeRows.Err(); err != nil {
				vdbeRows.Close()
				return err
			}
			vdbeRows.Close()
			if len(forbidden) > 0 {
				t.Errorf("query uses SQLite temp sorter/ephemeral opcodes %v:\n  sql: %s", forbidden, q.query)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadStartItemsSignatureUsesNoTempSortOrJSONGroupArray(t *testing.T) {
	f, _ := passFixture(t, "item-a", "item-b", "item-c")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET prose = ?, workflow = 'planned-build@v1' WHERE item_id = 'item-a'`, strings.Repeat("prose ", 512)); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by) VALUES ('item-b', 'item-a')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id, criterion_id, text, state, claim_class) VALUES ('item-a', 'c-extra', 'check behavior', 'not-done', 'verification-only')`)
		return err
	})

	var recorded []recordedSQLQuery
	fixtureRead(t, f, func(r *store.Reader) error {
		schema, err := readVersionSchema(r)
		if err != nil {
			return err
		}
		rec := &recordingSQLReader{r: r}
		_, err = readStartItemsSignatureOn(rec, schema)
		recorded = rec.queries
		return err
	})
	assertNoSQLiteTempSortOrEphemeral(t, f.storePath, recorded)
}

func TestReadStartItemsSignatureDetectsRawEditsAndIgnoresRowOrder(t *testing.T) {
	f, _ := passFixture(t, "item-a", "item-b")
	readSig := func() [32]byte {
		t.Helper()
		var sig [32]byte
		fixtureRead(t, f, func(r *store.Reader) error {
			schema, err := readVersionSchema(r)
			if err != nil {
				return err
			}
			sig, err = readStartItemsSignature(r, schema)
			return err
		})
		return sig
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET body_ref = NULL, title = 'ab', rank = 'c', prose = 'alpha' WHERE item_id = 'item-a'`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id, criterion_id, text, state, claim_class) VALUES ('item-a', 'c-sig', 'first-text', 'open', 'verification-only')`)
		return err
	})
	base := readSig()

	checkChanged := func(name string, mutate func(tx *store.Tx) error) {
		t.Helper()
		prev := readSig()
		fixtureWrite(t, f, mutate)
		next := readSig()
		if next == prev {
			t.Fatalf("%s did not change items signature", name)
		}
	}

	checkChanged("null-to-empty-body_ref", func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref = '' WHERE item_id = 'item-a'`)
		return err
	})
	checkChanged("column-boundary-shift", func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET title = 'a', rank = 'bc' WHERE item_id = 'item-a'`)
		return err
	})
	checkChanged("same-size-prose-edit", func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET prose = 'bravo' WHERE item_id = 'item-a'`)
		return err
	})
	checkChanged("same-size-state-edit", func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = 'blocked!' WHERE item_id = 'item-a'`)
		return err
	})
	checkChanged("express-toggle", func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET express = 1 WHERE item_id = 'item-a'`)
		return err
	})
	checkChanged("edge-insert", func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by) VALUES ('item-b', 'item-a')`)
		return err
	})
	checkChanged("criterion-same-size-text", func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET text = 'other-text' WHERE item_id = 'item-a' AND criterion_id = 'c-sig'`)
		return err
	})
	checkChanged("criterion-claim-class", func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET claim_class = 'owner-only' WHERE item_id = 'item-a' AND criterion_id = 'c-sig'`)
		return err
	})
	checkChanged("edge-delete", func(tx *store.Tx) error {
		_, err := tx.Exec(`DELETE FROM item_edges WHERE item_id = 'item-b' AND blocked_by = 'item-a'`)
		return err
	})

	// Restore original logical content in reversed physical rowid order and verify signature matches base.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, stmt := range []string{
			`UPDATE items SET body_ref = NULL, title = 'ab', rank = 'c', prose = 'alpha', state = 'in-queue', express = 0 WHERE item_id = 'item-a'`,
			`UPDATE item_criteria SET text = 'first-text', claim_class = 'verification-only' WHERE item_id = 'item-a' AND criterion_id = 'c-sig'`,
			`CREATE TEMP TABLE saved_item_a AS SELECT * FROM items WHERE item_id = 'item-a'`,
			`CREATE TEMP TABLE saved_crit_a AS SELECT * FROM item_criteria WHERE item_id = 'item-a'`,
			`DELETE FROM item_criteria WHERE item_id = 'item-a'`,
			`DELETE FROM items WHERE item_id = 'item-a'`,
			`INSERT INTO items SELECT * FROM saved_item_a`,
			`INSERT INTO item_criteria SELECT * FROM saved_crit_a`,
			`DROP TABLE saved_item_a`,
			`DROP TABLE saved_crit_a`,
		} {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		var rowA, rowB int64
		if err := tx.QueryRow(`SELECT rowid FROM items WHERE item_id = 'item-a'`).Scan(&rowA); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT rowid FROM items WHERE item_id = 'item-b'`).Scan(&rowB); err != nil {
			return err
		}
		if rowA <= rowB {
			t.Fatalf("expected item-a rowid (%d) > item-b rowid (%d) after reinsert", rowA, rowB)
		}
		return nil
	})
	if got := readSig(); got != base {
		t.Fatalf("signature changed when physical rowid order changed for identical logical rows: got %x, want %x", got, base)
	}
}
