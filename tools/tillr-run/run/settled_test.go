package run_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The deadline is a deadlock guard, never an elapsed-time assertion.
func TestSettledLingeringHarnessEndsAsAnswered(t *testing.T) {
	if run.SettledIdleSamples != 5 || run.SettledSampleInterval != time.Second {
		t.Fatal("sample policy changed")
	}
	s, root := leaseFixture(t)
	lane := filepath.Join(root, "lanes", "lane_settled")
	if err := os.MkdirAll(lane, 0755); err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(root, "backend")
	if err := os.MkdirAll(backend, 0755); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(backend, "run-pi")
	helperRelease := filepath.Join(lane, "release-helper")
	helperReady := filepath.Join(lane, "late-child-pid")
	// Publish settlement before the last helper starts. Process-group activity
	// after the baseline observation must restart the idle sample count.
	script := "#!/bin/sh\nsleep 60 & echo $! > child-pid\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"report\\nRESULT: done\\n\"}]}}' '{\"type\":\"agent_settled\"}'\n" +
		"while [ ! -f '" + helperRelease + "' ]; do sleep 0.01; done\nsleep 60 & echo $! > '" + helperReady + "'\nwait\n"
	if err := os.WriteFile(adapter, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	extractor, err := filepath.Abs("../backends/pi/extract-report")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "extract-report"), []byte("#!/bin/sh\nexec '"+extractor+"' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "pause-kind"), []byte(run.PauseWhereItStands+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sibling := exec.Command("/bin/sleep", "60")
	sibling.Dir = lane
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { sibling.Process.Kill(); sibling.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticks := make(chan time.Time)
	armed := make(chan time.Duration)
	done := make(chan record.Envelope, 1)
	errs := make(chan error, 1)
	go func() {
		env, err := run.Exec(ctx, s, run.Request{RunID: "settled", Backend: "fixture", WorkingDir: ".", Command: []string{adapter}}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), BackendsDir: backend, SettledAfter: func(d time.Duration) <-chan time.Time {
			select {
			case armed <- d:
			case <-ctx.Done():
			}
			return ticks
		}})
		errs <- err
		done <- env
	}()
	awaitArm := func() {
		t.Helper()
		select {
		case d := <-armed:
			if d != time.Second {
				t.Fatalf("interval %v", d)
			}
		case <-ctx.Done():
			t.Fatal("sample not armed")
		}
	}
	awaitArm()
	if err := os.WriteFile(helperRelease, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Wait for the deliberately late helper without assuming startup wall time.
	for {
		if info, err := os.Stat(helperReady); err == nil && info.Size() > 0 {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("late helper did not start")
		}
	}
	epoch := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	var env record.Envelope
	var endedAt time.Time
	// Supply each observation the monitor asks for. Startup activity can reset
	// the count; five ticks from the first baseline are not five idle samples.
observations:
	for i := 1; ; i++ {
		endedAt = epoch.Add(time.Duration(i) * time.Second)
		select {
		case ticks <- endedAt:
		case <-ctx.Done():
			t.Fatal("sample stalled")
		}
		select {
		case env = <-done:
			if i <= run.SettledIdleSamples {
				t.Fatal("late helper activity did not restart idle sample count")
			}
			break observations
		case d := <-armed:
			if d != run.SettledSampleInterval {
				t.Fatalf("interval %v", d)
			}
		case <-ctx.Done():
			t.Fatal("settled harness did not end")
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	rec := recordOf(t, env)
	if rec.EndCause != run.EndSettledIdle || rec.State != run.StateExited || rec.SettledAt == "" || rec.HarnessEndedAt != endedAt.Format(time.RFC3339Nano) {
		t.Fatalf("record: %+v", rec)
	}
	if err := syscall.Kill(sibling.Process.Pid, 0); err != nil {
		t.Fatalf("sibling killed: %v", err)
	}
	for _, pidFile := range []string{filepath.Join(lane, "child-pid"), helperReady} {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal(err)
		}
		// A killed child may remain as a zombie until the container's init reaps it.
		if rawStat, err := os.ReadFile(filepath.Join("/proc", strings.TrimSpace(string(raw)), "stat")); err == nil && !strings.Contains(string(rawStat), ") Z ") {
			t.Fatalf("helper still running: %s", rawStat)
		}
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		status, found, err := run.StatusRecord(r, "settled")
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("missing run")
		}
		var body struct {
			Run run.Record `json:"run"`
		}
		if err := json.Unmarshal(status.Payload, &body); err != nil {
			return err
		}
		if body.Run.SettledAt != rec.SettledAt || body.Run.HarnessEndedAt != rec.HarnessEndedAt || body.Run.EndCause != rec.EndCause {
			t.Fatalf("status lost observations: %+v", body.Run)
		}
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Kind == run.FinishKind {
				var finish run.Record
				json.Unmarshal(e.Payload, &finish)
				if finish.SettledAt != rec.SettledAt || finish.HarnessEndedAt != rec.HarnessEndedAt {
					t.Fatal("finish lost observations")
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNaturalSettledExitNeverArmsClock(t *testing.T) {
	for _, stream := range []string{"", "printf '%s\\n' '{\"type\":\"agent_settled\"}' 'RESULT: done';"} {
		s, root := leaseFixture(t)
		env, err := run.Exec(context.Background(), s, run.Request{RunID: "natural", Backend: "fixture", WorkingDir: ".", Command: []string{"/bin/sh", "-c", stream + "exit 7"}}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), SettledAfter: func(time.Duration) <-chan time.Time { t.Error("natural exit read sample clock"); return nil }})
		if err != nil {
			t.Fatal(err)
		}
		rec := recordOf(t, env)
		if rec.EndCause != run.EndNormalExit || rec.State != run.StateExited || *rec.ExitCode != 7 || rec.SettledAt != "" || rec.HarnessEndedAt != "" {
			t.Fatalf("changed natural record: %+v", rec)
		}
	}
}
