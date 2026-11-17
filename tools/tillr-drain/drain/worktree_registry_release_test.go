package drain

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDeskReleaseTakesLandLockBeforeSQLiteWriter(t *testing.T) {
	f, l, c := heldCandidateFixtureInLane(t, "desk-review")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	scratch := scratchUnderDurable(t)
	// Shard scratch paths can exceed Unix socket limits; loopback rendezvous
	// is independent of the checkout's path length.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// Stop immediately before the registry lock request. The competing lander
	// already owns that lock, and must be able to obtain its SQLite writer.
	script := fmt.Sprintf(`#!/usr/bin/env python3
import os, socket, sys
if '--git-common-dir' in sys.argv:
    with socket.create_connection((%q, %d)) as sock:
        sock.recv(1)
os.execv(%q,[%q]+sys.argv[1:])
`, address.IP.String(), address.Port, real, real)
	if err := os.WriteFile(filepath.Join(scratch, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scratch+string(os.PathListSeparator)+os.Getenv("PATH"))
	s, err := store.Open(ctx, f.storePath, true, store.WithBusyTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lock, err := land.OpenLock(filepath.Join(f.root, ".git", "land.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Acquire(ctx, 0); err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	ready := make(chan net.Conn, 1)
	go func() { conn, _ := listener.Accept(); ready <- conn }()
	type outcome struct {
		released bool
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		released, err := l.releaseHeldCandidate(ctx, &pendingWork{Candidate: &c}, "gate-harness-error")
		done <- outcome{released, err}
	}()
	var conn net.Conn
	select {
	case conn = <-ready:
		if conn == nil {
			t.Fatal("registry rendezvous failed")
		}
	case got := <-done:
		t.Fatalf("release did not reach registry coordination: %+v", got)
	case <-ctx.Done():
		t.Fatal("release did not reach registry coordination")
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec("INSERT INTO meta(key,value) VALUES('competing-land','written')")
		return err
	})
	if err != nil {
		t.Errorf("landing's SQLite writer blocked by a desk release waiting for land.lock: %v", err)
	}
	conn.Close()
	lock.Release()
	select {
	case got := <-done:
		if got.err != nil || !got.released {
			t.Fatalf("release=%+v", got)
		}
	case <-ctx.Done():
		t.Fatal("desk release did not complete after landing released its lock")
	}
	if _, err := os.Stat(c.Worktree); !os.IsNotExist(err) {
		t.Fatalf("desk checkout remains: %v", err)
	}
	if head := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c.RunID); head != c.HeadSHA {
		t.Fatalf("candidate pin changed: %s", head)
	}
}
