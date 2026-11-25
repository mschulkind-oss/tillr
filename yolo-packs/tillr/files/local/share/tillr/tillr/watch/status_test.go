package watch

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchStatusBeforeWatchMigration(t *testing.T) {
	s := watchStore(t)
	watchSQL(t, s, `DROP TABLE foreground_delivery; DROP TABLE alert_episodes; DROP TABLE watch_rule_overrides; DROP TABLE watch_rule_baselines;`)
	ro, err := store.OpenRead(t.Context(), s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	e := Evaluator{Store: ro}
	got, err := e.ReadStatus(t.Context(), func(owner string) (bool, error) {
		t.Fatalf("probed owner %q without a delivery table", owner)
		return false, nil
	})
	if err != nil || len(got.Rules) != 0 || len(got.Alerts) != 0 || len(got.Owners) != 0 {
		t.Fatalf("unmigrated status: %+v, %v", got, err)
	}
}

func TestWatchStatusReportsNamedScopes(t *testing.T) {
	register(t, "test.watch")
	s := watchStore(t)
	const content = `{"definition":"custom","version":1,"states":["ready","blocked","closed"],"ready":"ready","blocked":"blocked","closed":"closed","finished":["closed"],"completes":"landing"}`
	record, err := json.Marshal(map[string]any{"definition": "custom", "version": 1, "content": content, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(content))), "default": false})
	if err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, `INSERT INTO meta(key,value) VALUES('store.definition',?)`, string(record))
	charter := strings.NewReplacer(`id = "case"`, `id = "z-charter"`, `scope = "workflow:*"`, `scope = "charter:other"`).Replace(validRule)
	workflow := strings.NewReplacer(`id = "case"`, `id = "a-workflow"`, `scope = "workflow:*"`, `scope = "workflow:build@v1"`).Replace(validRule)
	e := Evaluator{Store: s, Rules: watchRules(t, charter+"\n"+workflow)}
	got, err := e.ReadStatus(t.Context(), func(string) (bool, error) { return false, nil })
	if err != nil || len(got.Rules) != 2 || got.Rules[0].ID != "a-workflow" || !strings.Contains(got.Rules[0].Unreadable, "workflow_selections") || got.Rules[1].ID != "z-charter" || got.Rules[1].OutOfScope != "custom" {
		t.Fatalf("scope results: %+v, %v", got.Rules, err)
	}
	global := strings.Replace(workflow, `group_by = "detail.item-id"`, `group_by = "global"`, 1)
	if _, err := LoadRuleDocument("global workflow", []byte(global)); err == nil || !strings.Contains(err.Error(), "named workflow scope requires a subject") {
		t.Fatalf("global workflow rule should be refused: %v", err)
	}
}

func TestWatchStatusReportsStoredAlertsAndLatestNotices(t *testing.T) {
	register(t, "watch-test.fact")
	s := watchStore(t)
	ctx := t.Context()
	now := time.Unix(1700000000, 0)
	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`)
	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":"will-reopen"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	opened, err := s.EventHeadSeq()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Shown(ctx, "foreground", opened); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "foreground", opened); err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, `UPDATE foreground_delivery SET waiting_since=1000, last_wait_at=2000;
		INSERT INTO foreground_delivery(owner_handle) VALUES ('other');
		INSERT INTO alert_episodes(alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,
			opened_seq,opened_at,last_observed_seq,last_observed_at,bundle_json,acked_by,silence_until_ms) VALUES
			('b','removed','two',1,'silenced','warning','f',1,0,1,0,'{}',NULL,NULL),
			('c','removed','three',1,'resolved','warning','f',1,0,1,0,'{}',NULL,NULL),
			('a','removed','one',1,'acknowledged','warning','f',1,0,1,0,'{}','operator',NULL),
			('d','removed','four',1,'silenced','warning','f',2,0,2,0,'{}',NULL,2000);`)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var id string
		if err := tx.QueryRow(`SELECT alert_id FROM alert_episodes WHERE subject_key='will-reopen'`).Scan(&id); err != nil {
			return err
		}
		for range 2 {
			if _, err := tx.Append(event.Event{Kind: reopenedKind, At: now, ObjectID: &id, Payload: []byte(`{}`)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A later evaluation would clear the open episode: status must not do so.
	e.Clock = func() time.Time { return now.Add(24 * time.Hour) }
	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	e.Store = ro
	got, err := e.ReadStatus(ctx, func(owner string) (bool, error) { return owner == "other", nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Alerts) != 4 || got.Alerts[0].ID != "a" || got.Alerts[0].By == nil || *got.Alerts[0].By != "operator" || got.Alerts[1].Subject != "will-reopen" || got.Alerts[2].ID != "b" || got.Alerts[2].Until != "change" || got.Alerts[3].ID != "d" || got.Alerts[3].Until != "1970-01-01T00:00:02Z" {
		t.Fatalf("unresolved episodes: %+v", got.Alerts)
	}
	if len(got.Owners) != 2 || got.Owners[0].Owner != "foreground" || got.Owners[0].Attached || got.Owners[0].Since != nil || got.Owners[0].LastWait == nil || *got.Owners[0].LastWait != "1970-01-01T00:00:02Z" || got.Owners[0].Pending != 1 || got.Owners[1].Owner != "other" || !got.Owners[1].Attached || got.Owners[1].Since != nil || got.Owners[1].Pending != 1 {
		t.Fatalf("owner status: %+v", got.Owners)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "will-reopen" {
		t.Fatalf("expected one foreground reopening: %+v, %v", notices, err)
	}
	// The effective receiver changes delivery without dropping any stored alert rows.
	seedLiveOverride(t, s, rules[0].ID, strings.Replace(doc, `receiver = "foreground"`, `receiver = "subscribers"`, 1))
	got, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(got.Alerts) != 4 || len(got.Owners) != 2 || got.Owners[0].Pending != 0 || got.Owners[1].Pending != 0 {
		t.Fatalf("status with receiver override: %+v, %v", got, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("subscribers override must keep the reopening out of foreground delivery: %+v, %v", notices, err)
	}
	// An override can route a reopening into foreground delivery as well.
	seedLiveOverride(t, s, rules[0].ID, doc)
	e.Rules = watchRules(t, strings.Replace(doc, `receiver = "foreground"`, `receiver = "subscribers"`, 1))
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "will-reopen" {
		t.Fatalf("foreground override should deliver the reopening: %+v, %v", notices, err)
	}
}

func TestWatchStatusAlertVarsAndUnmigratedSchema(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := t.Context()
	now := time.Unix(1700000000, 0).UTC()

	rulesDoc := `
[[rule]]
id = "r-global"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "global"
pattern = "count_in_window"
match = 'event.kind == "watch-test.fact"'
threshold = 1
window = "1h"
[rule.guidance]
summary = "global"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "r-single"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "detail.item_id"
pattern = "count_in_window"
threshold = 1
window = "1h"
[rule.guidance]
summary = "single"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "r-composite"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
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
events = ["watch-test.fact"]
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

	s := watchStore(t)
	e := Evaluator{Store: s, Rules: watchRules(t, rulesDoc), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"lane_id":"lane-1","reason":"dirty","detail":{"item_id":"it-1","node":"build"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	e.Store = ro

	got, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Alerts) != 4 {
		t.Fatalf("expected 4 alerts, got %+v", got.Alerts)
	}
	byRule := make(map[string]AlertStatus, len(got.Alerts))
	for _, a := range got.Alerts {
		byRule[a.RuleID] = a
	}
	wantVars := map[string]map[string]string{
		"r-global":    {},
		"r-single":    {"item_id": "it-1"},
		"r-composite": {"lane_id": "lane-1", "reason": "dirty"},
		"r-vars":      {"item": "it-1", "node": "build"},
	}
	for ruleID, want := range wantVars {
		a, ok := byRule[ruleID]
		if !ok {
			t.Fatalf("missing alert for %q in %+v", ruleID, got.Alerts)
		}
		if a.Vars == nil || !maps.Equal(a.Vars, want) {
			t.Errorf("alert %q Vars = %#v, want %#v", ruleID, a.Vars, want)
		}
	}

	// Unmigrated alert_episodes lacking variables_json must safely return {} without mutating the store.
	ro.Close()
	watchSQL(t, s, `
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
		VALUES ('old-1','r-single','it-1',1,'open','warning','fp',1,0,1,0,'{}');
	`)
	before, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	ro2, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro2.Close()
	e.Store = ro2
	got2, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("ReadStatus on unmigrated alert_episodes: %v", err)
	}
	if len(got2.Alerts) != 1 || got2.Alerts[0].ID != "old-1" || got2.Alerts[0].Vars == nil || len(got2.Alerts[0].Vars) != 0 {
		t.Fatalf("unmigrated alert Vars = %+v", got2.Alerts)
	}
	after, err := os.ReadFile(s.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("ReadStatus mutated store on unmigrated schema: %v", err)
	}
}
