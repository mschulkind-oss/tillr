package main_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// These cases pin landing idempotence across the one non-atomic boundary the lander has:
// Git's trunk move and the SQLite receipt are two writes, so a process killed between
// them, or an OS crash that keeps the ref and loses the store's last commits, leaves a
// trunk that carries a landing the store never recorded. Re-landing the same gated
// candidate must then record the existing commit, and must never squash it a second time.

// candidateFrom is another candidate on the fixture's base: a branch that writes body to
// path, so a second item has a change of its own to land.
func (f fixture) candidateFrom(t *testing.T, branch, path, body string) fixture {
	t.Helper()
	git(t, f.dir, "checkout", "-q", "-b", branch, f.baseSHA)
	write(t, filepath.Join(f.dir, path), body)
	git(t, f.dir, "add", path)
	git(t, f.dir, "commit", "-q", "-m", branch)
	next := f
	next.headSHA = git(t, f.dir, "rev-parse", "HEAD")
	next.treeSHA = git(t, f.dir, "rev-parse", "HEAD^{tree}")
	git(t, f.dir, "checkout", "-q", f.ref)
	return next
}

// passArgs is `pass` for this fixture's head against an explicit store, item and run.
func (f fixture) passArgs(t *testing.T, db, item, runID string, extra ...string) []string {
	t.Helper()
	return append([]string{
		"pass", "--store", db, "--repo", f.dir,
		"--item", item, "--run", runID,
		"--base", f.baseSHA, "--head", f.headSHA,
		"--verdict", f.verdictFile(t, "pass"),
		"--now", "2026-11-16T12:00:00Z",
	}, extra...)
}

// passBoth drives `pass` and answers the exit code and both streams.
func passBoth(t *testing.T, args []string) (int, []byte, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(append(withoutIdentity(os.Environ()), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state")), fixtureEnv(t)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out, stderr.String()
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out, stderr.String()
}

// killAfterMove runs `pass` and SIGKILLs it after the trunk's fast-forward has completed
// and before the receipt transaction commits. A git wrapper on PATH runs the real
// `merge --ff-only` — Move's write in a repository whose trunk is checked out — and then
// waits on a barrier file, so the kill lands exactly in the window, with no clock. It
// answers the commit the killed landing left the trunk at.
func killAfterMove(t *testing.T, f fixture, args []string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "git-after-move")
	entered, release := filepath.Join(dir, "entered"), filepath.Join(dir, "release")
	script := "#!/bin/sh\n'" + realGit + "' \"$@\"\nstatus=$?\nfor arg do\n if [ \"$arg\" = \"--ff-only\" ]; then\n  touch '" + entered + "'\n  while [ ! -e '" + release + "' ]; do sleep 0.01; done\n fi\ndone\nexit $status\n"
	write(t, filepath.Join(dir, "git"), script)
	if err := os.Chmod(filepath.Join(dir, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.WriteFile(release, nil, 0o600) })
	cmd := exec.Command(bin, args...)
	env := append(append(withoutIdentity(os.Environ()), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state")), fixtureEnv(t)...)
	cmd.Env = append(env, "PATH="+dir+":"+os.Getenv("PATH"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
	}()
	awaitGit(t, entered)
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	waited = true
	tip := f.trunk(t)
	if tip == f.baseSHA {
		t.Fatal("the killed landing did not reach the trunk move")
	}
	return tip
}

// landingRows answers every landing row's run, result, reason and trunk_after, in
// insertion order.
func landingRows(t *testing.T, db string) [][4]string {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	var rows [][4]string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		q, err := r.Query(`SELECT run_id, result, reason, trunk_after FROM landings ORDER BY rowid`)
		if err != nil {
			return err
		}
		defer q.Close()
		for q.Next() {
			var row [4]string
			if err := q.Scan(&row[0], &row[1], &row[2], &row[3]); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return q.Err()
	}); err != nil {
		t.Fatalf("reading the landings failed: %v", err)
	}
	return rows
}

func commitsSince(t *testing.T, f fixture) []string {
	t.Helper()
	return strings.Fields(git(t, f.dir, "rev-list", "--first-parent", f.baseSHA+".."+f.ref))
}

// TestALandingKilledBetweenMoveAndReceiptIsRecordedNotRepeated is the confirmed defect:
// the trunk took the squash, the receipt never committed, and the drain lands the same
// gated candidate again. The second call records the commit the trunk already carries.
func TestALandingKilledBetweenMoveAndReceiptIsRecordedNotRepeated(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	args := f.passArgs(t, db, "item_042", "run-1")
	landed := killAfterMove(t, f, args)
	if n := rowCount(t, db, "landings"); n != 0 {
		t.Fatalf("the killed landing committed %d receipts", n)
	}

	code, out, stderr := passBoth(t, args)
	if code != 0 {
		t.Fatalf("re-landing exited %d: %s %s", code, out, stderr)
	}
	got := decodeLanding(t, out).Payload
	if got.Result != "landed" || got.TrunkAfter != landed || got.TrunkBefore != f.baseSHA || got.LandedTree != f.treeSHA {
		t.Fatalf("re-landing did not record the existing commit %s: %+v", landed, got)
	}
	if commits := commitsSince(t, f); len(commits) != 1 || commits[0] != landed {
		t.Fatalf("the trunk carries %v since the base, want exactly the one landing %s", commits, landed)
	}
	if rows := landingRows(t, db); len(rows) != 1 || rows[0] != [4]string{"run-1", "landed", "", landed} {
		t.Fatalf("landings are %v, want one landed row at %s", rows, landed)
	}
	if n := eventsOfKind(t, db, "land.landing"); n != 1 {
		t.Fatalf("the store holds %d land.landing events, want 1", n)
	}
}

// TestARecoveredLandingIsRecordedBeneathAnotherItemsLaterLanding: another item landed on
// top of the unrecorded commit before the retry. The retry still finds its own commit in
// the trunk's history, records it, and adds nothing on top.
func TestARecoveredLandingIsRecordedBeneathAnotherItemsLaterLanding(t *testing.T) {
	f := newFixture(t)
	other := f.candidateFrom(t, "other-item", "second.txt", "second\n")
	db := filepath.Join(scratch(t, "store"), "state.db")
	args := f.passArgs(t, db, "item_042", "run-1")
	landed := killAfterMove(t, f, args)

	code, out, stderr := passBoth(t, other.passArgs(t, db, "item_043", "run-2"))
	if code != 0 {
		t.Fatalf("the other item's landing exited %d: %s %s", code, out, stderr)
	}
	otherCommit := decodeLanding(t, out).Payload.TrunkAfter

	code, out, stderr = passBoth(t, args)
	if code != 0 {
		t.Fatalf("re-landing exited %d: %s %s", code, out, stderr)
	}
	got := decodeLanding(t, out).Payload
	if got.Result != "landed" || got.TrunkAfter != landed || got.TrunkBefore != f.baseSHA {
		t.Fatalf("re-landing did not record the buried commit %s: %+v", landed, got)
	}
	if tip := f.trunk(t); tip != otherCommit {
		t.Fatalf("the trunk is at %s, want the other item's landing %s untouched", tip, otherCommit)
	}
	if commits := commitsSince(t, f); len(commits) != 2 {
		t.Fatalf("the trunk carries %v since the base, want the two landings", commits)
	}
	want := [][4]string{{"run-2", "landed", "", otherCommit}, {"run-1", "landed", "", landed}}
	if rows := landingRows(t, db); len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("landings are %v, want %v", rows, want)
	}
}

// TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding: the trunk holds the
// candidate's exact tree, but in a commit this landing did not write — the owner rewound
// the killed landing and committed the same change by hand. Tree equality is not the
// landing: the retry records no landing at either commit, and adds no empty commit.
func TestAnUnrelatedCommitWithTheSameTreeIsNotTakenForTheLanding(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	args := f.passArgs(t, db, "item_042", "run-1")
	killed := killAfterMove(t, f, args)
	git(t, f.dir, "reset", "-q", "--hard", f.baseSHA)
	write(t, filepath.Join(f.dir, "candidate.txt"), "candidate\n")
	git(t, f.dir, "add", "candidate.txt")
	git(t, f.dir, "commit", "-q", "-m", "owner applies the change by hand")
	owner := f.trunk(t)
	if tree := git(t, f.dir, "rev-parse", owner+"^{tree}"); tree != git(t, f.dir, "rev-parse", killed+"^{tree}") {
		t.Fatalf("fixture: the owner's commit does not share the killed landing's tree")
	}

	code, out, stderr := passBoth(t, args)
	if code != 1 {
		t.Fatalf("re-landing over an unrelated same-tree commit exited %d, want a refusal: %s %s", code, out, stderr)
	}
	got := decodeLanding(t, out).Payload
	if got.Result != "refused" || got.Reason != "already-on-trunk" || got.TrunkBefore != owner || got.TrunkAfter != owner {
		t.Fatalf("re-landing is %+v, want already-on-trunk at the owner's commit with no move", got)
	}
	if tip := f.trunk(t); tip != owner {
		t.Fatalf("the trunk moved to %s", tip)
	}
}

// TestAnEmptySquashIsRefusedAsAlreadyOnTrunk: a second candidate whose change the trunk
// already carries squashes to the tip's own tree. That is a finding, not a commit: the
// landing is refused as already-on-trunk, nothing moves, and the head is pinned.
func TestAnEmptySquashIsRefusedAsAlreadyOnTrunk(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out, stderr := passBoth(t, f.passArgs(t, db, "item_042", "run-1"))
	if code != 0 {
		t.Fatalf("the first landing exited %d: %s %s", code, out, stderr)
	}
	first := f.trunk(t)
	twin := f.candidateFrom(t, "twin", "candidate.txt", "candidate\n")
	if twin.headSHA == f.headSHA {
		t.Fatal("fixture: the twin candidate is the same commit")
	}

	code, out, stderr = passBoth(t, twin.passArgs(t, db, "item_044", "run-2"))
	if code != 1 {
		t.Fatalf("an empty squash exited %d, want a refusal: %s %s", code, out, stderr)
	}
	got := decodeLanding(t, out).Payload
	if got.Result != "refused" || got.Reason != "already-on-trunk" || got.TrunkAfter != first || got.LandedTree != "" {
		t.Fatalf("the empty squash is %+v, want already-on-trunk at %s", got, first)
	}
	if tip := f.trunk(t); tip != first {
		t.Fatalf("the trunk moved to %s for an empty squash", tip)
	}
	if pin := git(t, f.dir, "rev-parse", "refs/tillr/runs/run-2"); pin != twin.headSHA {
		t.Fatalf("the refused head is pinned at %s, want %s", pin, twin.headSHA)
	}
}

// TestTheSameRunLandingADifferentHeadIsNotTakenForItsEarlierLanding: an unrecorded landing
// of run-1 is on the trunk, and run-1 is then asked to land a different head. The earlier
// commit is not the squash of this head, so this is a new landing, not a recovery.
func TestTheSameRunLandingADifferentHeadIsNotTakenForItsEarlierLanding(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	earlier := killAfterMove(t, f, f.passArgs(t, db, "item_042", "run-1"))
	git(t, f.dir, "checkout", "-q", "-b", "candidate-2", f.headSHA)
	write(t, filepath.Join(f.dir, "more.txt"), "more\n")
	git(t, f.dir, "add", "more.txt")
	git(t, f.dir, "commit", "-q", "-m", "more")
	later := f
	later.headSHA = git(t, f.dir, "rev-parse", "HEAD")
	later.treeSHA = git(t, f.dir, "rev-parse", "HEAD^{tree}")
	git(t, f.dir, "checkout", "-q", f.ref)

	code, out, stderr := passBoth(t, later.passArgs(t, db, "item_042", "run-1"))
	if code != 0 {
		t.Fatalf("the different head exited %d: %s %s", code, out, stderr)
	}
	got := decodeLanding(t, out).Payload
	if got.Result != "landed" || got.TrunkBefore != earlier || got.TrunkAfter == earlier {
		t.Fatalf("the different head was taken for the earlier landing: %+v", got)
	}
	if blob := git(t, f.dir, "rev-parse", f.ref+":more.txt"); blob == "" {
		t.Fatal("the different head's change did not reach the trunk")
	}
}

// TestAStoredTrunkTipGitNoLongerHasIsAnActionableExternalFailure is the reverse loss: the
// store committed a landing and its trunk observation, and Git lost the commit (an OS crash
// before Git flushed it). The next landing must say what was lost and move nothing.
func TestAStoredTrunkTipGitNoLongerHasIsAnActionableExternalFailure(t *testing.T) {
	f := newFixture(t)
	other := f.candidateFrom(t, "other-item", "second.txt", "second\n")
	db := filepath.Join(scratch(t, "store"), "state.db")
	st, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TABLE trunk_history(id INTEGER PRIMARY KEY,observed_tip TEXT NOT NULL,lost_tip TEXT NOT NULL)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	code, out, stderr := passBoth(t, f.passArgs(t, db, "item_042", "run-1", "--observed-trunk", f.baseSHA))
	if code != 0 {
		t.Fatalf("the first landing exited %d: %s %s", code, out, stderr)
	}
	lost := f.trunk(t)

	// Git loses the landing: the trunk ref and every object only it reached.
	git(t, f.dir, "reset", "-q", "--hard", f.baseSHA)
	for _, ref := range strings.Fields(git(t, f.dir, "for-each-ref", "--format=%(refname)", "refs/tillr/")) {
		git(t, f.dir, "update-ref", "-d", ref)
	}
	git(t, f.dir, "reflog", "expire", "--expire=now", "--all")
	git(t, f.dir, "gc", "-q", "--prune=now")
	if err := exec.Command("git", "-C", f.dir, "cat-file", "-e", lost+"^{commit}").Run(); err == nil {
		t.Fatalf("fixture: %s survived the simulated loss", lost)
	}

	code, out, stderr = passBoth(t, other.passArgs(t, db, "item_043", "run-2", "--observed-trunk", f.baseSHA))
	if code != 4 {
		t.Fatalf("a lost stored landing exited %d, want 4: %s %s", code, out, stderr)
	}
	for _, want := range []string{lost, "no longer has", "nothing was moved"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("the failure does not say %q: %s", want, stderr)
		}
	}
	if tip := f.trunk(t); tip != f.baseSHA {
		t.Fatalf("the trunk moved to %s", tip)
	}
	if n := rowCount(t, db, "landings"); n != 1 {
		t.Fatalf("the store holds %d landings, want the one recorded before the loss", n)
	}
}

// TestARetryOfARecordedLandingAnswersTheStoredRow: the lander's receipt committed, and the
// drain was killed (or lost its later writes) before it recorded the item as landed. It
// lands the same gated candidate again under the same run. That is the landing already
// recorded: it is answered as stored, exit 0, with no second row, event or commit.
func TestARetryOfARecordedLandingAnswersTheStoredRow(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	args := f.passArgs(t, db, "item_042", "run-1")
	code, out, stderr := passBoth(t, args)
	if code != 0 {
		t.Fatalf("the first landing exited %d: %s %s", code, out, stderr)
	}
	first := decodeLanding(t, out).Payload

	code, out, stderr = passBoth(t, args)
	if code != 0 {
		t.Fatalf("retrying a recorded landing exited %d: %s %s", code, out, stderr)
	}
	if got := decodeLanding(t, out).Payload; !reflect.DeepEqual(got, first) || got.Result != "landed" {
		t.Fatalf("the retry answered %+v, want the stored landing %+v", got, first)
	}
	if rows := landingRows(t, db); len(rows) != 1 {
		t.Fatalf("landings are %v, want the one recorded landing", rows)
	}
	if n := eventsOfKind(t, db, "land.landing"); n != 1 {
		t.Fatalf("the store holds %d land.landing events, want 1", n)
	}
	if commits := commitsSince(t, f); len(commits) != 1 || commits[0] != first.TrunkAfter {
		t.Fatalf("the trunk carries %v since the base, want only %s", commits, first.TrunkAfter)
	}
}

// TestATwinHeadUnderARecordedRunIsNotRecordedAsASecondLanding: run-1's landing is recorded,
// and run-1 is then asked to land a different commit with the same change. Its squash onto
// the marker's parent reproduces the marker's tree, so only the receipt that already names
// the marker's commit keeps it from being recorded as a second landing of that commit.
func TestATwinHeadUnderARecordedRunIsNotRecordedAsASecondLanding(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out, stderr := passBoth(t, f.passArgs(t, db, "item_042", "run-1"))
	if code != 0 {
		t.Fatalf("the first landing exited %d: %s %s", code, out, stderr)
	}
	landed := decodeLanding(t, out).Payload.TrunkAfter
	twin := f.candidateFrom(t, "twin", "candidate.txt", "candidate\n")

	code, out, stderr = passBoth(t, twin.passArgs(t, db, "item_042", "run-1"))
	if code != 1 {
		t.Fatalf("a twin head under a recorded run exited %d, want a refusal: %s %s", code, out, stderr)
	}
	if got := decodeLanding(t, out).Payload; got.Reason != "already-on-trunk" {
		t.Fatalf("the twin head is %+v, want already-on-trunk", got)
	}
	want := [][4]string{{"run-1", "landed", "", landed}, {"run-1", "refused", "already-on-trunk", landed}}
	if rows := landingRows(t, db); len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("landings are %v, want %v", rows, want)
	}
}

// TestARunIDThatIsNotARefComponentIsRefusedBeforeAnyGitRead: the run id is spliced into the
// marker's ref name, so an id carrying revision syntax would read a different object. It is
// refused as a malformed request, before anything is read or moved.
func TestARunIDThatIsNotARefComponentIsRefusedBeforeAnyGitRead(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out, stderr := passBoth(t, f.passArgs(t, db, "item_042", "x~1"))
	if code != 2 || !strings.Contains(stderr, "x~1") {
		t.Fatalf("a run id with revision syntax exited %d, want 2 naming it: %s %s", code, out, stderr)
	}
	if tip := f.trunk(t); tip != f.baseSHA {
		t.Fatalf("the trunk moved to %s", tip)
	}
}

// TestAGitFailureReadingAStoredTipIsNotReportedAsALoss: only a missing object is a lost
// landing. A Git command that fails for another reason is reported as that failure, so an
// operator is not sent to restore a commit that is still there.
func TestAGitFailureReadingAStoredTipIsNotReportedAsALoss(t *testing.T) {
	f := newFixture(t)
	stored := f.advance(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	st, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE trunk_history(id INTEGER PRIMARY KEY,observed_tip TEXT NOT NULL,lost_tip TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_history VALUES(1,?,'')`, stored)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "git-cat-file-fails")
	write(t, filepath.Join(dir, "git"), "#!/bin/sh\ncase \" $* \" in *\" cat-file -e "+stored+"\"*) echo 'fatal: simulated object store failure' >&2; exit 128;; esac\nexec '"+realGit+"' \"$@\"\n")
	if err := os.Chmod(filepath.Join(dir, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	code, out, stderr := passBoth(t, f.passArgs(t, db, "item_042", "run-1", "--observed-trunk", f.baseSHA))
	if code != 4 || !strings.Contains(stderr, stored) || !strings.Contains(stderr, "simulated object store failure") || strings.Contains(stderr, "lost") {
		t.Fatalf("a failed read of the stored tip exited %d: %s %s", code, out, stderr)
	}
	if tip := f.trunk(t); tip != stored {
		t.Fatalf("the trunk moved to %s", tip)
	}
}
