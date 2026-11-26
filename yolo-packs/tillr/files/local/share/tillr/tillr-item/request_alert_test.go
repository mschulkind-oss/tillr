package main_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	_ "github.com/mschulkind-oss/tillr/watch"
)

func TestRequestCLIAlertFlagAndShow(t *testing.T) {
	path, s := placeFixture(t, "a")
	root := scratch(t, "request-alert-root")
	now := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)

	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, settlement_state)
			VALUES ('alrt-cli-1', 'rule-cli', 'subj-cli', 1, 'open', 'high', 'fp-cli', 1, ?, 1, ?, '{}', 'uninvestigated')`,
			now.UnixMilli(), now.UnixMilli())
		return err
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	// Unknown alert rolls back and exits non-zero.
	code, out := requestRun(t, "Should fail.\n",
		"--store", path, "--root", root, "--title", "Fail on missing alert",
		"--workflow", "single-worker@v1", "--alert", "alrt-missing",
		"--now", now.Format(time.RFC3339),
	)
	if code == 0 || !strings.Contains(out, "alert-not-found") {
		t.Fatalf("expected alert-not-found rejection, got %d: %s", code, out)
	}

	// Valid alert links and appears in request output and `tillr item show`.
	code, out = requestRun(t, "Fix cli alert.\n",
		"--store", path, "--root", root, "--title", "Fix cli alert",
		"--workflow", "single-worker@v1", "--alert", "alrt-cli-1",
		"--now", now.Format(time.RFC3339),
	)
	if code != 0 {
		t.Fatalf("request failed (%d): %s", code, out)
	}
	var reqEnv struct {
		Payload struct {
			ItemID       string   `json:"item_id"`
			LinkedAlerts []string `json:"linked_alerts"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(out), &reqEnv); err != nil {
		t.Fatalf("unmarshal request output: %v\n%s", err, out)
	}
	if reqEnv.Payload.ItemID == "" || len(reqEnv.Payload.LinkedAlerts) != 1 || reqEnv.Payload.LinkedAlerts[0] != "alrt-cli-1" {
		t.Fatalf("unexpected request payload: %+v", reqEnv.Payload)
	}

	cmd := exec.Command(bin, "show", "--store", path, "--root", root, "--item", reqEnv.Payload.ItemID)
	showOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("show failed: %v\n%s", err, showOut)
	}
	if !strings.Contains(string(showOut), `"linked_alerts"`) || !strings.Contains(string(showOut), `"alrt-cli-1"`) {
		t.Fatalf("show output missing linked_alerts: %s", showOut)
	}
}

func TestRequestCLITriageProvenanceAndDedupKey(t *testing.T) {
	path, s := placeFixture(t, "a")
	root := scratch(t, "request-triage-root")
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, settlement_state)
			VALUES ('alrt-cli-t1', 'drain-item-fault', 'subj-1', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', 'uninvestigated'),
			       ('alrt-cli-t2', 'drain-item-fault', 'subj-2', 1, 'open', 'high', 'fp2', 2, ?, 2, ?, '{}', 'uninvestigated')`,
			now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli())
		return err
	}); err != nil {
		t.Fatalf("seed alerts: %v", err)
	}

	code, out := requestRun(t, "Fix shared triage fault (first alert).\n",
		"--store", path, "--root", root, "--title", "Fix shared triage fault",
		"--workflow", "single-worker@v1",
		"--filed-by", "tillr-triage", "--dedup-key", "drain-item-fault:shared-cli",
		"--alert", "alrt-cli-t1", "--now", now.Format(time.RFC3339),
	)
	if code != 0 {
		t.Fatalf("first request failed (%d): %s", code, out)
	}
	var first struct {
		Payload struct {
			ItemID       string   `json:"item_id"`
			FiledBy      string   `json:"filed_by"`
			DedupKey     string   `json:"dedup_key"`
			Deduplicated bool     `json:"deduplicated"`
			LinkedAlerts []string `json:"linked_alerts"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatalf("unmarshal first: %v\n%s", err, out)
	}
	if first.Payload.ItemID == "" || first.Payload.FiledBy != "tillr-triage" || first.Payload.DedupKey != "drain-item-fault:shared-cli" || first.Payload.Deduplicated {
		t.Fatalf("unexpected first payload: %+v", first.Payload)
	}

	code, out = requestRun(t, "Fix shared triage fault (second alert).\n",
		"--store", path, "--root", root, "--title", "Fix shared triage fault (2)",
		"--workflow", "single-worker@v1",
		"--filed-by", "tillr-triage", "--dedup-key", "drain-item-fault:shared-cli",
		"--alert", "alrt-cli-t2", "--now", now.Add(time.Minute).Format(time.RFC3339),
	)
	if code != 0 {
		t.Fatalf("second request failed (%d): %s", code, out)
	}
	var second struct {
		Payload struct {
			ItemID       string   `json:"item_id"`
			FiledBy      string   `json:"filed_by"`
			DedupKey     string   `json:"dedup_key"`
			Deduplicated bool     `json:"deduplicated"`
			LinkedAlerts []string `json:"linked_alerts"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(out), &second); err != nil {
		t.Fatalf("unmarshal second: %v\n%s", err, out)
	}
	if second.Payload.ItemID != first.Payload.ItemID || !second.Payload.Deduplicated {
		t.Fatalf("expected dedup onto %s, got %+v", first.Payload.ItemID, second.Payload)
	}
	if len(second.Payload.LinkedAlerts) != 2 || second.Payload.LinkedAlerts[0] != "alrt-cli-t1" || second.Payload.LinkedAlerts[1] != "alrt-cli-t2" {
		t.Fatalf("expected both alerts on second response, got %+v", second.Payload.LinkedAlerts)
	}

	cmd := exec.Command(bin, "show", "--store", path, "--root", root, "--item", first.Payload.ItemID)
	showOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("show failed: %v\n%s", err, showOut)
	}
	var shown struct {
		Payload struct {
			FiledBy      string `json:"filed_by"`
			DedupKey     string `json:"dedup_key"`
			LinkedAlerts []struct {
				AlertID  string `json:"alert_id"`
				LinkedBy string `json:"linked_by"`
			} `json:"linked_alerts"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(showOut, &shown); err != nil {
		t.Fatalf("unmarshal show: %v\n%s", err, showOut)
	}
	if shown.Payload.FiledBy != "tillr-triage" || shown.Payload.DedupKey != "drain-item-fault:shared-cli" {
		t.Fatalf("show provenance mismatch: %+v", shown.Payload)
	}
	if len(shown.Payload.LinkedAlerts) != 2 || shown.Payload.LinkedAlerts[0].AlertID != "alrt-cli-t1" || shown.Payload.LinkedAlerts[1].AlertID != "alrt-cli-t2" {
		t.Fatalf("show linked_alerts mismatch: %+v", shown.Payload.LinkedAlerts)
	}
	if shown.Payload.LinkedAlerts[0].LinkedBy != "tillr-triage" || shown.Payload.LinkedAlerts[1].LinkedBy != "tillr-triage" {
		t.Fatalf("show linked_by mismatch: %+v", shown.Payload.LinkedAlerts)
	}
}
