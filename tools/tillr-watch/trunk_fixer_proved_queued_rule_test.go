package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainTrunkFixerProvedQueuedRule(t *testing.T) {
	const ruleID = "drain-trunk-fixer-proved-queued"

	// (a) LoadRules loads drain-trunk-fixer-proved-queued and tillr watch lint passes.
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(r watch.Rule) bool { return r.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := rules[idx]
	if rule.Scope != "charter:"+definition.GitLandingName || rule.Severity != "warning" || !rule.Enabled ||
		rule.Delivery.Receiver != "foreground" ||
		!slices.Equal(rule.Reads.Projections, []string{"items", "fixer_checks", "green_proof:refs/tillr/green"}) ||
		len(rule.Reads.Events) != 0 ||
		rule.When.Pattern != watch.PredicateMatch ||
		rule.When.GroupBy != "item_id" ||
		rule.When.Match == nil {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}
	if strings.Contains(strings.ToLower(rule.Guidance.Summary+" "+strings.Join(rule.Guidance.Check, " ")+" "+strings.Join(rule.Guidance.Do, " ")), "expected") {
		t.Fatalf("%s guidance must not contain \"expected\" per D-TW73: %+v", ruleID, rule.Guidance)
	}

	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	ctx := context.Background()
	root := t.TempDir()
	var gitEnv []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			gitEnv = append(gitEnv, entry)
		}
	}
	gitEnv = append(gitEnv,
		"GIT_AUTHOR_NAME=Watch Fixture", "GIT_AUTHOR_EMAIL=watch@example.invalid",
		"GIT_COMMITTER_NAME=Watch Fixture", "GIT_COMMITTER_EMAIL=watch@example.invalid",
	)
	gitAt := func(at time.Time, input string, args ...string) string {
		t.Helper()
		stamp := at.UTC().Format("2006-01-02T15:04:05+0000")
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "--git-dir=" + filepath.Join(root, ".git")}, args...)...)
		cmd.Env = append(slices.Clone(gitEnv), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	now := time.Date(2026, 11, 23, 14, 25, 0, 0, time.UTC)
	gitAt(now, "", "init", "--quiet", "--initial-branch=main")
	tree := gitAt(now, "", "mktree")
	preFilingOID := gitAt(now.Add(-20*time.Minute), "pre-filing", "commit-tree", tree)
	redOID := gitAt(now.Add(-10*time.Minute), "red", "commit-tree", tree, "-p", preFilingOID)
	greenOID := gitAt(now.Add(-2*time.Minute), "green", "commit-tree", tree, "-p", redOID)
	gitAt(now, "", "update-ref", "refs/heads/main", greenOID)

	writeGreenProof := func(oid string, provedAt time.Time, passed []string) {
		t.Helper()
		gateDir := filepath.Join(root, ".git", "tillr-gate")
		if err := os.MkdirAll(gateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{
			"commit":    oid,
			"proved_at": provedAt.UTC().Format(time.RFC3339),
			"passed":    passed,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gateDir, "green-"+oid+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	e := watch.Evaluator{
		Store: s,
		Root:  root,
		Rules: []watch.Rule{rule},
		Clock: func() time.Time { return now },
	}

	evalClean := func() []watch.Notice {
		t.Helper()
		res, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].ID != ruleID || res[0].Diagnostic() != "" || res[0].Skipped != 0 {
			t.Fatalf("want clean %s evaluation, got %+v", ruleID, res)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}

	insertItem := func(itemID, state, redChecks string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"item_id": itemID, "state": state})
		if err != nil {
			t.Fatal(err)
		}
		id := itemID
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			seq, err := tx.Append(event.Event{Kind: "item.add", ObjectType: "item", ObjectID: &id, At: at, Payload: body})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO items (item_id, title, state, updated_at, event_seq) VALUES (?, ?, ?, ?, ?)
				 ON CONFLICT(item_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at, event_seq = excluded.event_seq`,
				itemID, itemID, state, at.UnixMilli(), seq,
			); err != nil {
				return err
			}
			if redChecks != "" {
				if _, err := tx.Exec(
					`INSERT INTO item_criteria (item_id, criterion_id, text, state) VALUES (?, 'trunk-green', ?, 'not-done')`,
					itemID, "(red: "+redChecks+")",
				); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	updateItemState := func(itemID, state string, at time.Time) {
		t.Helper()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state = ?, updated_at = ? WHERE item_id = ?`, state, at.UnixMilli(), itemID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	// (a) Clean evaluation with no green-<sha>.json and no refs/tillr/green yet.
	if notices := evalClean(); len(notices) != 0 {
		t.Fatalf("empty store should have no notices, got %+v", notices)
	}

	// (b) Single-check fixer (like fixer-main-18f649ca0940) in-queue stays quiet when no green proof exists.
	singleFixer := "fixer-main-" + redOID[:12]
	insertItem(singleFixer, "in-queue", "check-a", now.Add(-10*time.Minute))
	if notices := evalClean(); len(notices) != 0 {
		t.Fatalf("fixer with no green proof must stay quiet, got %+v", notices)
	}

	// (b) Pre-filing commit on refs/tillr/green (not a descendant of redOID) stays quiet.
	gitAt(now, "", "update-ref", "refs/tillr/green", preFilingOID)
	writeGreenProof(preFilingOID, now.Add(-1*time.Minute), []string{"check-a"})
	if notices := evalClean(); len(notices) != 0 {
		t.Fatalf("pre-filing green proof before <red> must stay quiet, got %+v", notices)
	}

	// (b) Descendant commit on refs/tillr/green missing the filed check stays quiet.
	gitAt(now, "", "update-ref", "refs/tillr/green", greenOID)
	writeGreenProof(greenOID, now.Add(-1*time.Minute), []string{"other-check"})
	if notices := evalClean(); len(notices) != 0 {
		t.Fatalf("green proof missing filed check must stay quiet, got %+v", notices)
	}

	// (d) Ordinary item-request-* in in-queue and terminal fixers (closed, landed) stay quiet.
	insertItem("item-request-2e362fc80a", "in-queue", "", now.Add(-5*time.Minute))
	closedFixer := "fixer-main-" + preFilingOID[:12]
	insertItem(closedFixer, "closed", "check-a", now.Add(-15*time.Minute))
	landedFixer := "fixer-main-" + greenOID[:12]
	insertItem(landedFixer, "landed", "check-a", now.Add(-15*time.Minute))

	// (c) Descendant green proof covering the single-check fixer fires once; repeat evaluation opens no duplicate.
	writeGreenProof(greenOID, now.Add(-1*time.Minute), []string{"check-a", "check-b"})
	first := evalClean()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != singleFixer || first[0].Bundle.Severity != "warning" {
		t.Fatalf("want 1 warning notice for %s, got %+v", singleFixer, first)
	}
	if !strings.Contains(first[0].Bundle.Guidance.Summary, singleFixer) {
		t.Fatalf("guidance summary must name %s, got %q", singleFixer, first[0].Bundle.Guidance.Summary)
	}
	again := evalClean()
	if len(again) != 1 || again[0].AlertID != first[0].AlertID {
		t.Fatalf("repeat evaluation must not open duplicate episode or notice: first=%+v again=%+v", first, again)
	}

	// (d) Transitioning the queued fixer to closed resolves the episode with item-closed.
	updateItemState(singleFixer, "closed", now)
	if notices := evalClean(); len(notices) != 0 {
		t.Fatalf("closed fixer should resolve episode, got %+v", notices)
	}
	var closedReason string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT resolved_reason FROM alert_episodes WHERE alert_id = ?`, first[0].AlertID).Scan(&closedReason)
	}); err != nil {
		t.Fatal(err)
	}
	if closedReason != "item-closed" {
		t.Fatalf("resolved_reason = %q, want item-closed", closedReason)
	}

	// (b)+(c)+(d) Multi-check fixer: stays quiet until all filed checks are in passed, fires once when covered, resolves item-landed.
	multiRedOID := gitAt(now.Add(1*time.Minute), "multi-red", "commit-tree", tree, "-p", greenOID)
	multiGreenOID := gitAt(now.Add(2*time.Minute), "multi-green", "commit-tree", tree, "-p", multiRedOID)
	gitAt(now.Add(2*time.Minute), "", "update-ref", "refs/heads/main", multiGreenOID)
	gitAt(now.Add(2*time.Minute), "", "update-ref", "refs/tillr/green", multiGreenOID)

	multiFixer := "fixer-main-" + multiRedOID[:12]
	now = now.Add(3 * time.Minute)
	insertItem(multiFixer, "in-queue", "check-a, check-b", now.Add(-1*time.Minute))

	writeGreenProof(multiGreenOID, now, []string{"check-a"})
	if notices := evalClean(); len(notices) != 0 {
		t.Fatalf("multi-check fixer missing check-b must stay quiet, got %+v", notices)
	}

	writeGreenProof(multiGreenOID, now, []string{"check-a", "check-b"})
	multiNotices := evalClean()
	if len(multiNotices) != 1 || multiNotices[0].Bundle.Subject != multiFixer {
		t.Fatalf("want 1 notice for multi-check fixer %s, got %+v", multiFixer, multiNotices)
	}
	if again := evalClean(); len(again) != 1 || again[0].AlertID != multiNotices[0].AlertID {
		t.Fatalf("repeat evaluation on multi-check fixer must not duplicate: %+v", again)
	}

	updateItemState(multiFixer, "landed", now.Add(time.Minute))
	if notices := evalClean(); len(notices) != 0 {
		t.Fatalf("landed fixer should resolve episode, got %+v", notices)
	}
	var landedReason string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT resolved_reason FROM alert_episodes WHERE alert_id = ?`, multiNotices[0].AlertID).Scan(&landedReason)
	}); err != nil {
		t.Fatal(err)
	}
	if landedReason != "item-landed" {
		t.Fatalf("resolved_reason = %q, want item-landed", landedReason)
	}
}
