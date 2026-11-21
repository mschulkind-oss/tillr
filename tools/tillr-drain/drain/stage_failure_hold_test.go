package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr/store"
)

func TestForkStagingFaultFreesClassifiersLaneForOtherWork(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["classify"] = "fixture"
	if err := os.Mkdir(filepath.Join(f.root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", "bin/pi-forks.list", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "fork catalog")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET workflow='single-worker@v1' WHERE item_id='item-a'`)
		return err
	})
	fork := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-qb", "main")
	runGit(t, fork, "commit", "--allow-empty", "-qm", "delivered base")
	runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	runGit(t, fork, "commit", "--allow-empty", "-qm", "unknown lane work")
	head := runGit(t, fork, "rev-parse", "HEAD")
	l.cfg.Steps = &mixedDeliverySteps{workflowSteps: steps}
	for i := 0; i < 8; i++ {
		graphPass(t, l, &LoopResult{})
		if planFor(t, l, "item-a").Reason == ReasonStageFailed {
			break
		}
	}
	if d := planFor(t, l, "item-a"); d.Action != ActionHold || d.Reason != ReasonStageFailed {
		t.Fatalf("fork item = %+v, want operator staging hold", d)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue' WHERE item_id='item-b'`)
		return err
	})
	claimed := false
	for _, p := range mustPlan(t, l) {
		if d := p.decision; d.ItemID == "item-b" {
			if d.Action != ActionDispatch || d.LaneID != "lane-1" {
				t.Fatalf("other item = %+v, want dispatch on the ordinary lane", d)
			}
			ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", "HEAD"), l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("other item could not claim the freed lane: (%v, %v)", ok, err)
			}
			claimed = true
		}
	}
	if !claimed {
		t.Fatal("other item was not planned")
	}
	if got := runGit(t, fork, "rev-parse", "HEAD"); got != head {
		t.Fatalf("unretained fork work moved from %s to %s", head, got)
	}

	// Once cleared, the writer must still be the classifier's successor. It
	// can use another fork checkout even if that lane has an old target marker.
	const delivery = "refs/heads/patch/pi"
	base := runGit(t, f.root, "rev-parse", delivery)
	fork2 := filepath.Join(f.root, ".forks/lanes/lane-2/pi")
	if err := os.MkdirAll(filepath.Dir(fork2), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "clone", "--quiet", "--branch", "patch/pi", f.root, fork2)
	runGit(t, fork2, "branch", "-m", "main")
	tip := runGit(t, f.root, "commit-tree", base+"^{tree}", "-p", base, "-m", "new delivery")
	runGit(t, f.root, "update-ref", delivery, tip, base)
	writeForkFile(t, filepath.Join(f.laneRoot, "lane-2", TargetFile), fork2+"\n")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		fault := mustFaults(t, tx.Reader())["item-a"]
		return ClearItemFault(tx, "item-a", fault.Seq, l.cfg.Now())
	})
	staged := false
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID != "item-a" {
			continue
		}
		if d.Action != ActionDispatch || d.LaneID != "lane-2" || d.NodeIndex != 1 {
			t.Fatalf("cleared item = %+v, want writer successor on lane-2", d)
		}
		ctx := context.Background()
		trunk := runGit(t, f.root, "rev-parse", "HEAD")
		ok, work, err := l.applyPlan(ctx, p, trunk, l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("cleared item claim: (%v, %v)", ok, err)
		}
		prepared, err := l.stageDispatches(ctx, []Decision{work.Decision}, trunk, &LoopResult{})
		if err != nil || len(prepared) != 1 || prepared[0].NodeBaseSHA != tip {
			t.Fatalf("cleared item did not stage at current delivery %s: %+v, %v", tip, prepared, err)
		}
		staged = true
	}
	if !staged {
		t.Fatal("cleared item was not planned")
	}
}

type perItemENAMETOOLONGStager struct {
	failingItem string
}

func (s *perItemENAMETOOLONGStager) Stage(_ context.Context, spec StageSpec) error {
	if spec.ItemID == s.failingItem {
		return &os.PathError{
			Op:   "mkdir",
			Path: filepath.Join("/lanes", spec.LaneID, "durable", spec.RunID),
			Err:  syscall.ENAMETOOLONG,
		}
	}
	return nil
}

// A per-item staging wrapper failure (blocking RunDir for item-a with a regular file so
// pipelineStart fails) holds only item-a with ReasonStageFailed and a named operator mover,
// retracts item-a's unlaunched claim, frees lane-1, counts zero infrastructure failures,
// and dispatches item-b on lane-1 in the same pass without ending the loop.
func TestStagingRunDirWrapperFailureHoldsItemWithNamedMoverAndKeepsLoopRunning(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "item-a", "item-b")
	steps := &recordingSteps{}
	loop := f.loop(t, steps, stubStager{})
	loop.cfg.Lanes = []string{"lane-1"}
	var report bytes.Buffer
	loop.cfg.Report = &report

	blockedRunDir := RunDir(f.root, "run-item-a-a1")
	if err := os.MkdirAll(filepath.Dir(blockedRunDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedRunDir, []byte("not-a-dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	stop, err := loop.pass(context.Background(), &result)
	for len(loop.active) > 0 {
		completion := <-loop.completions
		if _, cErr := loop.takeCompletion(completion, &result); cErr != nil {
			t.Fatalf("takeCompletion failed: %v", cErr)
		}
	}
	if err != nil {
		t.Fatalf("staging failure on item-a ended the loop: %v", err)
	}
	if stop != "" {
		t.Fatalf("loop stopped %q, want empty stop so the loop keeps running", stop)
	}
	if result.InfraFailures != 0 {
		t.Fatalf("InfraFailures = %d, want 0", result.InfraFailures)
	}
	if got := steps.items(); !reflect.DeepEqual(got, []string{"item-b"}) {
		t.Fatalf("dispatched items = %v, want [item-b] on the freed lane", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, ok := faults["item-a"]
		if !ok || fault.Reason != ReasonStageFailed || fault.RunID != "run-item-a-a1" || fault.LaneID != "lane-1" {
			t.Fatalf("item-a fault = %+v (ok=%v), want stage-failed on run-item-a-a1 / lane-1", fault, ok)
		}
		wantCommand := fmt.Sprintf("tillr drain clear-fault --store '%s' --item 'item-a' --fault %d", f.storePath, fault.Seq)
		if !strings.Contains(report.String(), "loop: item-a mover: fix the staging fault the detail names, then "+wantCommand+"\n") {
			t.Fatalf("loop report missing named mover with %q: %s", wantCommand, report.String())
		}
		if !strings.Contains(fault.Detail, "start stage pipeline") {
			t.Fatalf("item-a fault detail = %q, want start stage pipeline detail", fault.Detail)
		}
		if _, heldB := faults["item-b"]; heldB {
			t.Fatalf("sibling item-b unexpectedly held: %+v", faults["item-b"])
		}

		var dispatchesA int
		if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE item_id='item-a'`).Scan(&dispatchesA); err != nil {
			return err
		}
		if dispatchesA != 0 {
			t.Fatalf("item-a retained %d unlaunched dispatch rows, want 0", dispatchesA)
		}

		answer, err := Why(r, Pass{StorePath: f.storePath, Now: loop.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), AvailableLanes: &loop.cfg.Lanes}, loop.cfg.Policy, nil)
		if err != nil {
			return err
		}
		rule := holdrules.For(ReasonStageFailed)
		found := false
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			found = true
			if row.Reason != ReasonStageFailed || row.Class != rule.Class || row.Where != rule.Where || !strings.HasSuffix(row.Mover, wantCommand) {
				t.Fatalf("why row = (%q, %q, %q, %q), want rule %+v with %q", row.Reason, row.Class, row.Where, row.Mover, rule, wantCommand)
			}
			if !strings.Contains(row.Why, fault.Detail) {
				t.Fatalf("why = %q, missing fault detail %q", row.Why, fault.Detail)
			}
		}
		if !found {
			t.Fatal("Why() returned no row for held item-a")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// An ENAMETOOLONG staging error on one item holds only that item with ReasonStageFailed and
// a named operator mover, leaves the loop running so its sibling dispatches in the same pass,
// and releases the item once the fault is cleared.
func TestStagingENAMETOOLONGFailureHoldsOnlyThatItemAndContinuesLoop(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 2, "item-a", "item-b")
	steps := &recordingSteps{}
	stager := &perItemENAMETOOLONGStager{failingItem: "item-a"}
	loop := f.loop(t, steps, stager)
	// Start with only lane-1 so item-b can dispatch in pass 1 only if item-a's failed
	// staging retracts its claim and frees lane-1.
	loop.cfg.Lanes = []string{"lane-1"}

	drainActive := func(res *LoopResult) {
		for len(loop.active) > 0 {
			completion := <-loop.completions
			if _, cErr := loop.takeCompletion(completion, res); cErr != nil {
				t.Fatalf("takeCompletion failed: %v", cErr)
			}
		}
	}
	var res1 LoopResult
	stop, err := loop.pass(context.Background(), &res1)
	drainActive(&res1)
	if err != nil || stop != "" {
		t.Fatalf("pass 1 = (%q, %v), want empty stop and nil error", stop, err)
	}
	if res1.InfraFailures != 0 {
		t.Fatalf("pass 1 InfraFailures = %d, want 0", res1.InfraFailures)
	}
	if got := steps.items(); !reflect.DeepEqual(got, []string{"item-b"}) {
		t.Fatalf("pass 1 dispatched = %v, want [item-b]", got)
	}

	// Offer free lane-2: item-a must remain held until clear-fault acknowledges the fault,
	// even after the underlying staging error is repaired.
	loop.cfg.Lanes = []string{"lane-1", "lane-2"}
	stager.failingItem = ""
	var res2 LoopResult
	if _, err := loop.pass(context.Background(), &res2); err != nil {
		t.Fatalf("pass 2 failed: %v", err)
	}
	drainActive(&res2)
	if got := steps.items(); !reflect.DeepEqual(got, []string{"item-b"}) {
		t.Fatalf("held item-a dispatched before clear-fault: %v", got)
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		fault, ok := faults["item-a"]
		if !ok || fault.Reason != ReasonStageFailed {
			t.Fatalf("item-a fault before clear = %+v (ok=%v)", fault, ok)
		}
		return ClearItemFault(tx, "item-a", fault.Seq, loop.cfg.Now())
	})

	var res3 LoopResult
	if _, err := loop.pass(context.Background(), &res3); err != nil {
		t.Fatalf("pass 3 after clear-fault failed: %v", err)
	}
	drainActive(&res3)
	got := steps.items()
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"item-a", "item-b"}) {
		t.Fatalf("after clear-fault, dispatched = %v, want [item-a item-b]", got)
	}
}
