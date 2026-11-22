package main

import (
	"context"
	"encoding/json"
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
	rules, err := watch.LoadRules("../../config/tillr/watch")
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
	// catching up tip6 before 30m keeps trunk-proof-lag silent too.
	f.markGreen(tip6)
	if notices := f.pendingAt(t0.Add(35 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence while idle after ended and green caught up, got %+v", notices)
	}
}

func TestTrunkProofLagFiresAtThirtyMinutesUnprovedAge(t *testing.T) {
	f := newTrunkProofFixture(t)
	t0 := f.now
	tip := f.landAt(t0)

	if notices := f.pendingAt(t0.Add(29 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected silence at 29m unproved age, got %+v", notices)
	}

	notices := f.pendingAt(t0.Add(30 * time.Minute))
	if len(notices) != 1 || notices[0].Bundle.RuleID != "trunk-proof-lag" {
		t.Fatalf("want one trunk-proof-lag notice at 30m, got %+v", notices)
	}
	if !strings.Contains(notices[0].Bundle.Guidance.Summary, "30m") {
		t.Fatalf("want 30m in trunk-proof-lag summary, got %q", notices[0].Bundle.Guidance.Summary)
	}

	f.markGreen(tip)
	if after := f.pendingAt(t0.Add(31 * time.Minute)); len(after) != 0 {
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
