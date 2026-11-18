package main_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestLockedLandingRefusesRewrittenObservedTipNotCandidateBase(t *testing.T) {
	f := newFixture(t)
	observed := f.advance(t)
	git(t, f.dir, "reset", "--hard", f.baseSHA)
	_, code, out := f.land(t, "run-rewrite", "pass", "--observed-trunk", observed)
	if code != 1 {
		t.Fatalf("rewrite exit %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Reason != "trunk-rewritten" || got.Payload.Result != "refused" || f.trunk(t) != f.baseSHA {
		t.Fatalf("rewrite landed: %+v", got)
	}
	git(t, f.dir, "reset", "--hard", observed)
	_, code, out = f.land(t, "run-restored", "pass", "--observed-trunk", observed)
	if code != 0 {
		t.Fatalf("restored history refused: %d: %s", code, out)
	}
}

func TestLockedLandingAcceptsFastForwardBeyondObservedTip(t *testing.T) {
	f := newFixture(t)
	observed := f.trunk(t)
	moved := f.advance(t)
	_, code, out := f.land(t, "run-forward", "pass", "--observed-trunk", observed)
	if code != 0 {
		t.Fatalf("fast-forward refused: %d: %s", code, out)
	}
	if got := decodeLanding(t, out); got.Payload.TrunkBefore != moved || got.Payload.GatedTree == got.Payload.LandedTree {
		t.Fatalf("did not reapply onto moved trunk: %+v", got)
	}
}

func TestLandingWaitingForLockReadsNewDurableRewrite(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "waiting-history"), "state.db")
	st, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE trunk_history(id INTEGER PRIMARY KEY,observed_tip TEXT NOT NULL,lost_tip TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_history VALUES(1,?,'')`, f.baseSHA)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	gitDir := git(t, f.dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	lockPath := filepath.Join(gitDir, "land.lock")
	holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "pass", "--store", db, "--repo", f.dir,
		"--item", "item_042", "--run", "run-waiter", "--base", f.baseSHA,
		"--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--observed-trunk", f.baseSHA)
	cmd.Env = append(withoutIdentity(os.Environ()), fixtureEnv(t)...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cancel()
			_ = cmd.Wait()
		}
	}()
	// Acquire creates the guard only after validating the request. This handshake
	// puts the concurrent observations after argv processing, before lock entry.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(lockPath + ".guard"); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("lander never reached lock acquisition")
		}
	}
	lost := f.advance(t)
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE trunk_history SET observed_tip=? WHERE id=1`, lost)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	git(t, f.dir, "reset", "--hard", f.baseSHA)
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE trunk_history SET observed_tip=?,lost_tip=? WHERE id=1`, f.baseSHA, lost)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("waiting lander bypassed rewrite: %v: %s %s", err, out.String(), stderr.String())
	}
	if got := decodeLanding(t, out.Bytes()); got.Payload.Reason != "trunk-rewritten" || f.trunk(t) != f.baseSHA {
		t.Fatalf("waiting lander lost rewrite: %+v", got)
	}
}

func TestRestoringDurableLostTipStillChecksRequestObservation(t *testing.T) {
	f := newFixture(t)
	lost := f.advance(t)
	write(t, filepath.Join(f.dir, "later.txt"), "later observation\n")
	git(t, f.dir, "add", "later.txt")
	git(t, f.dir, "commit", "-m", "later")
	expected := f.trunk(t)
	git(t, f.dir, "reset", "--hard", lost)
	db := filepath.Join(scratch(t, "restored-history"), "state.db")
	st, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE trunk_history(id INTEGER PRIMARY KEY,observed_tip TEXT NOT NULL,lost_tip TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_history VALUES(1,?,?)`, f.baseSHA, lost)
		return err
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-newer-observation", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--observed-trunk", expected)
	if code != 1 || decodeLanding(t, out).Payload.Reason != "trunk-rewritten" || f.trunk(t) != lost {
		t.Fatalf("restoration bypassed newer observation: %d: %s", code, out)
	}
	st, err = store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		var kept string
		if err := r.QueryRow(`SELECT lost_tip FROM trunk_history WHERE id=1`).Scan(&kept); err != nil {
			return err
		}
		if kept != expected {
			t.Fatalf("lost newer observation was not persisted: %s", kept)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStaleLandingObservationCannotBypassDurableRewrite(t *testing.T) {
	for _, alreadyLost := range []bool{false, true} {
		t.Run(map[bool]string{false: "newly-detected", true: "already-observed"}[alreadyLost], func(t *testing.T) {
			f := newFixture(t)
			lost := f.advance(t)
			git(t, f.dir, "reset", "--hard", f.baseSHA)
			db := filepath.Join(scratch(t, "history-store"), "state.db")
			st, err := store.Open(context.Background(), db, true)
			if err != nil {
				t.Fatal(err)
			}
			observed, storedLost := lost, ""
			if alreadyLost {
				observed, storedLost = f.baseSHA, lost
			}
			err = st.WriteTx(context.Background(), func(tx *store.Tx) error {
				if _, err := tx.Exec(`CREATE TABLE trunk_history(id INTEGER PRIMARY KEY,observed_tip TEXT NOT NULL,lost_tip TEXT NOT NULL)`); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT INTO trunk_history VALUES(1,?,?)`, observed, storedLost)
				return err
			})
			st.Close()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-stale", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--observed-trunk", f.baseSHA}
			code, out := run(t, args...)
			if code != 1 || decodeLanding(t, out).Payload.Reason != "trunk-rewritten" || f.trunk(t) != f.baseSHA {
				t.Fatalf("stale observation bypassed durable hold: %d: %s", code, out)
			}
			git(t, f.dir, "reset", "--hard", lost)
			args[8] = "run-restored"
			if code, out := run(t, args...); code != 0 {
				t.Fatalf("restoration did not release durable hold: %d: %s", code, out)
			}
		})
	}
}
