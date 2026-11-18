package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

// A real fork build, real lander and two overlapping boundary brackets. The build
// intentionally deletes a tracked file: only the clean build copy may be damaged.
func TestForkBuildDeliveryAndSuccessiveLaneBases(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh-cache-%v", fresh), func(t *testing.T) { testForkDelivery(t, fresh) })
	}
}

func testForkDelivery(t *testing.T, fresh bool) {
	f := newForkLaneFixture(t)
	git(t, f.fork, "remote", "add", "upstream", f.fork)
	git(t, f.fork, "update-ref", "refs/remotes/upstream/main", f.forkBase)
	helper, err := os.ReadFile("fork-gate.py")
	if err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(f.root, "tools/tillr-drain/fork-gate.py")
	if err := os.MkdirAll(filepath.Dir(helperPath), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, helperPath, string(helper))
	pack := filepath.Join(f.root, "yolo-packs/fork/pack.json")
	if err := os.MkdirAll(filepath.Dir(pack), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, pack, `{"name":"fork-fixture","contributes":[{"kind":"program","fork_of":"pi","source":"git+https://example.invalid/pi?ref=main","patch_branch":"git+https://example.invalid/repo?ref=patch/pi","build":"test -f build-ok && git rev-parse HEAD > built-head && rm fork.txt"}]}`)
	writeFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n.tillr/\n")
	git(t, f.root, "add", ".")
	git(t, f.root, "commit", "-m", "fork fixture infrastructure")
	f.base = git(t, f.root, "rev-parse", "HEAD")
	cache := filepath.Join(f.root, ".forks/pi")
	if !fresh {
		git(t, f.root, "clone", "--quiet", f.fork, cache)
		git(t, cache, "fetch", "--quiet", f.fork, "+refs/remotes/upstream/*:refs/remotes/upstream/*")
		git(t, cache, "remote", "add", "upstream", f.fork)
	}
	fixtureAt(t, f.store, fencedItemRecord("item_042", "in-queue", "base.txt"))
	spec := f.spec("lane-2")
	spec.ItemID = "item_042"
	spec.RunID = "run-ordinary"
	if err := f.stager().Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(scratch(t, "fork-identity"), "gitconfig")
	writeFile(t, identity, "[user]\n name = Fixture Landing\n email = landing@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", identity)
	gateBin := sibling(t, "gate")
	t.Setenv("PATH", filepath.Dir(gateBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	steps := &drain.ProcessSteps{Root: f.root, Store: f.store, Now: time.Now, GateProgram: "this ordinary gate must not run"}

	beforeFork := f.snapshot(t, "fork-before")
	ordinarySnapshot := func(name string) string {
		out := filepath.Join(scratch(t, "ordinary-boundary"), name+".json")
		code, output := invoke(t, "boundary", "snapshot", "--repo", f.root, "--target", filepath.Join(f.laneRoot, "lane-2/worktree"), "--out", out)
		if code != 0 {
			t.Fatalf("snapshot exited %d: %s", code, output)
		}
		return out
	}
	beforeOrdinary := ordinarySnapshot("before")
	// A sibling lease is store evidence that its boundary comparison is pending.
	setSibling := func(leased bool) {
		s, err := store.Open(context.Background(), f.store, true)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			state, run := "idle", ""
			if leased {
				state, run = "leased", "run-ordinary"
			}
			_, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id) VALUES('lane-2',?,NULLIF(?,'')) ON CONFLICT(lane_id) DO UPDATE SET state=excluded.state,run_id=excluded.run_id`, state, run)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setSibling(true)
	candidate := func(run string) drain.Candidate {
		return drain.Candidate{ItemID: "item_041", LaneID: "lane-1", RunID: run, Worktree: f.fork, BaseRef: "refs/heads/patch/pi", BaseSHA: f.forkBase, HeadSHA: git(t, f.fork, "rev-parse", "HEAD")}
	}
	// A failing build cannot advance delivery even with a real verdict passed to the lander.
	writeFile(t, filepath.Join(f.fork, "fork.txt"), "broken candidate\n")
	git(t, f.fork, "commit", "-qam", "broken build")
	bad := candidate("run-broken-fork")
	outcome, err := steps.Gate(context.Background(), bad)
	if err != nil || outcome.Outcome != drain.GateFailed {
		t.Fatalf("bad build: %+v, %v", outcome, err)
	}
	land := func(c drain.Candidate) (int, []byte) {
		return driveRecord(t, sibling(t, "land"), "pass", "--store", f.store, "--repo", f.root, "--item", c.ItemID, "--run", c.RunID, "--base", c.BaseSHA, "--head", c.HeadSHA, "--base-ref", c.BaseRef, "--verdict", filepath.Join(drain.RunDir(f.root, c.RunID), drain.GateVerdictRecord))
	}
	// The failed verdict's objects must be readable for a refusal; this imports no branch.
	git(t, f.root, "fetch", "--quiet", "--no-tags", f.fork, bad.HeadSHA)
	code, output := land(bad)
	if code != 1 || !strings.Contains(string(output), "verdict-failed") {
		t.Fatalf("broken build land exited %d: %s", code, output)
	}
	if tip := git(t, f.root, "rev-parse", "refs/heads/patch/pi"); tip != f.forkBase {
		t.Fatalf("failed build moved delivery to %s", tip)
	}

	writeFile(t, filepath.Join(f.fork, "build-ok"), "ready\n")
	git(t, f.fork, "add", "build-ok")
	git(t, f.fork, "commit", "-m", "buildable fork")
	first := candidate("run-first-fork")
	outcome, err = steps.Gate(context.Background(), first)
	if err != nil || outcome.Outcome != drain.GatePassed {
		t.Fatalf("first build: %+v, %v", outcome, err)
	}
	if _, err := os.Stat(filepath.Join(f.fork, "fork.txt")); err != nil {
		t.Fatal("build damaged the writer checkout:", err)
	}
	code, output = land(first)
	if code != 0 {
		t.Fatalf("first landing exited %d: %s", code, output)
	}
	for _, checkout := range []string{f.root, cache} {
		ref := "refs/heads/patch/pi"
		if checkout == cache {
			ref = "main"
		}
		if tip := git(t, checkout, "rev-parse", ref); tip != first.HeadSHA {
			t.Fatalf("%s %s at %s, want %s", checkout, ref, tip, first.HeadSHA)
		}
	}
	if _, err := drain.SweepRefs(context.Background(), drain.RefSweepRequest{Root: f.root, Store: f.store}); err != nil {
		t.Fatal(err)
	}
	marker := "refs/tillr/landed/" + first.RunID
	if tip := git(t, f.root, "rev-parse", marker); tip != first.HeadSHA {
		t.Fatal("fork marker not retained at built head")
	}
	// Neither delivery nor the lander's marker convicts a sibling ordinary run.
	for _, pair := range [][2]string{{beforeFork, f.snapshot(t, "fork-after")}, {beforeOrdinary, ordinarySnapshot("after")}} {
		load := func(path string) *drain.Boundary {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var boundary drain.Boundary
			if err := json.Unmarshal(body, &boundary); err != nil {
				t.Fatal(err)
			}
			return &boundary
		}
		violations, observed, err := drain.CompareRunObserved(load(pair[0]), load(pair[1]))
		if err != nil || len(violations) != 0 || len(observed) != 0 {
			t.Fatalf("fork delivery convicted or was misattributed to a run: violations=%+v observed=%+v error=%v", violations, observed, err)
		}
	}
	setSibling(false)
	if _, err := drain.SweepRefs(context.Background(), drain.RefSweepRequest{Root: f.root, Store: f.store}); err != nil {
		t.Fatal(err)
	}
	if refs := git(t, f.root, "for-each-ref", "--format=%(refname)", marker); refs != "" {
		t.Fatal("fork marker remains after sibling boundary compared")
	}
	// The main checkout's real driver can compare the cache against delivery.
	driver, err := filepath.Abs("../../bin/pi-forks")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "fixture/pi patch/pi\n")
	cmd := exec.Command(driver, "check", "--fork", "pi")
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "PI_FORKS_ROOT="+f.root, "PI_FORKS_SERIES_ROOT="+f.root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pi-forks check: %v: %s", err, output)
	}
	// Reuse a different lane's stale checkout: staging refreshes it to the first delivery.
	secondCheckout := filepath.Join(f.root, ".forks/lanes/lane-3/pi")
	git(t, f.root, "clone", "--quiet", cache, secondCheckout)
	git(t, secondCheckout, "fetch", "--quiet", cache, "+refs/remotes/upstream/*:refs/remotes/upstream/*")
	git(t, secondCheckout, "remote", "add", "upstream", f.fork)
	git(t, secondCheckout, "reset", "--hard", f.forkBase)
	secondSpec := f.spec("lane-3")
	secondSpec.RunID = "run-second-fork"
	if err := f.stager().Stage(context.Background(), secondSpec); err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-3", drain.BaseSHAFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(base)) != first.HeadSHA {
		t.Fatalf("second base=%s want %s", base, first.HeadSHA)
	}
	writeFile(t, filepath.Join(secondCheckout, "fork.txt"), "second delivery\n")
	git(t, secondCheckout, "commit", "-qam", "second fork change")
	second := drain.Candidate{ItemID: "item_041", LaneID: "lane-3", RunID: "run-second-fork", Worktree: secondCheckout, BaseRef: first.BaseRef, BaseSHA: first.HeadSHA, HeadSHA: git(t, secondCheckout, "rev-parse", "HEAD")}
	outcome, err = steps.Gate(context.Background(), second)
	if err != nil || outcome.Outcome != drain.GatePassed {
		t.Fatalf("second build: %+v, %v", outcome, err)
	}
	code, output = land(second)
	if code != 0 {
		t.Fatalf("second landing exited %d: %s", code, output)
	}
	if tip := git(t, cache, "rev-parse", "main"); tip != second.HeadSHA {
		t.Fatal(fmt.Sprintf("cache at %s want second %s", tip, second.HeadSHA))
	}
}
