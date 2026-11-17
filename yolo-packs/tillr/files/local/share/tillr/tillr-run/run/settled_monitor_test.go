package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func settledScratch(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		t.Fatal("YOLO_DURABLE_DIR required")
	}
	dir, err := os.MkdirTemp(root, "settled-monitor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSilentHarnessRequiresSettlementAndFooter(t *testing.T) {
	for _, tc := range []struct{ name, stream string }{
		{"unsettled", "RESULT: done\n"},
		{"no-footer", "{\"type\":\"agent_settled\"}\nunfinished\n"},
		{"quoted-settlement", "{\"type\":\"message_end\",\"text\":\"agent_settled\"}\nRESULT: done\n"},
		{"aborted", "{\"type\":\"agent_settled\",\"aborted\":true}\nRESULT: done\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := settledScratch(t)
			log := filepath.Join(dir, "stream")
			os.WriteFile(log, []byte(tc.stream), 0600)
			cmd := exec.Command("/bin/sleep", "60")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Kill(); cmd.Wait() }()
			monitor := settledMonitor{log: log, stderr: filepath.Join(dir, "err")}
			// These are injected observation instants, not time passing in the test.
			epoch := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 10*SettledIdleSamples; i++ {
				at := epoch.Add(time.Duration(i) * SettledSampleInterval)
				if monitor.sample(context.Background(), cmd.Process.Pid) {
					t.Fatalf("ended silent unfinished harness at injected sample %v", at)
				}
				if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
					t.Fatalf("harness gone: %v", err)
				}
			}
		})
	}
}

func TestSettledActivityRestartsSampleCount(t *testing.T) {
	for _, kind := range []string{"stdout", "stderr", "cpu", "new-turn"} {
		t.Run(kind, func(t *testing.T) {
			dir := settledScratch(t)
			log := filepath.Join(dir, "stream")
			errPath := filepath.Join(dir, "err")
			os.WriteFile(log, []byte("{\"type\":\"agent_settled\"}\nRESULT: done\n"), 0600)
			os.WriteFile(errPath, nil, 0600)
			cmd := exec.Command("/bin/sleep", "60")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Kill(); cmd.Wait() }()
			ticks := 0
			monitor := settledMonitor{log: log, stderr: errPath, cpu: func(int) (string, error) { return fmt.Sprint(ticks), nil }}
			monitor.sample(context.Background(), cmd.Process.Pid)
			for i := 0; i < SettledIdleSamples-1; i++ {
				if monitor.sample(context.Background(), cmd.Process.Pid) {
					t.Fatal("early termination")
				}
			}
			switch kind {
			case "stdout":
				f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
				fmt.Fprintln(f, "one line\nRESULT: done")
				f.Close()
			case "stderr":
				os.WriteFile(errPath, []byte("one line\n"), 0600)
			case "cpu":
				ticks++ // Inject increased CPU ticks without burning wall time.
			case "new-turn":
				f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
				fmt.Fprintln(f, `{"type":"agent_start"}`)
				f.Close()
			}
			if monitor.sample(context.Background(), cmd.Process.Pid) || monitor.idle != 0 {
				t.Fatalf("%s did not reset: %d", kind, monitor.idle)
			}
			if kind == "new-turn" {
				return
			} // The session is no longer settled.
			for i := 1; i <= SettledIdleSamples; i++ {
				ended := monitor.sample(context.Background(), cmd.Process.Pid)
				if ended != (i == SettledIdleSamples) {
					t.Fatalf("sample %d ended=%v", i, ended)
				}
			}
		})
	}
}

func TestSettledReportUsesFreshExtractor(t *testing.T) {
	dir := settledScratch(t)
	log := filepath.Join(dir, "stream")
	os.WriteFile(log, []byte("{\"type\":\"agent_settled\"}\n"), 0600)
	os.WriteFile(filepath.Join(dir, "extract-report"), []byte("#!/bin/sh\nexit 4\n"), 0755)
	os.MkdirAll(filepath.Join(dir, "settled-probe"), 0700)
	os.WriteFile(filepath.Join(dir, "settled-probe", "report.md"), []byte("RESULT: done\n"), 0600)
	m := settledMonitor{log: log, durable: dir, adapter: filepath.Join(dir, "run-stub"), backendDir: dir}
	if err := m.observe(); err != nil {
		t.Fatal(err)
	}
	if m.reportReady(context.Background()) {
		t.Fatal("accepted old report")
	}
}

func TestGroupCPUReadsHelperTicks(t *testing.T) {
	dir := settledScratch(t)
	cmd := exec.Command("/bin/sh", "-c", "sleep 60 & echo $! > child; wait")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var child string
	for child == "" {
		b, _ := os.ReadFile(filepath.Join(dir, "child"))
		child = strings.TrimSpace(string(b))
		if child == "" {
			select {
			case <-ctx.Done():
				t.Fatal("child missing")
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
	cpu, err := groupCPU(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cpu, child+":") || !strings.Contains(cpu, fmt.Sprint(cmd.Process.Pid)+":") {
		t.Fatalf("group missed harness or helper: %s", cpu)
	}
}
