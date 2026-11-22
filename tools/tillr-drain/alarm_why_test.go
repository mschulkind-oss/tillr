package main_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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
