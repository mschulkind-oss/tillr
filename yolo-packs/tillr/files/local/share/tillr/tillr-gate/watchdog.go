package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/checkresult"
)

// These are the single definitions of the sampling cadence and stall threshold.
const WatchdogSampleInterval = 1000 * time.Millisecond
const WatchdogSilentSamples = 60
const ExitStalled = 6

// watchSources injects observation and scheduling, not a kill deadline. A nil
// sampler requests the kernel CPU clock, initialized before the child runs.
type watchSources struct {
	Context context.Context
	Now     func() time.Time
	Ticks   func() (<-chan time.Time, func())
	Sample  func(int, *os.File) (watchSample, error)
	Command func([]string) *exec.Cmd
}
type watchSample struct {
	Bytes, CPUTimeMS int64
	CPUTimeNS        uint64 // compare unrounded CPU: sub-millisecond work is progress too
	LastLine         string
	CPUSource        string
	CPUChanged       bool // read-only process samples saw runtime or membership change
}

func (s watchSample) cpuProgress() uint64 {
	if s.CPUTimeNS != 0 {
		return s.CPUTimeNS
	}
	return uint64(s.CPUTimeMS) * 1000000
}

type watchdogState struct {
	previous      watchSample
	initialized   bool
	SilentSamples int
	Unavailable   string
}

func (s *watchdogState) observe(sample watchSample, err error) bool {
	if s.Unavailable != "" {
		return false
	}
	if err != nil {
		s.Unavailable = "watchdog unavailable: " + err.Error()
		s.SilentSamples = 0
		return false
	}
	if !s.initialized {
		s.previous = sample
		s.initialized = true
		return false
	}
	if sample.Bytes > s.previous.Bytes || sample.CPUChanged || sample.cpuProgress() > s.previous.cpuProgress() {
		s.SilentSamples = 0
	} else {
		s.SilentSamples++
	}
	s.previous = sample
	return s.SilentSamples >= WatchdogSilentSamples
}
func realWatchSources() *watchSources {
	return &watchSources{Now: time.Now, Ticks: func() (<-chan time.Time, func()) {
		ticker := time.NewTicker(WatchdogSampleInterval)
		return ticker.C, ticker.Stop
	}, Command: func(args []string) *exec.Cmd { return exec.Command(args[0], args[1:]...) }}
}
func sourcesFor(check PlanCheck) *watchSources {
	if check.watchdog == nil {
		return realWatchSources()
	}
	copy := *check.watchdog
	defaults := realWatchSources()
	if copy.Now == nil {
		copy.Now = defaults.Now
	}
	if copy.Ticks == nil {
		copy.Ticks = defaults.Ticks
	}
	if copy.Command == nil {
		copy.Command = defaults.Command
	}
	return &copy
}

// Only sixty consecutive complete observations without output or CPU growth kill
// the process group. Unavailable accounting is reported immediately, not only when
// a potentially unbounded check finally exits. Our warning cannot count as progress:
// accounting failure has already permanently disabled this execution's detection.
func watchCommand(cmd *exec.Cmd, log *os.File, sources *watchSources) (error, watchdogState) {
	state := watchdogState{}
	observe := func(sample watchSample, err error) bool {
		before := state.Unavailable
		stalled := state.observe(sample, err)
		if before == "" && state.Unavailable != "" {
			fmt.Fprintln(log, state.Unavailable)
		}
		return stalled
	}
	sample, err := sources.Sample(cmd.Process.Pid, log)
	if err == nil && sample.CPUSource != "" {
		fmt.Fprintln(log, "watchdog CPU source: "+sample.CPUSource)
		// The source line is our metadata, not progress by the command.
		if info, statErr := log.Stat(); statErr == nil {
			sample.Bytes = info.Size()
		}
	}
	observe(sample, err)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticks, stop := sources.Ticks()
	defer stop()
	var canceled <-chan struct{}
	if sources.Context != nil {
		canceled = sources.Context.Done()
	}
	for {
		select {
		case err := <-done:
			return err, state
		case <-canceled:
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
			return sources.Context.Err(), state
		case <-ticks:
			select {
			case err := <-done:
				return err, state
			default:
			}
			sample, err := sources.Sample(cmd.Process.Pid, log)
			if observe(sample, err) {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return <-done, state
			}
		}
	}
}

func accountingContinuity(sample func(int, *os.File) (watchSample, error)) func(int, *os.File) (watchSample, error) {
	var previous uint64
	initialized := false
	return func(pid int, log *os.File) (watchSample, error) {
		reading, err := sample(pid, log)
		if err == nil && initialized && reading.cpuProgress() < previous {
			err = fmt.Errorf("CPU accounting decreased")
		}
		previous, initialized = reading.cpuProgress(), true
		return reading, err
	}
}

// clockSampler reads the preferred inherited kernel counter. The Linux launcher
// selects read-only process accounting when this counter is refused.
func clockSampler(counter io.Reader, unavailable error) func(int, *os.File) (watchSample, error) {
	return accountingContinuity(func(_ int, log *os.File) (watchSample, error) {
		var ns uint64
		err := unavailable
		if err == nil {
			var raw [8]byte
			_, err = io.ReadFull(counter, raw[:])
			if err == nil {
				ns = binary.NativeEndian.Uint64(raw[:])
			}
		}
		reading, err := sampleOutput(log, ns, err)
		reading.CPUSource = "kernel task-clock counter"
		return reading, err
	})
}
func sampleOutput(log *os.File, cpuNS uint64, cpuError error) (watchSample, error) {
	info, err := log.Stat()
	if err != nil {
		return watchSample{}, err
	}
	sample := watchSample{Bytes: info.Size(), CPUTimeNS: cpuNS, CPUTimeMS: int64(cpuNS / 1000000)}
	tail := make([]byte, 4096)
	offset := info.Size() - int64(len(tail))
	if offset < 0 {
		offset = 0
	}
	n, _ := log.ReadAt(tail, offset)
	lines := strings.Split(strings.TrimRight(string(tail[:n]), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(lines[i], "watchdog CPU source: ") {
			sample.LastLine = lines[i]
			break
		}
	}
	return sample, cpuError
}
func readFastVerdict(path string) (VerdictPayload, error) {
	var record struct {
		Payload VerdictPayload `json:"payload"`
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return VerdictPayload{}, err
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return VerdictPayload{}, err
	}
	for _, check := range record.Payload.Checks {
		if check.CheckID == "" || check.SilentSamples != WatchdogSilentSamples {
			continue
		}
		res := classifyCheck(checkresult.Observation{
			Check:  check.CheckID,
			Commit: record.Payload.TreeSHA,
			Source: checkresult.ResultWord,
			Word:   check.Result,
		})
		if res.Outcome == checkresult.Unknown && res.Detail == checkresult.DetailStalled {
			return record.Payload, nil
		}
	}
	return VerdictPayload{}, fmt.Errorf("no stalled check with sample observations in %s", path)
}
func stallNote(check VerdictCheck) string {
	return fmt.Sprintf("%s: stalled after %d ms; last output %q; group CPU %d ms; %d silent samples", check.CheckID, check.DurationMS, check.LastOutputLine, check.CPUTimeMS, check.SilentSamples)
}
