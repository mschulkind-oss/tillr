package main_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

// stallGit blocks a named subcommand, not a wall-clock guess about how far Land got.
func stallGit(t *testing.T, subcommand string) (entered, release string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "git-barrier")
	entered, release = filepath.Join(dir, "entered"), filepath.Join(dir, "release")
	script := "#!/bin/sh\nfor arg do\n if [ \"$arg\" = \"" + subcommand + "\" ]; then\n  touch '" + entered + "'\n  while [ ! -e '" + release + "' ]; do sleep 0.01; done\n fi\ndone\nexec '" + realGit + "' \"$@\"\n"
	write(t, filepath.Join(dir, "git"), script)
	if err := os.Chmod(filepath.Join(dir, "git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Cleanup(func() { os.WriteFile(release, nil, 0600) })
	return
}

// hangGuard is how long a test waits for an event that is certain to happen before it
// concludes the code deadlocked. It is a deadlock detector and never an expectation: no
// passing run reaches it however loaded the machine is, and no test may assert that
// something happened "quickly".
const hangGuard = 5 * time.Minute

func awaitGit(t *testing.T, entered string) {
	t.Helper()
	// No wall-clock bound: the barrier file is the readiness signal, and a true hang is
	// stopped by the test binary's own timeout.
	for {
		if _, err := os.Stat(entered); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type contentionFixture struct {
	f           fixture
	s, writer   *store.Store
	db, itemBin string
	req         land.LandingRequest
	trunk       *land.Trunk
}

func newContentionFixture(t *testing.T) contentionFixture {
	t.Helper()
	return newContentionFixtureFor(t, newFixture(t))
}

func newContentionFixtureFor(t *testing.T, f fixture) contentionFixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", deployedConfig(t, fixtureName, fixtureEmail))
	db := filepath.Join(scratch(t, "contention-store"), "state.db")
	itemBin := filepath.Join(scratch(t, "item-bin"), "tillr-item")
	if out, err := exec.Command("go", "build", "-C", "../tillr-item", "-o", itemBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build item: %v %s", err, out)
	}
	rec := input(t, envelope(t, "item.record", "item-record", map[string]any{"item_id": "item_042", "title": "an item", "state": "in-queue", "criteria": []any{}, "blocked_by": []any{}}))
	if out, err := exec.Command(itemBin, "apply", "--store", db, "--input", rec, "--now", "2026-11-16T11:00:00Z").CombinedOutput(); err != nil {
		t.Fatalf("apply: %v %s", err, out)
	}
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, refreshed_at INTEGER);`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	writer, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	trunk, err := land.OpenTrunk(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	req := land.LandingRequest{ItemID: "item_042", RunID: "run-contention", BaseRef: f.ref, BaseSHA: f.baseSHA, HeadSHA: f.headSHA, At: time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC), Verdict: land.Verdict{VerdictID: "verdict-contention", BaseSHA: f.baseSHA, TreeSHA: f.treeSHA, Result: land.VerdictPass}}
	return contentionFixture{f: f, s: s, writer: writer, db: db, itemBin: itemBin, req: req, trunk: trunk}
}

func (f contentionFixture) close() ([]byte, error) {
	return exec.Command(f.itemBin, "close", "--store", f.db, "--item", "item_042", "--reason", "not wanted", "--by", "owner").CombinedOutput()
}

type landAnswer struct {
	landing land.Landing
	err     error
}

func startLanding(t *testing.T, ctx context.Context, f contentionFixture) <-chan landAnswer {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan landAnswer, 1)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(hangGuard):
			t.Error("landing goroutine did not stop")
		}
	})
	go func() { l, err := land.Land(ctx, f.s, f.trunk, f.req); done <- landAnswer{l, err}; close(done) }()
	return done
}
func finishLanding(t *testing.T, done <-chan landAnswer) landAnswer {
	t.Helper()
	select {
	case a := <-done:
		return a
	case <-time.After(hangGuard):
		t.Fatal("Land did not finish")
		return landAnswer{}
	}
}

func TestGitPreparationAllowsHeartbeatAndClose(t *testing.T) {
	f := newContentionFixture(t)
	entered, release := stallGit(t, "merge-tree")
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered)
	// The old implementation holds BEGIN IMMEDIATE here: this real heartbeat fails
	// after SQLite's five-second timeout, while preparation is still stalled.
	err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_sessions VALUES('session-contention', ?) ON CONFLICT(session_id) DO UPDATE SET refreshed_at=excluded.refreshed_at`, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		os.WriteFile(release, nil, 0600)
		finishLanding(t, done)
		t.Fatalf("heartbeat blocked by Git preparation: %v", err)
	}
	if out, err := f.close(); err != nil {
		os.WriteFile(release, nil, 0600)
		finishLanding(t, done)
		t.Fatalf("close during preparation: %v %s", err, out)
	}
	// Keep the preparation stalled longer than busy_timeout even on the green path. The sleep is
	// a floor, not a bound: a slower machine only stalls longer, so load cannot fail it.
	time.Sleep(5100 * time.Millisecond)
	os.WriteFile(release, nil, 0600)
	a := finishLanding(t, done)
	if a.err != nil || a.landing.Reason != land.ReasonItemNotLandable {
		t.Fatalf("Land after close: %+v %v", a.landing, a.err)
	}
	if tip := f.f.trunk(t); tip != f.f.baseSHA {
		t.Fatalf("closed item moved trunk to %s", tip)
	}
}

// The trunk moves with no store write open (criteria c1, c2 and c4 of "Move the trunk in a
// landing with no write open", docs/design/tillr-storage.md): while the move's git command
// is blocked, this process holds no write transaction, another connection's heartbeat write
// commits, and a close of the item is refused by the landing's claim rather than waiting on
// a lock. Releasing git lets the landing record its receipt, and the close then sees it.
func TestFinalMoveHoldsNoStoreWriteAndTheClaimRefusesClose(t *testing.T) {
	f := newContentionFixture(t)
	entered, release := stallGit(t, "tillr.move=final")
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered)
	fail := func(format string, args ...any) {
		t.Helper()
		os.WriteFile(release, nil, 0600)
		finishLanding(t, done)
		t.Fatalf(format, args...)
	}
	if n := store.OpenWriteTransactions(); n != 0 {
		fail("a store write is open while the trunk move's git runs: %d", n)
	}
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_sessions VALUES('session-move', ?) ON CONFLICT(session_id) DO UPDATE SET refreshed_at=excluded.refreshed_at`, time.Now().UnixMilli())
		return err
	}); err != nil {
		fail("a heartbeat did not commit while the move's git was blocked: %v", err)
	}
	if out, err := f.close(); err == nil || !strings.Contains(string(out), "landing-in-progress") {
		fail("close between the claim and the receipt was not refused by the claim: %v %s", err, out)
	}
	os.WriteFile(release, nil, 0600)
	a := finishLanding(t, done)
	if a.err != nil || !a.landing.Landed() {
		t.Fatalf("Land: %+v %v", a.landing, a.err)
	}
	assertLandingCount(t, f, 1)
	assertNoClaims(t, f)
	if out, err := f.close(); err == nil || !strings.Contains(string(out), "already-landed") {
		t.Fatalf("Close did not see the committed receipt: %v %s", err, out)
	}
}

// A landing killed after the trunk moved and before its receipt (criterion c3) leaves the
// moved trunk and its claim. The same run's next attempt moves nothing, records the landing
// once and removes the claim in that receipt; until then the claim refuses a close.
func TestALandingKilledAfterTheMoveIsFinishedByTheRetry(t *testing.T) {
	f := newContentionFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperLandThenKillAfterMove$")
	cmd.Env = append(os.Environ(), "TILLR_HELPER_KILL=1", "HELPER_DB="+f.db, "HELPER_REPO="+f.f.dir,
		"HELPER_REF="+f.f.ref, "HELPER_BASE="+f.f.baseSHA, "HELPER_HEAD="+f.f.headSHA, "HELPER_TREE="+f.f.treeSHA,
		"GIT_CONFIG_GLOBAL="+deployedConfig(t, fixtureName, fixtureEmail))
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the helper was not killed: %v", err)
	}
	moved := f.f.trunk(t)
	if moved == f.f.baseSHA {
		t.Fatal("the helper was killed before the trunk moved")
	}
	assertLandingCount(t, f, 0)
	if out, err := f.close(); err == nil || !strings.Contains(string(out), "landing-in-progress") {
		t.Fatalf("close after a killed publication was not refused by its claim: %v %s", err, out)
	}
	a := finishLanding(t, startLanding(t, context.Background(), f))
	if a.err != nil || !a.landing.Landed() || a.landing.TrunkAfter != moved || a.landing.TrunkBefore != f.f.baseSHA {
		t.Fatalf("the retry did not record the moved trunk %s: %+v %v", moved, a.landing, a.err)
	}
	if tip := f.f.trunk(t); tip != moved {
		t.Fatalf("the retry moved the trunk again, to %s", tip)
	}
	assertLandingCount(t, f, 1)
	assertNoClaims(t, f)
	// A second retry is answered by the receipt: nothing is moved or recorded again.
	if a := finishLanding(t, startLanding(t, context.Background(), f)); a.err != nil || a.landing.TrunkAfter != moved {
		t.Fatalf("a retry of the recorded landing: %+v %v", a.landing, a.err)
	}
	assertLandingCount(t, f, 1)
}

func TestHelperLandThenKillAfterMove(t *testing.T) {
	if os.Getenv("TILLR_HELPER_KILL") == "" {
		t.Skip("run by a test that kills it")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, os.Getenv("HELPER_DB"), false)
	if err != nil {
		t.Fatal(err)
	}
	trunk, err := land.OpenTrunk(os.Getenv("HELPER_REPO"))
	if err != nil {
		t.Fatal(err)
	}
	base, head, tree := os.Getenv("HELPER_BASE"), os.Getenv("HELPER_HEAD"), os.Getenv("HELPER_TREE")
	req := land.LandingRequest{ItemID: "item_042", RunID: "run-contention", BaseRef: os.Getenv("HELPER_REF"), BaseSHA: base, HeadSHA: head, At: time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC), Verdict: land.Verdict{VerdictID: "verdict-contention", BaseSHA: base, TreeSHA: tree, Result: land.VerdictPass}}
	land.OwnerHooks.AfterTrunkMove = func() { syscall.Kill(os.Getpid(), syscall.SIGKILL) }
	land.Land(ctx, s, trunk, req)
	t.Fatal("the helper was not killed")
}

// A landing killed after its claim and before the move leaves a claim whose commit no trunk
// carries. The same run's retry lands normally, and its receipt replaces the claim.
func TestALandingKilledBeforeTheMoveIsLandedByTheRetry(t *testing.T) {
	f := newContentionFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperLandThenKillBeforeMove$")
	cmd.Env = append(os.Environ(), "TILLR_HELPER_KILL=1", "HELPER_DB="+f.db, "HELPER_REPO="+f.f.dir,
		"HELPER_REF="+f.f.ref, "HELPER_BASE="+f.f.baseSHA, "HELPER_HEAD="+f.f.headSHA, "HELPER_TREE="+f.f.treeSHA,
		"GIT_CONFIG_GLOBAL="+deployedConfig(t, fixtureName, fixtureEmail))
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the helper was not killed: %v", err)
	}
	if tip := f.f.trunk(t); tip != f.f.baseSHA {
		t.Fatalf("the helper moved the trunk before it was killed: %s", tip)
	}
	a := finishLanding(t, startLanding(t, context.Background(), f))
	if a.err != nil || !a.landing.Landed() || a.landing.TrunkAfter != f.f.trunk(t) {
		t.Fatalf("the retry did not land: %+v %v", a.landing, a.err)
	}
	assertLandingCount(t, f, 1)
	assertNoClaims(t, f)
}

func TestHelperLandThenKillBeforeMove(t *testing.T) {
	if os.Getenv("TILLR_HELPER_KILL") == "" {
		t.Skip("run by a test that kills it")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, os.Getenv("HELPER_DB"), false)
	if err != nil {
		t.Fatal(err)
	}
	trunk, err := land.OpenTrunk(os.Getenv("HELPER_REPO"))
	if err != nil {
		t.Fatal(err)
	}
	base, head, tree := os.Getenv("HELPER_BASE"), os.Getenv("HELPER_HEAD"), os.Getenv("HELPER_TREE")
	req := land.LandingRequest{ItemID: "item_042", RunID: "run-contention", BaseRef: os.Getenv("HELPER_REF"), BaseSHA: base, HeadSHA: head, At: time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC), Verdict: land.Verdict{VerdictID: "verdict-contention", BaseSHA: base, TreeSHA: tree, Result: land.VerdictPass}}
	land.OwnerHooks.Publishing = func() { syscall.Kill(os.Getpid(), syscall.SIGKILL) }
	land.Land(ctx, s, trunk, req)
	t.Fatal("the helper was not killed")
}

// A claim another run left, whose move never happened, is cleared by the next landing,
// so an abandoned claim cannot keep an item unclosable.
func TestAStaleClaimIsClearedByTheNextLanding(t *testing.T) {
	f := newContentionFixture(t)
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO landing_claims (run_id, item_id, base_ref, head_sha, commit_sha, trunk_before, claimed_at) VALUES ('run-abandoned', 'item_other', ?, ?, ?, ?, 1)`,
			f.f.ref, f.f.headSHA, f.f.headSHA, f.f.baseSHA)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if a := finishLanding(t, startLanding(t, context.Background(), f)); a.err != nil || !a.landing.Landed() {
		t.Fatalf("Land: %+v %v", a.landing, a.err)
	}
	assertNoClaims(t, f)
}

func assertNoClaims(t *testing.T, f contentionFixture) {
	t.Helper()
	if err := f.writer.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		err := r.QueryRow(`SELECT count(*) FROM landing_claims`).Scan(&n)
		if err == nil && n != 0 {
			t.Errorf("landing claims left: %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func errorString(err error) string {
	if err == nil {
		return "success"
	}
	return err.Error()
}

func TestCanceledGitPreparationDoesNotMoveOrRecord(t *testing.T) {
	f := newContentionFixture(t)
	entered, _ := stallGit(t, "merge-tree")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startLanding(t, ctx, f)
	awaitGit(t, entered)
	cancel()
	a := finishLanding(t, done)
	if !errors.Is(a.err, context.Canceled) {
		t.Fatalf("canceled preparation lost context identity: %v", a.err)
	}
	assertNoLandingMove(t, f)
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_sessions VALUES('after-cancel',1)`)
		return err
	}); err != nil {
		t.Fatalf("writer retained after cancellation: %v", err)
	}
}

func TestPreparationRevalidatesTrunkAndHistory(t *testing.T) {
	for _, change := range []string{"trunk", "history-row", "history-schema"} {
		t.Run(change, func(t *testing.T) {
			f := newContentionFixture(t)
			f.req.ObservedTrunk = f.f.baseSHA
			if change == "history-row" {
				if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
					_, err := tx.Exec(`CREATE TABLE trunk_history(id INTEGER PRIMARY KEY, observed_tip TEXT NOT NULL, lost_tip TEXT NOT NULL); INSERT INTO trunk_history VALUES(1,?,'')`, f.f.baseSHA)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			entered, release := stallGit(t, "merge-tree")
			done := startLanding(t, context.Background(), f)
			awaitGit(t, entered)
			expectedTip := f.f.baseSHA
			if change == "trunk" {
				expectedTip = f.f.advance(t)
			} else {
				err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
					if change == "history-schema" {
						_, err := tx.Exec(`CREATE TABLE trunk_history(id INTEGER PRIMARY KEY, observed_tip TEXT NOT NULL, lost_tip TEXT NOT NULL)`)
						if err != nil {
							return err
						}
						_, err = tx.Exec(`INSERT INTO trunk_history VALUES(1,?,'')`, f.f.baseSHA)
						return err
					}
					_, err := tx.Exec(`UPDATE trunk_history SET lost_tip=? WHERE id=1`, f.f.headSHA)
					return err
				})
				if err != nil {
					os.WriteFile(release, nil, 0600)
					finishLanding(t, done)
					t.Fatal(err)
				}
			}
			os.WriteFile(release, nil, 0600)
			a := finishLanding(t, done)
			if a.err == nil || !strings.Contains(a.err.Error(), "changed during landing preparation") {
				t.Fatalf("stale preparation not rejected: %+v %v", a.landing, a.err)
			}
			if tip := f.f.trunk(t); tip != expectedTip {
				t.Fatalf("stale preparation moved trunk: %s", tip)
			}
			assertLandingCount(t, f, 0)
		})
	}
}

func TestFinalMoveFailureRollsBackReceiptAndReleasesWriter(t *testing.T) {
	f := newContentionFixture(t)
	entered, release := stallGit(t, "tillr.move=final")
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered)
	// A trunk that moves between the comparison and the ref write is refused by the
	// guarded ref update; the install is undone and nothing is recorded.
	moved := git(t, f.f.dir, "-c", "user.name=owner", "-c", "user.email=owner@example.invalid", "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "owner")
	git(t, f.f.dir, "update-ref", "refs/heads/master", moved)
	ownerTip := f.f.trunk(t)
	os.WriteFile(release, nil, 0600)
	a := finishLanding(t, done)
	if a.err == nil {
		t.Fatalf("the move went through a moved trunk: %+v", a.landing)
	}
	if tip := f.f.trunk(t); tip != ownerTip {
		t.Fatalf("a failed move changed the trunk: %s", tip)
	}
	if _, err := os.Stat(filepath.Join(f.f.dir, "candidate.txt")); err == nil {
		t.Fatal("a failed move left the installed file behind")
	}
	assertLandingCount(t, f, 0)
	if out, err := f.close(); err != nil {
		t.Fatalf("close after failed Move: %v %s", err, out)
	}
}

// A receipt that fails after the Move is reported, and the retry records the commit the
// trunk already carries rather than squashing the change a second time.
func TestReceiptFailureAfterMoveIsReportedWithoutReplay(t *testing.T) {
	f := newContentionFixture(t)
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON landings BEGIN SELECT RAISE(ABORT,'receipt fixture failure'); END;`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a := finishLanding(t, startLanding(t, context.Background(), f))
	if a.err == nil || !strings.Contains(a.err.Error(), "receipt fixture failure") {
		t.Fatalf("receipt failure lost: %+v %v", a.landing, a.err)
	}
	moved := f.f.trunk(t)
	if moved == f.f.baseSHA {
		t.Fatal("fixture did not reach the external Move")
	}
	assertLandingCount(t, f, 0)
	// The boundary stays non-atomic; the retry is what makes it idempotent.
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER reject_receipt`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a = finishLanding(t, startLanding(t, context.Background(), f))
	if a.err != nil || !a.landing.Landed() || a.landing.TrunkAfter != moved || a.landing.TrunkBefore != f.f.baseSHA {
		t.Fatalf("retry did not record the moved trunk %s: %+v %v", moved, a.landing, a.err)
	}
	if tip := f.f.trunk(t); tip != moved {
		t.Fatalf("retry moved the trunk again, to %s", tip)
	}
	assertLandingCount(t, f, 1)
}

// A recovery decided during preparation is rechecked inside the writer: a receipt for the
// same commit committed meanwhile — by a lander that took another lock file, the one path
// past land.lock — is refused rather than recorded a second time.
func TestRecoveryRechecksTheReceiptInsideTheWriter(t *testing.T) {
	f := newContentionFixture(t)
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON landings BEGIN SELECT RAISE(ABORT,'receipt fixture failure'); END;`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if a := finishLanding(t, startLanding(t, context.Background(), f)); a.err == nil {
		t.Fatalf("fixture: the receipt did not fail: %+v", a.landing)
	}
	moved := f.f.trunk(t)
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER reject_receipt`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Preparation decides on the recovery, then pins: hold it at the pin.
	entered, release := stallGit(t, "update-ref")
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered)
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO landings (landing_id, item_id, run_id, verdict_id, result, base_sha, head_sha, trunk_before, trunk_after)
			VALUES ('landing_other', 'item_042', 'run-contention', 'verdict-other', 'landed', ?, ?, ?, ?)`,
			f.f.baseSHA, f.f.headSHA, f.f.baseSHA, moved)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := finishLanding(t, done)
	if a.err == nil || !strings.Contains(a.err.Error(), "recorded during preparation") {
		t.Fatalf("a receipt written during preparation was not rechecked: %+v %v", a.landing, a.err)
	}
	assertLandingCount(t, f, 1)
}

func assertNoLandingMove(t *testing.T, f contentionFixture) {
	t.Helper()
	if tip := f.f.trunk(t); tip != f.f.baseSHA {
		t.Fatalf("trunk moved to %s", tip)
	}
	assertLandingCount(t, f, 0)
}
func assertLandingCount(t *testing.T, f contentionFixture, want int) {
	t.Helper()
	if err := f.writer.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		err := r.QueryRow(`SELECT count(*) FROM landings`).Scan(&n)
		if err == nil && n != want {
			t.Errorf("receipts=%d want %d", n, want)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
