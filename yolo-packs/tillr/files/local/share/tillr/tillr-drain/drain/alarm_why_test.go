package drain_test

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
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyPrintsBackendAlarmBeforeDefinitionAndRows(t *testing.T) {
	path := fixture(t, itemRecord("ready", "in-queue"))
	ref := policyDoc(t, map[string]any{
		"default_backend": "fixture",
		"backends":        map[string]any{"fixture": map[string]any{"adapter": "true", "model": "fixture", "timeout_seconds": 60, "quota": []string{"quota-fixture"}}},
	})
	code, out := invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1")
	if code != 0 || !strings.HasPrefix(string(out), "ALARM backend-window (fixture)") || !strings.Contains(string(out), "no quota reading recorded") {
		t.Fatalf("why: %d %s", code, out)
	}
	code, out = invoke(t, "why", "ready", "--store", path, "--policy", ref, "--json")
	if code != 0 || !strings.HasPrefix(string(out), "{\n  \"alarms\":") || !strings.Contains(string(out), `"item_id": "ready"`) {
		t.Fatalf("named why: %d %s", code, out)
	}
}

func TestWhyAlarmsWhenInstalledBuildIsNewerThanLiveLoop(t *testing.T) {
	s := startRoll(t, 0, "")
	t.Cleanup(func() {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	})
	ctx := context.Background()
	db, err := store.OpenRead(ctx, s.f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var started int64
	s.waitFor(t, "the idle loop's session", func() bool {
		return db.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT started_at FROM drain_sessions WHERE ended_at IS NULL`).Scan(&started)
		}) == nil
	})
	why := func() string {
		t.Helper()
		out, err := exec.Command(s.exe, "why", "--root", s.f.root, "--store", s.f.store, "--lanes", "0").CombinedOutput()
		if err != nil {
			t.Fatalf("why: %v\n%s", err, out)
		}
		return string(out)
	}
	setBuildTime := func(at time.Time) {
		t.Helper()
		if err := os.Chtimes(s.exe, at, at); err != nil {
			t.Fatal(err)
		}
	}
	const alarm = "ALARM stale-loop-build"
	loopStart := time.UnixMilli(started).UTC()
	built := loopStart.Add(time.Second)
	setBuildTime(built)
	if out := why(); strings.Contains(out, alarm) {
		t.Fatalf("why alarmed with the current loop image:\n%s", out)
	}
	s.replaceBinary(t)
	setBuildTime(loopStart.Add(-time.Second))
	if out := why(); strings.Contains(out, alarm) {
		t.Fatalf("why alarmed with an older installed build:\n%s", out)
	}
	setBuildTime(built)
	out := why()
	if strings.Count(out, alarm) != 1 || !strings.Contains(out, "loop started at "+loopStart.Format(time.RFC3339Nano)) ||
		!strings.Contains(out, "installed tillr-drain was built at "+built.Format(time.RFC3339Nano)) {
		t.Fatalf("why did not report one alarm with both times:\n%s", out)
	}
	if image, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", s.cmd.Process.Pid)); err != nil || image != s.exe+" (deleted)" {
		t.Fatalf("why changed the running loop: exe=%q err=%v", image, err)
	}
}

func TestWhyStaleLoopBuildDistinguishesNormalWaitFromStarvation(t *testing.T) {
	s := startRoll(t, 0, "")
	t.Cleanup(func() {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	})
	ctx := context.Background()
	var started int64
	var ownerRaw string
	s.waitFor(t, "the idle loop's session", func() bool {
		db, err := store.OpenRead(ctx, s.f.store)
		if err != nil {
			return false
		}
		defer db.Close()
		return db.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT started_at, loop_owner FROM drain_sessions WHERE ended_at IS NULL`).Scan(&started, &ownerRaw)
		}) == nil && ownerRaw != ""
	})
	s.replaceBinary(t)
	loopStart := time.UnixMilli(started).UTC()
	setBuildTime := func(at time.Time) {
		t.Helper()
		if err := os.Chtimes(s.exe, at, at); err != nil {
			t.Fatal(err)
		}
	}
	why := func(extra ...string) string {
		t.Helper()
		args := append([]string{"why", "--root", s.f.root, "--store", s.f.store, "--lanes", "0"}, extra...)
		out, err := exec.Command(s.exe, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("why %v: %v\n%s", extra, err, out)
		}
		return string(out)
	}
	appendEvent := func(kind, objType, objID string, at time.Time, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		db, err := store.Open(ctx, s.f.store, false)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		id := objID
		if err := db.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: kind, ObjectType: objType, ObjectID: &id, At: at, Payload: raw})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	seedItem := func(itemID string) {
		t.Helper()
		db, err := store.Open(ctx, s.f.store, false)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := db.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO items(item_id, title, state, updated_at, seat) VALUES(?, ?, 'in-queue', ?, '')`, itemID, itemID, started)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	const alarm = "ALARM stale-loop-build"
	var owner json.RawMessage = json.RawMessage(ownerRaw)
	livePID, liveTicks, err := heavyrun.HeavySupervisorIdentity()
	if err != nil {
		t.Fatal(err)
	}
	writeGateFace := func(runID, itemID, laneID string, startedAt time.Time) {
		t.Helper()
		dir := drain.RunDir(s.f.root, runID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{
			"pid":           livePID,
			"start_ticks":   liveTicks,
			"run_id":        runID,
			"item_id":       itemID,
			"lane_id":       laneID,
			"started_at_ms": startedAt.UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, drain.GateFaceFile), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removeGateFace := func(runID string) {
		t.Helper()
		_ = os.Remove(filepath.Join(drain.RunDir(s.f.root, runID), drain.GateFaceFile))
	}

	// 1. Candidate gate already running when the new build was installed -> status line, no alarm.
	seedItem("item-pre")
	gateAt := loopStart.Add(2 * time.Second)
	gatePayload := map[string]any{
		"decision": map[string]any{
			"item_id":     "item-pre",
			"lane_id":     "lane-1",
			"run_request": map[string]any{"run_id": "run-pre", "item_id": "item-pre", "lane_id": "lane-1"},
		},
		"base_sha": "base",
	}
	appendEvent("drain.started", "run", "run-pre", gateAt, gatePayload)
	appendEvent("drain.examination.owner", "run", "run-pre", gateAt, owner)
	writeGateFace("run-pre", "item-pre", "lane-1", gateAt)

	setBuildTime(gateAt.Add(time.Second))
	out := why()
	if strings.Contains(out, alarm) {
		t.Fatalf("why alarmed while waiting behind a pre-install gate:\n%s", out)
	}
	if !strings.Contains(out, "loop restart: waiting for gate item-pre on lane-1 (run-pre)") {
		t.Fatalf("why did not show waiting status line naming the gate:\n%s", out)
	}
	var ans drain.WhyAnswer
	if err := json.Unmarshal([]byte(why("--json")), &ans); err != nil {
		t.Fatalf("decode why --json: %v", err)
	}
	if len(ans.Alarms) != 0 || !strings.Contains(ans.LoopRestartLine, "gate item-pre on lane-1 (run-pre)") {
		t.Fatalf("json why for pre-install gate: alarms=%+v restart=%q", ans.Alarms, ans.LoopRestartLine)
	}

	// 2. Candidate gate started after the new build was installed -> stale-loop-build alarm naming it.
	setBuildTime(gateAt.Add(-time.Second))
	out = why()
	if strings.Count(out, alarm) != 1 || !strings.Contains(out, "gate item-pre on lane-1 (run-pre)") {
		t.Fatalf("why did not alarm naming the starving gate:\n%s", out)
	}
	if strings.Contains(out, "loop restart: waiting for") {
		t.Fatalf("why printed waiting status line alongside starvation alarm:\n%s", out)
	}

	// 3. Mixed: pre-install gate + post-install gate -> alarm stands and names the post-install gate.
	seedItem("item-post")
	postGateAt := gateAt.Add(4 * time.Second)
	postGatePayload := map[string]any{
		"decision": map[string]any{
			"item_id":     "item-post",
			"lane_id":     "lane-2",
			"run_request": map[string]any{"run_id": "run-post", "item_id": "item-post", "lane_id": "lane-2"},
		},
		"base_sha": "base",
	}
	appendEvent("drain.started", "run", "run-post", postGateAt, postGatePayload)
	appendEvent("drain.examination-claimed", "run", "run-post", postGateAt, owner)
	writeGateFace("run-post", "item-post", "lane-2", postGateAt)

	setBuildTime(gateAt.Add(2 * time.Second))
	out = why()
	if strings.Count(out, alarm) != 1 || !strings.Contains(out, "gate item-post on lane-2 (run-post)") {
		t.Fatalf("why did not alarm naming the post-install gate when both pre- and post-install gates run:\n%s", out)
	}

	// Finish both candidate gates so only the trunk proof cases remain.
	removeGateFace("run-pre")
	removeGateFace("run-post")
	appendEvent("drain.examined", "run", "run-pre", postGateAt.Add(time.Second), map[string]any{})
	appendEvent("drain.examined", "run", "run-post", postGateAt.Add(time.Second), map[string]any{})

	// 4. Trunk proof already running when the new build was installed -> status line, no alarm.
	proofAt := postGateAt.Add(2 * time.Second)
	appendEvent(drain.HeavyProgressKind, "trunk", "main", proofAt, drain.HeavyProgressFact{
		TipSHA: "abcdef1234567890",
		Phase:  "started",
	})
	setBuildTime(proofAt.Add(time.Second))
	out = why()
	if strings.Contains(out, alarm) {
		t.Fatalf("why alarmed while waiting behind a pre-install trunk proof:\n%s", out)
	}
	if !strings.Contains(out, "loop restart: waiting for trunk proof at abcdef123456") {
		t.Fatalf("why did not show waiting status line naming the trunk proof:\n%s", out)
	}

	// 4b. Pre-install trunk proof running + finished candidates whose gates have not started
	// (examination claimed/owned after install, no live gate face) -> waiting status line, no alarm.
	seedItem("item-unstarted-a")
	seedItem("item-unstarted-b")
	unstartedAt := proofAt.Add(2 * time.Second)
	appendEvent("drain.started", "run", "run-unstarted-a", unstartedAt, map[string]any{
		"decision": map[string]any{
			"item_id":     "item-unstarted-a",
			"lane_id":     "lane-2",
			"run_request": map[string]any{"run_id": "run-unstarted-a", "item_id": "item-unstarted-a", "lane_id": "lane-2"},
		},
		"base_sha": "base",
	})
	appendEvent("drain.examination.owner", "run", "run-unstarted-a", unstartedAt, owner)
	appendEvent("drain.started", "run", "run-unstarted-b", unstartedAt, map[string]any{
		"decision": map[string]any{
			"item_id":     "item-unstarted-b",
			"lane_id":     "express-1",
			"run_request": map[string]any{"run_id": "run-unstarted-b", "item_id": "item-unstarted-b", "lane_id": "express-1"},
		},
		"base_sha": "base",
	})
	appendEvent("drain.examination-claimed", "run", "run-unstarted-b", unstartedAt, owner)

	out = why()
	if strings.Contains(out, alarm) {
		t.Fatalf("why alarmed for candidates whose gates have not started while waiting behind a pre-install proof:\n%s", out)
	}
	if !strings.Contains(out, "loop restart: waiting for trunk proof at abcdef123456") {
		t.Fatalf("why did not show waiting status line for pre-install proof with unstarted candidates:\n%s", out)
	}
	ans = drain.WhyAnswer{}
	if err := json.Unmarshal([]byte(why("--json")), &ans); err != nil {
		t.Fatalf("decode why --json: %v", err)
	}
	if len(ans.Alarms) != 0 || !strings.Contains(ans.LoopRestartLine, "trunk proof at abcdef123456") {
		t.Fatalf("json why for pre-install proof + unstarted candidates: alarms=%+v restart=%q", ans.Alarms, ans.LoopRestartLine)
	}

	// 4c. Once one of those candidates actually starts a gate after the install and it is still running,
	// stale-loop-build alarms and names that gate (and not the other unstarted candidate).
	writeGateFace("run-unstarted-a", "item-unstarted-a", "lane-2", unstartedAt)
	out = why()
	if strings.Count(out, alarm) != 1 || !strings.Contains(out, "gate item-unstarted-a on lane-2 (run-unstarted-a)") {
		t.Fatalf("why did not alarm naming the post-install running gate:\n%s", out)
	}
	ans = drain.WhyAnswer{}
	if err := json.Unmarshal([]byte(why("--json")), &ans); err != nil {
		t.Fatalf("decode why --json: %v", err)
	}
	if len(ans.Alarms) != 1 || !strings.Contains(ans.Alarms[0].Cause, "gate item-unstarted-a on lane-2 (run-unstarted-a)") || strings.Contains(ans.Alarms[0].Cause, "item-unstarted-b") {
		t.Fatalf("json why alarm cause for post-install running gate: alarms=%+v", ans.Alarms)
	}

	// Once that gate writes gate.done.json, it is no longer running and no longer named as starving.
	doneRaw, err := json.Marshal(map[string]any{"run_id": "run-unstarted-a", "exit_code": 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drain.RunDir(s.f.root, "run-unstarted-a"), drain.GateDoneFile), doneRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	out = why()
	if strings.Contains(out, alarm) {
		t.Fatalf("why alarmed after gate wrote gate.done.json:\n%s", out)
	}
	if !strings.Contains(out, "loop restart: waiting for trunk proof at abcdef123456") {
		t.Fatalf("why did not return to waiting status line after gate wrote gate.done.json:\n%s", out)
	}
	removeGateFace("run-unstarted-a")
	_ = os.Remove(filepath.Join(drain.RunDir(s.f.root, "run-unstarted-a"), drain.GateDoneFile))
	appendEvent("drain.examined", "run", "run-unstarted-a", unstartedAt.Add(time.Second), map[string]any{})
	appendEvent("drain.examined", "run", "run-unstarted-b", unstartedAt.Add(time.Second), map[string]any{})

	// 5. Trunk proof started after the new build was installed -> stale-loop-build alarm naming it.
	setBuildTime(proofAt.Add(-time.Second))
	out = why()
	if strings.Count(out, alarm) != 1 || !strings.Contains(out, "trunk proof at abcdef123456") {
		t.Fatalf("why did not alarm naming the starving trunk proof:\n%s", out)
	}
	if strings.Contains(out, "loop restart: waiting for") {
		t.Fatalf("why printed waiting status line alongside trunk proof starvation alarm:\n%s", out)
	}

	// 6. Once the trunk proof ends, it is no longer named as a waiting blocker.
	appendEvent(drain.HeavyProgressKind, "trunk", "main", proofAt.Add(2*time.Second), drain.HeavyProgressFact{
		TipSHA:  "abcdef1234567890",
		Phase:   "ended",
		Outcome: "green",
	})
	setBuildTime(proofAt.Add(time.Second))
	out = why()
	if strings.Contains(out, "loop restart: waiting for") || strings.Contains(out, "trunk proof at abcdef123456") {
		t.Fatalf("ended trunk proof still appeared in why output:\n%s", out)
	}
}
