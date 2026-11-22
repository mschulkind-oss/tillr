package watch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

const leaderRef = "refs/heads/leader"
const followerRef = "refs/heads/follower"

func fixtureGit(t *testing.T, root, input string, at time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	date := fmt.Sprintf("@%d +0000", at.Unix())
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Watch Fixture", "GIT_AUTHOR_EMAIL=watch@example.invalid", "GIT_COMMITTER_NAME=Watch Fixture", "GIT_COMMITTER_EMAIL=watch@example.invalid", "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func forkedRepo(t *testing.T, now time.Time) (root, leader, follower string) {
	t.Helper()
	root = t.TempDir()
	git := func(input string, at time.Time, args ...string) string {
		t.Helper()
		return fixtureGit(t, root, input, at, args...)
	}
	git("", now, "init", "--quiet")
	tree := git("", now, "mktree")
	base := git("base", now.Add(-time.Hour), "commit-tree", tree)
	// The least committer time isn't the oldest in history order.
	parent := git("leader parent", now.Add(-time.Minute), "commit-tree", tree, "-p", base)
	leader = git("leader", now.Add(-16*time.Minute), "commit-tree", tree, "-p", parent)
	follower = git("diverged follower", now.Add(-2*time.Hour), "commit-tree", tree, "-p", base)
	git("", now, "update-ref", leaderRef, leader)
	git("", now, "update-ref", followerRef, follower)
	return
}

func TestWatchGitRefOperandAndRootIsolation(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	root, leader, follower := forkedRepo(t, now)
	foreign, _, _ := forkedRepo(t, now.Add(time.Hour))
	t.Setenv("GIT_DIR", foreign+"/.git")
	s := watchStore(t)
	watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
	text := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, "[rule.when]", "projections=['git_refs:"+leaderRef+"']\n[rule.when]", `window = "90s"`, "window = '90s'\n"+fmt.Sprintf(`match = 'git.ref("%s") == "%s"'`, leaderRef, leader)).Replace(validRule)
	child := filepath.Join(root, "ordinary-child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	e := Evaluator{Store: s, Root: child, Rules: watchRules(t, text), Clock: func() time.Time { return now }}
	got, err := e.EvaluateRules(ctx)
	if err != nil || len(got) != 1 || got[0].Unreadable == "" {
		t.Fatalf("non-repository root: %+v: %v", got, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("non-repository root opened an episode: %+v: %v", notices, err)
	}
	e.Root = root
	e.afterGit = func() {
		if _, err := gitOutput(ctx, root, "update-ref", leaderRef, follower); err != nil {
			t.Fatal(err)
		}
	}
	got, err = e.EvaluateRules(ctx)
	e.afterGit = nil
	if err != nil || len(got) != 1 || got[0].Diagnostic() != "" {
		t.Fatalf("results %+v: %v", got, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 {
		t.Fatalf("root isolation: %+v: %v", notices, err)
	}
	e.Root = ""
	got, err = e.EvaluateRules(ctx)
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Unreadable, "root") {
		t.Fatalf("missing root: %+v: %v", got, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 {
		t.Fatalf("unreadable changed episode: %+v: %v", notices, err)
	}
	e.Root = root
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("next pass must see ref movement: %+v: %v", notices, err)
	}
}

func TestWatchGitRefSupportsLinkedAndBareRoots(t *testing.T) {
	now := time.Unix(1700000000, 0)
	root, leader, _ := forkedRepo(t, now)
	parent := t.TempDir()
	linked := filepath.Join(parent, "linked")
	bare := filepath.Join(parent, "bare")
	fixtureGit(t, root, "", now, "worktree", "add", "--detach", linked, leader)
	fixtureGit(t, root, "", now, "clone", "--bare", ".", bare)
	for _, repo := range []string{linked, bare} {
		if oid, err := gitCommit(context.Background(), repo, leaderRef); err != nil || oid != leader {
			t.Errorf("git ref from %s: %q, want %q: %v", repo, oid, leader, err)
		}
	}
}

func TestWatchLagEvaluation(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	root, leader, follower := forkedRepo(t, now)
	template := `[[rule]]
id = "lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
THRESHOLDS
[rule.guidance]
summary = "{{subject}} {{count}} {{elapsed}} {{leader}} {{leader_short}} {{follower}} {{follower_short}}"
check = ["look"]
do = ["consider"]
[rule.delivery]
receiver = "foreground"
`
	for _, tc := range []struct {
		name, thresholds string
		offset           time.Duration
		fires            bool
	}{
		{"commits-inclusive", "threshold_commits=2", 0, true},
		{"commits-under", "threshold_commits=3", 0, false},
		{"future-committer-time", "threshold_duration='1s'", -17 * time.Minute, false},
		{"duration-inclusive", "threshold_duration='16m'", 0, true},
		{"duration-under", "threshold_duration='16m'", -time.Millisecond, false},
		{"any-default", "threshold_commits=3\nthreshold_duration='16m'", 0, true},
		{"any-explicit", "threshold_commits=2\nthreshold_duration='20m'\nmode='any'", 0, true},
		{"all-commits-under", "threshold_commits=3\nthreshold_duration='16m'\nmode='all'", 0, false},
		{"all-time-under", "threshold_commits=2\nthreshold_duration='17m'\nmode='all'", 0, false},
		{"all-met", "threshold_commits=2\nthreshold_duration='16m'\nmode='all'", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := watchStore(t)
			e := Evaluator{Store: s, Root: root, Rules: watchRules(t, strings.Replace(template, "THRESHOLDS", tc.thresholds, 1)), Clock: func() time.Time { return now.Add(tc.offset) }}
			result, err := e.EvaluateRules(ctx)
			if err != nil || len(result) != 1 || result[0].Diagnostic() != "" {
				t.Fatalf("result: %+v: %v", result, err)
			}
			notices, err := e.Pending(ctx, "foreground")
			if err != nil || (len(notices) == 1) != tc.fires {
				t.Fatalf("notices: %v: %v", notices, err)
			}
			if !tc.fires {
				return
			}
			b := notices[0].Bundle
			want := fmt.Sprintf("global 2 16m %s %s %s %s", leaderRef, leader[:12], followerRef, follower[:12])
			if b.Events == nil || len(b.Events) != 0 || b.Guidance.Summary != want {
				t.Fatalf("bundle: %+v, want %s", b, want)
			}
			var seq int64
			if err := s.ReadTx(ctx, func(rd *store.Reader) error {
				return rd.QueryRow(`SELECT opened_seq FROM alert_episodes`).Scan(&seq)
			}); err != nil || seq != 0 {
				t.Fatalf("opened_seq on empty store = %d: %v", seq, err)
			}
		})
	}
	s := watchStore(t)
	head := watchFact(t, s, now, `{}`)
	e := Evaluator{Store: s, Root: root, Rules: watchRules(t, strings.Replace(template, "THRESHOLDS", "threshold_commits=1", 1)), Clock: func() time.Time { return now }}
	var passes int
	var appendedDuringPass bool
	e.afterGit = func() {
		passes++
		if passes > 1 {
			t.Fatal("unexpected pass restart")
		}
		if !appendedDuringPass {
			// A concurrent append after the Git read must not move the
			// opening or last-observed sequence of the new lag episode.
			watchFact(t, s, now, `{}`)
			appendedDuringPass = true
		}
	}
	eval := func() {
		t.Helper()
		passes = 0
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
	}
	episode := func(wantState string, wantResolved int64) {
		t.Helper()
		var state string
		var opened, last, count, resolved int64
		err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT state,opened_seq,last_observed_seq,observation_count,coalesce(resolved_seq,0) FROM alert_episodes`).Scan(&state, &opened, &last, &count, &resolved)
		})
		if err != nil || state != wantState || opened != head || last != head || count != 1 || resolved != wantResolved {
			t.Fatalf("episode: %s %d %d %d %d (%v)", state, opened, last, count, resolved, err)
		}
	}
	eval()
	watchFact(t, s, now, `{}`)
	eval()
	episode("open", 0)
	fixtureGit(t, root, "", now, "update-ref", "-d", followerRef)
	passes = 0
	if result, err := e.EvaluateRules(ctx); err != nil || len(result) != 1 || !strings.Contains(result[0].Unreadable, followerRef) {
		t.Fatalf("result %+v: %v", result, err)
	}
	episode("open", 0)
	fixtureGit(t, root, "", now, "update-ref", followerRef, leader)
	startHead, err := s.EventHeadSeq()
	if err != nil {
		t.Fatal(err)
	}
	e.afterGit = func() { watchFact(t, s, now, `{}`) }
	eval()
	episode("resolved", startHead)
	assertWatchResolutionEvent(t, s, startHead)
}
