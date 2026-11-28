package keep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func loadUnrequestedStartRule(t *testing.T) []watch.Rule {
	t.Helper()
	rulePath := filepath.Join(repoRoot(t), "tools", "tillr-watch", "rules", "drain-unrequested-start.toml")
	raw, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := watch.LoadRuleDocument(rulePath, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != "drain-unrequested-start" {
		t.Fatalf("loaded rules = %+v, want single drain-unrequested-start rule", rules)
	}
	return rules
}

func readRestartRequests(t *testing.T, dbPath string) []restart.Request {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var reqs []restart.Request
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, restart.RequestKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var payload []byte
			if err := rows.Scan(&payload); err != nil {
				return err
			}
			var req restart.Request
			if err := json.Unmarshal(payload, &req); err != nil {
				return err
			}
			reqs = append(reqs, req)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return reqs
}

func TestRestartDrainRecordsRequestAndQuietsAlert(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 8, 29, 0, 0, time.UTC)

	// Initialize store with a prior drain.build-started (initial=true) so any subsequent
	// start without provenance opens drain-unrequested-start.
	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return restart.PublishBuildTx(tx, restart.BuildFact{
			Role:    restart.RoleDrain,
			Initial: true,
			PID:     1001,
			At:      now.Add(-10 * time.Minute),
		})
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	rules := loadUnrequestedStartRule(t)
	evalNow := func(at time.Time) int {
		t.Helper()
		st, err := store.Open(ctx, dbPath, true)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		e := watch.Evaluator{Store: st, Rules: rules, Clock: func() time.Time { return at }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "tillr-triage")
		if err != nil {
			t.Fatal(err)
		}
		return len(notices)
	}

	// Baseline: initial start produces 0 notices.
	if got := evalNow(now); got != 0 {
		t.Fatalf("baseline notices = %d, want 0", got)
	}

	// Run RestartDrain with no running loop (the exact case where tillr-drain stop is skipped).
	var out bytes.Buffer
	err = RestartDrain(root, nil, filepath.Join(root, ".tillr", "keeper.lock"), RestartOpts{
		FindLoopsFn:      func(string) ([]int, error) { return nil, nil },
		FindKeepersFn:    func(string) ([]int, error) { return nil, nil },
		StopKeeperFn:     func(string, io.Writer) error { return nil },
		WaitKeeperExitFn: func(string, time.Duration) error { return nil },
		StartKeeperFn: func(argv []string, dir string, logPath string) (int, error) {
			return 2002, nil
		},
	}, &out)
	if err != nil {
		t.Fatalf("RestartDrain failed: %v\n%s", err, out.String())
	}

	reqs := readRestartRequests(t, dbPath)
	if len(reqs) != 1 {
		t.Fatalf("got %d restart requests, want 1: %+v", len(reqs), reqs)
	}
	if reqs[0].Role != restart.RoleDrain || reqs[0].By != "operator" || reqs[0].Why == "" {
		t.Fatalf("restart request = %+v, want role=drain, by=operator, non-empty why", reqs[0])
	}

	// A second call from the spawned keeper's initial launch must not duplicate the request.
	if err := recordOperatorStartRequest(root, nil, "operator start (tillr-drain keep)"); err != nil {
		t.Fatalf("recordOperatorStartRequest after RestartDrain: %v", err)
	}
	if reqsAfter := readRestartRequests(t, dbPath); len(reqsAfter) != 1 {
		t.Fatalf("duplicate request filed by spawned keeper: got %d, want 1", len(reqsAfter))
	}

	// Simulate the started loop publishing drain.build-started after checking PendingAfterBuild.
	st, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	var pickedID string
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		pending, ok, err := restart.PendingAfterBuild(tx.Reader(), restart.RoleDrain)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("expected pending restart request after RestartDrain")
		}
		pickedID = pending.ID
		return restart.PublishBuildTx(tx, restart.BuildFact{
			Role:           restart.RoleDrain,
			RestartRequest: pending.ID,
			PID:            2003,
			At:             now.Add(time.Second),
		})
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	if pickedID != reqs[0].ID {
		t.Fatalf("picked restart_request = %q, want %q", pickedID, reqs[0].ID)
	}

	// Evaluating drain-unrequested-start after the requested start must open 0 alerts.
	if got := evalNow(now.Add(2 * time.Second)); got != 0 {
		t.Fatalf("notices after requested restart = %d, want 0", got)
	}

	// Now simulate an unrequested loop start (no recorded request).
	st, err = store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		if _, ok, err := restart.PendingAfterBuild(tx.Reader(), restart.RoleDrain); err != nil {
			return err
		} else if ok {
			t.Fatalf("unexpected pending restart request for unrequested start")
		}
		return restart.PublishBuildTx(tx, restart.BuildFact{
			Role: restart.RoleDrain,
			PID:  2004,
			At:   now.Add(3 * time.Second),
		})
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	// Evaluating drain-unrequested-start after an unrequested start must open 1 alert.
	if got := evalNow(now.Add(4 * time.Second)); got != 1 {
		t.Fatalf("notices after unrequested start = %d, want 1", got)
	}
}

func TestOperatorKeepRecordsRequestOnInitialLaunchOnly(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return restart.PublishBuildTx(tx, restart.BuildFact{
			Role:    restart.RoleDrain,
			Initial: true,
			PID:     1001,
			At:      time.Now().UTC().Add(-time.Minute),
		})
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	if err := recordOperatorStartRequest(root, nil, "operator start (tillr-drain keep)"); err != nil {
		t.Fatalf("recordOperatorStartRequest: %v", err)
	}
	reqs := readRestartRequests(t, dbPath)
	if len(reqs) != 1 {
		t.Fatalf("got %d restart requests, want 1", len(reqs))
	}
	if reqs[0].By != "operator" || reqs[0].Why != "operator start (tillr-drain keep)" {
		t.Fatalf("request = %+v, want by=operator why=%q", reqs[0], "operator start (tillr-drain keep)")
	}
}

func TestJustTillrRestartEndToEndRecordsRequestAndQuietsAlert(t *testing.T) {
	ctx := context.Background()
	drainBin := buildDrainBinary(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	root := filepath.Join(work, "repo")
	live := filepath.Join(work, "bin")
	if err := os.MkdirAll(filepath.Join(root, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(root, ".tillr", "state.db")
	now := time.Now().UTC()
	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return restart.PublishBuildTx(tx, restart.BuildFact{
			Role:    restart.RoleDrain,
			Initial: true,
			PID:     1001,
			At:      now.Add(-5 * time.Minute),
		})
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	pidFile := filepath.Join(root, ".tillr", "stub.pid")
	drainStub := filepath.Join(live, "tillr-drain")
	stubScript := fmt.Sprintf(`#!/usr/bin/env bash
case "$1" in
  keep)
    exec %q "$@"
    ;;
  loop)
    export TILLR_KEEP_TEST_HELPER=drain-stub
    export TILLR_KEEP_TEST_PROC_NAME=tillr-drain
    export TILLR_KEEP_TEST_PID_FILE=%q
    exec -a %q %q "$@"
    ;;
  stop)
    exit 2
    ;;
esac
`, drainBin, pidFile, drainStub, self)
	if err := os.WriteFile(drainStub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(),
		"YOLO_DURABLE_DIR="+work,
		"PATH="+live+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	cmd := exec.Command("just", "--justfile", filepath.Join(repoRoot(t), ".justfile"), "tillr-restart", "--root", root)
	cmd.Dir = root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("just tillr-restart failed: %v\n%s", err, out)
	}
	defer func() {
		stopCmd := exec.Command(drainBin, "keep", "--root", root, "--stop")
		stopCmd.Dir = root
		stopCmd.Env = env
		_ = stopCmd.Run()
		if raw, err := os.ReadFile(pidFile); err == nil {
			var pid int
			if _, scanErr := fmt.Sscanf(string(bytes.TrimSpace(raw)), "%d", &pid); scanErr == nil && pid > 0 {
				_ = signalProcessSIGTERM(pid, "")
			}
		}
	}()

	// Wait for the keeper started by `just tillr-restart` to launch the loop stub.
	deadline := time.Now().Add(hangGuard)
	for {
		if data, err := os.ReadFile(pidFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for restarted loop stub to start\njust output:\n%s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}

	reqs := readRestartRequests(t, dbPath)
	if len(reqs) != 1 {
		t.Fatalf("got %d restart requests from just tillr-restart, want 1: %+v", len(reqs), reqs)
	}
	if reqs[0].By != "operator" || reqs[0].Why == "" {
		t.Fatalf("just tillr-restart request = %+v, want by=operator and non-empty why", reqs[0])
	}
}
