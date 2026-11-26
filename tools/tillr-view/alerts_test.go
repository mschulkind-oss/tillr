package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func runAlertsCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	code := run(append([]string{"alerts"}, args...))
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = origOut, origErr
	var outBuf, errBuf bytes.Buffer
	_, _ = io.Copy(&outBuf, outR)
	_, _ = io.Copy(&errBuf, errR)
	outR.Close()
	errR.Close()
	return code, outBuf.String(), errBuf.String()
}

func startAlertsServer(t *testing.T, storePath string, now time.Time) (*server, *httptest.Server) {
	t.Helper()
	dir := filepath.Dir(storePath)
	srv := newServer(context.Background(), storePath, filepath.Join(dir, "loop"), filepath.Join(dir, "lanes"), "", dir, "", []string{"false"})
	srv.now = func() time.Time { return now }
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(func() {
		ts.Close()
		srv.close()
	})
	return srv, ts
}

func TestAlertsEmptyStoreSafe(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	st, err := store.Open(context.Background(), dbPath, true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		t.Fatalf("read serve-fixture.sql: %v", err)
	}
	// Remove watch alert tables to simulate a store that has never run watch.
	rawDB, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := rawDB.Exec(string(schema)); err != nil {
		rawDB.Close()
		t.Fatalf("apply fixture schema: %v", err)
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS alert_item_links`,
		`DROP TABLE IF EXISTS watch_silences`,
		`DROP TABLE IF EXISTS alert_episodes`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
		 VALUES ('item-empty', 'Empty store item', '', 'in-queue', '', 1000, '[]')`,
	} {
		if _, err := rawDB.Exec(stmt); err != nil {
			rawDB.Close()
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	rawDB.Close()

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	// CLI --json on store without alert_episodes
	code, out, errOut := runAlertsCLI(t, "--store", dbPath, "--now", now.Format(time.RFC3339), "--json")
	if code != record.ExitOK {
		t.Fatalf("CLI --json exited %d, want 0 (stderr: %s)", code, errOut)
	}
	var cliData alertsData
	if err := json.Unmarshal([]byte(out), &cliData); err != nil {
		t.Fatalf("unmarshal CLI alerts: %v (out=%q)", err, out)
	}
	if !cliData.Absent {
		t.Fatalf("CLI Absent = false, want true")
	}
	if cliData.Episodes == nil || len(cliData.Episodes) != 0 {
		t.Fatalf("CLI Episodes = %#v, want non-nil empty slice", cliData.Episodes)
	}
	if cliData.ActiveSilences == nil || len(cliData.ActiveSilences) != 0 {
		t.Fatalf("CLI ActiveSilences = %#v, want non-nil empty slice", cliData.ActiveSilences)
	}

	// CLI text on store without alert_episodes
	code, textOut, errOut := runAlertsCLI(t, "--store", dbPath)
	if code != record.ExitOK {
		t.Fatalf("CLI text exited %d, want 0 (stderr: %s)", code, errOut)
	}
	if !strings.Contains(textOut, "no alert_episodes table") {
		t.Fatalf("CLI text = %q, want absent notice", textOut)
	}

	// CLI --alert on missing alert exits 1
	code, _, errOut = runAlertsCLI(t, "--store", dbPath, "--alert", "alert-missing")
	if code != record.ExitNegative {
		t.Fatalf("CLI --alert missing exited %d, want %d (stderr: %s)", code, record.ExitNegative, errOut)
	}

	// HTTP routes on store without alert_episodes
	_, ts := startAlertsServer(t, dbPath, now)

	resp, err := http.Get(ts.URL + "/api/v1/alerts")
	if err != nil {
		t.Fatalf("GET /api/v1/alerts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/alerts status = %d, want 200", resp.StatusCode)
	}
	var httpLedger struct {
		Data alertsData `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&httpLedger); err != nil {
		t.Fatalf("decode /api/v1/alerts: %v", err)
	}
	if !reflect.DeepEqual(cliData, httpLedger.Data) {
		t.Fatalf("CLI and HTTP alertsData differ:\nCLI:  %+v\nHTTP: %+v", cliData, httpLedger.Data)
	}

	// GET /api/v1/alerts/{id} returns 404 alert-not-found
	resp404, err := http.Get(ts.URL + "/api/v1/alerts/alert-missing")
	if err != nil {
		t.Fatalf("GET /api/v1/alerts/alert-missing: %v", err)
	}
	defer resp404.Body.Close()
	if resp404.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /api/v1/alerts/alert-missing status = %d, want 404", resp404.StatusCode)
	}
	var errBody struct {
		Error apiErrorBody `json:"error"`
	}
	if err := json.NewDecoder(resp404.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if errBody.Error.Code != codeAlertNotFound {
		t.Fatalf("error.code = %q, want %q", errBody.Error.Code, codeAlertNotFound)
	}

	// GET /api/v1/items/{id} is safe and returns empty linked_alerts
	respItem, err := http.Get(ts.URL + "/api/v1/items/item-empty")
	if err != nil {
		t.Fatalf("GET /api/v1/items/item-empty: %v", err)
	}
	defer respItem.Body.Close()
	if respItem.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/items/item-empty status = %d, want 200", respItem.StatusCode)
	}
	var itemResp struct {
		Data struct {
			LinkedAlerts []watch.LinkedAlert `json:"linked_alerts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(respItem.Body).Decode(&itemResp); err != nil {
		t.Fatalf("decode /api/v1/items/item-empty: %v", err)
	}
	if itemResp.Data.LinkedAlerts == nil || len(itemResp.Data.LinkedAlerts) != 0 {
		t.Fatalf("linked_alerts = %#v, want non-nil empty slice", itemResp.Data.LinkedAlerts)
	}
}

func TestAlertsCLIAndHTTPParity(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	st, err := store.Open(context.Background(), dbPath, true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	baseMS := now.Add(-20 * time.Minute).UnixMilli()
	untilMS := now.Add(2 * time.Hour).UnixMilli()

	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		t.Fatalf("read serve-fixture.sql: %v", err)
	}
	rawDB, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer rawDB.Close()
	if _, err := rawDB.Exec(string(schema)); err != nil {
		t.Fatalf("apply fixture schema: %v", err)
	}

	stmts := []struct {
		q    string
		args []any
	}{
		{
			`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
			 VALUES ('item-fix-1', 'Fix flaky gate check', '', 'in-queue', '', ?, '[]')`,
			[]any{baseMS},
		},
		{
			`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
			 VALUES ('item-sil-2', 'Track silenced package', '', 'landed', '', ?, '[]')`,
			[]any{baseMS},
		},
		// Episode 1: resolved, settled, with multiple transitions including unrecorded actors
		{
			`INSERT INTO alert_episodes (
				alert_id, rule_id, subject_key, episode_num, state, settlement_state, severity,
				condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at,
				observation_count, resolved_seq, resolved_at, resolved_reason,
				variables_json, outcome, item_link_kind, tracking_item_id,
				investigated_by, investigated_at, investigation_cause, investigation_evidence,
				settled_by, settled_at, settled_seq, bundle_json
			) VALUES (
				'alert-ep1', 'fast-gate-run-slow', 'go-build', 1, 'resolved', 'settled', 'high',
				'fp-1', 1, ?, 3, ?, 3, 7, ?, 'manual',
				'{"slowest_check":"go-build"}', 'fix-fault', 'filed', 'item-fix-1',
				'operator-session', ?, 'cold build cache', 'seq 1..3',
				'operator-session', ?, 6, '{"count":3}'
			)`,
			[]any{baseMS, baseMS + 60_000, baseMS + 180_000, baseMS + 120_000, baseMS + 150_000},
		},
		// Episode 2: same rule+subject (related episode), open and uninvestigated
		{
			`INSERT INTO alert_episodes (
				alert_id, rule_id, subject_key, episode_num, state, settlement_state, severity,
				condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at,
				observation_count, variables_json, bundle_json
			) VALUES (
				'alert-ep2', 'fast-gate-run-slow', 'go-build', 2, 'open', 'uninvestigated', 'high',
				'fp-2', 8, ?, 8, ?, 1, '{"slowest_check":"go-build"}', '{"count":1}'
			)`,
			[]any{baseMS + 300_000, baseMS + 300_000},
		},
		// Episode 3: silenced under active silence
		{
			`INSERT INTO alert_episodes (
				alert_id, rule_id, subject_key, episode_num, state, settlement_state, severity,
				condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at,
				observation_count, variables_json, outcome, tracking_item_id, silence_id,
				settled_by, settled_at, bundle_json
			) VALUES (
				'alert-ep3', 'pkg-red', 'tools/flaky', 1, 'silenced', 'settled', 'medium',
				'fp-3', 9, ?, 9, ?, 2, '{"package":"tools/flaky"}', 'silence', 'item-sil-2', 'sil-1',
				'operator-session', ?, '{}'
			)`,
			[]any{baseMS + 360_000, baseMS + 360_000, baseMS + 370_000},
		},
		// Active silence
		{
			`INSERT INTO watch_silences (
				silence_id, rule_id, vars_json, tracking_item_id, created_by, created_at,
				reason, investigation_cause, investigation_evidence, source_alert_id, silence_until_ms
			) VALUES (
				'sil-1', 'pkg-red', '{"package":"tools/flaky"}', 'item-sil-2', 'operator-session', ?,
				'known flake while fix lands', 'race in test', 'run-9 log', 'alert-ep3', ?
			)`,
			[]any{baseMS + 370_000, untilMS},
		},
		// Alert-item links
		{
			`INSERT INTO alert_item_links (alert_id, item_id, relation, linked_at, linked_by, note, event_seq)
			 VALUES ('alert-ep1', 'item-fix-1', 'fix-fault', ?, 'operator-session', 'filed from triage', 5)`,
			[]any{baseMS + 140_000},
		},
		{
			`INSERT INTO alert_item_links (alert_id, item_id, relation, linked_at, linked_by, note, event_seq)
			 VALUES ('alert-ep3', 'item-sil-2', 'silence', ?, 'operator-session', '', 10)`,
			[]any{baseMS + 370_000},
		},
		// Transition events for alert-ep1 (including legacy tracked without actor & manual resolved without actor)
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (1, ?, 'watch.alert.opened', 'alert', 'alert-ep1', '{"cause":"threshold"}')`,
			[]any{baseMS},
		},
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (2, ?, 'watch.alert.acknowledged', 'alert', 'alert-ep1', '{"by":"operator-session","note":"looking"}')`,
			[]any{baseMS + 30_000},
		},
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (3, ?, 'watch.alert.tracked', 'alert', 'alert-ep1', '{"item_id":"item-fix-1"}')`,
			[]any{baseMS + 60_000},
		},
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (4, ?, 'watch.alert.investigated', 'alert', 'alert-ep1', '{"by":"operator-session","cause":"cold build cache"}')`,
			[]any{baseMS + 120_000},
		},
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (5, ?, 'watch.alert.item-linked', 'alert', 'alert-ep1', '{"by":"operator-session","item_id":"item-fix-1","relation":"filed"}')`,
			[]any{baseMS + 140_000},
		},
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (6, ?, 'watch.alert.settled', 'alert', 'alert-ep1', '{"by":"operator-session","outcome":"fix-fault","item_id":"item-fix-1"}')`,
			[]any{baseMS + 150_000},
		},
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (7, ?, 'watch.alert.resolved', 'alert', 'alert-ep1', '{"reason":"manual"}')`,
			[]any{baseMS + 180_000},
		},
		{
			`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES (8, ?, 'watch.alert.opened', 'alert', 'alert-ep2', '{"cause":"threshold"}')`,
			[]any{baseMS + 300_000},
		},
	}
	for _, s := range stmts {
		if _, err := rawDB.Exec(s.q, s.args...); err != nil {
			t.Fatalf("seed exec failed: %v\nquery: %s", err, s.q)
		}
	}

	_, ts := startAlertsServer(t, dbPath, now)

	// 1. Full ledger parity between CLI and HTTP
	code, cliOut, errOut := runAlertsCLI(t, "--store", dbPath, "--now", now.Format(time.RFC3339), "--json")
	if code != record.ExitOK {
		t.Fatalf("CLI alerts --json exited %d: %s", code, errOut)
	}
	var cliLedger alertsData
	if err := json.Unmarshal([]byte(cliOut), &cliLedger); err != nil {
		t.Fatalf("unmarshal CLI ledger: %v", err)
	}
	resp, err := http.Get(ts.URL + "/api/v1/alerts")
	if err != nil {
		t.Fatalf("GET /api/v1/alerts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/alerts status = %d, want 200", resp.StatusCode)
	}
	var httpLedger struct {
		Data alertsData `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&httpLedger); err != nil {
		t.Fatalf("decode HTTP ledger: %v", err)
	}
	if !reflect.DeepEqual(cliLedger, httpLedger.Data) {
		t.Fatalf("CLI and HTTP ledger differ:\nCLI:  %+v\nHTTP: %+v", cliLedger, httpLedger.Data)
	}
	if cliLedger.Counts.Total != 3 || cliLedger.Counts.Unsettled != 1 || cliLedger.Counts.Settled != 2 || cliLedger.Counts.ActiveSilences != 1 {
		t.Fatalf("unexpected counts: %+v", cliLedger.Counts)
	}
	// Active/unsettled episodes come first
	if cliLedger.Episodes[0].AlertID != "alert-ep2" {
		t.Fatalf("first episode = %q, want alert-ep2 (open+uninvestigated first)", cliLedger.Episodes[0].AlertID)
	}
	if cliLedger.ActiveSilences[0].TrackingItemTitle != "Track silenced package" || cliLedger.ActiveSilences[0].TrackingItemState != "landed" {
		t.Fatalf("silence item enrichment = (%q, %q), want ('Track silenced package', 'landed')",
			cliLedger.ActiveSilences[0].TrackingItemTitle, cliLedger.ActiveSilences[0].TrackingItemState)
	}

	// 2. Filter parity (state + rule + severity + settlement)
	code, filtCLIOut, errOut := runAlertsCLI(t, "--store", dbPath, "--now", now.Format(time.RFC3339),
		"--state", "open", "--rule", "fast-gate-run-slow", "--severity", "high", "--settlement", "uninvestigated", "--json")
	if code != record.ExitOK {
		t.Fatalf("CLI filtered exited %d: %s", code, errOut)
	}
	var filtCLI alertsData
	if err := json.Unmarshal([]byte(filtCLIOut), &filtCLI); err != nil {
		t.Fatalf("unmarshal filtered CLI: %v", err)
	}
	respFilt, err := http.Get(ts.URL + "/api/v1/alerts?state=open&rule=fast-gate-run-slow&severity=high&settlement=uninvestigated")
	if err != nil {
		t.Fatalf("GET filtered /api/v1/alerts: %v", err)
	}
	defer respFilt.Body.Close()
	var filtHTTP struct {
		Data alertsData `json:"data"`
	}
	if err := json.NewDecoder(respFilt.Body).Decode(&filtHTTP); err != nil {
		t.Fatalf("decode filtered HTTP: %v", err)
	}
	if !reflect.DeepEqual(filtCLI, filtHTTP.Data) {
		t.Fatalf("filtered CLI and HTTP differ:\nCLI:  %+v\nHTTP: %+v", filtCLI, filtHTTP.Data)
	}
	if len(filtCLI.Episodes) != 1 || filtCLI.Episodes[0].AlertID != "alert-ep2" {
		t.Fatalf("filtered episodes = %+v, want only alert-ep2", filtCLI.Episodes)
	}

	// 3. Single alert detail parity + transition actor truthfulness ("not recorded" for unrecorded actors)
	code, detCLIOut, errOut := runAlertsCLI(t, "--store", dbPath, "--alert", "alert-ep1", "--now", now.Format(time.RFC3339), "--json")
	if code != record.ExitOK {
		t.Fatalf("CLI detail exited %d: %s", code, errOut)
	}
	var detCLI alertDetailData
	if err := json.Unmarshal([]byte(detCLIOut), &detCLI); err != nil {
		t.Fatalf("unmarshal CLI detail: %v", err)
	}
	respDet, err := http.Get(ts.URL + "/api/v1/alerts/alert-ep1")
	if err != nil {
		t.Fatalf("GET /api/v1/alerts/alert-ep1: %v", err)
	}
	defer respDet.Body.Close()
	if respDet.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/alerts/alert-ep1 status = %d, want 200", respDet.StatusCode)
	}
	var detHTTP struct {
		Data alertDetailData `json:"data"`
	}
	if err := json.NewDecoder(respDet.Body).Decode(&detHTTP); err != nil {
		t.Fatalf("decode HTTP detail: %v", err)
	}
	if !reflect.DeepEqual(detCLI, detHTTP.Data) {
		t.Fatalf("CLI and HTTP detail differ:\nCLI:  %+v\nHTTP: %+v", detCLI, detHTTP.Data)
	}
	if len(detCLI.Transitions) != 7 {
		t.Fatalf("transitions count = %d, want 7", len(detCLI.Transitions))
	}
	wantActors := []string{
		"evaluator",        // seq 1: opened
		"operator-session", // seq 2: acknowledged
		"not recorded",     // seq 3: legacy tracked without by
		"operator-session", // seq 4: investigated
		"operator-session", // seq 5: item-linked
		"operator-session", // seq 6: settled
		"not recorded",     // seq 7: legacy manual resolved without by
	}
	for i, want := range wantActors {
		if detCLI.Transitions[i].Actor != want {
			t.Errorf("transition[%d] (%s) actor = %q, want %q", i, detCLI.Transitions[i].Kind, detCLI.Transitions[i].Actor, want)
		}
	}
	if len(detCLI.LinkedItems) != 1 || detCLI.LinkedItems[0].ItemTitle != "Fix flaky gate check" || detCLI.LinkedItems[0].ItemState != "in_queue" {
		t.Fatalf("detail linked_items = %+v, want item-fix-1 enriched with title and in_queue", detCLI.LinkedItems)
	}
	if len(detCLI.RelatedEpisodes) != 1 || detCLI.RelatedEpisodes[0].AlertID != "alert-ep2" {
		t.Fatalf("related_episodes = %+v, want [alert-ep2]", detCLI.RelatedEpisodes)
	}

	// 4. Text output renders "not recorded" actor and linked items
	code, detText, errOut := runAlertsCLI(t, "--store", dbPath, "--alert", "alert-ep1", "--now", now.Format(time.RFC3339))
	if code != record.ExitOK {
		t.Fatalf("CLI detail text exited %d: %s", code, errOut)
	}
	if !strings.Contains(detText, "not recorded") || !strings.Contains(detText, "Fix flaky gate check") {
		t.Fatalf("CLI detail text missing expected content:\n%s", detText)
	}

	// 5. Bidirectional link on GET /api/v1/items/{id}
	respItem, err := http.Get(ts.URL + "/api/v1/items/item-fix-1")
	if err != nil {
		t.Fatalf("GET /api/v1/items/item-fix-1: %v", err)
	}
	defer respItem.Body.Close()
	if respItem.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/items/item-fix-1 status = %d, want 200", respItem.StatusCode)
	}
	var itemResp struct {
		Data struct {
			LinkedAlerts []watch.LinkedAlert `json:"linked_alerts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(respItem.Body).Decode(&itemResp); err != nil {
		t.Fatalf("decode item response: %v", err)
	}
	if len(itemResp.Data.LinkedAlerts) != 1 {
		t.Fatalf("item linked_alerts = %+v, want 1 entry", itemResp.Data.LinkedAlerts)
	}
	la := itemResp.Data.LinkedAlerts[0]
	if la.AlertID != "alert-ep1" || la.RuleID != "fast-gate-run-slow" || la.Relation != "fix-fault" || la.State != "resolved" || la.SettlementState != "settled" {
		t.Fatalf("unexpected linked alert on item: %+v", la)
	}

	// 6. Help surface names alerts and its flags
	code, helpOut, _ := runAlertsCLI(t, "--help")
	if code != record.ExitOK || !strings.Contains(helpOut, "Usage of tillr-view alerts") {
		t.Fatalf("alerts --help exited %d: %s", code, helpOut)
	}
}

func TestWSAlertInvalidation(t *testing.T) {
	idAlert1 := "alert-1"
	idSil1 := "sil-1"

	// Unit check of eventResources mapping
	cases := []struct {
		name  string
		event event.Event
		want  []string
	}{
		{
			name: "alert opened",
			event: event.Event{
				Kind:       "watch.alert.opened",
				ObjectType: "alert",
				ObjectID:   &idAlert1,
				Payload:    []byte(`{"alert_id":"alert-1"}`),
			},
			want: []string{"alerts", "alerts:alert-1"},
		},
		{
			name: "alert item-linked invalidates alert and item",
			event: event.Event{
				Kind:       "watch.alert.item-linked",
				ObjectType: "alert",
				ObjectID:   &idAlert1,
				Payload:    []byte(`{"alert_id":"alert-1","item_id":"item-42"}`),
			},
			want: []string{"alerts", "alerts:alert-1", "items:item-42"},
		},
		{
			name: "alert settled invalidates alert and item",
			event: event.Event{
				Kind:       "watch.alert.settled",
				ObjectType: "alert",
				ObjectID:   &idAlert1,
				Payload:    []byte(`{"outcome":"fix-rule","item_id":"item-99"}`),
			},
			want: []string{"alerts", "alerts:alert-1", "items:item-99"},
		},
		{
			name: "silence lifted invalidates alerts, source alert, and tracking item",
			event: event.Event{
				Kind:       "watch.silence.lifted",
				ObjectType: "silence",
				ObjectID:   &idSil1,
				Payload:    []byte(`{"source_alert_id":"alert-2","tracking_item_id":"item-77"}`),
			},
			want: []string{"alerts", "alerts:alert-2", "items:item-77"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sortedUnique(eventResources([]event.Event{tc.event}))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("eventResources = %v, want %v", got, tc.want)
			}
		})
	}

	// Integration check through changeDetector.poll against a real store
	h := newServeHarness(t)
	d := newChangeDetector(h.srv)
	if err := d.prime(context.Background()); err != nil {
		t.Fatalf("prime: %v", err)
	}
	h.exec(
		`INSERT INTO events (ts, kind, object_type, object_id, revision, payload)
		 VALUES (?, 'watch.alert.item-linked', 'alert', 'alert-ws-1', NULL, '{"alert_id":"alert-ws-1","item_id":"item-ws-1","relation":"filed"}')`,
		time.Now().UnixMilli(),
	)
	frame, ok, err := d.poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !ok {
		t.Fatalf("poll returned no frame after watch.alert.item-linked event")
	}
	wantChanged := []string{"alerts", "alerts:alert-ws-1", "items:item-ws-1", "snapshot"}
	if !reflect.DeepEqual(frame.Changed, wantChanged) {
		t.Fatalf("frame.Changed = %v, want %v", frame.Changed, wantChanged)
	}
}
