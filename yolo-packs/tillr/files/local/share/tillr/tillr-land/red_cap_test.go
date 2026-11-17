package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRedCapDoesNotHoldAnAlreadyOnTrunkCandidate(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "update-ref", "refs/tillr/green", f.baseSHA)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out, stderr := passBoth(t, f.passArgs(t, db, "item-first", "run-first"))
	if code != 0 {
		t.Fatalf("first landing: %d %s %s", code, out, stderr)
	}
	tip := f.trunk(t)
	twin := f.candidateFrom(t, "twin-cap", "candidate.txt", "candidate\n")
	write(t, filepath.Join(f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":1,"minutes":0}}`)
	code, out, stderr = passBoth(t, twin.passArgs(t, db, "item-twin", "run-twin"))
	got := decodeLanding(t, out).Payload
	if code != 1 || got.Reason != "already-on-trunk" || got.TrunkAfter != tip || got.LandedTree != "" || f.trunk(t) != tip {
		t.Fatalf("cap masks supersession: %d %+v %s", code, got, stderr)
	}
	if git(t, f.dir, "rev-parse", "refs/tillr/runs/run-twin") != twin.headSHA {
		t.Fatal("superseded candidate not pinned")
	}
}

func TestRedCapRecordClockDoesNotAuthorizeLanding(t *testing.T) {
	t.Setenv("GIT_COMMITTER_DATE", time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339))
	f := newFixture(t)
	git(t, f.dir, "update-ref", "refs/tillr/green", f.baseSHA)
	tip := f.advance(t)
	write(t, filepath.Join(f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":1}}`)
	_, code, out := f.land(t, "record-clock", "pass") // --now precedes green; real adapter also always sends --now.
	if code != 1 || decodeLanding(t, out).Payload.Reason != "red-cap" || f.trunk(t) != tip {
		t.Fatalf("injected record timestamp authorized move: %d %s", code, out)
	}
}

func TestRedCapLandingsCountsOnlyRecordedMoves(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "update-ref", "refs/tillr/green", f.baseSHA)
	db, code, out := f.land(t, "first-run", "pass")
	if code != 0 {
		t.Fatalf("first landing: %d %s", code, out)
	}
	tip := f.trunk(t)
	// A separate owner commit is not another recorded landing.
	f.advance(t)
	current := f.trunk(t)
	git(t, f.dir, "checkout", "-b", "second-candidate")
	write(t, filepath.Join(f.dir, "second.txt"), "second\n")
	git(t, f.dir, "add", "second.txt")
	git(t, f.dir, "commit", "-m", "second")
	f.headSHA = git(t, f.dir, "rev-parse", "HEAD")
	f.treeSHA = git(t, f.dir, "rev-parse", "HEAD^{tree}")
	f.baseSHA = current
	git(t, f.dir, "checkout", f.ref)
	write(t, filepath.Join(f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":1,"minutes":0}}`)
	common := git(t, f.dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	write(t, filepath.Join(common, "tillr-gate/heavy.json"), fmt.Sprintf(`{"tip_sha":%q,"result":"red","checks":[{"check_id":"unit","result":"fail","first_red_landing":%q}]}`, current, tip))
	code, out = run(t, "pass", "--store", db, "--repo", f.dir, "--base-ref", f.ref, "--item", "item-second", "--run", "second-run", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--now", "2026-11-17T00:00:00Z")
	if code != 1 || decodeLanding(t, out).Payload.Reason != "red-cap" || f.trunk(t) != current || !strings.Contains(string(out), `"landings_since":1`) {
		t.Fatalf("landing cap counts wrong or moved trunk: %d %s", code, out)
	}
	// Changing the configured bound releases this same tree and verdict without a run.
	write(t, filepath.Join(f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":2,"minutes":0}}`)
	code, out = run(t, "pass", "--store", db, "--repo", f.dir, "--base-ref", f.ref, "--item", "item-second", "--run", "second-run", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--now", "2026-11-17T00:00:00Z")
	if code != 0 || decodeLanding(t, out).Payload.Result != "landed" {
		t.Fatalf("owner commit counted as landing: %d %s", code, out)
	}
}

func TestRedCapRefusesBeforeMoveAndExemptsFixer(t *testing.T) {
	for _, tc := range []struct {
		name, strategy string
		minutes        int
		fixer          bool
		refuse         bool
	}{
		{"minutes", "after-merge", 1, false, true},
		{"zero", "after-merge", 0, false, false},
		{"before merge", "before-merge", 1, false, false},
		{"fixer", "after-merge", 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_COMMITTER_DATE", time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339))
			f := newFixture(t)
			git(t, f.dir, "update-ref", "refs/tillr/green", f.baseSHA)
			tip := f.advance(t)
			write(t, filepath.Join(f.dir, "config/tillr/gate-policy.json"), fmt.Sprintf(`{"landing_strategy":%q,"red_cap":{"landings":0,"minutes":%d}}`, tc.strategy, tc.minutes))
			common := git(t, f.dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
			write(t, filepath.Join(common, "tillr-gate/heavy.json"), fmt.Sprintf(`{"base_sha":%q,"tip_sha":%q,"result":"red","checks":[{"check_id":"unit","result":"fail","first_red_landing":%q}]}`, f.baseSHA, tip, tip))
			db := filepath.Join(scratch(t, "store"), "state.db")
			s, err := store.InitFixture(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			fixer := "fixer-main-" + tip[:12]
			err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
				_, err := tx.Exec(`CREATE TABLE items (item_id TEXT PRIMARY KEY,state TEXT); INSERT INTO items VALUES (?, 'queued')`, fixer)
				return err
			})
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			seconds, err := strconv.ParseInt(git(t, f.dir, "show", "-s", "--format=%ct", f.baseSHA), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			id := "item_042"
			if tc.fixer {
				id = fixer
			}
			code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--base-ref", f.ref, "--item", id, "--run", "cap-run", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--now", time.Unix(seconds+121, 0).UTC().Format(time.RFC3339))
			got := decodeLanding(t, out)
			if tc.refuse {
				if code != 1 || got.Payload.Reason != "red-cap" || f.trunk(t) != tip {
					t.Fatalf("cap moved trunk or did not refuse: %d %s", code, out)
				}
				var detail struct {
					Payload struct {
						RedCap struct {
							FixerItem string   `json:"fixer_item"`
							CheckIDs  []string `json:"check_ids"`
							Detail    string   `json:"detail"`
						} `json:"red_cap"`
					}
				}
				if err := json.Unmarshal(out, &detail); err != nil {
					t.Fatal(err)
				}
				if detail.Payload.RedCap.FixerItem != fixer || !strings.Contains(detail.Payload.RedCap.Detail, "minutes") || len(detail.Payload.RedCap.CheckIDs) != 1 || detail.Payload.RedCap.CheckIDs[0] != "unit" {
					t.Fatalf("cap omits check, bound, or fixer: %s", out)
				}
				if git(t, f.dir, "rev-parse", "refs/tillr/runs/cap-run") != f.headSHA {
					t.Fatal("candidate not pinned")
				}
			} else if code != 0 || got.Payload.Result != "landed" {
				t.Fatalf("unexpected refusal: %d %s", code, out)
			}
		})
	}
}
