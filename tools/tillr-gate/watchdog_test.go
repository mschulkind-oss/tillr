package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The CLI tests are in the external main_test package, so their private scratch
// helper is not visible here. Keep these injected-observer fixtures in the same
// owned scratch root, rather than relying on the system's temporary directory.
func scratch(t *testing.T, prefix string) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, prefix+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestGateWrapperPreservesCheckStall(t *testing.T) {
	root := scratch(t, "watchdog-root")
	logs := scratch(t, "watchdog-logs")
	command := []string{"python3", "-c", `import os,json,sys
check={'check_id':'blocked','result':'stalled','duration_ms':60000,'cpu_time_ms':12,'silent_samples':60,'last_output_line':'last'}
open(os.environ['TILLR_FAST_VERDICT_OUT'],'w').write(json.dumps({'payload':{'result':'stalled','checks':[check]}}))
sys.exit(6)`}
	envelope, err := RunGate(planEnvelope(t, PlanPayload{PlanID: "plan", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "digest"}), root, logs, GateProgram{Command: command})
	var result VerdictPayload
	json.Unmarshal(envelope.Payload, &result)
	if exitCode(err) != ExitStalled || result.Result != ResultStalled || len(result.Checks) != 2 || result.Checks[1].CheckID != "blocked" || result.Checks[1].CPUTimeMS != 12 {
		t.Fatalf("wrapper lost stall: %+v, %v", result, err)
	}
}

func TestFastDeclarationRefusesTimeout(t *testing.T) {
	budget, timeout := 100, 200
	err := (checkFile{ID: "fast", Label: "fast", Command: []string{"true"}, Scope: "tree", Tier: "fast", BudgetMS: &budget, TimeoutMS: &timeout}).validate()
	if err == nil || !strings.Contains(err.Error(), "timeout_ms") {
		t.Fatalf("timeout admitted: %v", err)
	}
	if _, err := treeOverrunFlag("carry"); err == nil {
		t.Fatal("carrying admitted")
	}
}

func TestWatchdogSampleRule(t *testing.T) {
	if WatchdogSampleInterval != time.Second || WatchdogSilentSamples != 60 {
		t.Fatal("sample rule changed")
	}
	for _, mode := range []string{"silent", "cpu", "child", "output", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			state := watchdogState{}
			state.observe(watchSample{}, nil)
			for n := 1; n <= 60; n++ {
				sample := watchSample{}
				switch mode {
				case "cpu", "child":
					sample.CPUTimeMS = int64(n)
				case "output":
					sample.Bytes = int64(n)
				}
				var err error
				if mode == "unavailable" {
					err = errors.New("no process accounting")
				}
				stalled := state.observe(sample, err)
				if stalled != (mode == "silent" && n == 60) {
					t.Fatalf("%s sample %d: stalled %v", mode, n, stalled)
				}
			}
			if mode == "unavailable" && state.Unavailable == "" {
				t.Fatal("unavailability not recorded")
			}
		})
	}
}

// A real child prints one line per injected clock sample; the sampler uses an
// acknowledgment pipe, not sleeps or CPU work, to know the log has been written.
func TestProgressingCheckOutlivesEveryTarget(t *testing.T) {
	for _, tier := range []string{"fast", "heavy", "landing", "unavailable"} {
		t.Run(tier, func(t *testing.T) {
			dir := scratch(t, "watchdog-heartbeats")
			inputR, inputW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer inputR.Close()
			defer inputW.Close()
			ackR, ackW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer ackR.Close()
			defer ackW.Close()
			var clock atomic.Int64
			ticks := make(chan time.Time)
			stop := make(chan struct{})
			sources := &watchSources{
				Now: func() time.Time { return time.Unix(100, 0).Add(time.Duration(clock.Load()) * time.Second) },
				Ticks: func() (<-chan time.Time, func()) {
					go func() {
						for n := int64(1); n <= 70; n++ {
							select {
							case <-stop:
								return
							case ticks <- time.Unix(100+n, 0):
							}
						}
					}()
					return ticks, func() { close(stop) }
				},
				Command: func(args []string) *exec.Cmd {
					cmd := exec.Command(args[0], args[1:]...)
					cmd.Stdin = inputR
					cmd.ExtraFiles = []*os.File{ackW}
					return cmd
				},
				Sample: func(pid int, log *os.File) (watchSample, error) {
					n := clock.Add(1) - 1
					if n > 0 && n <= 70 {
						if _, err := inputW.Write([]byte("heartbeat\n")); err != nil {
							return watchSample{}, err
						}
						var ack [1]byte
						if _, err := ackR.Read(ack[:]); err != nil {
							return watchSample{}, err
						}
					}
					if n == 70 {
						inputW.Close()
					}
					info, err := log.Stat()
					if err != nil {
						return watchSample{}, err
					}
					if tier == "unavailable" {
						return watchSample{Bytes: info.Size(), LastLine: "heartbeat"}, errors.New("CPU accounting absent")
					}
					return watchSample{Bytes: info.Size(), LastLine: "heartbeat"}, nil
				},
			}
			check := PlanCheck{CheckID: "progress", Tier: tier, Scope: "change", BudgetMS: 1, TimeoutMS: 2, watchdog: sources,
				Command: []string{"python3", "-c", "import os,sys\nfor line in sys.stdin:\n print(line.strip(),flush=True)\n os.write(3,b'x')"}}
			result := runCheck(check, dir, dir, 0)
			if result.Result != ResultPass || result.OverBudgetMS == 0 {
				t.Fatalf("own verdict lost: %+v", result)
			}
			// An explicit budget is the target for every tier, even when an older
			// timeout spelling is also present. Neither one stops progress.
			if result.BudgetMS != 1 {
				t.Fatalf("wrong target recorded: %+v", result)
			}
			if result.DurationMS != 71000 {
				t.Fatalf("injected clock duration not recorded: %d", result.DurationMS)
			}
			if tier == "unavailable" && (result.WatchdogUnavailable == "" || !strings.Contains(strings.Join(budgetNotes(VerdictPayload{Checks: []VerdictCheck{result}}), "\n"), "watchdog unavailable")) {
				t.Fatalf("unavailable watchdog hidden: %+v", result)
			}
			if result.DurationMS != result.EndUnixNano/1e6-result.StartUnixNano/1e6 {
				t.Fatal("injected clock not recorded")
			}
			body, err := os.ReadFile(result.LogRef)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(body), "heartbeat\n") != 70 {
				t.Fatalf("child did not finish: %s", body)
			}
		})
	}
}

func TestGateProgramUsesWatchdogNotTarget(t *testing.T) {
	for _, mode := range []string{"progress", "silent"} {
		t.Run(mode, func(t *testing.T) {
			var clock atomic.Int64
			inputR, inputW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer inputR.Close()
			defer inputW.Close()
			ticks := make(chan time.Time)
			stop := make(chan struct{})
			sources := &watchSources{
				Now: func() time.Time { return time.Unix(100+clock.Load(), 0) },
				Command: func(args []string) *exec.Cmd {
					cmd := exec.Command(args[0], args[1:]...)
					cmd.Stdin = inputR
					return cmd
				},
				Ticks: func() (<-chan time.Time, func()) {
					go func() {
						for {
							select {
							case <-stop:
								return
							case ticks <- time.Time{}:
							}
						}
					}()
					return ticks, func() { close(stop) }
				},
				Sample: func(pid int, log *os.File) (watchSample, error) {
					n := clock.Add(1)
					cpu := int64(12)
					if mode == "progress" {
						cpu += n
						if n == 70 {
							inputW.Close()
						}
					}
					return watchSample{CPUTimeMS: cpu, LastLine: "last line"}, nil
				},
			}
			dir := scratch(t, "wrapper-clock")
			out := runGateProgram(GateProgram{Command: []string{"python3", "-c", "import sys; sys.stdin.read(); print('verdict')"}, Timeout: time.Millisecond, watchdog: sources}, dir, dir)
			if mode == "progress" {
				if out.check.Result != ResultPass || out.check.OverBudgetMS == 0 || out.failure != nil {
					t.Fatalf("target killed progress: %+v", out)
				}
			} else if out.check.Result != ResultStalled || out.check.SilentSamples != WatchdogSilentSamples || out.check.LastOutputLine != "last line" || exitCode(out.failure) != ExitStalled {
				t.Fatalf("wrapper lost stall: %+v", out)
			}
		})
	}
}

func TestStalledCheckIsKilledAndRecorded(t *testing.T) {
	var clock atomic.Int64
	ticks := make(chan time.Time)
	stop := make(chan struct{})
	sources := &watchSources{Now: func() time.Time { return time.Unix(100+clock.Load(), 0) },
		Ticks: func() (<-chan time.Time, func()) {
			go func() {
				for n := int64(1); ; n++ {
					select {
					case <-stop:
						return
					case ticks <- time.Unix(100+n, 0):
					}
				}
			}()
			return ticks, func() { close(stop) }
		},
		Sample: func(pid int, log *os.File) (watchSample, error) {
			clock.Add(1)
			return watchSample{LastLine: "last line", CPUTimeMS: 12}, nil
		},
	}
	dir := scratch(t, "watchdog-stall")
	result := runCheck(PlanCheck{CheckID: "blocked", Scope: "change", Tier: "fast", BudgetMS: 1, watchdog: sources, Command: []string{"python3", "-c", "import signal; signal.pause()"}}, dir, dir, 0)
	if result.Result != ResultStalled || result.SilentSamples != 60 || result.CPUTimeMS != 12 || result.LastOutputLine != "last line" {
		t.Fatalf("stall facts: %+v", result)
	}
	body, err := os.ReadFile(result.LogRef)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "stalled") || len(budgetNotes(VerdictPayload{Checks: []VerdictCheck{result}})) == 0 {
		t.Fatalf("stall not visible: %s", body)
	}
}

func TestStallIsNotRememberedOrMaskedByFailure(t *testing.T) {
	root, tree := memoryRepo(t)
	for i := 0; i < 2; i++ {
		sources := &watchSources{Now: time.Now, Ticks: func() (<-chan time.Time, func()) {
			ticks := make(chan time.Time)
			stop := make(chan struct{})
			go func() {
				for {
					select {
					case <-stop:
						return
					case ticks <- time.Unix(0, 0):
					}
				}
			}()
			return ticks, func() { close(stop) }
		}, Sample: func(int, *os.File) (watchSample, error) { return watchSample{}, nil }, Command: func(args []string) *exec.Cmd { return exec.Command(args[0], args[1:]...) }}
		check := PlanCheck{CheckID: "blocked", Scope: "tree", Tier: "fast", BudgetMS: 100, Reads: []string{"input/"}, Command: []string{"python3", "-c", "import signal; signal.pause()"}}
		plan := memoryPlan(t, tree, PlanCheck{CheckID: "failed", Command: []string{"false"}}, check)
		envelope, err := RunWith(plan, root, scratch(t, "watchdog-memory"), RunOptions{watchdog: sources})
		var verdict VerdictPayload
		json.Unmarshal(envelope.Payload, &verdict)
		if exitCode(err) != ExitStalled || verdict.Result != ResultStalled || verdict.Checks[1].Memory == "remembered" {
			t.Fatalf("stall lost: %+v, %v", verdict, err)
		}
	}
	if files := memoryFiles(t, memoryStore(t, root)); len(files) != 0 {
		t.Fatalf("stall remembered: %v", files)
	}
}

func TestDisappearingMemberCPUDisablesWatchdog(t *testing.T) {
	readings := []int64{100, 50, 50}
	sampler := accountingContinuity(func(int, *os.File) (watchSample, error) {
		next := readings[0]
		readings = readings[1:]
		return watchSample{CPUTimeMS: next}, nil
	})
	state := watchdogState{}
	first, err := sampler(1, nil)
	state.observe(first, err)
	next, err := sampler(1, nil)
	if err == nil {
		t.Fatal("lost child CPU was treated as a complete reading")
	}
	state.observe(next, err)
	for i := 0; i < 60; i++ {
		if state.observe(next, nil) {
			t.Fatal("unknown CPU was treated as a stall")
		}
	}
}

func TestDiscardedChildCPURetainedByKernelCounter(t *testing.T) {
	// Inject a cumulative kernel counter while children work and exit entirely
	// between samples. /proc may show only a stationary leader in every case;
	// its signal masks cannot distinguish SIG_DFL+SA_NOCLDWAIT from ordinary
	// default handling, so those masks are deliberately not observation inputs.
	for _, disposition := range []string{"SIG_IGN", "handler+SA_NOCLDWAIT", "SIG_DFL+SA_NOCLDWAIT"} {
		t.Run(disposition, func(t *testing.T) {
			log, err := os.CreateTemp(scratch(t, "watchdog-child-cpu"), "output")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			var counters bytes.Buffer
			for child := 0; child <= 61; child++ {
				binary.Write(&counters, binary.NativeEndian, uint64(child)*120000000)
			}
			sampler := clockSampler(&counters, nil)
			state := watchdogState{}
			for child := 0; child <= 61; child++ {
				sample, err := sampler(11, log)
				if err != nil {
					t.Fatal(err)
				}
				if state.observe(sample, err) {
					t.Fatal("discarded child CPU falsely stalled")
				}
			}
			if state.previous.CPUTimeMS != 61*120 {
				t.Fatalf("exited-child usage lost: %+v", state)
			}
		})
	}
}

func TestSubMillisecondCPUIsProgress(t *testing.T) {
	state := watchdogState{}
	state.observe(watchSample{}, nil)
	for i := uint64(1); i <= 61; i++ {
		if state.observe(watchSample{CPUTimeNS: i}, nil) {
			t.Fatal("rounding erased CPU progress")
		}
	}
}

func TestMissingKernelCounterNeverUsesSnapshots(t *testing.T) {
	log, err := os.CreateTemp(scratch(t, "watchdog-missing-cpu"), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	sampler := clockSampler(nil, errors.New("kernel counter denied"))
	state := watchdogState{}
	for i := 0; i <= 61; i++ {
		sample, err := sampler(os.Getpid(), log)
		if err == nil {
			t.Fatal("unavailable CPU became a snapshot")
		}
		if state.observe(sample, err) {
			t.Fatal("unavailable CPU triggered a stall")
		}
	}
}
