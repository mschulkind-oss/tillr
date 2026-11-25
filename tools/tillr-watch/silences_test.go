package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestWatchCLISilenceByRuleAndVarAndGlobalAndListAndLift(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "state.db")
	rulesDir := filepath.Join(tmp, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "pkg-red.toml"), []byte(`
[[rule]]
id = "pkg-red"
scope = "core"
severity = "warning"
enabled = true

[rule.reads]
events = ["drain.lane-refused"]

[rule.when]
pattern = "predicate_match"
group_by = "detail.package"
window = "1h"

[rule.delivery]
receiver = "foreground"

[rule.guidance]
summary = "package {{var.package}} failed"
check = ["inspect logs"]
do = ["fix it"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "global-red.toml"), []byte(`
[[rule]]
id = "global-red"
scope = "core"
severity = "warning"
enabled = true

[rule.reads]
events = ["drain.lane-refused"]

[rule.when]
pattern = "predicate_match"
group_by = "global"
window = "1h"

[rule.delivery]
receiver = "foreground"

[rule.guidance]
summary = "global red"
check = ["inspect logs"]
do = ["fix it"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id, title, state, updated_at, event_seq) VALUES ('item-open', 'open item', 'in-queue', ?, 1)`, now.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.lane-refused', 'lane', 'l1', '{"detail":{"package":"tools/flaky"}}')`, now.UnixMilli()); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	invoke := func(args ...string) (int, string, string) {
		var out, errBuf bytes.Buffer
		code := run(ctx, args, &out, &errBuf)
		return code, out.String(), errBuf.String()
	}

	// Evaluate once to open episode for tools/flaky.
	if code, _, errOut := invoke("eval", "--once", "--store", storePath, "--rules", rulesDir); code != 0 {
		t.Fatalf("eval: %d %s", code, errOut)
	}

	// Exit 2 checks: missing --var, unknown --var, malformed --var, --var on global rule, missing investigation.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing-var", []string{"silence", "--rule", "pkg-red", "--store", storePath, "--rules", rulesDir, "--by", "op", "--item", "item-open", "--reason", "r", "--until", "1h", "--cause", "c", "--evidence", "e"}},
		{"unknown-var", []string{"silence", "--rule", "pkg-red", "--var", "bogus=1", "--store", storePath, "--rules", rulesDir, "--by", "op", "--item", "item-open", "--reason", "r", "--until", "1h", "--cause", "c", "--evidence", "e"}},
		{"malformed-var", []string{"silence", "--rule", "pkg-red", "--var", "noequals", "--store", storePath, "--rules", rulesDir, "--by", "op", "--item", "item-open", "--reason", "r", "--until", "1h", "--cause", "c", "--evidence", "e"}},
		{"global-with-var", []string{"silence", "--rule", "global-red", "--var", "x=y", "--store", storePath, "--rules", rulesDir, "--by", "op", "--item", "item-open", "--reason", "r", "--until", "1h", "--cause", "c", "--evidence", "e"}},
		{"missing-investigation", []string{"silence", "--rule", "pkg-red", "--var", "package=tools/flaky", "--store", storePath, "--rules", rulesDir, "--by", "op", "--item", "item-open", "--reason", "r", "--until", "1h"}},
	} {
		code, _, errOut := invoke(tc.args...)
		if code != 2 {
			t.Errorf("%s: code=%d, want 2 (stderr=%q)", tc.name, code, errOut)
		}
	}

	// Create silence for parameterized rule and global rule.
	code, out, errOut := invoke("silence", "--rule", "pkg-red", "--var", "package=tools/flaky", "--store", storePath, "--rules", rulesDir, "--by", "operator-session", "--item", "item-open", "--reason", "flaky", "--until", "1h", "--cause", "known race", "--evidence", "seq 1")
	if code != 0 || !strings.Contains(out, "silence sil-") {
		t.Fatalf("silence pkg-red: code=%d out=%q err=%q", code, out, errOut)
	}
	code, out, errOut = invoke("silence", "--rule", "global-red", "--store", storePath, "--rules", rulesDir, "--by", "operator-session", "--item", "item-open", "--reason", "global", "--until", "1h", "--cause", "known global", "--evidence", "seq 1")
	if code != 0 || !strings.Contains(out, `vars={}`) {
		t.Fatalf("silence global-red: code=%d out=%q err=%q", code, out, errOut)
	}

	// silence list --json must be read-only.
	beforeBytes, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	code, listJSON, errOut := invoke("silence", "list", "--store", storePath, "--json")
	if code != 0 {
		t.Fatalf("silence list --json: code=%d err=%q", code, errOut)
	}
	afterBytes, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("silence list mutated store")
	}
	var recs []watch.SilenceRecord
	if err := json.Unmarshal([]byte(listJSON), &recs); err != nil {
		t.Fatalf("unmarshal list: %v (%s)", err, listJSON)
	}
	if len(recs) != 2 {
		t.Fatalf("active silences = %d, want 2", len(recs))
	}
	var pkgSilID string
	for _, r := range recs {
		if r.RuleID == "pkg-red" {
			pkgSilID = r.SilenceID
		}
	}
	if pkgSilID == "" {
		t.Fatalf("missing pkg-red silence in %+v", recs)
	}

	// Lift pkg-red silence -> reopens firing episode as open and uninvestigated.
	if code, _, errOut := invoke("silence", "lift", pkgSilID, "--store", storePath, "--by", "", "--reason", "done"); code != 1 {
		t.Fatalf("expected empty --by to fail with code 1, got %d (%q)", code, errOut)
	}
	code, out, errOut = invoke("silence", "lift", pkgSilID, "--store", storePath, "--by", "operator-session", "--reason", "fixed early")
	if code != 0 || !strings.Contains(out, "lifted") {
		t.Fatalf("silence lift: code=%d out=%q err=%q", code, out, errOut)
	}
	// Already lifted -> refused.
	if code, _, _ := invoke("silence", "lift", pkgSilID, "--store", storePath, "--by", "operator-session", "--reason", "again"); code != 1 {
		t.Fatalf("expected already-lifted to fail, got %d", code)
	}

	// Active list now has 1; --all has 2.
	code, listText, errOut := invoke("silence", "list", "--store", storePath)
	if code != 0 || strings.Count(listText, "silence sil-") != 1 {
		t.Fatalf("active list: code=%d out=%q err=%q", code, listText, errOut)
	}
	code, allText, errOut := invoke("silence", "list", "--store", storePath, "--all")
	if code != 0 || strings.Count(allText, "silence sil-") != 2 || !strings.Contains(allText, `lifted_reason="fixed early"`) {
		t.Fatalf("all list: code=%d out=%q err=%q", code, allText, errOut)
	}
}

func TestWatchCLILegacyStoreMigrationOnSilence(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "legacy.db")

	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		PRAGMA journal_mode=DELETE;
		CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES (6, 1000);
		CREATE TABLE definition_versions (version INTEGER PRIMARY KEY, raw TEXT NOT NULL, parsed_json TEXT NOT NULL, at INTEGER NOT NULL);
		CREATE TABLE events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			kind TEXT NOT NULL,
			seat TEXT,
			object_type TEXT,
			object_id TEXT,
			revision INTEGER,
			payload TEXT NOT NULL
		);
		CREATE TABLE items (
			item_id TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			event_seq INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE alert_episodes (
			alert_id TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			subject_key TEXT NOT NULL,
			episode_num INTEGER NOT NULL,
			state TEXT NOT NULL,
			severity TEXT NOT NULL,
			condition_fingerprint TEXT NOT NULL,
			opened_seq INTEGER NOT NULL,
			opened_at INTEGER NOT NULL,
			last_observed_seq INTEGER NOT NULL,
			last_observed_at INTEGER NOT NULL,
			observation_count INTEGER NOT NULL DEFAULT 1,
			acked_by TEXT,
			acked_note TEXT,
			acked_at INTEGER,
			silence_until_ms INTEGER,
			silence_on_fingerprint TEXT,
			resolved_seq INTEGER,
			resolved_at INTEGER,
			resolved_reason TEXT,
			bundle_json TEXT NOT NULL
		);
		INSERT INTO items(item_id, state, event_seq) VALUES ('item-open', 'in-queue', 1);
		INSERT INTO alert_episodes(alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json)
			VALUES ('alrt-1', 'pkg-red', 'tools/flaky', 1, 'open', 'warning', '', 1, 1000, 1, 1000, '{}');
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	// Refused silence (missing investigation) must leave the unmigrated store byte-for-byte untouched.
	var out, errBuf bytes.Buffer
	code := run(ctx, []string{"silence", "alrt-1", "--store", storePath, "--by", "operator", "--item", "item-open", "--reason", "r", "--until", "1h"}, &out, &errBuf)
	if code != 2 {
		t.Fatalf("expected exit 2 on uninvestigated silence, got %d (%q)", code, errBuf.String())
	}
	after, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused silence mutated unmigrated legacy store")
	}

	// Valid silence with inline investigation migrates and succeeds.
	out.Reset()
	errBuf.Reset()
	code = run(ctx, []string{"silence", "alrt-1", "--store", storePath, "--by", "operator", "--item", "item-open", "--reason", "r", "--until", "1h", "--cause", "known", "--evidence", "seq 1"}, &out, &errBuf)
	if code != 0 || !strings.Contains(out.String(), "alrt-1 silenced (sil-") {
		t.Fatalf("valid silence on legacy store: code=%d out=%q err=%q", code, out.String(), errBuf.String())
	}
}
