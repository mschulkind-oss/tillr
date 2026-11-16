package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func commitMessage(t *testing.T, dir, sha string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "cat-file", "commit", sha)
	cmd.Env = gitEnv()
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	_, message, ok := strings.Cut(string(raw), "\n\n")
	if !ok {
		t.Fatal("commit has no message boundary")
	}
	return message
}

func TestSeveralWorkerCommitsLandAsOneAndRemainReadable(t *testing.T) {
	for _, retired := range []bool{false, true} {
		name := "workers-only"
		if retired {
			name = "retired-block"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			git(t, f.dir, "checkout", "candidate")
			for i, subject := range []string{"feat: first run continues", "fix: last run finishes\n\nWorker body with trailing spaces  \n\n"} {
				write(t, filepath.Join(f.dir, "candidate.txt"), subject)
				author := fixtureName
				email := fixtureEmail
				if i == 0 {
					author, email = "other worker", "other@example.invalid"
				}
				t.Setenv("GIT_AUTHOR_DATE", "2026-11-01T12:00:00-04:00")
				gitAs(t, f.dir, author, email, "commit", "--cleanup=verbatim", "-am", subject)
			}
			worker := git(t, f.dir, "rev-parse", "HEAD")
			sourceDate := git(t, f.dir, "show", "-s", "--format=%aI", worker)
			t.Setenv("GIT_AUTHOR_DATE", "2026-11-02T12:00:00-04:00")
			if retired {
				write(t, filepath.Join(f.dir, "retired.txt"), "document block removed\n")
				git(t, f.dir, "add", "retired.txt")
				git(t, f.dir, "commit", "-m", "chore: retire landed document block")
			}
			f.headSHA = git(t, f.dir, "rev-parse", "HEAD")
			f.treeSHA = git(t, f.dir, "rev-parse", "HEAD^{tree}")
			expectedList := git(t, f.dir, "log", "--reverse", "--format=%H %s", f.baseSHA+".."+f.headSHA)
			git(t, f.dir, "checkout", f.ref)
			before := f.advance(t)
			expectedTree := strings.Split(git(t, f.dir, "merge-tree", "--write-tree", "--merge-base="+f.baseSHA, before, f.headSHA), "\n")[0]
			dir := filepath.Join(scratch(t, "run"), "run-last")
			extra := []string{"--run-dir", dir}
			if retired {
				extra = append(extra, "--message-from", worker)
			}
			_, code, out := f.land(t, "run-last", "pass", extra...)
			if code != 0 {
				t.Fatalf("pass exited %d: %s", code, out)
			}
			landed := f.trunk(t)
			if count := git(t, f.dir, "rev-list", "--count", before+".."+landed); count != "1" {
				t.Fatalf("trunk gained %s commits", count)
			}
			if tree := git(t, f.dir, "rev-parse", landed+"^{tree}"); tree != expectedTree {
				t.Fatalf("landed tree %s != merged tree %s", tree, expectedTree)
			}
			wantIdentity := fixtureName + " <" + fixtureEmail + ">"
			if got := git(t, f.dir, "show", "-s", "--format=%an <%ae>%n%cn <%ce>", landed); got != wantIdentity+"\n"+wantIdentity {
				t.Fatalf("landing identity: %s", got)
			}
			if got, want := commitMessage(t, f.dir, landed), commitMessage(t, f.dir, worker); got != want {
				t.Fatalf("message changed: %q != %q", got, want)
			}
			if date := git(t, f.dir, "show", "-s", "--format=%aI", landed); date != sourceDate {
				t.Fatalf("author date %s != %s", date, sourceDate)
			}
			if pinned := git(t, f.dir, "rev-parse", "refs/tillr/runs/run-last"); pinned != f.headSHA {
				t.Fatalf("pinned %s != head %s", pinned, f.headSHA)
			}
			listed, err := os.ReadFile(filepath.Join(dir, "commits.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(listed) != expectedList+"\n" {
				t.Fatalf("commit list: %q != %q", listed, expectedList)
			}
			git(t, f.dir, "checkout", "candidate")
			git(t, f.dir, "reset", "--hard", landed)
			git(t, f.dir, "reflog", "expire", "--expire=now", "--all")
			git(t, f.dir, "gc", "--prune=now")
			for _, line := range strings.Split(strings.TrimSpace(string(listed)), "\n") {
				sha, _, _ := strings.Cut(line, " ")
				git(t, f.dir, "cat-file", "-e", sha+"^{commit}")
			}
		})
	}
}

func TestDefaultCommitListKeepsTheTrunkWorktreeClean(t *testing.T) {
	f := newFixture(t)
	_, code, out := f.land(t, "run-single", "pass")
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	common := git(t, f.dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	list, err := os.ReadFile(filepath.Join(common, "tillr", "runs", "run-single", "commits.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(list) != f.headSHA+" candidate\n" {
		t.Fatalf("commit list: %q", list)
	}
	if status := git(t, f.dir, "status", "--porcelain"); status != "" {
		t.Fatalf("dirty trunk: %s", status)
	}
	if got, want := commitMessage(t, f.dir, f.ref), commitMessage(t, f.dir, f.headSHA); got != want {
		t.Fatalf("single message %q != %q", got, want)
	}
	if tree := git(t, f.dir, "rev-parse", f.ref+"^{tree}"); tree != f.treeSHA {
		t.Fatalf("single tree %s != %s", tree, f.treeSHA)
	}
}

func TestUnwritableCommitListDoesNotMoveTrunk(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(scratch(t, "run-file"), "not-a-directory")
	write(t, dir, "file\n")
	_, code, out := f.land(t, "run-unwritable", "pass", "--run-dir", dir)
	if code != 4 {
		t.Fatalf("pass exited %d, want external error: %s", code, out)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("unwritable commit list moved trunk")
	}
}

func TestMessageSourceMustBeACommitInTheCandidateRange(t *testing.T) {
	for _, invalid := range []string{"base", "outside", "blob", "missing"} {
		t.Run(invalid, func(t *testing.T) {
			f := newFixture(t)
			outside := f.advance(t)
			source := map[string]string{"base": f.baseSHA, "outside": outside, "blob": git(t, f.dir, "rev-parse", f.headSHA+":candidate.txt"), "missing": "deadbeef"}[invalid]
			before := f.trunk(t)
			_, code, out := f.land(t, "invalid-run", "pass", "--message-from", source)
			if code != 2 {
				t.Fatalf("pass exited %d, want usage error: %s", code, out)
			}
			if f.trunk(t) != before {
				t.Fatal("invalid source moved trunk")
			}
			if refs := git(t, f.dir, "for-each-ref", "--format=%(refname)", "refs/tillr/runs/"); refs != "" {
				t.Fatalf("invalid source pinned a ref: %s", refs)
			}
		})
	}
}
