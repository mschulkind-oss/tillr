package main_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

func TestLockedLandingFindsForeignCommitAfterLoopObservation(t *testing.T) {
	for _, mode := range []string{"unanswered", "accepted", "unchecked-observation"} {
		t.Run(mode, func(t *testing.T) {
			accepted := mode == "accepted"
			f := newFixture(t)
			// Plumbing publishes a foreign commit without any hook or working index.
			tree := git(t, f.dir, "rev-parse", "HEAD^{tree}")
			foreign := gitAs(t, f.dir, "Other", "other@example.invalid", "commit-tree", tree, "-p", f.baseSHA, "-m", "foreign")
			git(t, f.dir, "update-ref", "refs/heads/master", foreign, f.baseSHA)
			db := filepath.Join(scratch(t, "identity-store"), "state.db")
			s, err := store.Open(context.Background(), db, true)
			if err != nil {
				t.Fatal(err)
			}
			observed := f.baseSHA
			if mode == "unchecked-observation" {
				// The identity read certified A; a foreign F arrived before
				// the separate history read. The request now observes F, but
				// the durable identity cursor must still force checking A..F.
				observed = foreign
				err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
					return land.SaveIdentityHistory(tx, land.IdentityHistory{Tip: f.baseSHA})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if accepted {
				err = s.WriteTx(context.Background(), func(tx *store.Tx) error { return land.AcceptIdentity(tx, foreign, "owner", "intentional import") })
				if err != nil {
					t.Fatal(err)
				}
			}
			s.Close()
			code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-identity", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--observed-trunk", observed)
			got := decodeLanding(t, out).Payload
			if accepted {
				if code != 0 || got.Result != "landed" {
					t.Fatalf("accepted commit refused: %d %s", code, out)
				}
			} else if code != 1 || got.Reason != "trunk-foreign-identity" || f.trunk(t) != foreign {
				t.Fatalf("published onto foreign commit: %d %s", code, out)
			}
		})
	}
}

// A foreign commit reachable from a published pointer arrived from the remote and is
// trusted; the same commit with no pointer still holds the landing.
func TestLockedLandingTrustsForeignCommitOnPublishedPointer(t *testing.T) {
	for _, published := range []bool{false, true} {
		f := newFixture(t)
		tree := git(t, f.dir, "rev-parse", "HEAD^{tree}")
		foreign := gitAs(t, f.dir, "Other", "other@example.invalid", "commit-tree", tree, "-p", f.baseSHA, "-m", "foreign")
		git(t, f.dir, "update-ref", "refs/heads/master", foreign, f.baseSHA)
		if published {
			git(t, f.dir, "update-ref", land.PublishedRefPrefix+"origin/master", foreign)
		}
		db := filepath.Join(scratch(t, "identity-store"), "state.db")
		s, err := store.Open(context.Background(), db, true)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-identity", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--observed-trunk", foreign)
		got := decodeLanding(t, out).Payload
		if published && (code != 0 || got.Result != "landed") {
			t.Fatalf("published commit refused: %d %s", code, out)
		}
		if !published && (code != 1 || got.Reason != "trunk-foreign-identity") {
			t.Fatalf("unpublished foreign commit landed: %d %s", code, out)
		}
	}
}
