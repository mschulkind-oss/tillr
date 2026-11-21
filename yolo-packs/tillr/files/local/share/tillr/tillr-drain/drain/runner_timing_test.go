package drain

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRunnerProofTimeIsCountedApartAndNeverFailsALanding(t *testing.T) {
	path := filepath.Join(scratchUnderDurable(t), "store.db")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	pass := PassTiming{TickID: "pass", AtMS: 1000, GateBudgetMS: 10000, DecisionCounts: map[string]int{"judge": 2}}
	if err := recordPassTiming(context.Background(), path, pass); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	l := &Loop{cfg: LoopConfig{Store: path, Report: &report}, gateBudget: 10 * time.Second}
	for _, runner := range []bool{true, false} {
		timing := PassTiming{TickID: "pass", AtMS: 1000, GateBudgetMS: 10000}
		if runner {
			timing.addGateTime(120000, runner)
		} else {
			timing.addGateTime(1000, runner)
		}
		timing.addLandTime(200, runner)
		if err := l.recordPass(&timing); err != nil {
			t.Fatal(err)
		}
	}
	got, err := aggregatePassTiming(context.Background(), path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	if got.GateMS != 1000 || got.Gates != 1 || got.LandMS != 200 || got.RunnerGateMS != 120000 || got.RunnerGates != 1 || got.RunnerLandMS != 200 || got.IsOverBudget() {
		t.Fatalf("mixed measurements: %+v", got)
	}
	if !strings.Contains(report.String(), "runner changes 1 gate 120.0s land 0.2s (outside ordinary budget)") {
		t.Fatalf("hidden runner cost: %s", report.String())
	}
	s, err = store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("runner duration filed %d items", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerTimingUpgradeReplacesTheLegacyCopyTrigger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratchUnderDurable(t), "legacy-trigger.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER drain_copy_legacy_claim_passes;
CREATE TRIGGER drain_copy_legacy_passes BEFORE INSERT ON schema_migrations BEGIN
 INSERT OR IGNORE INTO drain_passes
 SELECT tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
 gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count
 FROM drain_ticks;
END;
CREATE TRIGGER drain_copy_legacy_runner_passes BEFORE INSERT ON schema_migrations BEGIN
 INSERT OR IGNORE INTO drain_passes
 (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
  gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count,
  runner_gate_count, runner_gate_ms, runner_land_ms)
 SELECT tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
 gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count,
 runner_gate_count, runner_gate_ms, runner_land_ms
 FROM drain_ticks;
END;`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var legacy, runner, current int
		if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='drain_copy_legacy_passes'`).Scan(&legacy); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='drain_copy_legacy_runner_passes'`).Scan(&runner); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='drain_copy_legacy_claim_passes'`).Scan(&current); err != nil {
			return err
		}
		if legacy != 0 || runner != 0 || current != 1 {
			t.Errorf("after the upgrade the legacy/runner/current copy trigger counts are %d/%d/%d, want 0/0/1", legacy, runner, current)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerChangePaths(t *testing.T) {
	for _, path := range []string{"tools/tillr-gate/main.go", "tools/gate/checks/go-build.py", "tools/fork-check.py", "tests/fork-check.test.py", "yolo-packs/tillr/files/local/share/tillr/tillr-gate/main.go"} {
		if !runnerProofPaths(path + "\x00") {
			t.Errorf("runner path not classified: %s", path)
		}
	}
	for _, path := range []string{"tools/tillr-gateway/main.go", "docs/example.md", "tools/tillr-drain/drain/loop.go"} {
		if runnerProofPaths(path + "\x00") {
			t.Errorf("ordinary path classified as runner: %s", path)
		}
	}
}

func TestClaimMSAndAsyncStageMSAccumulation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratchUnderDurable(t), "claim-timing.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Claims == 0 stores NULL claim_ms.
	if err := recordPassTiming(ctx, path, PassTiming{
		TickID: "gen-no-claim", AtMS: 1000, StageMS: 5, GateBudgetMS: 10000,
		DecisionCounts: map[string]int{"hold": 1},
	}); err != nil {
		t.Fatal(err)
	}
	// Claims > 0 stores ClaimMS (including 0).
	if err := recordPassTiming(ctx, path, PassTiming{
		TickID: "gen-zero-claim", AtMS: 2000, ClaimMS: 0, Claims: 1, StageMS: 10, GateBudgetMS: 10000,
		DecisionCounts: map[string]int{"dispatch": 1},
	}); err != nil {
		t.Fatal(err)
	}
	zero, err := aggregatePassTiming(ctx, path, "gen-zero-claim")
	if err != nil || zero.Claims != 1 || zero.ClaimMS != 0 {
		t.Fatalf("measured zero did not round trip: %+v, %v", zero, err)
	}
	// Async updates add stage time but keep the longest measured claim.
	for _, update := range []PassTiming{
		{TickID: "gen-zero-claim", ClaimMS: 7, Claims: 1, StageMS: 15},
		{TickID: "gen-zero-claim", ClaimMS: 3, Claims: 1, StageMS: 4},
		{TickID: "gen-zero-claim", StageMS: 1},
	} {
		if err := recordPassTiming(ctx, path, update); err != nil {
			t.Fatal(err)
		}
	}

	s, err = store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var claim sql.NullInt64
		var stage int64
		if err := r.QueryRow(`SELECT claim_ms, stage_ms FROM drain_passes WHERE tick_id = 'gen-no-claim'`).Scan(&claim, &stage); err != nil {
			return err
		}
		if claim.Valid || stage != 5 {
			t.Errorf("gen-no-claim: claim=%v stage=%d, want NULL/5", claim, stage)
		}
		if err := r.QueryRow(`SELECT claim_ms, stage_ms FROM drain_passes WHERE tick_id = 'gen-zero-claim'`).Scan(&claim, &stage); err != nil {
			return err
		}
		if !claim.Valid || claim.Int64 != 7 || stage != 30 {
			t.Errorf("gen-zero-claim: claim=%v stage=%d, want 7/30", claim, stage)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
