package main_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAcceptIdentityRecordsExactCommitAndValidatesArguments(t *testing.T) {
	f := newLoopFixture(t, "fixture")
	sha := git(t, f.root, "rev-parse", "HEAD")
	s, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	base := []string{"accept-identity", "--root", f.root, "--store", f.store, "--commit", sha, "--by", "owner"}
	code, out := invoke(t, append(base, "--why", "intentional import")...)
	if code != 0 || !strings.Contains(string(out), "accepted identity of trunk commit "+sha) {
		t.Fatalf("accept: %d %s", code, out)
	}
	s, err = store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var by, why string
		err := r.QueryRow(`SELECT accepted_by,reason FROM trunk_identity_acceptances WHERE sha=?`, sha).Scan(&by, &why)
		if by != "owner" || why != "intentional import" {
			t.Fatalf("acceptance %s %s", by, why)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	code, _ = invoke(t, base...)
	if code == 0 {
		t.Fatal("missing rationale accepted")
	}
	code, _ = invoke(t, "accept-identity", "--root", f.root, "--store", f.store, "--commit", sha[:8], "--by", "owner", "--why", "short id")
	if code == 0 {
		t.Fatal("abbreviated id accepted")
	}
	// The shared writer used by both binaries applies the same validation.
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { return land.AcceptIdentity(tx, "not-a-sha", "owner", "bad") }); err == nil {
		t.Fatal("invalid id accepted")
	}
}
