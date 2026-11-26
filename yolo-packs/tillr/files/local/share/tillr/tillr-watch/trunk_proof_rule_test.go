package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

type trunkProofFixture struct {
	t    *testing.T
	ctx  context.Context
	root string
	env  []string
	tree string
	head string
	s    *store.Store
	e    watch.Evaluator
	now  time.Time
}

func newTrunkProofFixture(t *testing.T) *trunkProofFixture {
	t.Helper()
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	var selected []watch.Rule
	for _, want := range []string{"trunk-proof-lag", "trunk-proof-stalled"} {
		idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == want })
		if idx < 0 {
			t.Fatalf("missing %s rule in config/tillr/watch", want)
		}
		selected = append(selected, rules[idx])
	}

	ctx := context.Background()
	root := t.TempDir()
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env,
		"GIT_AUTHOR_NAME=Watch Fixture", "GIT_AUTHOR_EMAIL=watch@example.invalid",
		"GIT_COMMITTER_NAME=Watch Fixture", "GIT_COMMITTER_EMAIL=watch@example.invalid",
	)
	f := &trunkProofFixture{
		t:    t,
		ctx:  ctx,
		root: root,
		env:  env,
		now:  time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC),
	}
	f.gitAt(f.now, "", "init", "--quiet", "--initial-branch=main")
	f.tree = f.gitAt(f.now, "", "mktree")
	f.head = f.gitAt(f.now, "base", "commit-tree", f.tree)
	f.gitAt(f.now, "", "update-ref", "refs/heads/main", f.head)
	f.gitAt(f.now, "", "update-ref", "refs/tillr/green", f.head)

	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f.s = s
	f.e = watch.Evaluator{
		Store: s,
		Root:  root,
		Rules: selected,
		Clock: func() time.Time { return f.now },
	}
	if _, err := f.e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *trunkProofFixture) gitAt(at time.Time, input string, args ...string) string {
	f.t.Helper()
	stamp := at.UTC().Format("2006-01-02T15:04:05+0000")
	commitEnv := append(slices.Clone(f.env), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	cmd := exec.CommandContext(f.ctx, "git", append([]string{"-C", f.root, "--git-dir=" + filepath.Join(f.root, ".git")}, args...)...)
	cmd.Env = commitEnv
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("fixture git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *trunkProofFixture) landAt(at time.Time) string {
	f.t.Helper()
	f.head = f.gitAt(at, "land", "commit-tree", f.tree, "-p", f.head)
	f.gitAt(at, "", "update-ref", "refs/heads/main", f.head)
	return f.head
}

func (f *trunkProofFixture) markGreen(sha string) {
	f.t.Helper()
	f.gitAt(f.now, "", "update-ref", "refs/tillr/green", sha)
}

func (f *trunkProofFixture) appendEvent(kind, objectType, objectID string, at time.Time, payload any) {
	f.t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	id := objectID
	if err := f.s.WriteTx(f.ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       kind,
			ObjectType: objectType,
			ObjectID:   &id,
			At:         at.UTC(),
			Payload:    body,
		})
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *trunkProofFixture) appendProgress(at time.Time, fact drain.HeavyProgressFact) {
	f.t.Helper()
	f.appendEvent(drain.HeavyProgressKind, "trunk", "main", at, fact)
}

func (f *trunkProofFixture) pendingAt(at time.Time) []watch.Notice {
	f.t.Helper()
	f.now = at
	results, err := f.e.EvaluateRules(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, r := range results {
		if r.Diagnostic() != "" {
			f.t.Fatalf("rule %s returned diagnostic %q", r.ID, r.Diagnostic())
		}
	}
	notices, err := f.e.Pending(f.ctx, "foreground")
	if err != nil {
		f.t.Fatal(err)
	}
	return notices
}

func TestTrunkProofRulesStayQuietDuringActiveFifteenMinuteProofWithNormalLandings(t *testing.T) {
	f := newTrunkProofFixture(t)
	t0 := f.now

	// Six lanes land ~3m apart while a 15m heavy proof runs on tip1 and reports progress < 10m apart.
	tip1 := f.landAt(t0)
	f.appendProgress(t0, drain.HeavyProgressFact{TipSHA: tip1, Phase: "started"})
	f.landAt(t0.Add(3 * time.Minute))
	f.appendProgress(t0.Add(5*time.Minute), drain.HeavyProgressFact{TipSHA: tip1, Phase: "progress", CheckID: "go-shards", Outcome: "pass"})
	f.landAt(t0.Add(6 * time.Minute))
	f.landAt(t0.Add(9 * time.Minute))
	if notices := f.pendingAt(t0.Add(9 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence at 9m with 4 commits ahead during active proof, got %+v", notices)
	}

	f.appendProgress(t0.Add(11*time.Minute), drain.HeavyProgressFact{TipSHA: tip1, Phase: "progress", CheckID: "fork-build", Outcome: "pass"})
	f.landAt(t0.Add(12 * time.Minute))
	tip6 := f.landAt(t0.Add(14 * time.Minute))
	if notices := f.pendingAt(t0.Add(14 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence at 14m with 6 commits ahead during active proof, got %+v", notices)
	}

	// Proof on tip1 finishes green at 15m; oldest remaining unproved commit landed at t0+3m (12m old).
	f.markGreen(tip1)
	f.appendProgress(t0.Add(15*time.Minute), drain.HeavyProgressFact{TipSHA: tip1, Phase: "ended", Outcome: "green"})
	if notices := f.pendingAt(t0.Add(15 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence when proof ends at 15m, got %+v", notices)
	}

	// Even 15m after ended (no running proof), trunk-proof-stalled stays disarmed;
	// catching up tip6 before 95m keeps trunk-proof-lag silent too.
	f.markGreen(tip6)
	if notices := f.pendingAt(t0.Add(35 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence while idle after ended and green caught up, got %+v", notices)
	}
}

func TestTrunkProofLagStaysQuietAcrossTwoWorkingProofs(t *testing.T) {
	f := newTrunkProofFixture(t)
	t0 := f.now

	// Proof 1 starts on tip1, then tip2 lands 1m later and must wait for both
	// proof 1 (40m) and proof 2 (45m) before refs/tillr/green covers it (85m total).
	tip1 := f.landAt(t0)
	f.appendProgress(t0, drain.HeavyProgressFact{TipSHA: tip1, Phase: "started"})
	tip2 := f.landAt(t0.Add(1 * time.Minute))

	for _, elapsed := range []time.Duration{8, 16, 24, 32} {
		f.appendProgress(t0.Add(elapsed*time.Minute), drain.HeavyProgressFact{TipSHA: tip1, Phase: "progress", CheckID: "go-shards", Outcome: "pass"})
		if notices := f.pendingAt(t0.Add(elapsed * time.Minute)); len(notices) != 0 {
			t.Fatalf("expected silence during proof 1 at %dm, got %+v", elapsed, notices)
		}
	}
	f.markGreen(tip1)
	f.appendProgress(t0.Add(40*time.Minute), drain.HeavyProgressFact{TipSHA: tip1, Phase: "ended", Outcome: "green"})
	if notices := f.pendingAt(t0.Add(40 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence when proof 1 ends at 40m (tip2 age 39m), got %+v", notices)
	}

	f.appendProgress(t0.Add(41*time.Minute), drain.HeavyProgressFact{TipSHA: tip2, Phase: "started"})
	for _, elapsed := range []time.Duration{49, 57, 65, 73, 81, 89} {
		f.appendProgress(t0.Add(elapsed*time.Minute), drain.HeavyProgressFact{TipSHA: tip2, Phase: "progress", CheckID: "go-shards", Outcome: "pass"})
		if notices := f.pendingAt(t0.Add(elapsed * time.Minute)); len(notices) != 0 {
			t.Fatalf("expected silence during proof 2 at %dm (tip2 age %dm), got %+v", elapsed, elapsed-1, notices)
		}
	}
	f.markGreen(tip2)
	f.appendProgress(t0.Add(91*time.Minute), drain.HeavyProgressFact{TipSHA: tip2, Phase: "ended", Outcome: "green"})
	if notices := f.pendingAt(t0.Add(91 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence when proof 2 covers tip2 at 90m wait, got %+v", notices)
	}
}

func TestTrunkProofLagFiresAtNinetyFiveMinutesUnprovedAge(t *testing.T) {
	f := newTrunkProofFixture(t)
	t0 := f.now
	tip := f.landAt(t0)

	for _, elapsed := range []time.Duration{30, 60, 90, 94} {
		if notices := f.pendingAt(t0.Add(elapsed * time.Minute)); len(notices) != 0 {
			t.Fatalf("expected silence at %dm unproved age, got %+v", elapsed, notices)
		}
	}

	notices := f.pendingAt(t0.Add(95 * time.Minute))
	if len(notices) != 1 || notices[0].Bundle.RuleID != "trunk-proof-lag" {
		t.Fatalf("want one trunk-proof-lag notice at 95m, got %+v", notices)
	}
	if !strings.Contains(notices[0].Bundle.Guidance.Summary, "95m") {
		t.Fatalf("want 95m in trunk-proof-lag summary, got %q", notices[0].Bundle.Guidance.Summary)
	}

	f.markGreen(tip)
	if after := f.pendingAt(t0.Add(96 * time.Minute)); len(after) != 0 {
		t.Fatalf("expected trunk-proof-lag to resolve once green catches up, got %+v", after)
	}
}

func TestTrunkProofStalledFiresAtTenMinutesWithoutProgressAndResolves(t *testing.T) {
	cases := []struct {
		name    string
		resolve func(f *trunkProofFixture, tip string, at time.Time)
	}{
		{
			name: "fresh_progress",
			resolve: func(f *trunkProofFixture, tip string, at time.Time) {
				f.appendProgress(at, drain.HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: "unit", Outcome: "pass"})
			},
		},
		{
			name: "ended",
			resolve: func(f *trunkProofFixture, tip string, at time.Time) {
				f.appendProgress(at, drain.HeavyProgressFact{TipSHA: tip, Phase: "ended", Outcome: "green"})
			},
		},
		{
			name: "heavy_fault",
			resolve: func(f *trunkProofFixture, tip string, at time.Time) {
				f.appendEvent(drain.HeavyFaultKind, "trunk", "main", at, map[string]any{"tip_sha": tip, "detail": "boom"})
			},
		},
		{
			name: "loop_ended",
			resolve: func(f *trunkProofFixture, _ string, at time.Time) {
				f.appendEvent("drain.loop-ended", "session", "sess-1", at, map[string]any{"session_id": "sess-1", "reason": "stopped"})
			},
		},
		{
			name: "drain_build_started",
			resolve: func(f *trunkProofFixture, _ string, at time.Time) {
				f.appendEvent("drain.build-started", "process", "drain", at, map[string]any{"role": "drain"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTrunkProofFixture(t)
			t0 := f.now
			tip := f.landAt(t0)

			f.appendProgress(t0, drain.HeavyProgressFact{TipSHA: tip, Phase: "started"})
			f.appendProgress(t0.Add(4*time.Minute), drain.HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: "lint", Outcome: "pass"})

			// 9m after last progress (13m total): neither stalled nor 30m lag fires.
			if notices := f.pendingAt(t0.Add(13 * time.Minute)); len(notices) != 0 {
				t.Fatalf("expected silence 9m after last progress, got %+v", notices)
			}

			// 10m after last progress (14m total, still < 30m lag): trunk-proof-stalled fires.
			notices := f.pendingAt(t0.Add(14 * time.Minute))
			if len(notices) != 1 || notices[0].Bundle.RuleID != "trunk-proof-stalled" {
				t.Fatalf("want one trunk-proof-stalled notice at 10m without progress, got %+v", notices)
			}
			if !strings.Contains(notices[0].Bundle.Guidance.Summary, "10m") || !strings.Contains(notices[0].Bundle.Guidance.Summary, tip) {
				t.Fatalf("want elapsed and tip SHA in summary, got %q", notices[0].Bundle.Guidance.Summary)
			}

			// Starting a view process (role != "drain") must not clear an active stall.
			f.appendEvent("drain.build-started", "process", "view", t0.Add(15*time.Minute), map[string]any{"role": "view"})
			if still := f.pendingAt(t0.Add(15 * time.Minute)); len(still) != 1 || still[0].AlertID != notices[0].AlertID {
				t.Fatalf("view build-started must not clear stall, got %+v", still)
			}

			// Applying the resolution signal clears the open episode.
			tc.resolve(f, tip, t0.Add(16*time.Minute))
			if after := f.pendingAt(t0.Add(16 * time.Minute)); len(after) != 0 {
				t.Fatalf("expected %s to resolve trunk-proof-stalled, got %+v", tc.name, after)
			}
		})
	}
}

func TestTrunkProofStalledStaysQuietDuring28e0b8a725TenMinuteShardedCheck(t *testing.T) {
	f := newTrunkProofFixture(t)
	// 2026-11-22 04:00:00 AM ET = 2026-11-22T09:00:00Z
	t0 := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	const shardedCheck = "go-tool/tools-tillr-drain-s-whole-suite-passes"

	tip := f.landAt(t0)
	f.appendProgress(t0, drain.HeavyProgressFact{TipSHA: tip, Phase: "started"})

	// 04:03:47 AM ET: self-suite finishes.
	f.appendProgress(t0.Add(3*time.Minute+47*time.Second), drain.HeavyProgressFact{
		TipSHA:  tip,
		Phase:   "progress",
		CheckID: "self-suite",
		Outcome: "pass",
	})

	// 04:04:47 through 04:12:47 AM ET (9 snapshots, 1m apart): throttled in-check completion progress.
	for m := 4; m <= 12; m++ {
		f.appendProgress(t0.Add(time.Duration(m)*time.Minute+47*time.Second), drain.HeavyProgressFact{
			TipSHA:  tip,
			Phase:   "progress",
			CheckID: shardedCheck,
			Outcome: "running",
		})
	}

	// 04:13:47 AM ET (10m 00s after self-suite, when the false alert fired on 2026-11-22): stays quiet.
	if notices := f.pendingAt(t0.Add(13*time.Minute + 47*time.Second)); len(notices) != 0 {
		t.Fatalf("expected silence at 04:13:47 AM ET during active sharded check, got %+v", notices)
	}

	// 04:13:48 AM ET (10m 01s check duration): terminal check completion.
	f.appendProgress(t0.Add(13*time.Minute+48*time.Second), drain.HeavyProgressFact{
		TipSHA:  tip,
		Phase:   "progress",
		CheckID: shardedCheck,
		Outcome: "pass",
	})

	// 04:13:53 AM ET: proof finishes green.
	endAt := t0.Add(13*time.Minute + 53*time.Second)
	f.now = endAt
	f.markGreen(tip)
	f.appendProgress(endAt, drain.HeavyProgressFact{
		TipSHA:  tip,
		Phase:   "ended",
		Outcome: "green",
	})
	if notices := f.pendingAt(endAt); len(notices) != 0 {
		t.Fatalf("expected silence when proof ends at 04:13:53 AM ET, got %+v", notices)
	}

	// 04:30:00 AM ET: stays quiet while idle after ended.
	if notices := f.pendingAt(t0.Add(30 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence while idle after ended at 04:30:00 AM ET, got %+v", notices)
	}
}

func TestTrunkProofStalledFiresWhenCheckReportsNothingForTenMinuteWindow(t *testing.T) {
	const shardedCheck = "go-tool/tools-tillr-drain-s-whole-suite-passes"

	t.Run("hang_from_check_start", func(t *testing.T) {
		f := newTrunkProofFixture(t)
		t0 := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)

		tip := f.landAt(t0)
		f.appendProgress(t0, drain.HeavyProgressFact{TipSHA: tip, Phase: "started"})
		f.appendProgress(t0.Add(3*time.Minute+47*time.Second), drain.HeavyProgressFact{
			TipSHA:  tip,
			Phase:   "progress",
			CheckID: "self-suite",
			Outcome: "pass",
		})

		// 04:12:47 AM ET (9m after self-suite): stays quiet.
		if notices := f.pendingAt(t0.Add(12*time.Minute + 47*time.Second)); len(notices) != 0 {
			t.Fatalf("expected silence at 04:12:47 AM ET (9m), got %+v", notices)
		}

		// 04:13:47 AM ET (10m 00s after self-suite): trunk-proof-stalled opens naming tip.
		notices := f.pendingAt(t0.Add(13*time.Minute + 47*time.Second))
		if len(notices) != 1 || notices[0].Bundle.RuleID != "trunk-proof-stalled" {
			t.Fatalf("want one trunk-proof-stalled notice at 04:13:47 AM ET, got %+v", notices)
		}
		if !strings.Contains(notices[0].Bundle.Guidance.Summary, tip) {
			t.Fatalf("want tip SHA %s in summary, got %q", tip, notices[0].Bundle.Guidance.Summary)
		}

		// Fresh progress resolves the stall.
		resolveAt := t0.Add(14*time.Minute + 47*time.Second)
		f.appendProgress(resolveAt, drain.HeavyProgressFact{
			TipSHA:  tip,
			Phase:   "progress",
			CheckID: shardedCheck,
			Outcome: "running",
		})
		if after := f.pendingAt(resolveAt); len(after) != 0 {
			t.Fatalf("expected fresh progress to resolve trunk-proof-stalled, got %+v", after)
		}
	})

	t.Run("mid_check_hang_after_running_progress", func(t *testing.T) {
		f := newTrunkProofFixture(t)
		t0 := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)

		tip := f.landAt(t0)
		f.appendProgress(t0, drain.HeavyProgressFact{TipSHA: tip, Phase: "started"})
		f.appendProgress(t0.Add(3*time.Minute+47*time.Second), drain.HeavyProgressFact{
			TipSHA:  tip,
			Phase:   "progress",
			CheckID: "self-suite",
			Outcome: "pass",
		})

		// 04:04:47, 04:05:47, 04:06:47 AM ET: 1m running progress events, then hang.
		for m := 4; m <= 6; m++ {
			f.appendProgress(t0.Add(time.Duration(m)*time.Minute+47*time.Second), drain.HeavyProgressFact{
				TipSHA:  tip,
				Phase:   "progress",
				CheckID: shardedCheck,
				Outcome: "running",
			})
		}

		// 04:13:47 AM ET and 04:15:47 AM ET (9m after last running event): stays quiet.
		if notices := f.pendingAt(t0.Add(13*time.Minute + 47*time.Second)); len(notices) != 0 {
			t.Fatalf("expected silence at 04:13:47 AM ET, got %+v", notices)
		}
		if notices := f.pendingAt(t0.Add(15*time.Minute + 47*time.Second)); len(notices) != 0 {
			t.Fatalf("expected silence at 04:15:47 AM ET (9m after last running event), got %+v", notices)
		}

		// 04:16:47 AM ET (10m after last running event): trunk-proof-stalled opens naming tip.
		notices := f.pendingAt(t0.Add(16*time.Minute + 47*time.Second))
		if len(notices) != 1 || notices[0].Bundle.RuleID != "trunk-proof-stalled" {
			t.Fatalf("want one trunk-proof-stalled notice at 04:16:47 AM ET, got %+v", notices)
		}
		if !strings.Contains(notices[0].Bundle.Guidance.Summary, tip) {
			t.Fatalf("want tip SHA %s in summary, got %q", tip, notices[0].Bundle.Guidance.Summary)
		}
	})
}

func TestTrunkProofLagReopensOnRedProofCLI(t *testing.T) {
	const rulesDir = "rules"

	for _, ackMode := range []string{"ack", "settle"} {
		t.Run(ackMode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			now := time.Now().UTC()
			root := t.TempDir()
			var env []string
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "GIT_") {
					env = append(env, entry)
				}
			}
			env = append(env,
				"GIT_AUTHOR_NAME=Watch Fixture", "GIT_AUTHOR_EMAIL=watch@example.invalid",
				"GIT_COMMITTER_NAME=Watch Fixture", "GIT_COMMITTER_EMAIL=watch@example.invalid",
			)

			s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })

			f := &trunkProofFixture{
				t:    t,
				ctx:  ctx,
				root: root,
				env:  env,
				s:    s,
				now:  now,
			}
			f.gitAt(now.Add(-110*time.Minute), "", "init", "--quiet", "--initial-branch=main")
			f.tree = f.gitAt(now.Add(-110*time.Minute), "", "mktree")
			f.head = f.gitAt(now.Add(-110*time.Minute), "base", "commit-tree", f.tree)
			f.gitAt(now.Add(-110*time.Minute), "", "update-ref", "refs/heads/main", f.head)
			f.gitAt(now.Add(-110*time.Minute), "", "update-ref", "refs/tillr/green", f.head)

			if ackMode == "settle" {
				seedCLIItems(t, s, map[string]string{"item-open": "in-queue"})
			}

			invoke := func(args ...string) (int, string, string) {
				var out, stderr bytes.Buffer
				code := run(ctx, args, &out, &stderr)
				return code, out.String(), stderr.String()
			}
			invokeStore := func(face string, args ...string) (int, string, string) {
				full := append([]string{face, "--store", s.Path()}, args...)
				return invoke(full...)
			}
			readStatus := func() (alertState string, pending int) {
				t.Helper()
				code, out, errOut := invokeStore("status", "--rules", rulesDir, "--root", root, "--json")
				if code != 0 || errOut != "" {
					t.Fatalf("status --json: code=%d out=%q err=%q", code, out, errOut)
				}
				var payload struct {
					Alerts []watch.AlertStatus `json:"alerts"`
					Owners []watch.OwnerStatus `json:"owners"`
				}
				if err := json.Unmarshal([]byte(out), &payload); err != nil {
					t.Fatalf("unmarshal status --json: %v\n%s", err, out)
				}
				for _, a := range payload.Alerts {
					if a.RuleID == "trunk-proof-lag" {
						alertState = a.State
						break
					}
				}
				for _, o := range payload.Owners {
					if o.Owner == "foreground" {
						pending = o.Pending
						break
					}
				}
				return alertState, pending
			}
			acknowledge := func(alertID string) {
				t.Helper()
				var code int
				var out, errOut string
				if ackMode == "ack" {
					code, out, errOut = invokeStore("ack", alertID, "--by", "operator", "--note", "investigating lag")
					if code != 0 || errOut != "" || out != fmt.Sprintf("alert %s acknowledged\n", alertID) {
						t.Fatalf("ack: code=%d out=%q err=%q", code, out, errOut)
					}
				} else {
					code, out, errOut = invokeStore("settle", alertID, "--by", "operator", "--outcome", "fix-fault", "--item", "item-open", "--cause", "investigating lag", "--evidence", "lagged")
					if code != 0 || errOut != "" || out != fmt.Sprintf("alert %s settled (fix-fault -> item-open)\n", alertID) {
						t.Fatalf("settle: code=%d out=%q err=%q", code, out, errOut)
					}
				}
			}

			// Establish baselines before opening lag.
			if code, out, errOut := invokeStore("eval", "--once", "--rules", rulesDir, "--root", root); code != 0 || errOut != "" {
				t.Fatalf("init eval: code=%d out=%q err=%q", code, out, errOut)
			}

			// Open trunk-proof-lag (>= 95m lag), deliver via wait, mark seen, and acknowledge.
			tip1 := f.landAt(now.Add(-100 * time.Minute))
			if code, out, errOut := invokeStore("eval", "--once", "--rules", rulesDir, "--root", root); code != 0 || errOut != "" {
				t.Fatalf("open eval: code=%d out=%q err=%q", code, out, errOut)
			}
			code, firstOut, firstErr := invokeStore("wait", "--rules", rulesDir, "--root", root, "--timeout", "2s")
			if code != 0 || firstErr != "" || !strings.Contains(firstOut, "trunk-proof-lag") {
				t.Fatalf("initial wait: code=%d out=%q err=%q", code, firstOut, firstErr)
			}
			seq1 := cursor(t, firstOut)
			if code, seenOut, seenErr := invokeStore("seen", "--through", fmt.Sprint(seq1)); code != 0 || seenErr != "" {
				t.Fatalf("seen seq1: code=%d out=%q err=%q", code, seenOut, seenErr)
			}

			var alertID string
			faceRead(t, s, `SELECT alert_id FROM alert_episodes WHERE rule_id='trunk-proof-lag'`, &alertID)
			acknowledge(alertID)
			wantState := "acknowledged"
			if ackMode == "settle" {
				wantState = "open"
			}
			if state, pending := readStatus(); state != wantState || pending != 0 {
				t.Fatalf("after %s: want state=%s pending=0, got state=%q pending=%d", ackMode, wantState, state, pending)
			}

			// Criterion c6: with trunk-proof-lag acknowledged and no red proof since
			// (started, running, ended fault, ended canceled, and a new landing), wait delivers nothing.
			f.appendProgress(now.Add(-2*time.Minute), drain.HeavyProgressFact{TipSHA: tip1, Phase: "started"})
			f.appendProgress(now.Add(-90*time.Second), drain.HeavyProgressFact{TipSHA: tip1, Phase: "progress", CheckID: "unit", Outcome: "running"})
			f.appendProgress(now.Add(-60*time.Second), drain.HeavyProgressFact{TipSHA: tip1, Phase: "ended", Outcome: "fault"})
			f.appendProgress(now.Add(-45*time.Second), drain.HeavyProgressFact{TipSHA: tip1, Phase: "ended", Outcome: "canceled"})
			tip2 := f.landAt(now.Add(-30 * time.Second))

			if code, out, errOut := invokeStore("eval", "--once", "--rules", rulesDir, "--root", root); code != 0 || errOut != "" {
				t.Fatalf("c6 eval: code=%d out=%q err=%q", code, out, errOut)
			}
			if code, out, errOut := invokeStore("wait", "--rules", rulesDir, "--root", root, "--timeout", "30ms"); code != 2 || errOut != "" || strings.Contains(out, "TILLR_WATCH_NOTICE") || strings.Contains(out, "trunk-proof-lag") {
				t.Fatalf("c6 wait expected silence (code 2), got code=%d out=%q err=%q", code, out, errOut)
			}
			if state, pending := readStatus(); state != wantState || pending != 0 {
				t.Fatalf("c6 status: want state=%s pending=0, got state=%q pending=%d", wantState, state, pending)
			}

			// Criterion c5: a red proof reopens trunk-proof-lag and delivers again naming failing_checks and tip_sha.
			f.appendProgress(now.Add(-20*time.Second), drain.HeavyProgressFact{
				TipSHA:        tip2,
				Phase:         "ended",
				Outcome:       "red",
				FailingChecks: "go-tillr-drain",
			})
			if code, out, errOut := invokeStore("eval", "--once", "--rules", rulesDir, "--root", root); code != 0 || errOut != "" {
				t.Fatalf("c5 eval: code=%d out=%q err=%q", code, out, errOut)
			}
			var reopenCause string
			faceRead(t, s, `SELECT json_extract(payload,'$.cause') FROM events WHERE kind='watch.alert.reopened' AND object_id='`+alertID+`' ORDER BY seq DESC LIMIT 1`, &reopenCause)
			if reopenCause != "reopen-when" {
				t.Fatalf("want watch.alert.reopened cause=reopen-when, got %q", reopenCause)
			}
			code, reopenOut, reopenErr := invokeStore("wait", "--rules", rulesDir, "--root", root, "--timeout", "2s")
			if code != 0 || reopenErr != "" {
				t.Fatalf("c5 wait: code=%d out=%q err=%q", code, reopenOut, reopenErr)
			}
			if !strings.Contains(reopenOut, "trunk-proof-lag") || !strings.Contains(reopenOut, "go-tillr-drain") || !strings.Contains(reopenOut, tip2) {
				t.Fatalf("c5 wait output missing expected rule/check/tip_sha:\n%s", reopenOut)
			}
			seq2 := cursor(t, reopenOut)
			if code, seenOut, seenErr := invokeStore("seen", "--through", fmt.Sprint(seq2)); code != 0 || seenErr != "" {
				t.Fatalf("seen seq2: code=%d out=%q err=%q", code, seenOut, seenErr)
			}

			// Deduplication before re-ack: a duplicate eval/wait while open and already seen delivers nothing.
			if code, out, errOut := invokeStore("eval", "--once", "--rules", rulesDir, "--root", root); code != 0 || errOut != "" {
				t.Fatalf("dedup eval: code=%d out=%q err=%q", code, out, errOut)
			}
			if code, out, errOut := invokeStore("wait", "--rules", rulesDir, "--root", root, "--timeout", "30ms"); code != 2 || errOut != "" || strings.Contains(out, "TILLR_WATCH_NOTICE") || strings.Contains(out, "trunk-proof-lag") {
				t.Fatalf("dedup wait expected silence (code 2), got code=%d out=%q err=%q", code, out, errOut)
			}
			if state, pending := readStatus(); state != "open" || pending != 0 {
				t.Fatalf("dedup status: want state=open pending=0, got state=%q pending=%d", state, pending)
			}

			// Re-acknowledge after first red proof: stays silent until a second red proof names a new failing check.
			acknowledge(alertID)
			if code, out, errOut := invokeStore("eval", "--once", "--rules", rulesDir, "--root", root); code != 0 || errOut != "" {
				t.Fatalf("re-ack eval: code=%d out=%q err=%q", code, out, errOut)
			}
			if code, out, errOut := invokeStore("wait", "--rules", rulesDir, "--root", root, "--timeout", "30ms"); code != 2 || errOut != "" || strings.Contains(out, "TILLR_WATCH_NOTICE") || strings.Contains(out, "trunk-proof-lag") {
				t.Fatalf("re-ack wait expected silence (code 2), got code=%d out=%q err=%q", code, out, errOut)
			}
			if state, pending := readStatus(); state != wantState || pending != 0 {
				t.Fatalf("re-ack status: want state=%s pending=0, got state=%q pending=%d", wantState, state, pending)
			}

			f.appendProgress(now.Add(-10*time.Second), drain.HeavyProgressFact{
				TipSHA:        tip2,
				Phase:         "ended",
				Outcome:       "red",
				FailingChecks: "go-tillr-watch",
			})
			if code, out, errOut := invokeStore("eval", "--once", "--rules", rulesDir, "--root", root); code != 0 || errOut != "" {
				t.Fatalf("second red eval: code=%d out=%q err=%q", code, out, errOut)
			}
			code, secondRedOut, secondRedErr := invokeStore("wait", "--rules", rulesDir, "--root", root, "--timeout", "2s")
			if code != 0 || secondRedErr != "" {
				t.Fatalf("second red wait: code=%d out=%q err=%q", code, secondRedOut, secondRedErr)
			}
			if !strings.Contains(secondRedOut, "trunk-proof-lag") || !strings.Contains(secondRedOut, "go-tillr-watch") || !strings.Contains(secondRedOut, tip2) {
				t.Fatalf("second red wait output missing expected rule/check/tip_sha:\n%s", secondRedOut)
			}
		})
	}
}

func TestTrunkProofLagRuleLint(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(t.Context(), []string{"lint", "../.."}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "rule trunk-proof-lag source=shipped:rules/trunk-proof-lag.toml") || errOut.Len() != 0 {
		t.Fatalf("tillr watch lint ../..: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestTrunkProofLagWithoutProvedPointer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env,
		"GIT_AUTHOR_NAME=Watch Fixture", "GIT_AUTHOR_EMAIL=watch@example.invalid",
		"GIT_COMMITTER_NAME=Watch Fixture", "GIT_COMMITTER_EMAIL=watch@example.invalid",
	)
	gitAt := func(at time.Time, input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Stdin = strings.NewReader(input)
		date := fmt.Sprintf("@%d +0000", at.Unix())
		cmd.Env = append(append([]string(nil), env...), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	now := time.Now().UTC()
	gitAt(now.Add(-2*time.Hour), "", "init", "--quiet", "--initial-branch=main")
	tree := gitAt(now.Add(-2*time.Hour), "", "mktree")
	base := gitAt(now.Add(-2*time.Hour), "base", "commit-tree", tree)
	tip := gitAt(now.Add(-time.Hour), "tip", "commit-tree", tree, "-p", base)
	gitAt(now, "", "update-ref", "refs/heads/main", tip)

	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	rulesDir := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rawRule, err := os.ReadFile("rules/trunk-proof-lag.toml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "trunk-proof-lag.toml"), rawRule, 0o644); err != nil {
		t.Fatal(err)
	}

	invoke := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(ctx, append(args, "--store", s.Path(), "--rules", rulesDir, "--root", root), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	if code, out, errOut := invoke("eval", "--once"); code != 0 || errOut != "" {
		t.Fatalf("eval --once without refs/tillr/green: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := invoke("wait", "--timeout", "30ms"); code != 2 || errOut != "" || strings.Contains(out, "TILLR_WATCH_NOTICE") || strings.Contains(out, "trunk-proof-lag") {
		t.Fatalf("wait without refs/tillr/green: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := invoke("status"); code != 0 || errOut != "" || !strings.Contains(out, "rule trunk-proof-lag skipped=0") || !strings.Contains(out, "pending=0") {
		t.Fatalf("status without refs/tillr/green: code=%d out=%q err=%q", code, out, errOut)
	}
}
