package main_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

const rebaseRun = "run-release-rebase"
const patchBranch = "refs/heads/patch/pi"

// Stage the old patch tip, advance upstream, then replay the lane's patch commit.
// The fixture supplies the same target TSV and retained artifacts as the production
// resolver and fork gate, without fetching a remote or building pi.
type rebaseFixture struct {
	fixture
	cache, release, gateDir, target string
}

func newRebaseFixture(t *testing.T) rebaseFixture {
	t.Helper()
	f := newFixture(t)
	upstream, old := f.baseSHA, f.headSHA
	git(t, f.dir, "branch", patchBranch[len("refs/heads/"):], old)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", old)
	git(t, cache, "branch", "patch/pi", old)
	git(t, f.dir, "checkout", "-b", "upstream", upstream)
	write(t, filepath.Join(f.dir, "release.txt"), "upstream release\n")
	git(t, f.dir, "add", "release.txt")
	git(t, f.dir, "commit", "-m", "upstream release")
	release := git(t, f.dir, "rev-parse", "HEAD")
	git(t, f.dir, "tag", "v2", release)
	git(t, f.dir, "checkout", "candidate")
	git(t, f.dir, "rebase", "--onto", release, upstream)
	f.baseSHA, f.headSHA = old, git(t, f.dir, "rev-parse", "HEAD")
	f.treeSHA = git(t, f.dir, "rev-parse", "HEAD^{tree}")
	git(t, f.dir, "checkout", f.ref)
	target := filepath.Join(f.dir, "bin/pi-forks")
	r := rebaseFixture{fixture: f, cache: cache, release: release, gateDir: filepath.Join(f.dir, ".tillr/loop", rebaseRun), target: target}
	r.follow(t, "release", release)
	r.gate(t)
	return r
}

func (r rebaseFixture) follow(t *testing.T, kind, commit string) {
	t.Helper()
	write(t, r.target, "#!/bin/sh\n[ \"$*\" = 'patch-branch target --fetch --fork pi' ] || exit 2\nprintf 'upstream/pi\\t%s\\trefs/tags/v2\\t%s\\n' '"+kind+"' '"+commit+"'\n")
	if err := os.Chmod(r.target, 0755); err != nil {
		t.Fatal(err)
	}
}

const forkBuildDeclaration = `[{"id":"fork-build","kind":"fork-build","label":"pi: pack build"}]`

func (r rebaseFixture) gate(t *testing.T) {
	t.Helper()
	v := r.verdict("pass")
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(forkBuildDeclaration+"[]")))
	v["registry_digest"] = digest
	write(t, filepath.Join(r.gateDir, "gate.verdict.json"), envelope(t, "gate.verdict", "verdict_001", v))
	write(t, filepath.Join(r.gateDir, "gate.change.json"), envelope(t, "gate.change", "change-"+rebaseRun, map[string]string{
		"base_sha": r.baseSHA, "head_sha": r.headSHA, "tree_sha": r.treeSHA,
	}))
	write(t, filepath.Join(r.gateDir, "fork.registry.json"), fmt.Sprintf(`{"digest":%q,"declarations":%s,"in_process":[]}`, digest, forkBuildDeclaration))
}

func (r rebaseFixture) passArgs(t *testing.T, db, item, run string, extra ...string) []string {
	t.Helper()
	return r.fixture.passArgs(t, db, item, run, append([]string{"--verdict", filepath.Join(r.gateDir, "gate.verdict.json")}, extra...)...)
}

func (r rebaseFixture) land(t *testing.T, extra ...string) (int, []byte) {
	t.Helper()
	db := filepath.Join(scratch(t, "rebase-store"), "state.db")
	return run(t, r.passArgs(t, db, "item_042", rebaseRun, extra...)...)
}

func (r rebaseFixture) pin() string { return "refs/tillr/patch-history/pi/" + r.baseSHA }

func (r rebaseFixture) delivered(t *testing.T, cacheTip string) {
	t.Helper()
	for _, check := range []struct{ dir, ref, want string }{
		{r.dir, patchBranch, cacheTip}, {r.dir, r.pin(), r.baseSHA},
		{r.dir, "refs/tillr/landed/" + rebaseRun, r.headSHA},
		{r.cache, patchBranch, cacheTip}, {r.cache, "refs/heads/main", cacheTip},
	} {
		if got := git(t, check.dir, "rev-parse", check.ref); got != check.want {
			t.Errorf("%s %s = %s; want %s", check.dir, check.ref, got, check.want)
		}
	}
	if branch, status := git(t, r.cache, "symbolic-ref", "--short", "HEAD"), git(t, r.cache, "status", "--porcelain"); branch != "main" || status != "" {
		t.Errorf("cache worktree branch=%s status=%q", branch, status)
	}
	if body, err := os.ReadFile(filepath.Join(r.cache, "release.txt")); err != nil || string(body) != "upstream release\n" {
		t.Errorf("cache release.txt: %q, %v", body, err)
	}
}

func (r rebaseFixture) runGate(t *testing.T) string {
	t.Helper()
	gate := filepath.Join(scratch(t, "release-gate"), "tillr-gate")
	if out, err := exec.Command("go", "build", "-C", "../tillr-gate", "-o", gate, ".").CombinedOutput(); err != nil {
		t.Fatalf("build gate: %v\n%s", err, out)
	}
	candidate := filepath.Join(r.gateDir, "candidate")
	git(t, r.dir, "clone", "--quiet", "--no-checkout", r.dir, candidate)
	git(t, candidate, "checkout", "--detach", r.headSHA)
	change := filepath.Join(r.gateDir, "gate.change.json")
	registry := filepath.Join(r.gateDir, "fork.registry.json")
	plan := filepath.Join(r.gateDir, "gate.plan.json")
	verdict := filepath.Join(r.gateDir, "gate.verdict.json")
	decls := `[{"id":"fork-build","kind":"fork-build","label":"pi: pack build","selects":[{"regex":"."}],"watches":[{"paths":true}],"command":["sh","-c","test -f release.txt && test -f candidate.txt"],"timeout_ms":0}]`
	write(t, registry, fmt.Sprintf(`{"digest":"%x","declarations":%s,"in_process":[]}`, sha256.Sum256([]byte(decls+"[]")), decls))
	for _, args := range [][]string{
		{"change", "--item", "item_042", "--run", rebaseRun, "--worktree", candidate, "--base", r.baseSHA, "--head", r.headSHA, "--output", change},
		{"plan", "--registry", registry, "--input", change, "--root", candidate, "--output", plan},
		{"run", "--input", plan, "--worktree", candidate, "--output", verdict, "--logs", filepath.Join(r.gateDir, "gate.logs")},
	} {
		if out, err := exec.Command(gate, args...).CombinedOutput(); err != nil {
			t.Fatalf("gate %s: %v\n%s", args[0], err, out)
		}
	}
	return verdict
}

func TestForkReleaseRebaseLandsExactHead(t *testing.T) {
	r := newRebaseFixture(t)
	verdict := r.runGate(t)
	db := filepath.Join(scratch(t, "rebase-store"), "state.db")
	code, out, stderr := passBoth(t, r.passArgs(t, db, "item_042", rebaseRun, "--base-ref", "patch/pi", "--verdict", verdict))
	if code != 0 {
		t.Fatalf("release rebase exited %d: %s\n%s", code, out, stderr)
	}
	r.delivered(t, r.headSHA)
	r.checkReceipt(t, db)
}

func (r rebaseFixture) checkReceipt(t *testing.T, db string) {
	t.Helper()
	s, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var receipts []land.Landing
	for _, e := range events {
		if e.Kind != land.LandingKind {
			continue
		}
		var got land.Landing
		if err := json.Unmarshal(e.Payload, &got); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, got)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected one durable landing receipt, got %+v", receipts)
	}
	got := receipts[0]
	if !got.Landed() || got.RunID != rebaseRun || got.BaseRef != patchBranch || got.TrunkBefore != r.baseSHA || got.TrunkAfter != r.headSHA || got.LandedTree != r.treeSHA || got.GatedTree != r.treeSHA {
		t.Fatalf("wrong receipt: %+v", got)
	}
}

func (r rebaseFixture) unchanged(t *testing.T, tip, pin string) {
	t.Helper()
	if got := git(t, r.dir, "rev-parse", patchBranch); got != tip {
		t.Errorf("delivery moved to %s; want %s", got, tip)
	}
	if got := git(t, r.dir, "for-each-ref", "--format=%(objectname)", r.pin()); got != pin {
		t.Errorf("history pin changed to %q; want %q", got, pin)
	}
	if got := git(t, r.dir, "for-each-ref", "--format=%(objectname)", "refs/tillr/landed/"+rebaseRun); got != "" {
		t.Errorf("failed publication wrote a marker at %s", got)
	}
}

func TestForkReleaseRebaseRequiresPassingBoundVerdict(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"missing-input", ""}, {"failed", "verdict-failed"}, {"wrong-tree", "verdict-mismatch"},
		{"wrong-verdict-base", "verdict-mismatch"}, {"no-build", "verdict-mismatch"},
		{"failed-build", "verdict-mismatch"}, {"no-run-verdict", "verdict-mismatch"},
		{"wrong-run", "verdict-mismatch"}, {"wrong-fork", "verdict-mismatch"}, {"changed-registry", "verdict-mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRebaseFixture(t)
			v := r.verdict("pass")
			v["registry_digest"] = fmt.Sprintf("%x", sha256.Sum256([]byte(forkBuildDeclaration+"[]")))
			verdictPath := filepath.Join(r.gateDir, "gate.verdict.json")
			changePath := filepath.Join(r.gateDir, "gate.change.json")
			switch tc.name {
			case "failed":
				v = r.verdict("fail")
			case "wrong-tree":
				v["tree_sha"] = git(t, r.dir, "rev-parse", r.baseSHA+"^{tree}")
			case "wrong-verdict-base":
				v["base_sha"] = r.release
			case "no-build":
				delete(v, "checks")
			case "failed-build":
				v["checks"] = []map[string]any{{"check_id": "fork-build", "result": "fail"}}
			case "no-run-verdict":
				if err := os.Remove(verdictPath); err != nil {
					t.Fatal(err)
				}
			case "wrong-run":
				write(t, changePath, envelope(t, "gate.change", "change-other-run", map[string]string{
					"base_sha": r.baseSHA, "head_sha": r.headSHA, "tree_sha": r.treeSHA,
				}))
			case "wrong-fork":
				other := strings.ReplaceAll(forkBuildDeclaration, "pi: pack build", "other: pack build")
				digest := fmt.Sprintf("%x", sha256.Sum256([]byte(other+"[]")))
				v["registry_digest"] = digest
				write(t, filepath.Join(r.gateDir, "fork.registry.json"), fmt.Sprintf(`{"digest":%q,"declarations":%s,"in_process":[]}`, digest, other))
				write(t, verdictPath, envelope(t, "gate.verdict", "verdict_001", v))
			case "changed-registry":
				path := filepath.Join(r.gateDir, "fork.registry.json")
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				write(t, path, strings.ReplaceAll(string(body), `"in_process":[]`, `"in_process":[{}]`))
			}
			arg := input(t, envelope(t, "gate.verdict", "verdict_001", v))
			if tc.name == "missing-input" {
				arg += ".missing"
			}
			db := filepath.Join(scratch(t, "rebase-store"), "state.db")
			code, out, stderr := passBoth(t, r.passArgs(t, db, "item_042", rebaseRun, "--base-ref", patchBranch, "--verdict", arg))
			if tc.want == "" {
				if code != 2 || !strings.Contains(stderr, arg) {
					t.Fatalf("missing verdict: code=%d stderr=%s", code, stderr)
				}
			} else if code != 1 || decodeLanding(t, out).Payload.Reason != tc.want {
				t.Fatalf("expected %s: code=%d out=%s stderr=%s", tc.want, code, out, stderr)
			}
			r.unchanged(t, r.baseSHA, "")
		})
	}
}

func TestForkReleaseRebaseRequiresSelectedNewReleaseAndLinearRange(t *testing.T) {
	for _, name := range []string{"wrong-staged-base", "nonrelease", "same-release", "wrong-release", "merge-above-release"} {
		t.Run(name, func(t *testing.T) {
			r := newRebaseFixture(t)
			delivered := r.baseSHA
			switch name {
			case "wrong-staged-base":
				// The current delivery is an ancestor of new, but the staged old is
				// not. Do not let a different delivery sneak in as a fast-forward.
				git(t, r.dir, "update-ref", patchBranch, r.release, delivered)
				delivered = r.release
			case "nonrelease":
				r.follow(t, "head", r.release) // v2 still tags this exact commit
			case "same-release":
				r.follow(t, "release", r.trunk(t))
			case "wrong-release":
				r.follow(t, "release", r.advance(t))
			case "merge-above-release":
				git(t, r.dir, "checkout", "candidate")
				git(t, r.dir, "merge", "--no-edit", "--no-ff", r.advanceFromRelease(t))
				r.headSHA = git(t, r.dir, "rev-parse", "HEAD")
				r.treeSHA = git(t, r.dir, "rev-parse", "HEAD^{tree}")
				git(t, r.dir, "checkout", r.ref)
				r.gate(t)
			}
			code, out := r.land(t, "--base-ref", patchBranch)
			if code != 1 || decodeLanding(t, out).Payload.Reason != "patch-rewind" {
				t.Fatalf("ineligible rewrite: code=%d out=%s", code, out)
			}
			r.unchanged(t, delivered, "")
		})
	}
}

func (r rebaseFixture) advanceFromRelease(t *testing.T) string {
	t.Helper()
	// An empty side commit is sufficient to make a non-linear range.
	return git(t, r.dir, "commit-tree", r.release+"^{tree}", "-p", r.release, "-m", "side change")
}

func TestForkReleaseRebasePinsAtomically(t *testing.T) {
	t.Run("conflicting", func(t *testing.T) {
		r := newRebaseFixture(t)
		git(t, r.dir, "update-ref", r.pin(), r.release)
		db := filepath.Join(scratch(t, "rebase-store"), "state.db")
		code, _, stderr := passBoth(t, r.passArgs(t, db, "item_042", rebaseRun, "--base-ref", patchBranch))
		if code != 4 || !strings.Contains(stderr, r.pin()) {
			t.Fatalf("conflicting pin: code=%d stderr=%s", code, stderr)
		}
		r.unchanged(t, r.baseSHA, r.release)
		if got := git(t, r.cache, "rev-parse", "main"); got != r.baseSHA {
			t.Fatalf("failed delivery moved cache to %s", got)
		}
	})
	t.Run("identical", func(t *testing.T) {
		r := newRebaseFixture(t)
		git(t, r.dir, "update-ref", r.pin(), r.baseSHA)
		code, out := r.land(t, "--base-ref", patchBranch)
		if code != 0 {
			t.Fatalf("identical pin: code=%d out=%s", code, out)
		}
		r.delivered(t, r.headSHA)
	})
}

func TestForkReleaseRebaseGuardsCacheBeforeDelivery(t *testing.T) {
	for _, name := range []string{"dirty", "staged-only", "detached", "advanced-main", "advanced-patch"} {
		t.Run(name, func(t *testing.T) {
			r := newRebaseFixture(t)
			switch name {
			case "dirty":
				write(t, filepath.Join(r.cache, "candidate.txt"), "saved work\n")
			case "staged-only":
				path := filepath.Join(r.cache, "candidate.txt")
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				write(t, path, "saved in the index\n")
				git(t, r.cache, "add", "candidate.txt")
				write(t, path, string(original))
			case "detached":
				git(t, r.cache, "checkout", "--detach", r.baseSHA)
			case "advanced-main", "advanced-patch":
				commit := git(t, r.cache, "commit-tree", r.baseSHA+"^{tree}", "-p", r.baseSHA, "-m", "unsent cache work")
				ref := "refs/heads/main"
				if name == "advanced-patch" {
					ref = patchBranch
				}
				git(t, r.cache, "update-ref", ref, commit, r.baseSHA)
			}
			before := git(t, r.cache, "rev-parse", "main", patchBranch)
			db := filepath.Join(scratch(t, "rebase-store"), "state.db")
			code, _, stderr := passBoth(t, r.passArgs(t, db, "item_042", rebaseRun, "--base-ref", patchBranch))
			if code != 4 || !strings.Contains(stderr, "fork cache") {
				t.Fatalf("unsafe cache: code=%d stderr=%s", code, stderr)
			}
			r.unchanged(t, r.baseSHA, "")
			if got := git(t, r.cache, "rev-parse", "main", patchBranch); got != before {
				t.Errorf("failed delivery moved cache: %s", got)
			}
		})
	}
}

func (r rebaseFixture) contention(t *testing.T) contentionFixture {
	t.Helper()
	f := newContentionFixtureFor(t, r.fixture)
	body, err := os.ReadFile(filepath.Join(r.gateDir, "gate.verdict.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.req.Verdict, err = land.DecodeVerdict(body)
	if err != nil {
		t.Fatal(err)
	}
	f.req.RunID, f.req.BaseRef = rebaseRun, patchBranch
	return f
}

func TestForkReleaseRebaseRechecksLockedTip(t *testing.T) {
	r := newRebaseFixture(t)
	f := r.contention(t)
	f.req.LockPath = filepath.Join(scratch(t, "rebase-lock"), "land.lock")
	lock, err := land.OpenLock(f.req.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Acquire(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	entered, release := stallGit(t, "rev-list") // old observed, release validation in progress
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered)
	git(t, r.dir, "update-ref", patchBranch, r.release, r.baseSHA)
	write(t, release, "")
	lock.Release()
	answer := finishLanding(t, done)
	if answer.err != nil || answer.landing.Reason != "patch-rewind" {
		t.Fatalf("locked tip change: %+v, %v", answer.landing, answer.err)
	}
	r.unchanged(t, r.release, "")
}

func TestForkReleaseRebaseCASRejectsChangeDuringPublication(t *testing.T) {
	r := newRebaseFixture(t)
	f := r.contention(t)
	land.OwnerHooks.Publishing = func() { git(t, r.dir, "update-ref", patchBranch, r.release, r.baseSHA) }
	t.Cleanup(func() { land.OwnerHooks.Publishing = nil })
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil || !strings.Contains(err.Error(), "delivering release rebase") {
		t.Fatalf("CAS lost race: %v", err)
	}
	r.unchanged(t, r.release, "")
}

func TestForkReleaseRebaseRechecksCacheAfterMove(t *testing.T) {
	r := newRebaseFixture(t)
	f := r.contention(t)
	fetches := 0
	path := filepath.Join(r.cache, "candidate.txt")
	land.OwnerHooks.GitCall = func(dir string, args []string) {
		if dir == r.cache && args[0] == "fetch" {
			fetches++
			if fetches == 2 {
				write(t, path, "saved while delivery moved\n")
			}
		}
	}
	t.Cleanup(func() { land.OwnerHooks.GitCall = nil })
	ctx := context.Background()
	if _, err := land.Land(ctx, f.s, f.trunk, f.req); err == nil || !strings.Contains(err.Error(), "uncommitted work") {
		t.Fatalf("refresh did not stop on new uncommitted work: %v", err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "saved while delivery moved\n" {
		t.Fatalf("cache work was overwritten: %q, %v", body, err)
	}
	if got := git(t, r.dir, "rev-parse", patchBranch); got != r.headSHA {
		t.Fatalf("delivery was not kept at %s: %s", r.headSHA, got)
	}
	events, err := f.s.Events(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == land.LandingKind {
			t.Fatalf("unstable cache produced a receipt: %s", e.Payload)
		}
	}
}

func TestForkReleaseRebaseDoesNotReplaceRacingCacheCommit(t *testing.T) {
	r := newRebaseFixture(t)
	f := r.contention(t)
	var racer string
	land.OwnerHooks.GitCall = func(dir string, args []string) {
		if dir != r.cache || racer != "" || len(args) < 2 || args[0] != "update-ref" || args[1] != "refs/heads/main" {
			return
		}
		old := git(t, r.cache, "rev-parse", "main")
		write(t, filepath.Join(r.cache, "candidate.txt"), "new independent cache work\n")
		git(t, r.cache, "add", "candidate.txt")
		git(t, r.cache, "commit", "-m", "racing cache work")
		racer = git(t, r.cache, "rev-parse", "HEAD")
		git(t, r.cache, "update-ref", "refs/heads/main", racer, old)
	}
	t.Cleanup(func() { land.OwnerHooks.GitCall = nil })
	ctx := context.Background()
	if _, err := land.Land(ctx, f.s, f.trunk, f.req); err == nil {
		t.Fatal("refresh accepted a concurrently advanced cache")
	}
	if got := git(t, r.cache, "rev-parse", "main"); racer == "" || got != racer {
		t.Fatalf("cache main is %s, want racing commit %s", got, racer)
	}
	if got := git(t, r.cache, "status", "--porcelain"); got != "" {
		t.Fatalf("refresh changed the racing checkout: %s", got)
	}
	if got := git(t, r.dir, "rev-parse", patchBranch); got != r.headSHA {
		t.Fatalf("delivery was not kept at %s: %s", r.headSHA, got)
	}
}

func TestForkReceiptRecoveryRecordsActualPreviousTip(t *testing.T) {
	f := newFixture(t)
	old := git(t, f.dir, "commit-tree", f.baseSHA+"^{tree}", "-p", f.baseSHA, "-m", "intervening delivery")
	f.headSHA = git(t, f.dir, "commit-tree", f.treeSHA, "-p", old, "-m", "candidate delivery")
	git(t, f.dir, "update-ref", patchBranch, old)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-B", "main", old)
	c := newContentionFixtureFor(t, f)
	c.req.BaseRef = "patch/pi"
	c.req.Verdict.Checks = []land.VerdictCheck{{CheckID: "fork-build", Result: land.VerdictPass}}
	fetches := 0
	dirtyFile := filepath.Join(cache, "unsaved.txt")
	land.OwnerHooks.GitCall = func(dir string, args []string) {
		if dir == cache && args[0] == "fetch" {
			fetches++
			if fetches == 2 {
				write(t, dirtyFile, "interrupt refresh\n")
			}
		}
	}
	t.Cleanup(func() { land.OwnerHooks.GitCall = nil })
	ctx := context.Background()
	if _, err := land.Land(ctx, c.s, c.trunk, c.req); err == nil || !strings.Contains(err.Error(), "uncommitted work") {
		t.Fatalf("expected interrupted refresh: %v", err)
	}
	if err := os.Remove(dirtyFile); err != nil {
		t.Fatal(err)
	}
	got, err := land.Land(ctx, c.s, c.trunk, c.req)
	if err != nil || !got.Landed() || got.BaseRef != patchBranch || got.TrunkBefore != old || got.TrunkAfter != f.headSHA {
		t.Fatalf("wrong recovered fast-forward receipt: %+v, %v", got, err)
	}
}

func TestForkReleaseRebaseRecoversKilledCacheRefresh(t *testing.T) {
	for _, stage := range []string{"after-detach", "after-main-update"} {
		t.Run(stage, func(t *testing.T) {
			r := newRebaseFixture(t)
			db := filepath.Join(scratch(t, "rebase-store"), "state.db")
			args := r.passArgs(t, db, "item_042", rebaseRun, "--base-ref", patchBranch)
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			shim := scratch(t, "kill-rebase-cache")
			write(t, filepath.Join(shim, "git"), `#!/bin/sh
"$REAL_GIT" "$@" || exit $?
if [ "$1" = -C ] && [ "$2" = "$KILL_CACHE" ]; then
    shift 2
    case "$KILL_STAGE:$*" in
        after-detach:checkout*--detach*|after-main-update:update-ref*refs/heads/main*)
            kill -KILL "$PPID" ;;
    esac
fi
`)
			if err := os.Chmod(filepath.Join(shim, "git"), 0755); err != nil {
				t.Fatal(err)
			}
			path := os.Getenv("PATH")
			t.Setenv("REAL_GIT", realGit)
			t.Setenv("KILL_CACHE", r.cache)
			t.Setenv("KILL_STAGE", stage)
			t.Setenv("PATH", shim+string(os.PathListSeparator)+path)
			if code, _, stderr := passBoth(t, args); code != -1 {
				t.Fatalf("expected killed lander, got code=%d stderr=%s", code, stderr)
			}
			t.Setenv("PATH", path)
			if got := git(t, r.cache, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
				t.Fatalf("cache HEAD is %s, want detached", got)
			}
			wantMain := r.baseSHA
			if stage == "after-main-update" {
				wantMain = r.headSHA
			}
			if got := git(t, r.cache, "rev-parse", "main"); got != wantMain {
				t.Fatalf("cache main is %s, want %s", got, wantMain)
			}
			if got := git(t, r.dir, "rev-parse", patchBranch); got != r.headSHA {
				t.Fatalf("delivery is %s, want %s before recovery", got, r.headSHA)
			}
			git(t, r.cache, "checkout", "--detach", r.headSHA)
			if code, _, stderr := passBoth(t, args); code != 4 || !strings.Contains(stderr, "detached HEAD does not match") {
				t.Fatalf("unrelated detached cache: code=%d stderr=%s", code, stderr)
			}
			git(t, r.cache, "checkout", "--detach", r.baseSHA)
			dirty := filepath.Join(r.cache, "unsaved.txt")
			write(t, dirty, "keep this work\n")
			if code, _, stderr := passBoth(t, args); code != 4 || !strings.Contains(stderr, "uncommitted work") {
				t.Fatalf("dirty interrupted cache: code=%d stderr=%s", code, stderr)
			}
			if err := os.Remove(dirty); err != nil {
				t.Fatal(err)
			}
			code, out, stderr := passBoth(t, args)
			if code != 0 {
				t.Fatalf("receipt recovery: code=%d out=%s stderr=%s", code, out, stderr)
			}
			r.delivered(t, r.headSHA)
			r.checkReceipt(t, db)
			if got := git(t, r.cache, "for-each-ref", "refs/tillr/cache-refresh/"); got != "" {
				t.Fatalf("recovery left a cache journal: %s", got)
			}
		})
	}
}

func TestForkReleaseRebaseRecoversCacheBeforeReceipt(t *testing.T) {
	for _, name := range []string{"repair-old-cache", "cache-already-current", "keep-later-delivery"} {
		t.Run(name, func(t *testing.T) {
			r := newRebaseFixture(t)
			db := filepath.Join(scratch(t, "rebase-store"), "state.db")
			args := r.passArgs(t, db, "item_042", rebaseRun, "--base-ref", "patch/pi")
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			shim := scratch(t, "interrupt-rebase-cache")
			write(t, filepath.Join(shim, "git"), "#!/bin/sh\nfor arg do\n if [ \"$arg\" = --no-overwrite-ignore ]; then echo 'interrupted cache refresh' >&2; exit 1; fi\ndone\nexec '"+realGit+"' \"$@\"\n")
			if err := os.Chmod(filepath.Join(shim, "git"), 0755); err != nil {
				t.Fatal(err)
			}
			path := os.Getenv("PATH")
			t.Setenv("PATH", shim+string(os.PathListSeparator)+path)
			code, _, stderr := passBoth(t, args)
			if code != 4 || !strings.Contains(stderr, "interrupted cache refresh") {
				t.Fatalf("cache refresh did not fail: code=%d stderr=%s", code, stderr)
			}
			if got := git(t, r.dir, "rev-parse", patchBranch); got != r.headSHA {
				t.Fatalf("delivery is %s, want %s before recovery", got, r.headSHA)
			}
			t.Setenv("PATH", path)
			tip := r.headSHA
			if name == "cache-already-current" {
				git(t, r.cache, "reset", "--hard", tip)
				git(t, r.cache, "update-ref", patchBranch, tip, r.baseSHA)
				// Retry must not attempt another checkout when the cache is current.
				t.Setenv("PATH", shim+string(os.PathListSeparator)+path)
			} else if name == "keep-later-delivery" {
				tip = git(t, r.dir, "commit-tree", r.treeSHA, "-p", r.headSHA, "-m", "later delivery")
				git(t, r.dir, "update-ref", patchBranch, tip, r.headSHA)
			}
			// Recovery needs no current release decision.
			if err := os.Remove(r.target); err != nil {
				t.Fatal(err)
			}
			code, out, stderr := passBoth(t, args)
			if code != 0 {
				t.Fatalf("receipt recovery: code=%d out=%s stderr=%s", code, out, stderr)
			}
			got := decodeLanding(t, out).Payload
			if got.Result != "landed" || got.TrunkBefore != r.baseSHA || got.TrunkAfter != r.headSHA {
				t.Fatalf("wrong recovered receipt: %+v", got)
			}
			r.delivered(t, tip)
			r.checkReceipt(t, db)
			code, out, stderr = passBoth(t, args)
			if code != 0 || decodeLanding(t, out).Payload.LandingID != got.LandingID {
				t.Fatalf("second retry did not return the same receipt: code=%d out=%s stderr=%s", code, out, stderr)
			}
		})
	}
}
