package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func seedWorkerStart(t *testing.T, tx *store.Tx, laneID, itemID, runID string, owner *LoopOwner) {
	t.Helper()
	payload := map[string]any{
		"decision": map[string]any{
			"item_id": itemID,
			"lane_id": laneID,
			"run_request": map[string]any{
				"run_id":  runID,
				"item_id": itemID,
				"lane_id": laneID,
				"backend": "fixture",
			},
		},
	}
	if owner != nil {
		payload["loop_owner"] = owner
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Append(event.Event{
		Kind:       dispatchStartedKind,
		ObjectType: "run",
		ObjectID:   &runID,
		Payload:    raw,
		At:         time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES(?, 'leased', ?, NULL)`, laneID, runID); err != nil {
		t.Fatal(err)
	}
}

func startMarkedProcess(t *testing.T, storePath, runID string) *exec.Cmd {
	t.Helper()
	identity, err := processStoreIdentity(storePath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "60")
	cmd.Env = append(withoutProcessEnv(os.Environ(), "TILLR_RUN_ID", "TILLR_LOOP_STORE"),
		"TILLR_RUN_ID="+runID,
		"TILLR_LOOP_STORE="+identity,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting marked process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return cmd
}

func TestWorkersReportsDeadFixtureRunAndLiveProcessWithoutALoop(t *testing.T) {
	dir := scratchUnderDurable(t)
	storePath := filepath.Join(dir, "state.db")
	loopDir := filepath.Join(dir, "loop")
	ctx := context.Background()

	s, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	deadOwner := *here
	deadOwner.PID = 999999999
	deadOwner.Start = "0"

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		seedWorkerStart(t, tx, "lane-1", "item-dead", "run-dead", &deadOwner)
		seedWorkerStart(t, tx, "lane-2", "item-live", "run-live", &deadOwner)
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-3', 'idle', NULL, NULL)`); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	child := startMarkedProcess(t, storePath, "run-live")

	ro, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	var got []Worker
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var rerr error
		got, rerr = Workers(r, storePath, loopDir)
		return rerr
	}); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("Workers = %+v, want 2 rows", got)
	}
	if got[0] != (Worker{Lane: "lane-1", Item: "item-dead", Run: "run-dead", PID: 0, Alive: false}) {
		t.Errorf("got[0] = %+v, want lane-1 dead", got[0])
	}
	if got[1] != (Worker{Lane: "lane-2", Item: "item-live", Run: "run-live", PID: child.Process.Pid, Alive: true}) {
		t.Errorf("got[1] = %+v, want lane-2 live with pid %d", got[1], child.Process.Pid)
	}

	// Once the process exits, Workers immediately reports it as gone without any loop.
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var rerr error
		got, rerr = Workers(r, storePath, loopDir)
		return rerr
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Alive || got[1].PID != 0 {
		t.Fatalf("after process exit, Workers = %+v, want lane-2 dead with pid 0", got)
	}
}

func TestWorkersForeignDomainSessionRelaunch(t *testing.T) {
	dir := scratchUnderDurable(t)
	storePath := filepath.Join(dir, "state.db")
	loopDir := filepath.Join(dir, "loop")
	ctx := context.Background()

	s, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	boot, _, _ := strings.Cut(here.Domain, ":")
	otherNS := *here
	otherNS.Domain = boot + ":pid:[999999999]"
	otherNS.PID = 4242

	rebooted := *here
	rebooted.Domain = "other-boot:pid:[1]"
	rebooted.PID = 4243

	// Dead foreign run: stream file exists with no writer (as after a session relaunch).
	deadStream := filepath.Join(loopDir, "run-relaunch-dead", "run.stderr")
	if err := os.MkdirAll(filepath.Dir(deadStream), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deadStream, []byte("interrupted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Live foreign run: stream file is held open for writing.
	liveStream := filepath.Join(loopDir, "run-relaunch-live", "run.stderr")
	if err := os.MkdirAll(filepath.Dir(liveStream), 0o755); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(liveStream, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		seedWorkerStart(t, tx, "lane-1", "item-relaunch-dead", "run-relaunch-dead", &otherNS)
		seedWorkerStart(t, tx, "lane-2", "item-relaunch-live", "run-relaunch-live", &otherNS)
		seedWorkerStart(t, tx, "lane-3", "item-rebooted", "run-rebooted", &rebooted)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	var got []Worker
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var rerr error
		got, rerr = Workers(r, storePath, loopDir)
		return rerr
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("Workers = %+v, want 3 rows", got)
	}
	if got[0] != (Worker{Lane: "lane-1", Item: "item-relaunch-dead", Run: "run-relaunch-dead", PID: 0, Alive: false}) {
		t.Errorf("got[0] = %+v, want dead relaunched run", got[0])
	}
	if got[1] != (Worker{Lane: "lane-2", Item: "item-relaunch-live", Run: "run-relaunch-live", PID: 0, Alive: true}) {
		t.Errorf("got[1] = %+v, want live foreign run", got[1])
	}
	if got[2] != (Worker{Lane: "lane-3", Item: "item-rebooted", Run: "run-rebooted", PID: 0, Alive: false}) {
		t.Errorf("got[2] = %+v, want dead rebooted run", got[2])
	}
}

func TestWorkersIncludesUnsettledReadOnlyRunsAndEmptyStore(t *testing.T) {
	dir := scratchUnderDurable(t)
	storePath := filepath.Join(dir, "state.db")
	ctx := context.Background()

	s, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ro, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	var got []Worker
	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var rerr error
		got, rerr = Workers(r, storePath, filepath.Join(dir, "loop"))
		return rerr
	}); err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("empty store Workers = %#v, want empty non-nil slice", got)
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-1', 'leased', 'run-ro-1', NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled) VALUES
			('run-ro-1', 'grp-1', 'item-a', 'lane-1', 0),
			('run-ro-2', 'grp-1', 'item-b', 'lane-1', 0),
			('run-ro-done', 'grp-1', 'item-c', 'lane-1', 1)`); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		var rerr error
		got, rerr = Workers(r, storePath, filepath.Join(dir, "loop"))
		return rerr
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Workers = %+v, want 2 unsettled read-only runs", got)
	}
	if got[0] != (Worker{Lane: "lane-1", Item: "item-a", Run: "run-ro-1", PID: 0, Alive: false}) {
		t.Errorf("got[0] = %+v, want run-ro-1", got[0])
	}
	if got[1] != (Worker{Lane: "lane-1", Item: "item-b", Run: "run-ro-2", PID: 0, Alive: false}) {
		t.Errorf("got[1] = %+v, want run-ro-2", got[1])
	}
}
