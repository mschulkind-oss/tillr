package main_test

import (
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestNoRedCapWhenTrunkUnprovenOrRed(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "update-ref", "refs/tillr/green", f.baseSHA)
	// Advance trunk without updating green (unproven trunk) and record a red heavy report.
	tip := f.advance(t)
	write(t, filepath.Join(f.dir, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge"}`)
	common := git(t, f.dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	write(t, filepath.Join(common, "tillr-gate/heavy.json"), fmt.Sprintf(`{"base_sha":%q,"tip_sha":%q,"result":"red","checks":[{"check_id":"unit","result":"fail","first_red_landing":%q}]}`, f.baseSHA, tip, tip))

	// Even with 25+ minutes elapsed and multiple landings since green, ordinary items land without cap.
	seconds, err := strconv.ParseInt(git(t, f.dir, "show", "-s", "--format=%ct", f.baseSHA), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(scratch(t, "store"), "state.db")
	for i := 0; i < 3; i++ {
		cand := f.candidateFrom(t, fmt.Sprintf("cand-%d", i), fmt.Sprintf("file-%d.txt", i), "ok\n")
		code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--base-ref", f.ref,
			"--item", fmt.Sprintf("item-%d", i), "--run", fmt.Sprintf("run-%d", i),
			"--base", cand.baseSHA, "--head", cand.headSHA,
			"--verdict", cand.verdictFile(t, "pass"),
			"--now", time.Unix(seconds+3600+int64(i), 0).UTC().Format(time.RFC3339))
		got := decodeLanding(t, out)
		if code != 0 || got.Payload.Result != "landed" {
			t.Fatalf("landing %d refused while red/unproven: %d %s", i, code, out)
		}
	}
}
