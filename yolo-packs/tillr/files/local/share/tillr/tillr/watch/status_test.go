package watch

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchStatusBeforeWatchMigration(t *testing.T) {
	s := watchStore(t)
	watchSQL(t, s, `DROP TABLE foreground_delivery; DROP TABLE alert_episodes; DROP TABLE watch_rule_overrides;`)
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
	s := watchStore(t)
	ctx := t.Context()
	now := time.Unix(1700000000, 0)
	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`)
	watchFact(t, s, now, `{"detail":{"item-id":"will-reopen"}}`)
	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
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
