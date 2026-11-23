package presence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func init() {
	for _, kind := range []string{"drain.started", "run.resume"} {
		_ = event.Register(kind, "presence-test")
	}
}

func TestSameBoot(t *testing.T) {
	if !SameBoot("boot-a:pid:[1]", "boot-a:pid:[2]") {
		t.Fatal("same boot prefix returned false")
	}
	if SameBoot("boot-a:pid:[1]", "boot-b:pid:[1]") {
		t.Fatal("different boot prefix returned true")
	}
	if !SameBoot("unknown", "boot-b:pid:[1]") {
		t.Fatal("missing colon treated as reboot")
	}
	if !SameBoot(":pid:[1]", "boot-b:pid:[1]") {
		t.Fatal("empty boot prefix treated as reboot")
	}
}

func openPresenceTestStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS lanes (
			lane_id TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			run_id TEXT,
			leased_until INTEGER
		)`); err != nil {
			return err
		}
		_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS drain_sessions (
			session_id TEXT PRIMARY KEY,
			started_at INTEGER NOT NULL,
			refreshed_at INTEGER NOT NULL,
			next_pass_at INTEGER NOT NULL,
			loop_owner TEXT,
			ended_at INTEGER
		)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, s
}

func TestEndedSessionOwnersLatestWins(t *testing.T) {
	path, s := openPresenceTestStore(t)
	here, err := CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	foreign := *here
	boot, ns, _ := cutDomain(here.Domain)
	foreign.Domain = boot + ":" + ns + "-old"
	foreign.PID = 99999
	raw, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, ended_at) VALUES('s1', 100, 100, 100, ?, NULL)`, string(raw)); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, ended_at) VALUES('s2', 200, 200, 200, ?, 250)`, string(raw))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		ended, err := EndedSessionOwners(r)
		if err != nil {
			return err
		}
		if !ended[foreign] {
			t.Fatalf("EndedSessionOwners[%+v] = false, want true when latest session ended", foreign)
		}
		if !SessionOwnerGone(r, path, foreign) {
			t.Fatalf("SessionOwnerGone = false, want true when latest session ended")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestForeignOwnerGoneAndForeignRunLiveAcrossReboot(t *testing.T) {
	here, err := CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	rebooted := *here
	rebooted.Domain = "different-boot:pid:[1]"
	if !ForeignOwnerGone("/tmp/nonexistent", rebooted, *here, map[Owner]bool{}) {
		t.Fatal("ForeignOwnerGone returned false after reboot")
	}
	if ForeignRunLive(nil, false, rebooted.Domain, here.Domain) {
		t.Fatal("ForeignRunLive returned true after reboot even without locator")
	}
	if !ForeignRunLive(nil, false, here.Domain+"-other", here.Domain) {
		t.Fatal("ForeignRunLive returned false on same boot without locator")
	}
}

func TestOpenForWritingTreatsENAMETOOLONGAsAbsent(t *testing.T) {
	here, err := CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	boot, _, _ := cutDomain(here.Domain)
	otherDomain := boot + ":pid:[1]"
	oversizedRunID := "run-item-" + strings.Repeat("failover-", 28) + "restart"
	if len(oversizedRunID) <= 255 {
		t.Fatalf("oversizedRunID len = %d, want > 255", len(oversizedRunID))
	}
	path := filepath.Join(t.TempDir(), oversizedRunID, "run.record.json")
	state, err := OpenForWriting(path)
	if err != nil || state != StreamAbsent {
		t.Fatalf("OpenForWriting(ENAMETOOLONG) = (%v, %v), want (%v, nil)", state, err, StreamAbsent)
	}
	if ForeignRunLive([]string{path}, true, otherDomain, here.Domain) {
		t.Fatal("ForeignRunLive returned true for a stream path that cannot exist (ENAMETOOLONG)")
	}
}

func TestDeadRunsReplayAndProtection(t *testing.T) {
	path, s := openPresenceTestStore(t)
	here, err := CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	rebooted := *here
	rebooted.Domain = "old-boot:pid:[1]"
	rebooted.PID = 4242

	now := time.Now().UTC()
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		// 1. Rebooted owner's run -> dead.
		p1, _ := json.Marshal(map[string]any{
			"loop_owner": rebooted,
			"decision": map[string]any{
				"item_id":     "item-1",
				"lane_id":     "lane-1",
				"run_request": map[string]any{"run_id": "run-1", "item_id": "item-1", "lane_id": "lane-1", "backend": "fixture"},
			},
		})
		id1 := "run-1"
		if _, err := tx.Append(event.Event{Kind: "drain.started", ObjectType: "run", ObjectID: &id1, Payload: p1, At: now}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-1', 'leased', 'run-1', NULL)`); err != nil {
			return err
		}

		// 2. Resumed run whose owner was rebooted -> run.resume carries owner -> dead.
		p2, _ := json.Marshal(map[string]any{
			"loop_owner": rebooted,
			"decision": map[string]any{
				"item_id":     "item-2",
				"lane_id":     "lane-2",
				"run_request": map[string]any{"run_id": "run-2", "item_id": "item-2", "lane_id": "lane-2", "backend": "fixture"},
			},
		})
		id2 := "run-2"
		if _, err := tx.Append(event.Event{Kind: "drain.started", ObjectType: "run", ObjectID: &id2, Payload: p2, At: now}); err != nil {
			return err
		}
		resPayload, _ := json.Marshal(map[string]any{
			"run_id":     "run-2",
			"item_id":    "item-2",
			"lane_id":    "lane-2",
			"backend":    "fixture",
			"session_id": "33b5f2d2-0cd4-4a49-b293-c58f5635561f",
			"resumed_at": now.Format(time.RFC3339),
		})
		if _, err := tx.Append(event.Event{Kind: "run.resume", ObjectType: "run", ObjectID: &id2, Payload: resPayload, At: now}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-2', 'leased', 'run-2', NULL)`); err != nil {
			return err
		}

		// 3. Unowned lease -> protected (not dead).
		p3, _ := json.Marshal(map[string]any{
			"decision": map[string]any{
				"item_id":     "item-3",
				"lane_id":     "lane-3",
				"run_request": map[string]any{"run_id": "run-3", "item_id": "item-3", "lane_id": "lane-3", "backend": "fixture"},
			},
		})
		id3 := "run-3"
		if _, err := tx.Append(event.Event{Kind: "drain.started", ObjectType: "run", ObjectID: &id3, Payload: p3, At: now}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-3', 'leased', 'run-3', NULL)`); err != nil {
			return err
		}

		// 4. Live owner -> protected (not dead).
		p4, _ := json.Marshal(map[string]any{
			"loop_owner": here,
			"decision": map[string]any{
				"item_id":     "item-4",
				"lane_id":     "lane-4",
				"run_request": map[string]any{"run_id": "run-4", "item_id": "item-4", "lane_id": "lane-4", "backend": "fixture"},
			},
		})
		id4 := "run-4"
		if _, err := tx.Append(event.Event{Kind: "drain.started", ObjectType: "run", ObjectID: &id4, Payload: p4, At: now}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-4', 'leased', 'run-4', NULL)`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		dead, err := DeadRuns(r, path, "")
		if err != nil {
			return err
		}
		if !dead["run-1"] {
			t.Fatalf("run-1 (rebooted owner) not marked dead: %v", dead)
		}
		if !dead["run-2"] {
			t.Fatalf("run-2 (resumed, rebooted owner) not marked dead: %v", dead)
		}
		if dead["run-3"] {
			t.Fatalf("run-3 (unowned) should be protected, got dead: %v", dead)
		}
		if dead["run-4"] {
			t.Fatalf("run-4 (live owner) should be protected, got dead: %v", dead)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func cutDomain(d string) (string, string, bool) {
	for i := 0; i < len(d); i++ {
		if d[i] == ':' {
			return d[:i], d[i+1:], true
		}
	}
	return d, "", false
}
