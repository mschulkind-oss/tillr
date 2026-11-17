package drain

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRedCapZeroStillReportsLandingsSinceGreen(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "one")
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "status.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "fixture", "fixture-run", tip, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		close(entered)
		<-release
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	var report bytes.Buffer
	policy := GatePolicy{LandingStrategy: "after-merge"} // both zero: no refusal
	l := &Loop{gatePolicy: policy, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: &report}}
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	<-entered
	// The judge cannot report while blocked, so this reads only the pass-owned line.
	if !strings.Contains(report.String(), "landings_since=1") {
		close(release)
		l.heavyWG.Wait()
		t.Fatalf("zero cap hides count: %s", report.String())
	}
	close(release)
	l.heavyWG.Wait()
}
