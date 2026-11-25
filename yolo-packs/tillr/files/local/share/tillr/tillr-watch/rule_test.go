package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func ruleQueryRow(t *testing.T, s *store.Store, query string, dest ...any) {
	t.Helper()
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(query).Scan(dest...)
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestWatchTuneDisableResolvesEpisode(t *testing.T) {
	s, dir := fixture(t, false)
	ctx := context.Background()
	invoke := func(args ...string) string {
		t.Helper()
		args = append(args, "--store", s.Path(), "--rules", dir)
		var out, diag bytes.Buffer
		if code := run(ctx, args, &out, &diag); code != 0 || diag.Len() != 0 {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q", args, code, &out, &diag)
		}
		return out.String()
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO foreground_delivery(owner_handle) VALUES ('foreground')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	invoke("eval", "--once")
	for range 2 {
		fact(t, s, `{"item_id":"candidate"}`)
	}
	invoke("eval", "--once")
	assertOpen := func() {
		t.Helper()
		if out := invoke("status"); !strings.Contains(out, `rule=refusals subject="candidate" state=open`) || !strings.Contains(out, " pending=1\n") {
			t.Fatalf("expected a pending open episode: %s", out)
		}
	}
	assertOpen()
	invoke("rule", "tune", "refusals", "--set=enabled=false", "--by=operator")
	assertOpen() // The tune itself must not resolve the episode.
	for range 2 {
		fact(t, s, `{"item_id":"another-candidate"}`)
	}
	invoke("eval", "--once")
	var state, reason string
	ruleQueryRow(t, s, `SELECT state, COALESCE(resolved_reason, '') FROM alert_episodes WHERE subject_key='candidate'`, &state, &reason)
	if state != "resolved" || reason != "condition-cleared" {
		t.Fatalf("episode state=%q reason=%q after evaluating disabled rule", state, reason)
	}
	if out := invoke("status"); strings.Contains(out, "alert ") || !strings.Contains(out, " pending=0\n") {
		t.Fatalf("expected no unresolved or pending episode: %s", out)
	}
	var opened, resolved int
	ruleQueryRow(t, s, `SELECT count(*) FILTER (WHERE kind='watch.alert.opened'), count(*) FILTER (WHERE kind='watch.alert.resolved') FROM events`, &opened, &resolved)
	if opened != 1 || resolved != 1 {
		t.Fatalf("transition counts: opened=%d resolved=%d", opened, resolved)
	}
}

func TestWatchRuleRefusesGitRevision(t *testing.T) {
	s, dir := fixture(t, false)
	ctx := context.Background()
	invoke := func(wantCode int, args ...string) {
		t.Helper()
		args = append(append([]string{"rule"}, args...), "--store", s.Path(), "--rules", dir)
		var out, diag bytes.Buffer
		code := run(ctx, args, &out, &diag)
		if code != wantCode || (wantCode == 0 && diag.Len() != 0) ||
			(wantCode == 1 && (out.Len() != 0 || !strings.Contains(diag.String(), "reads.projections"))) {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q, want code %d", args, code, &out, &diag, wantCode)
		}
	}
	good, err := os.ReadFile("../tillr/watch/testdata/unproved-trunk-lag.toml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lag.toml")
	put := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(good)
	invoke(0, "add", path, "--by=owner")
	type override struct {
		id, toml, by string
		at, head     int64
	}
	snapshot := func() override {
		t.Helper()
		var got override
		ruleQueryRow(t, s, `SELECT rule_id,rule_toml,set_by,set_at,(SELECT max(seq) FROM events) FROM watch_rule_overrides`,
			&got.id, &got.toml, &got.by, &got.at, &got.head)
		return got
	}
	before := snapshot()
	put(bytes.ReplaceAll(good, []byte("refs/heads/main"), []byte("refs/heads/main~1")))
	for _, args := range [][]string{
		{"add", path, "--by=operator"},
		{"tune", before.id, "--by=operator", `--set=when.leader="refs/heads/main~1"`, `--set=reads.projections=["git_refs:refs/heads/main~1,refs/tillr/green"]`},
	} {
		invoke(1, args...)
		if got := snapshot(); got != before {
			t.Fatalf("refused rule %s changed the stored override or appended an event", args[0])
		}
	}
}

func TestWatchRuleCommands(t *testing.T) {
	s, dir := fixture(t, false)
	ctx := context.Background()
	// Guard every non-override/non-event table: rule commands never mutate episodes,
	// delivery cursors, items, runs or repairs.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		rows, err := tx.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT IN ('watch_rule_overrides', 'watch_rule_baselines', 'events', 'sqlite_sequence')`)
		if err != nil {
			return err
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for i, table := range tables {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
				if _, err := tx.Exec(fmt.Sprintf(`CREATE TRIGGER guard_rule_%d_%s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'rule touched %s'); END`, i, op, op, quoted, table)); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	invoke := func(ok bool, args ...string) (string, string) {
		t.Helper()
		args = append(append([]string{"rule"}, args...), "--store", s.Path(), "--rules", dir)
		var out, stderr bytes.Buffer
		code := run(ctx, args, &out, &stderr)
		if ok && (code != 0 || stderr.Len() != 0) || !ok && (code != 1 || stderr.Len() == 0 || out.Len() != 0) {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q", args, code, &out, &stderr)
		}
		return out.String(), stderr.String()
	}
	put := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	xrule := func(id string) string { return strings.Replace(testRule, `id="refusals"`, `id="`+id+`"`, 1) }
	fileOnly := filepath.Join(dir, "file-only.toml")
	put(fileOnly, xrule("zzz"))
	if out, _ := invoke(true, "list"); out != "rule refusals source=file enabled=true scope=core pattern=count_in_window\nrule zzz source=file enabled=true scope=core pattern=count_in_window\n" {
		t.Fatalf("file list: %q", out)
	}
	if out, _ := invoke(true, "export"); out != "" {
		t.Fatalf("empty export: %q", out)
	}
	for _, helpArgs := range [][]string{{"rule", "--help"}, {"rule", "tune", "--help"}} {
		var out, stderr bytes.Buffer
		if code := run(ctx, helpArgs, &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "tillr watch rule tune") {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q", helpArgs, code, &out, &stderr)
		}
	}
	for _, bad := range [][]string{
		{},
		{"unknown"},
		{"list", "extra"},
		{"export", "extra"},
		{"show"},
		{"show", "refusals", "extra"},
		{"show", "missing"},
		{"reset", "refusals", "--by=owner"},
		{"reset", "refusals"},
		{"tune", "missing", "--set=enabled=false", "--by=owner"},
		{"tune", "refusals", "--set=enabled=false"},
		{"tune", "refusals", "--by=owner"},
		{"tune", "refusals", "--unknown-flag", "--set=enabled=false", "--by=owner"},
	} {
		invoke(false, bad...)
	}
	for _, rawArgs := range [][]string{
		{"rule", "list", "--store", s.Path()},
		{"rule", "list", "--rules", dir},
	} {
		var out, stderr bytes.Buffer
		if code := run(ctx, rawArgs, &out, &stderr); code != 1 || out.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("missing required flag %q: code=%d stdout=%q stderr=%q", rawArgs, code, &out, &stderr)
		}
	}

	add := filepath.Join(t.TempDir(), "incoming.toml")
	storedRefusals := strings.Replace(testRule, `severity="warning"`, `severity="high"`, 1)
	put(add, xrule("aaa")+strings.Replace(storedRefusals, `receiver="foreground"`, `receiver="drain:build"`, 1))
	invoke(false, "add", add, "--by=owner")
	if out, _ := invoke(true, "export"); out != "" {
		t.Fatalf("bad add partially stored: %q", out)
	}
	put(add, storedRefusals+xrule("aaa"))
	invoke(true, "add", "--by=owner", add)
	invoke(true, "tune", "refusals", "--set=when.pattern='alternating_pair'", "--set=when.field='step'", `--set=when.pairs=[["a", "b"]]`, "--set=enabled=false", `--set=guidance.summary="review = only"`, "--by=operator")
	baseline, _ := invoke(true, "export")
	for _, set := range []string{
		"when.threshold=0",
		"when.unknown=true",
		"id='rename'",
		"guidance.do=7",
		"delivery.receiver='drain:design'",
		"guidance.remedy='run'",
		"guidance.summary=text",
		"enabled=false\nextra=true",
		"missing_equals",
	} {
		invoke(false, "tune", "refusals", "--set=enabled=true", "--set="+set, "--by=operator")
		if out, _ := invoke(true, "export"); out != baseline {
			t.Fatalf("refused tune %q changed the export", set)
		}
	}
	if out, _ := invoke(true, "list"); out != "rule aaa source=store enabled=true scope=core pattern=count_in_window\nrule refusals source=store enabled=false scope=core pattern=alternating_pair\nrule zzz source=file enabled=true scope=core pattern=count_in_window\n" {
		t.Fatalf("effective list: %q", out)
	}
	// While a stored rule exists, changing the file rule of the same ID has no effect.
	put(filepath.Join(dir, "nested/refusals.toml"), strings.Replace(testRule, `severity="warning"`, `severity="info"`, 1))
	show, _ := invoke(true, "show", "refusals")
	for src, text := range map[string]string{"show": show, "export": baseline} {
		loaded, err := watch.LoadRuleDocument(src, []byte(text))
		if err != nil || len(loaded) == 0 || src == "show" && len(loaded) != 1 || src == "export" && (len(loaded) != 2 || loaded[0].ID != "aaa") {
			t.Fatalf("%s did not round trip: %s (%v)", src, text, err)
		}
		rule := loaded[len(loaded)-1]
		if rule.ID != "refusals" || rule.Severity != "high" || rule.Enabled || rule.When.Pattern != watch.AlternatingPair || rule.Guidance.Summary != "review = only" {
			t.Fatalf("%s: unexpected whole rule: %+v", src, rule)
		}
	}
	invoke(true, "reset", "refusals", "--by=operator")
	show, _ = invoke(true, "show", "refusals")
	loaded, err := watch.LoadRuleDocument("show", []byte(show))
	if err != nil || len(loaded) != 1 || !loaded[0].Enabled || loaded[0].Severity != "info" || loaded[0].When.Pattern != watch.CountInWindow {
		t.Fatalf("reset did not restore file: %s (%v)", show, err)
	}
	invoke(true, "reset", "aaa", "--by=operator")
	invoke(false, "show", "aaa")
	if data, err := os.ReadFile(fileOnly); err != nil || string(data) != xrule("zzz") {
		t.Fatalf("rule commands touched a file: %s (%v)", data, err)
	}
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		rows, err := rd.Query(`SELECT kind, object_type, object_id, json_extract(payload,'$.rule_id'), json_extract(payload,'$.by') FROM events ORDER BY seq`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var kind, objType, objID, ruleID, by string
			if err := rows.Scan(&kind, &objType, &objID, &ruleID, &by); err != nil {
				return err
			}
			got = append(got, strings.Join([]string{kind, objType, objID, ruleID, by}, " "))
		}
		want := []string{
			"watch.rule.set rule refusals refusals owner",
			"watch.rule.set rule aaa aaa owner",
			"watch.rule.set rule refusals refusals operator",
			"watch.rule.reset rule refusals refusals operator",
			"watch.rule.reset rule aaa aaa operator",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("events: got %q, want %q", got, want)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchRuleRequiresExistingStoreAndLeavesSidecarsUntouched(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "0")
	s, rules := fixture(t, false)
	missing := filepath.Join(t.TempDir(), "missing.db")
	addFile := filepath.Join(t.TempDir(), "add.toml")
	if err := os.WriteFile(addFile, []byte(testRule), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"rule", "list", "--rules", rules},
		{"rule", "show", "refusals", "--rules", rules},
		{"rule", "export", "--rules", rules},
		{"rule", "add", addFile, "--by", "operator", "--rules", rules},
		{"rule", "tune", "refusals", "--set", "enabled=false", "--by", "operator", "--rules", rules},
		{"rule", "reset", "refusals", "--by", "operator", "--rules", rules},
	} {
		var stderr bytes.Buffer
		if code := run(t.Context(), append(args, "--store", missing), io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), missing) {
			t.Fatalf("%v on missing store: code=%d stderr=%q", args, code, &stderr)
		}
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Fatalf("%v created missing store: %v", args, err)
		}
	}

	// Seed one stored override, checkpoint and copy to a standalone file with no sidecars,
	// then verify list, show and export create no WAL/SHM sidecars and modify zero bytes.
	if code := run(t.Context(), []string{"rule", "tune", "refusals", "--set", "when.threshold=4", "--by", "operator", "--store", s.Path(), "--rules", rules}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("seed tune: code=%d", code)
	}
	seed := s.Path()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", seed)
	if err != nil {
		t.Fatal(err)
	}
	var busy, frames, checkpointed int
	err = db.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed)
	if closeErr := db.Close(); err != nil || closeErr != nil || busy != 0 {
		t.Fatalf("checkpoint: %d %d %d, query=%v close=%v", busy, frames, checkpointed, err, closeErr)
	}
	before, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	isolated := filepath.Join(t.TempDir(), "checkpointed.db")
	if err := os.WriteFile(isolated, before, 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{
		{"rule", "list"},
		{"rule", "show", "refusals"},
		{"rule", "export"},
	} {
		var out, stderr bytes.Buffer
		if code := run(t.Context(), append(cmd, "--store", isolated, "--rules", rules), &out, &stderr); code != 0 || stderr.Len() != 0 || out.Len() == 0 {
			t.Fatalf("%v on checkpointed store: code=%d stdout=%q stderr=%q", cmd, code, &out, &stderr)
		}
	}
	after, err := os.ReadFile(isolated)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read-only rule command modified checkpointed store bytes: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(isolated))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(isolated) {
		t.Fatalf("read-only rule command created sidecars: %v (%v)", entries, err)
	}

	// Refusals on a rollback-mode store must preserve DELETE journal mode and bytes.
	rollback := filepath.Join(t.TempDir(), "rollback.db")
	rdb, err := sql.Open("sqlite", rollback)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rdb.ExecContext(t.Context(), `CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		rdb.Close()
		t.Fatal(err)
	}
	if err := rdb.Close(); err != nil {
		t.Fatal(err)
	}
	rollbackBefore, err := os.ReadFile(rollback)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{
		{"rule", "tune", "refusals", "--set", "when.threshold=0", "--by", "operator"},
		{"rule", "reset", "refusals", "--by", "operator"},
	} {
		var out, stderr bytes.Buffer
		if code := run(t.Context(), append(cmd, "--store", rollback, "--rules", rules), &out, &stderr); code != 1 || out.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("%v on rollback store: code=%d stdout=%q stderr=%q", cmd, code, &out, &stderr)
		}
	}
	rollbackAfter, err := os.ReadFile(rollback)
	if err != nil || !bytes.Equal(rollbackBefore, rollbackAfter) {
		t.Fatalf("refusal modified rollback-mode store bytes: %v", err)
	}

	// A stored rule refused when read names store and exits 1 across rule subcommands.
	corrupt, _ := fixture(t, false)
	seedStatusOverride(t, corrupt, "refusals", strings.Replace(testRule, "drain.refusal", "unregistered.kind", 1))
	for _, cmd := range [][]string{
		{"rule", "list"},
		{"rule", "show", "refusals"},
		{"rule", "export"},
		{"rule", "tune", "refusals", "--set", "enabled=false", "--by", "operator"},
		{"rule", "reset", "refusals", "--by", "operator"},
		{"rule", "add", addFile, "--by", "operator"},
	} {
		var out, stderr bytes.Buffer
		if code := run(t.Context(), append(cmd, "--store", corrupt.Path(), "--rules", rules), &out, &stderr); code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "store") || !strings.Contains(stderr.String(), "unregistered.kind") {
			t.Fatalf("%v on corrupt stored rule: code=%d stdout=%q stderr=%q", cmd, code, &out, &stderr)
		}
	}
}
