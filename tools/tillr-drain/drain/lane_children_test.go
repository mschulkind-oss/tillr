package drain

import (
	"context"
	"encoding/json"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercise the planning pass itself: expiry release and child discovery must finish
// before it offers lanes, even for a legacy allocation with no terminal record.
func TestPassHoldsChildrenAfterExpiryRelease(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(strconv.FormatBool(legacy), func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			lane, id := l.cfg.Lanes[0], "expired-child-run"
			start := l.cfg.Now().Add(-time.Hour)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if legacy {
					_, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES(?,?,?,?) ON CONFLICT(lane_id) DO UPDATE SET state=excluded.state,run_id=excluded.run_id,leased_until=excluded.leased_until`, lane, run.LaneStateLeased, id, start.UnixMilli())
					return err
				}
				_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: id, Backend: "fixture", TimeoutMS: 1}, lane, start)
				return err
			})
			child := startLaneChild(t, l, lane, id)
			// This is the old pass ordering: its first scan sees no terminal run row.
			if err := l.recheckLanes(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertChildLaneNotPlanned(t, l, lane)
			if state, id, _ := laneRow(t, f, lane); state != run.LaneStateIdle || id != "" {
				t.Fatalf("lease not released: %s/%s", state, id)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var count int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id=?`, id).Scan(&count); err != nil {
					return err
				}
				if (count == 0) != legacy {
					t.Fatalf("terminal rows=%d with legacy=%v", count, legacy)
				}
				return nil
			})
			l.laneConditions, l.laneConditionsLoaded = nil, false
			assertChildLaneNotPlanned(t, l, lane)
			_ = child.Process.Kill()
			_ = child.Wait()
			mustPlan(t, l)
			if c := l.laneConditions[lane]; c != nil && c.out {
				t.Fatalf("exited child still holds lane: %+v", c)
			}
		})
	}
}

func TestHookDefinitionPassHoldsEndedRunChildren(t *testing.T) {
	f, l := passFixture(t, "item-a")
	lane, id := l.cfg.Lanes[0], "hook-child-run"
	at := l.cfg.Now().Format(time.RFC3339)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: id, LaneID: lane, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, EndCause: run.EndNormalExit})
	})
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Name, d.Completes, d.Prerequisites = "child-hold-hook", "hook", nil
	d.Hooks = &definition.Hooks{Argv: []string{"hook"}, Names: []string{"complete-item"}}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
	l.nonLanding = true
	child := startLaneChild(t, l, lane, id)
	assertChildLaneNotPlanned(t, l, lane)
	if !holdEnabled(d, ReasonLaneChildren) {
		t.Fatal("hook definition does not classify process holds")
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	mustPlan(t, l)
	if laneEvents(t, f, laneRestoredKind, lane) != 1 {
		t.Fatal("hook pass did not restore the exited child's lane")
	}
}

func startLaneChild(t *testing.T, l *Loop, lane, id string) *exec.Cmd {
	t.Helper()
	dir := filepath.Join(l.cfg.LaneRoot, lane, "durable")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "300")
	child.Dir = dir
	// Replace inherited parent-run identities so this fixture has exactly its own.
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "TILLR_RUN_ID=") && !strings.HasPrefix(value, "TILLR_LOOP_STORE=") {
			child.Env = append(child.Env, value)
		}
	}
	child.Env = append(child.Env, "TILLR_RUN_ID="+id, "TILLR_LOOP_STORE="+l.cfg.Store)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	return child
}

func assertChildLaneNotPlanned(t *testing.T, l *Loop, lane string) {
	t.Helper()
	for _, plan := range mustPlan(t, l) {
		if plan.decision.LaneID == lane && (plan.decision.Action == ActionDispatch || plan.decision.Action == ActionRetry) {
			t.Fatalf("pass offered child-occupied lane: %+v", plan.decision)
		}
	}
	c := l.laneConditions[lane]
	if c == nil || !c.out || c.reason != ReasonLaneChildren {
		t.Fatalf("pass left child-occupied lane in rotation: %+v", c)
	}
	for _, other := range l.rotationLanes() {
		if other != lane {
			return
		}
	}
	t.Fatal("a child holding one lane also removed the other lane from rotation")
}

func TestEndedRunChildrenHoldOnlyTheirLaneDirectory(t *testing.T) {
	for _, inside := range []bool{true, false} {
		t.Run(strconv.FormatBool(inside), func(t *testing.T) {
			f, l := passFixture(t, "item-a", "item-b")
			d, _ := examinedSettle(t, f, l)
			dir := filepath.Join(l.cfg.LaneRoot, d.LaneID, "durable")
			if !inside {
				dir = filepath.Join(l.cfg.LaneRoot, d.LaneID+"-other")
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			child := exec.Command("sleep", "300")
			child.Dir = dir
			child.Env = append(os.Environ(), "TILLR_RUN_ID="+d.RunRequest.RunID, "TILLR_LOOP_STORE="+l.cfg.Store)
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
			if err := l.releaseExamination(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			if state, id, _ := laneRow(t, f, d.LaneID); state != "idle" || id != "" {
				t.Fatalf("lease not released: %s/%s", state, id)
			}
			if err := l.recheckLanes(context.Background()); err != nil {
				t.Fatal(err)
			}
			offered := func() bool {
				for _, lane := range l.rotationLanes() {
					if lane == d.LaneID {
						return true
					}
				}
				return false
			}
			if offered() == inside {
				t.Fatalf("lane offered=%v with child inside=%v", offered(), inside)
			}
			if inside {
				c := l.laneConditions[d.LaneID]
				pid := strconv.Itoa(child.Process.Pid)
				if c == nil || !strings.Contains(c.detail, pid) || !strings.Contains(c.remedy, "kill "+pid) {
					t.Fatalf("missing PID/remedy: %+v", c)
				}
				if laneEvents(t, f, laneRefusedKind, d.LaneID) != 1 {
					t.Fatal("hold did not record one refusal")
				}
				// Even the slow retry interval, a changed worktree, or a fresh supervisor must
				// never offer a lane while this process still owns a directory in it.
				now := l.cfg.Now().Add(laneRecheckInterval * 2)
				l.cfg.Now = func() time.Time { return now }
				if err := l.recheckLanes(context.Background()); err != nil {
					t.Fatal(err)
				}
				if offered() {
					t.Fatal("slow retry offered a live child's lane")
				}
				l.laneConditions = nil
				l.laneConditionsLoaded = false
				if err := l.recheckLanes(context.Background()); err != nil {
					t.Fatal(err)
				}
				if offered() {
					t.Fatal("restart offered a live child's lane")
				}
			}
			_ = child.Process.Kill()
			_ = child.Wait()
			if err := l.recheckLanes(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !offered() {
				t.Fatal("lane not offered after child exited")
			}
			if inside && laneEvents(t, f, laneRestoredKind, d.LaneID) != 1 {
				t.Fatal("exit did not record restoration")
			}
		})
	}
}
