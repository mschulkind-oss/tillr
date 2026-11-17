package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestCopiedUsageFaces(t *testing.T) {
	path := filepath.Join(scratch(t, "copied-faces"), "state.db")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('lane','idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs(run_id,lane_id,backend,state,started_at,finished_at,usage_tokens,usage_cost_usd) VALUES
   ('source','lane','pi','exited',1000,1100,1500,.03),('copy','lane','pi','exited',2000,2100,1500,.03)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out := run(t, "copied-usage", "--store", path)
	if code != 0 {
		t.Fatalf("detector exit %d: %s", code, out)
	}
	var matches []map[string]any
	if err := json.Unmarshal(out, &matches); err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0]["run_id"] != "copy" || matches[0]["source_run_id"] != "source" {
		t.Fatalf("matches: %s", out)
	}
	for _, args := range [][]string{
		{"mark-copied-usage", "--store", path, "--run", "copy", "--source-run", "source"},
		{"mark-copied-usage", "--store", path, "--run", "source", "--source-run", "copy", "--reason", "bad"},
		{"copied-usage", "--store", path, "--apply"},
	} {
		code, _ := run(t, args...)
		if code != 2 {
			t.Fatalf("%v exit %d, want 2", args, code)
		}
	}
	for _, face := range []string{"mark-copied-usage", "copied-usage"} {
		// Each face corrects the same fixture independently.
		code, out = run(t, append([]string{face, "--store", path, "--reason", "preceding report reused"}, map[string][]string{
			"mark-copied-usage": {"--run", "copy", "--source-run", "source"}, "copied-usage": {"--apply"},
		}[face]...)...)
		if code != 0 {
			t.Fatalf("%s exit %d: %s", face, code, out)
		}
		var tokens, cost any
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT usage_tokens,usage_cost_usd FROM runs WHERE run_id='copy'`).Scan(&tokens, &cost)
		}); err != nil {
			t.Fatal(err)
		}
		if tokens != nil || cost != nil {
			t.Fatalf("usage not unknown: %v %v", tokens, cost)
		}
		if face == "mark-copied-usage" {
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE runs SET usage_tokens=1500,usage_cost_usd=.03 WHERE run_id='copy'`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	code, out = run(t, "copied-usage", "--store", path, "--apply", "--reason", "again")
	if code != 0 || string(out) != "[]\n" {
		t.Fatalf("empty batch: %d %s", code, out)
	}
}
