package store_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestJudgeMigrationArchivesLegacyRowsAtomically(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	const grant = ` {"grant_id":"z", "grantor":"a"} `
	const verdict = ` {"grant_id":"z", "actor":"judge", "evidence":["test:case"]} `
	const graph = `{"stages":[{"id":"verify","kind":"agent","access":"read_only"}],"exit":{"evaluator_slot":"verify","requires_nodes":["verify"]}}`
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='item_grants'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("fresh store created the retired table")
		}
		if err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind='store.judge-grants-archived'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("fresh store archived grants it never held")
		}
		_, err := tx.Exec(`
CREATE TABLE item_grants(grant_id TEXT PRIMARY KEY, claim_classes TEXT NOT NULL, grantor TEXT NOT NULL, issued_at INTEGER NOT NULL, expires_at INTEGER NOT NULL);
INSERT INTO item_grants VALUES('z','direction','owner',12,345),('a','verification-only','owner',6,789);
CREATE TABLE workflow_selections(item_id TEXT PRIMARY KEY, workflow TEXT, source TEXT, graph TEXT);
INSERT INTO workflow_selections VALUES('item-a','test@v1','block',?1),('item-b','single-worker@v1','implicit',''),('item-c','single-worker@v1','implicit','');
ALTER TABLE items ADD COLUMN judge_mode TEXT NOT NULL DEFAULT '';
INSERT INTO items(item_id,title,updated_at,state,judge_mode) VALUES
 ('item-a','Evaluator',1,'in-queue',''),('item-b','Opt-in',1,'in-queue','agent'),('item-c','Mechanical',1,'in-queue','');
CREATE TABLE node_runs(item_id TEXT, attempt INTEGER);
INSERT INTO node_runs VALUES('item-a',1),('item-a',2);
CREATE TABLE graph_revisions(revision_id INTEGER PRIMARY KEY,item_id TEXT,attempt INTEGER,rule TEXT,after_graph TEXT);
INSERT INTO graph_revisions VALUES(1,'item-a',2,'',?1),(2,'item-a',2,'proposal-refused','{}');
INSERT INTO events(ts,kind,payload) VALUES(1,'item.grant',?2),(2,'item.verdict',?3);
DELETE FROM schema_migrations;
INSERT INTO schema_migrations VALUES(5,1);
CREATE TRIGGER refuse_v6 BEFORE INSERT ON schema_migrations WHEN NEW.version=6
BEGIN SELECT RAISE(ABORT, 'test migration rollback'); END;`, graph, grant, verdict)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	check := func(migrated bool) {
		t.Helper()
		r, err := store.OpenRead(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if err := r.ReadTx(ctx, func(r *store.Reader) error {
			var tables, archived, version int
			var stored, oldGrant, oldVerdict string
			for _, q := range []struct {
				sql string
				out any
			}{
				{`SELECT count(*) FROM sqlite_master WHERE name='item_grants'`, &tables},
				{`SELECT count(*) FROM events WHERE kind='store.judge-grants-archived'`, &archived},
				{`SELECT max(version) FROM schema_migrations`, &version},
				{`SELECT graph FROM workflow_selections WHERE item_id='item-a'`, &stored},
				{`SELECT payload FROM events WHERE kind='item.grant'`, &oldGrant},
				{`SELECT payload FROM events WHERE kind='item.verdict'`, &oldVerdict},
			} {
				if err := r.QueryRow(q.sql).Scan(q.out); err != nil {
					return err
				}
			}
			if oldGrant != grant || oldVerdict != verdict {
				t.Errorf("historical event bytes changed: %q, %q", oldGrant, oldVerdict)
			}
			if !migrated {
				if tables != 1 || archived != 0 || version != 5 || stored != graph {
					t.Errorf("premature migration: table=%d archive=%d version=%d graph=%s", tables, archived, version, stored)
				}
				return nil
			}
			if tables != 0 || archived != 1 || version != store.SchemaVersion {
				t.Errorf("wrong migrated state: table=%d archive=%d version=%d", tables, archived, version)
			}
			var classes string
			if err := r.QueryRow(`SELECT json_extract(?,'$.stages[0].judge_classes')`, stored).Scan(&classes); err != nil {
				return err
			}
			if classes != `["verification-only"]` {
				t.Errorf("migrated classes: %s", classes)
			}
			var attempt int
			var selectedRevision string
			if err := r.QueryRow(`SELECT judge_attempt,json_extract(judge_graph,'$.stages[0].judge_classes') FROM workflow_selections WHERE item_id='item-a'`).Scan(&attempt, &selectedRevision); err != nil {
				return err
			}
			if attempt != 2 || selectedRevision != classes {
				t.Errorf("wrong in-flight evaluator revision: attempt=%d classes=%s", attempt, selectedRevision)
			}
			var compat, unchanged string
			if err := r.QueryRow(`SELECT graph FROM workflow_selections WHERE item_id='item-b'`).Scan(&compat); err != nil {
				return err
			}
			if err := r.QueryRow(`SELECT graph FROM workflow_selections WHERE item_id='item-c'`).Scan(&unchanged); err != nil {
				return err
			}
			if want := store.CompatibilityJudgeGraph("single-worker@v1", ""); compat != want || compat == "" || unchanged != "" {
				t.Errorf("legacy opt-ins changed incorrectly: compat=%q mechanical=%q", compat, unchanged)
			}
			var raw []byte
			if err := r.QueryRow(`SELECT payload FROM events WHERE kind='store.judge-grants-archived'`).Scan(&raw); err != nil {
				return err
			}
			var body struct{ Rows []map[string]any }
			if err := json.Unmarshal(raw, &body); err != nil {
				return err
			}
			want := []map[string]any{
				{"grant_id": "a", "claim_classes": "verification-only", "grantor": "owner", "issued_at": float64(6), "expires_at": float64(789)},
				{"grant_id": "z", "claim_classes": "direction", "grantor": "owner", "issued_at": float64(12), "expires_at": float64(345)},
			}
			if !reflect.DeepEqual(body.Rows, want) {
				t.Errorf("wrong archive or order: %s", raw)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check(false)
	if s, err := store.Open(ctx, path, true); err == nil {
		s.Close()
		t.Fatal("migration unexpectedly passed the rollback trigger")
	} else if !strings.Contains(err.Error(), "test migration rollback") {
		t.Fatalf("migration failed before reaching the marker: %v", err)
	}
	check(false)
	s, err = store.Open(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER refuse_v6`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for range 2 {
		s, err := store.Open(ctx, path, true)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		check(true)
	}
}
