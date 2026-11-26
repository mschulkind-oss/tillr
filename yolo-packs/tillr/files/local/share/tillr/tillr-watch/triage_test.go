package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
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

func TestTriageAndOperatorSessionWaitAndLintCLI(t *testing.T) {
	ctx := context.Background()

	// 1. CLI lint rejects loop-liveness rule routed to triage.
	lintDir := t.TempDir()
	rulesSub := filepath.Join(lintDir, "config", "tillr", "watch")
	if err := os.MkdirAll(rulesSub, 0o755); err != nil {
		t.Fatal(err)
	}
	badRule := `
[[rule]]
id = "drain-loop-fault"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-ended"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.stop == "fault"'
window = "1h"

[rule.guidance]
summary = "Loop faulted."
check = ["Check log."]
do = ["Restart."]

[rule.delivery]
receiver = "triage"
`
	if err := os.WriteFile(filepath.Join(rulesSub, "loop-fault.toml"), []byte(badRule), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run(ctx, []string{"lint", lintDir}, &out, &stderr); code != 1 {
		t.Fatalf("expected lint exit 1 for liveness rule on triage, got %d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "watch-lint-liveness-receiver") {
		t.Fatalf("expected watch-lint-liveness-receiver in lint output, got:\n%s", out.String())
	}

	// 2. CLI wait with --owner tillr-triage vs --owner operator-session routes each to its own rules only.
	s, _ := fixture(t, false)
	rulesDir := t.TempDir()
	routingRules := `
[[rule]]
id = "cli-triage-rule"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
window = "1h"

[rule.guidance]
summary = "Triage item {{subject}}"
check = ["Check item."]
do = ["File defect."]

[rule.delivery]
receiver = "triage"

[[rule]]
id = "cli-ops-rule"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-ended"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.stop == "fault"'
window = "1h"

[rule.guidance]
summary = "Loop faulted"
check = ["Check loop."]
do = ["Restart loop."]

[rule.delivery]
receiver = "operator-session"
`
	if err := os.WriteFile(filepath.Join(rulesDir, "routing.toml"), []byte(routingRules), 0o600); err != nil {
		t.Fatal(err)
	}
	// Establish rule load baselines.
	out.Reset()
	stderr.Reset()
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rulesDir}, &out, &stderr); code != 0 {
		t.Fatalf("baseline eval: %d %s", code, &stderr)
	}

	// Fire both rules.
	fact(t, s, `{"item_id":"item-triage-1"}`)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			At:      time.Now(),
			Kind:    "drain.loop-ended",
			Payload: []byte(`{"stop":"fault"}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	stderr.Reset()
	if code := run(ctx, []string{"wait", "--store", s.Path(), "--rules", rulesDir, "--owner", "tillr-triage", "--timeout", "100ms", "--no-notify"}, &out, &stderr); code != 0 {
		t.Fatalf("wait tillr-triage: %d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "cli-triage-rule") || strings.Contains(out.String(), "cli-ops-rule") {
		t.Fatalf("tillr-triage received wrong notices:\n%s", out.String())
	}

	out.Reset()
	stderr.Reset()
	if code := run(ctx, []string{"wait", "--store", s.Path(), "--rules", rulesDir, "--owner", "operator-session", "--timeout", "100ms", "--no-notify"}, &out, &stderr); code != 0 {
		t.Fatalf("wait operator-session: %d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "cli-ops-rule") || strings.Contains(out.String(), "cli-triage-rule") {
		t.Fatalf("operator-session received wrong notices:\n%s", out.String())
	}
}
