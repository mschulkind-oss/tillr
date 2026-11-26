package watch

import (
	"context"
	"errors"
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

func clearGitRepoEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE", "GIT_GRAFT_FILE",
			"GIT_SHALLOW_FILE", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
			"GIT_PREFIX", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func fixtureGit(t *testing.T, root, input string, at time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	date := fmt.Sprintf("@%d +0000", at.Unix())
	cmd.Env = append(clearGitRepoEnv(os.Environ()), "GIT_AUTHOR_NAME=Watch Fixture", "GIT_AUTHOR_EMAIL=watch@example.invalid", "GIT_COMMITTER_NAME=Watch Fixture", "GIT_COMMITTER_EMAIL=watch@example.invalid", "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
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
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	root, leader, follower := forkedRepo(t, now)
	foreign, _, _ := forkedRepo(t, now.Add(time.Hour))
	t.Setenv("GIT_DIR", foreign+"/.git")
	s := watchStore(t)
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
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
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
	if err == nil || !strings.Contains(err.Error(), "requires --root") || got != nil {
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

func TestFixerProvedGitProofAndRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	root := t.TempDir()
	fixtureGit(t, root, "", now, "init")
	fixtureGit(t, root, "", now, "config", "user.name", "Test")
	fixtureGit(t, root, "", now, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "", now, "commit", "--allow-empty", "-m", "base")
	baseOID := fixtureGit(t, root, "", now, "rev-parse", "HEAD")
	fixtureGit(t, root, "", now, "branch", "-M", "main")
	fixtureGit(t, root, "", now.Add(time.Minute), "commit", "--allow-empty", "-m", "red")
	redOID := fixtureGit(t, root, "", now.Add(time.Minute), "rev-parse", "HEAD")
	fixtureGit(t, root, "", now.Add(2*time.Minute), "commit", "--allow-empty", "-m", "green")
	greenOID := fixtureGit(t, root, "", now.Add(2*time.Minute), "rev-parse", "HEAD")

	s := watchStore(t)
	watchSQL(t, s, `CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT,event_seq INTEGER)`)
	watchSQL(t, s, `CREATE TABLE item_criteria(item_id TEXT,criterion_id TEXT,text TEXT)`)

	doc := `[[rule]]
id = "fixer-proved-git"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.id matches "^fixer-main-" and item.state == "in-queue" and item.fixer_proved("refs/tillr/green")'
[rule.guidance]
summary = "Fixer {{subject}} proved"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	rules := watchRules(t, doc)

	// Missing --root is refused.
	noRoot := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now.Add(3 * time.Minute) }}
	if _, err := noRoot.ReadStatus(ctx, func(string) (bool, error) { return false, nil }); err == nil || !strings.Contains(err.Error(), "--root") {
		t.Fatalf("expected --root error, got %v", err)
	}

	e := Evaluator{Store: s, Root: root, Rules: rules, Clock: func() time.Time { return now.Add(3 * time.Minute) }}

	// 1. Missing ref -> quiet.
	if res, err := e.EvaluateRules(ctx); err != nil || len(res) != 1 || res[0].Diagnostic() != "" {
		t.Fatalf("missing ref should be clean: %+v (%v)", res, err)
	}

	// 2. Corrupt proof file -> diagnostic contains green_proof:.
	fixtureGit(t, root, "", now, "update-ref", "refs/tillr/green", baseOID)
	gateDir := filepath.Join(root, ".git", "tillr-gate")
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(gateDir, "green-"+baseOID+".json")
	if err := os.WriteFile(corruptPath, []byte(`{bad json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := e.EvaluateRules(ctx); err != nil || len(res) != 1 || !strings.Contains(res[0].Unreadable, "green_proof:") {
		t.Fatalf("expected green_proof unreadable, got %+v (%v)", res, err)
	}

	// 3. redOID on main, but refs/tillr/green points to baseOID (not a descendant of redOID) -> quiet.
	writeGreenProofJSON(t, root, baseOID, now.Add(2*time.Minute), []string{"check-a"})
	fixerID := "fixer-main-" + redOID[:12]
	watchSQL(t, s, `INSERT INTO items VALUES (?,'in-queue',NULL,NULL,0)`, fixerID)
	watchSQL(t, s, `INSERT INTO item_criteria VALUES (?,'trunk-green','(red: check-a)')`, fixerID)
	if res, err := e.EvaluateRules(ctx); err != nil || len(res) != 1 || res[0].Diagnostic() != "" {
		t.Fatalf("non-descendant green proof should be clean: %+v (%v)", res, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("non-descendant green proof should stay quiet: %+v (%v)", notices, err)
	}

	// 4. Advance refs/tillr/green to greenOID (descendant of redOID) and test errNewGitFact retry
	// when a new fixer-main-* row is inserted between pre-Git read and ApplyPlan.
	fixtureGit(t, root, "", now.Add(2*time.Minute), "update-ref", "refs/tillr/green", greenOID)
	writeGreenProofJSON(t, root, greenOID, now.Add(2*time.Minute), []string{"check-a"})
	fixer2ID := "fixer-main-" + baseOID[:12]
	inserted := false
	e.afterGit = func() {
		if inserted {
			return
		}
		inserted = true
		watchSQL(t, s, `INSERT INTO items VALUES (?,'in-queue',NULL,NULL,0)`, fixer2ID)
		watchSQL(t, s, `INSERT INTO item_criteria VALUES (?,'trunk-green','(red: check-a)')`, fixer2ID)
	}
	res, err := e.EvaluateRules(ctx)
	if err != nil || len(res) != 1 || res[0].Diagnostic() != "" {
		t.Fatalf("EvaluateRules with errNewGitFact retry: %+v (%v)", res, err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 2 {
		t.Fatalf("expected 2 notices after retry, got %d (%+v, %v)", len(notices), notices, err)
	}

	// 5. ReadStatus runs cleanly outside SQLite transactions.
	st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Rules) != 1 || st.Rules[0].Diagnostic() != "" || len(st.Alerts) != 2 {
		t.Fatalf("ReadStatus = %+v (%v)", st, err)
	}
}

func TestWatchGitCacheSymbolicRefAndMissingRef(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	root, leader, _ := forkedRepo(t, now)
	fixtureGit(t, root, "", now, "symbolic-ref", "refs/heads/alias", leaderRef)

	var cache gitCache
	cache.reset(root)

	// 1. Symbolic ref under refs/ falls back to gitCommitWith and resolves target OID.
	execs := 0
	oid, err := cache.commit(ctx, root, "refs/heads/alias", nil, func([]string) { execs++ })
	if err != nil || oid != leader {
		t.Fatalf("symbolic ref commit = %q (%v), want %q", oid, err, leader)
	}
	if execs == 0 {
		t.Fatalf("expected git subprocess fallback for symbolic ref")
	}

	// 2. Genuinely missing ref under refs/ returns errMissingRef with zero git subprocesses.
	execs = 0
	_, err = cache.commit(ctx, root, "refs/tillr/does-not-exist", nil, func([]string) { execs++ })
	if !errors.Is(err, errMissingRef) {
		t.Fatalf("missing ref err = %v, want errMissingRef", err)
	}
	if execs != 0 {
		t.Fatalf("missing ref spawned %d git subprocesses, want 0", execs)
	}

	// 3. Non-NotExist IO error (directory at ref path) falls back rather than returning errMissingRef.
	dirRef := filepath.Join(root, ".git", "refs", "heads", "isdir")
	if err := os.MkdirAll(dirRef, 0o755); err != nil {
		t.Fatal(err)
	}
	execs = 0
	_, err = cache.commit(ctx, root, "refs/heads/isdir", nil, func([]string) { execs++ })
	if err == nil || errors.Is(err, errMissingRef) {
		t.Fatalf("unreadable ref dir err = %v, want non-errMissingRef error", err)
	}
	if execs == 0 {
		t.Fatalf("expected git subprocess fallback for unreadable ref dir")
	}
}

func TestWatchAbsentProvedPointer(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	root, leader, _ := forkedRepo(t, now)
	const greenRef = "refs/tillr/green"
	if oid, err := gitCommit(ctx, root, greenRef); oid != "" || !errors.Is(err, errMissingRef) {
		t.Fatalf("gitCommit absent %s = (%q, %v), want errMissingRef", greenRef, oid, err)
	}
	lagTemplate := `[[rule]]
id = "trunk-proof-lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/leader,refs/tillr/green"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/tillr/green"
threshold_commits = 1
[rule.guidance]
summary = "lag"
check = ["look"]
do = ["fix"]
[rule.delivery]
receiver = "foreground"
`
	s := watchStore(t)
	e := Evaluator{Store: s, Root: root, Rules: watchRules(t, lagTemplate), Clock: func() time.Time { return now }}
	results, err := e.EvaluateRules(ctx)
	if err != nil || len(results) != 1 || results[0].Diagnostic() != "" {
		t.Fatalf("absent %s should evaluate cleanly, got %+v: %v", greenRef, results, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("absent %s should not fire, got %+v: %v", greenRef, notices, err)
	}

	// An expression reading git.ref("refs/tillr/green") also sees "" without error.
	register(t, "watch-test.fact")
	exprText := strings.NewReplacer(
		`"test.watch"`, `"watch-test.fact"`,
		"[rule.when]", "projections=['git_refs:"+greenRef+"']\n[rule.when]",
		`window = "90s"`, "window = '90s'\n"+fmt.Sprintf(`match = 'git.ref("%s") == ""'`, greenRef),
	).Replace(validRule)
	exprStore := watchStore(t)
	exprEval := Evaluator{Store: exprStore, Root: root, Rules: watchRules(t, exprText), Clock: func() time.Time { return now }}
	if _, err := exprEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, exprStore, now, `{"detail":{"item-id":"one"}}`)
	exprResults, err := exprEval.EvaluateRules(ctx)
	if err != nil || len(exprResults) != 1 || exprResults[0].Diagnostic() != "" {
		t.Fatalf("absent %s expression should evaluate cleanly, got %+v: %v", greenRef, exprResults, err)
	}
	if notices, err := exprEval.Pending(ctx, "foreground"); err != nil || len(notices) != 1 {
		t.Fatalf("absent %s expression should match empty string, got %+v: %v", greenRef, notices, err)
	}

	// A proved pointer that exists and cannot be read still reports an error.
	blob := fixtureGit(t, root, "not a commit", now, "hash-object", "-w", "--stdin")
	fixtureGit(t, root, "", now, "update-ref", greenRef, blob)
	results, err = e.EvaluateRules(ctx)
	if err != nil || len(results) != 1 || !strings.Contains(results[0].Unreadable, greenRef) {
		t.Fatalf("blob %s should report unreadable error, got %+v: %v", greenRef, results, err)
	}

	// Corrupt loose ref file also reports an error.
	refPath := filepath.Join(root, ".git", filepath.FromSlash(greenRef))
	if err := os.WriteFile(refPath, []byte("not-a-valid-oid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err = e.EvaluateRules(ctx)
	if err != nil || len(results) != 1 || !strings.Contains(results[0].Unreadable, greenRef) {
		t.Fatalf("corrupt %s should report unreadable error, got %+v: %v", greenRef, results, err)
	}

	// Once pointed at a real commit, normal lag evaluation works.
	if err := os.Remove(refPath); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "", now, "update-ref", greenRef, leader)
	results, err = e.EvaluateRules(ctx)
	if err != nil || len(results) != 1 || results[0].Diagnostic() != "" {
		t.Fatalf("valid %s should evaluate cleanly, got %+v: %v", greenRef, results, err)
	}
}
