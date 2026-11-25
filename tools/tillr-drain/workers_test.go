package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkersFaceListsFixtureRunWhoseProcessHasGoneAndLiveWorker(t *testing.T) {
	dir := scratch(t, "workers-cli")
	storePath := filepath.Join(dir, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seed := func(tx *store.Tx, laneID, itemID, runID string) error {
		raw, err := json.Marshal(map[string]any{
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
		})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event.Event{
			Kind:       "drain.started",
			ObjectType: "run",
			ObjectID:   &runID,
			Payload:    raw,
			At:         now,
		}); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES(?, 'leased', ?, NULL)`, laneID, runID)
		return err
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := seed(tx, "lane-1", "item-gone", "run-gone"); err != nil {
			return err
		}
		return seed(tx, "lane-2", "item-live", "run-live")
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	identity, err := presence.StoreIdentity(storePath)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "60")
	child.Env = append(os.Environ(), "TILLR_RUN_ID=run-live", "TILLR_LOOP_STORE="+identity)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.Process != nil {
			_ = child.Process.Kill()
		}
		_ = child.Wait()
	})

	// JSON output via --root (default store resolution) while no loop is running.
	code, out := invoke(t, "workers", "--root", dir, "--json")
	if code != 0 {
		t.Fatalf("workers --json exited %d: %s", code, out)
	}
	var got []drain.Worker
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding workers JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("workers = %+v, want 2 rows", got)
	}
	if got[0] != (drain.Worker{Lane: "lane-1", Item: "item-gone", Run: "run-gone", PID: 0, Alive: false}) {
		t.Errorf("got[0] = %+v, want lane-1 dead", got[0])
	}
	if got[1] != (drain.Worker{Lane: "lane-2", Item: "item-live", Run: "run-live", PID: child.Process.Pid, Alive: true}) {
		t.Errorf("got[1] = %+v, want lane-2 live pid %d", got[1], child.Process.Pid)
	}

	// Plain text output via --store.
	code, out = invoke(t, "workers", "--store", storePath)
	if code != 0 {
		t.Fatalf("workers exited %d: %s", code, out)
	}
	wantText := "lane-1\titem-gone\trun-gone\t-\tdead\n" +
		"lane-2\titem-live\trun-live\t" + strconv.Itoa(child.Process.Pid) + "\talive\n"
	if string(out) != wantText {
		t.Fatalf("workers text =\n%q\nwant:\n%q", string(out), wantText)
	}

	// Help surface.
	code, out = invoke(t, "workers", "--help")
	if code != 0 {
		t.Fatalf("workers --help exited %d: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of tillr-drain workers") {
		t.Errorf("workers --help missing usage: %s", out)
	}
}
