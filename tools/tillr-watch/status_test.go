package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func seedStatusOverride(t *testing.T, s *store.Store, id, rule string) {
	t.Helper()
	if err := s.WriteTx(t.Context(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS watch_rule_overrides (
			rule_id TEXT PRIMARY KEY, rule_toml TEXT NOT NULL, set_by TEXT NOT NULL, set_at INTEGER NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`REPLACE INTO watch_rule_overrides VALUES (?, ?, 'operator', 1234)`, id, rule)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchStatusScopeDiagnostics(t *testing.T) {
	s, rules := fixture(t, false)
	const charter = "custom"
	content := `{"definition":"custom","version":1,"states":["ready","blocked","closed"],"ready":"ready","blocked":"blocked","closed":"closed","finished":["closed"],"completes":"landing"}`
	record, err := json.Marshal(map[string]any{"definition": charter, "version": 1, "content": content, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(content))), "default": false})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(t.Context(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('store.definition',?)`, string(record))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	seedStatusOverride(t, s, "refusals", strings.Replace(testRule, `scope="core"`, `scope="workflow:build@v1"`, 1))
	seedStatusOverride(t, s, "charter", strings.NewReplacer(`id="refusals"`, `id="charter"`, `scope="core"`, `scope="charter:other"`).Replace(testRule))
	seedStatusOverride(t, s, "unsupported", strings.NewReplacer(`id="refusals"`, `id="unsupported"`, "[rule.when]", "projections=['work_nodes']\n[rule.when]").Replace(testRule))
	if err := s.WriteTx(t.Context(), func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TABLE workflow_selections`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fact(t, s, `{"item_id":7}`)
	fact(t, s, `{"item_id":"selected"}`)
	fact(t, s, `{"item_id":"not-selected"}`)
	var stdout, stderr bytes.Buffer
	args := []string{"status", "--store", s.Path(), "--rules", rules}
	if code := run(t.Context(), append(args, "--json"), &stdout, &stderr); code != 0 {
		t.Fatalf("status: %d %s", code, &stderr)
	}
	var report struct {
		Rules []struct {
			ID, Unreadable string
			OutOfScope     string `json:"out_of_scope"`
			Skipped        *int
		}
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || len(report.Rules) != 2 ||
		report.Rules[0].ID != "charter" || report.Rules[0].OutOfScope != charter || report.Rules[0].Skipped != nil ||
		report.Rules[1].ID != "refusals" || report.Rules[1].Skipped != nil || !strings.Contains(report.Rules[1].Unreadable, "workflow_selections") {
		t.Fatalf("scope JSON: %s (%v)", &stdout, err)
	}
	if !strings.Contains(stderr.String(), `rule unsupported not evaluated: unsupported projection "work_nodes"`) {
		t.Fatalf("unsupported rule diagnostic: %s", &stderr)
	}
	if err := s.WriteTx(t.Context(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TABLE workflow_selections (item_id TEXT PRIMARY KEY, workflow TEXT NOT NULL);
			INSERT INTO workflow_selections VALUES ('selected', 'build@v1')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(t.Context(), args, &stdout, &stderr); code != 0 || stdout.String() != fmt.Sprintf("rule charter out-of-scope=%q\nrule refusals skipped=1\n", charter) {
		t.Fatalf("scope text: %d, stdout=%s stderr=%s", code, &stdout, &stderr)
	}
}

func TestWatchStatusRejectsInvalidStoredRule(t *testing.T) {
	s, rules := fixture(t, false)
	for _, tc := range []struct{ doc, want string }{
		{"[invalid\n", `stored rule "refusals"`},
		{strings.Replace(testRule, `id="refusals"`, `id="mismatch"`, 1), "expected one rule with the row's id"},
		{strings.NewReplacer(`scope="core"`, `scope="workflow:build@v1"`, `group_by="item_id"`, `group_by="global"`).Replace(testRule), "named workflow scope requires a subject"},
	} {
		seedStatusOverride(t, s, "refusals", tc.doc)
		var stdout, stderr bytes.Buffer
		if code := run(t.Context(), []string{"status", "--store", s.Path(), "--rules", rules}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("invalid stored rule: %d, stdout=%s stderr=%s; want %q", code, &stdout, &stderr, tc.want)
		}
	}
}

func TestWatchStatusMissingTablesJSON(t *testing.T) {
	s, rules := fixture(t, false)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TABLE alert_episodes; DROP TABLE foreground_delivery`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"status", "--store", s.Path(), "--rules", rules, "--json"}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("status = %d, %s", code, &stderr)
	}
	if want := "{\"rules\":[{\"id\":\"refusals\",\"skipped\":0}],\"alerts\":[],\"owners\":[]}\n"; out.String() != want {
		t.Fatalf("got %s, want %s", &out, want)
	}
	after, err := os.ReadFile(s.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("status changed database: %v", err)
	}
	if _, err := os.Stat(s.Path() + ".watch-attached"); !os.IsNotExist(err) {
		t.Fatalf("status created attachment directory: %v", err)
	}
}

func TestWatchStatusCountsLatestOpeningNotice(t *testing.T) {
	s, rules := fixture(t, false)
	ctx := t.Context()
	common := []string{"--store", s.Path(), "--rules", rules}
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	var stderr bytes.Buffer
	if code := run(ctx, append([]string{"eval", "--once"}, common...), io.Discard, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("eval: %d, stderr %q", code, &stderr)
	}
	write := func(f func(*store.Tx) error) {
		t.Helper()
		if err := s.WriteTx(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	write(func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO foreground_delivery(owner_handle, shown_seq, seen_seq)
			SELECT 'foreground', seq, seq FROM events WHERE kind='watch.alert.opened'`)
		return err
	})
	checkPending := func(want int) {
		t.Helper()
		var out, stderr bytes.Buffer
		if code := run(ctx, append([]string{"status", "--json"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("status: %d, stderr %q", code, &stderr)
		}
		var report struct {
			Owners []struct {
				Owner   string
				Pending *int
			}
		}
		if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Owners) != 1 ||
			report.Owners[0].Owner != "foreground" || report.Owners[0].Pending == nil || *report.Owners[0].Pending != want {
			t.Fatalf("want foreground pending=%d, got %s (%v)", want, &out, err)
		}
	}
	checkPending(0)
	// Seed multiple reopenings to test status independently of evaluation;
	// the episode is already open and its original notice was confirmed.
	write(func(tx *store.Tx) error {
		for i := 0; i < 2; i++ {
			if _, err := tx.Exec(`INSERT INTO events(ts, kind, object_type, object_id, payload)
				SELECT ts, 'watch.alert.reopened', object_type, object_id, '{"cause":"silence-expired"}'
				FROM events WHERE kind='watch.alert.opened'`); err != nil {
				return err
			}
		}
		return nil
	})
	checkPending(1) // Multiple unconfirmed opening events still count once.
	seedStatusOverride(t, s, "refusals", strings.Replace(testRule, `receiver="foreground"`, `receiver="subscribers"`, 1))
	checkPending(0) // The effective receiver no longer routes the reopening to foreground.
	seedStatusOverride(t, s, "refusals", testRule)
	write(func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE foreground_delivery SET (shown_seq, seen_seq) =
			(SELECT MAX(seq), MAX(seq) FROM events WHERE kind='watch.alert.reopened')`)
		return err
	})
	checkPending(0)
}

func TestWatchStatusSortedStoredRows(t *testing.T) {
	s, rules := fixture(t, false)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(rules, "a.toml"), []byte(strings.Replace(testRule, `id="refusals"`, `id="aaa"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []string{"bob", "alice", "charlie", "done"} {
		for i := 0; i < 2; i++ {
			fact(t, s, `{"item_id":"`+subject+`"}`)
		}
	}
	var stderr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, io.Discard, &stderr); code != 0 {
		t.Fatalf("eval = %d: %s", code, &stderr)
	}
	// Create timestamps and IDs with deliberate ties and an episode whose rule is no longer loaded.
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, q := range []string{
			`DELETE FROM alert_episodes WHERE rule_id='aaa'`,
			`UPDATE alert_episodes SET opened_seq=1,opened_at=1000`,
			`UPDATE alert_episodes SET alert_id='z',rule_id='removed',state='acknowledged',acked_by='a"b' WHERE subject_key='bob'`,
			`UPDATE alert_episodes SET alert_id='b',state='silenced',silence_on_fingerprint='abc' WHERE subject_key='alice'`,
			`UPDATE alert_episodes SET alert_id='c',state='silenced',silence_until_ms=3000 WHERE subject_key='charlie'`,
			`UPDATE alert_episodes SET state='resolved' WHERE subject_key='done'`,
			`INSERT INTO foreground_delivery(owner_handle,waiting_since,last_wait_at) VALUES ('z',1000,2000),('foreground',NULL,NULL)`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"status", "--store", s.Path(), "--rules", rules}
	var out bytes.Buffer
	if code := run(ctx, args, &out, &stderr); code != 0 {
		t.Fatalf("status = %d: %s", code, &stderr)
	}
	want := `rule aaa skipped=0
rule refusals skipped=0
alert b rule=refusals subject="alice" state=silenced severity=warning opened=1970-01-01T00:00:01Z observed=1 until=change
alert c rule=refusals subject="charlie" state=silenced severity=warning opened=1970-01-01T00:00:01Z observed=1 until=1970-01-01T00:00:03Z
alert z rule=removed subject="bob" state=acknowledged severity=warning opened=1970-01-01T00:00:01Z observed=1 by="a\"b"
owner foreground watcher=unattached last_wait=- pending=0
owner z watcher=unattached last_wait=1970-01-01T00:00:02Z pending=0
`
	if out.String() != want {
		t.Fatalf("status:\n%s\nwant:\n%s", &out, want)
	}
	out.Reset()
	if code := run(ctx, append(args, "--json"), &out, &stderr); code != 0 {
		t.Fatalf("status = %d: %s", code, &stderr)
	}
	want = `{"rules":[{"id":"aaa","skipped":0},{"id":"refusals","skipped":0}],"alerts":[{"alert_id":"b","rule_id":"refusals","subject":"alice","state":"silenced","severity":"warning","opened":"1970-01-01T00:00:01Z","observed":1,"until":"change"},{"alert_id":"c","rule_id":"refusals","subject":"charlie","state":"silenced","severity":"warning","opened":"1970-01-01T00:00:01Z","observed":1,"until":"1970-01-01T00:00:03Z"},{"alert_id":"z","rule_id":"removed","subject":"bob","state":"acknowledged","severity":"warning","opened":"1970-01-01T00:00:01Z","observed":1,"by":"a\"b"}],"owners":[{"owner":"foreground","attached":false,"since":null,"last_wait":null,"pending":0},{"owner":"z","attached":false,"since":null,"last_wait":"1970-01-01T00:00:02Z","pending":0}]}` + "\n"
	if out.String() != want {
		t.Fatalf("status:\n%s\nwant:\n%s", &out, want)
	}
}
