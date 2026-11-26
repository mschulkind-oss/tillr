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
