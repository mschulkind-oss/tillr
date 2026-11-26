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
	var stdout, stderr bytes.Buffer
	args := []string{"status", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}
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
	if code := run(t.Context(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, io.Discard, &stderr); code != 0 {
		t.Fatalf("init eval: %d %s", code, &stderr)
	}
	fact(t, s, `{"item_id":7}`)
	fact(t, s, `{"item_id":"selected"}`)
	fact(t, s, `{"item_id":"not-selected"}`)
	stdout.Reset()
	stderr.Reset()
	if code := run(t.Context(), args, &stdout, &stderr); code != 0 || stdout.String() != fmt.Sprintf("summary unsettled=0 (uninvestigated=0 investigating=0 investigated=0) settled=0 active_silences=0\ntriage untriaged=0 triaging=0 settled=0 escalated=0\nrule charter out-of-scope=%q\nrule refusals skipped=1\n", charter) {
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
		if code := run(t.Context(), []string{"status", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
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
	if code := run(context.Background(), []string{"status", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--json"}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("status = %d, %s", code, &stderr)
	}
	if want := "{\"summary\":{\"unsettled\":0,\"uninvestigated\":0,\"investigating\":0,\"investigated\":0,\"settled\":0,\"active_silences\":0,\"triage\":{\"untriaged\":0,\"triaging\":0,\"settled\":0,\"escalated\":0}},\"rules\":[{\"id\":\"refusals\",\"skipped\":0}],\"alerts\":[],\"silences\":[],\"owners\":[]}\n"; out.String() != want {
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
	common := []string{"--store", s.Path(), "--rules", rules, "--no-shipped-rules"}
	var stderr bytes.Buffer
	if code := run(ctx, append([]string{"eval", "--once"}, common...), io.Discard, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("init eval: %d, stderr %q", code, &stderr)
	}
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
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
	var stderr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, io.Discard, &stderr); code != 0 {
		t.Fatalf("init eval = %d: %s", code, &stderr)
	}
	for _, subject := range []string{"bob", "alice", "charlie", "done"} {
		for i := 0; i < 2; i++ {
			fact(t, s, `{"item_id":"`+subject+`"}`)
		}
	}
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, io.Discard, &stderr); code != 0 {
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
	args := []string{"status", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}
	var out bytes.Buffer
	if code := run(ctx, args, &out, &stderr); code != 0 {
		t.Fatalf("status = %d: %s", code, &stderr)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	wantLines := []struct{ prefix, suffix string }{
		{"summary unsettled=4 (uninvestigated=4 investigating=0 investigated=0) settled=0 active_silences=0", ""},
		{"triage untriaged=4 triaging=0 settled=0 escalated=0", ""},
		{"rule aaa skipped=0", ""},
		{"rule refusals skipped=0", ""},
		{`alert b rule=refusals subject="alice" state=silenced severity=warning opened=1970-01-01T00:00:01Z observed=1 vars={"item_id":"alice"} settlement=uninvestigated unsettled_for=`, " until=change triage_state=untriaged"},
		{`alert c rule=refusals subject="charlie" state=silenced severity=warning opened=1970-01-01T00:00:01Z observed=1 vars={"item_id":"charlie"} settlement=uninvestigated unsettled_for=`, " until=1970-01-01T00:00:03Z triage_state=untriaged"},
		{`alert z rule=removed subject="bob" state=acknowledged severity=warning opened=1970-01-01T00:00:01Z observed=1 vars={"item_id":"bob"} settlement=uninvestigated unsettled_for=`, ` by="a\"b" triage_state=untriaged`},
		{"owner foreground watcher=unattached last_wait=- pending=0", ""},
		{"owner z watcher=unattached last_wait=1970-01-01T00:00:02Z pending=0", ""},
	}
	if len(lines) != len(wantLines) {
		t.Fatalf("status lines (%d != %d):\n%s", len(lines), len(wantLines), &out)
	}
	for i, w := range wantLines {
		if !strings.HasPrefix(lines[i], w.prefix) || (w.suffix != "" && !strings.HasSuffix(lines[i], w.suffix)) {
			t.Fatalf("line %d = %q, want prefix %q suffix %q", i, lines[i], w.prefix, w.suffix)
		}
	}
	out.Reset()
	if code := run(ctx, append(args, "--json"), &out, &stderr); code != 0 {
		t.Fatalf("status = %d: %s", code, &stderr)
	}
	var report struct {
		Summary struct {
			Unsettled      int `json:"unsettled"`
			Uninvestigated int `json:"uninvestigated"`
			Investigating  int `json:"investigating"`
			Investigated   int `json:"investigated"`
			Settled        int `json:"settled"`
			ActiveSilences int `json:"active_silences"`
		} `json:"summary"`
		Rules []struct {
			ID      string `json:"id"`
			Skipped int    `json:"skipped"`
		} `json:"rules"`
		Alerts []struct {
			ID           string            `json:"alert_id"`
			RuleID       string            `json:"rule_id"`
			Subject      string            `json:"subject"`
			State        string            `json:"state"`
			Settlement   string            `json:"settlement"`
			UnsettledFor string            `json:"unsettled_for"`
			Vars         map[string]string `json:"vars"`
			By           *string           `json:"by"`
			Until        string            `json:"until"`
		} `json:"alerts"`
		Silences []any `json:"silences"`
		Owners   []struct {
			Owner    string  `json:"owner"`
			Attached bool    `json:"attached"`
			LastWait *string `json:"last_wait"`
		} `json:"owners"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal status JSON: %v\n%s", err, &out)
	}
	if report.Summary.Unsettled != 4 || report.Summary.Uninvestigated != 4 || len(report.Rules) != 2 || len(report.Alerts) != 3 || len(report.Owners) != 2 {
		t.Fatalf("unexpected JSON report: %+v", report)
	}
	if report.Alerts[0].ID != "b" || report.Alerts[0].Until != "change" || report.Alerts[0].Settlement != "uninvestigated" || report.Alerts[0].UnsettledFor == "" ||
		report.Alerts[1].ID != "c" || report.Alerts[1].Until != "1970-01-01T00:00:03Z" ||
		report.Alerts[2].ID != "z" || report.Alerts[2].By == nil || *report.Alerts[2].By != `a"b` {
		t.Fatalf("unexpected alerts order/fields: %+v", report.Alerts)
	}
}

func TestWatchStatusVarsAndUnmigratedStore(t *testing.T) {
	s, rules := fixture(t, false)
	ctx := t.Context()

	multiDoc := `
[[rule]]
id = "r-global"
scope = "core"
severity = "warning"
[rule.reads]
events = ["drain.refusal"]
[rule.when]
group_by = "global"
pattern = "count_in_window"
match = 'event.kind == "drain.refusal"'
threshold = 1
window = "1h"
[rule.guidance]
summary = "global"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "r-composite"
scope = "core"
severity = "warning"
[rule.reads]
events = ["drain.refusal"]
[rule.when]
group_by = ["lane_id", "reason"]
pattern = "count_in_window"
threshold = 1
window = "1h"
[rule.guidance]
summary = "composite"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "r-vars"
scope = "core"
severity = "warning"
[rule.reads]
events = ["drain.refusal"]
[rule.when]
group_by = "detail.item_id"
pattern = "count_in_window"
threshold = 1
window = "1h"
[rule.variables]
item = "detail.item_id"
node = "detail.node"
[rule.guidance]
summary = "vars"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`
	if err := os.WriteFile(filepath.Join(rules, "nested", "multi.toml"), []byte(multiDoc), 0600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, io.Discard, &stderr); code != 0 {
		t.Fatalf("init eval = %d: %s", code, &stderr)
	}
	fact(t, s, `{"item_id":"it-1","lane_id":"lane-1","reason":"dirty","detail":{"item_id":"it-1","node":"build"}}`)
	fact(t, s, `{"item_id":"it-1","lane_id":"lane-1","reason":"dirty","detail":{"item_id":"it-1","node":"build"}}`)
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, io.Discard, &stderr); code != 0 {
		t.Fatalf("eval = %d: %s", code, &stderr)
	}

	var out bytes.Buffer
	args := []string{"status", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}
	if code := run(ctx, args, &out, &stderr); code != 0 {
		t.Fatalf("status text = %d: %s", code, &stderr)
	}
	text := out.String()
	for _, want := range []string{
		`rule=r-global `,
		`vars={}`,
		`rule=refusals `,
		`vars={"item_id":"it-1"}`,
		`rule=r-composite `,
		`vars={"lane_id":"lane-1","reason":"dirty"}`,
		`rule=r-vars `,
		`vars={"item":"it-1","node":"build"}`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status text missing %q:\n%s", want, text)
		}
	}

	out.Reset()
	if code := run(ctx, append(args, "--json"), &out, &stderr); code != 0 {
		t.Fatalf("status json = %d: %s", code, &stderr)
	}
	var report struct {
		Alerts []struct {
			RuleID string            `json:"rule_id"`
			Vars   map[string]string `json:"vars"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal status json: %v\n%s", err, &out)
	}
	gotVars := make(map[string]map[string]string, len(report.Alerts))
	for _, a := range report.Alerts {
		if a.Vars == nil {
			t.Fatalf("alert %q had nil vars in JSON: %s", a.RuleID, &out)
		}
		gotVars[a.RuleID] = a.Vars
	}
	for ruleID, want := range map[string]map[string]string{
		"r-global":    {},
		"refusals":    {"item_id": "it-1"},
		"r-composite": {"lane_id": "lane-1", "reason": "dirty"},
		"r-vars":      {"item": "it-1", "node": "build"},
	} {
		got, ok := gotVars[ruleID]
		if !ok {
			t.Fatalf("missing alert for %q in %s", ruleID, &out)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("alert %q vars = %v, want %v", ruleID, got, want)
		}
	}

	// Unmigrated alert_episodes lacking variables_json must surface vars={} without mutating the store.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`
			DROP TABLE alert_episodes;
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
				silence_until_ms INTEGER,
				silence_on_fingerprint TEXT,
				acked_by TEXT,
				acked_note TEXT,
				acked_at INTEGER,
				resolved_seq INTEGER,
				resolved_at INTEGER,
				resolved_reason TEXT,
				bundle_json TEXT NOT NULL
			);
			INSERT INTO alert_episodes(alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,
				opened_seq,opened_at,last_observed_seq,last_observed_at,bundle_json)
			VALUES ('old-1','refusals','it-1',1,'open','warning','fp',1,0,1,0,'{}');
		`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}

	out.Reset()
	stderr.Reset()
	if code := run(ctx, args, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("unmigrated text status = %d, stderr=%s", code, &stderr)
	}
	if !strings.Contains(out.String(), `alert old-1 rule=refusals subject="it-1" state=open severity=warning opened=1970-01-01T00:00:00Z observed=1 vars={}`) {
		t.Fatalf("unmigrated text status = %s", &out)
	}

	out.Reset()
	if code := run(ctx, append(args, "--json"), &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("unmigrated json status = %d, stderr=%s", code, &stderr)
	}
	if !strings.Contains(out.String(), `"alert_id":"old-1"`) || !strings.Contains(out.String(), `"vars":{}`) {
		t.Fatalf("unmigrated json status = %s", &out)
	}

	after, err := os.ReadFile(s.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("status mutated unmigrated store: %v", err)
	}
}
