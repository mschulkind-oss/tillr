package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func seedWatchItems(t *testing.T, s *store.Store, ids ...string) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec(`INSERT INTO items(item_id, title, state, updated_at, event_seq) VALUES (?, ?, 'in-queue', 1000, 0)`, id, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTriageCLIClaimRecordWaitAndStatusRecent(t *testing.T) {
	s, rules := fixture(t, false)
	seedWatchItems(t, s, "item-1", "item-2")

	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 {
		t.Fatalf("init eval: %d %s", code, &stderr)
	}

	// Open an episode via eval.
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 {
		t.Fatalf("eval: %d %s", code, &stderr)
	}

	var alert1 string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE state='open'`).Scan(&alert1)
	}); err != nil {
		t.Fatal(err)
	}

	// Claim triage via CLI; second claim must fail.
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"claim-triage", alert1, "--run", "run-cli-1", "--store", s.Path()}, &out, &stderr); code != 0 {
		t.Fatalf("claim-triage: %d %s", code, &stderr)
	}
	if !strings.Contains(out.String(), "triage claimed (run run-cli-1)") {
		t.Fatalf("unexpected claim-triage output: %q", out.String())
	}

	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"claim-triage", alert1, "--run", "run-cli-2", "--store", s.Path()}, &out, &stderr); code == 0 {
		t.Fatalf("expected second claim-triage to fail")
	}

	// Refusal of 'expected' outcome with exit code 2.
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"record-triage", alert1, "--run", "run-cli-1", "--outcome", "expected", "--finding", "benign", "--store", s.Path()}, &out, &stderr); code != 2 {
		t.Fatalf("expected exit 2 for expected outcome, got %d (%s)", code, &stderr)
	}

	// Record 'filed' outcome.
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{
		"record-triage", alert1,
		"--run", "run-cli-1",
		"--outcome", "filed",
		"--outcome-kind", "fix-fault",
		"--item", "item-1",
		"--finding", "seq 1 fault",
		"--store", s.Path(),
	}, &out, &stderr); code != 0 {
		t.Fatalf("record-triage filed: %d %s", code, &stderr)
	}

	// wait must NOT wake on triage-settled alert (times out with exit 2).
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "10ms", "--no-notify"}, &out, &stderr); code != 2 {
		t.Fatalf("expected wait to time out on triage-settled alert, got %d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}

	// Mark alert1 resolved at current time so status --recent finds it.
	nowMs := time.Now().UnixMilli()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET state='resolved', resolved_at=? WHERE alert_id=?`, nowMs, alert1)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Check status --recent 1h (text + JSON).
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"status", "--store", s.Path(), "--rules", rules, "--recent", "1h"}, &out, &stderr); code != 0 {
		t.Fatalf("status --recent text: %d %s", code, &stderr)
	}
	if !strings.Contains(out.String(), "triage untriaged=0 triaging=0 settled=1 escalated=0") || !strings.Contains(out.String(), "triage_outcome=filed") {
		t.Fatalf("unexpected status --recent text output:\n%s", out.String())
	}

	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"status", "--store", s.Path(), "--rules", rules, "--recent", "1h", "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("status --recent json: %d %s", code, &stderr)
	}
	var report struct {
		Summary watch.StatusSummary `json:"summary"`
		Alerts  []watch.AlertStatus `json:"alerts"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.Triage.Settled != 1 || len(report.Alerts) != 1 || report.Alerts[0].Triage == nil || report.Alerts[0].Triage.Outcome != "filed" {
		t.Fatalf("unexpected status --recent JSON: %+v", report)
	}

	// Open a second episode, claim it, and escalate to operator-session.
	fact(t, s, `{"item_id":"item-b"}`)
	fact(t, s, `{"item_id":"item-b"}`)
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 {
		t.Fatalf("eval 2: %d %s", code, &stderr)
	}
	var alert2 string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE state='open'`).Scan(&alert2)
	}); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"claim-triage", alert2, "--run", "run-cli-esc", "--store", s.Path()}, &out, &stderr); code != 0 {
		t.Fatalf("claim-triage 2: %d %s", code, &stderr)
	}
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{
		"record-triage", alert2,
		"--run", "run-cli-esc",
		"--outcome", "escalated",
		"--escalate-to", "operator-session",
		"--finding", "ambiguous cause between lane-1 and lane-2",
		"--action-needed", "check lane-1 worktree",
		"--store", s.Path(),
	}, &out, &stderr); code != 0 {
		t.Fatalf("record-triage escalated: %d %s", code, &stderr)
	}

	// wait MUST wake on escalated alert with triage header leading.
	out.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "100ms", "--no-notify"}, &out, &stderr); code != 0 {
		t.Fatalf("expected wait to wake on escalated alert, got %d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	waitOut := out.String()
	triageIdx := strings.Index(waitOut, "triage: run=run-cli-esc outcome=escalated escalated_to=operator-session")
	ackIdx := strings.Index(waitOut, "ack: tillr watch ack "+alert2)
	if triageIdx < 0 || ackIdx < 0 || triageIdx > ackIdx {
		t.Fatalf("expected triage header ahead of actions for %s in wait output:\n%s", alert2, waitOut)
	}
}
