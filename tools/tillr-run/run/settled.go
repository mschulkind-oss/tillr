package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SettledSampleInterval and SettledIdleSamples define the completed-session rule:
// five consecutive one-second observations with neither output nor process-group CPU.
const SettledSampleInterval = time.Second
const SettledIdleSamples = 5

type settledEnd struct{ settledAt, endedAt string }

type settledMonitor struct {
	disabled                                           bool
	log, stderr, backendDir, adapter, backend, durable string
	offset, startOffset                                int64
	partial                                            []byte
	settledAt                                          string
	revision, checkedRevision                          int64
	ready                                              bool
	lastActivity                                       string
	cpu                                                func(int) (string, error)
	idle                                               int
	end                                                *settledEnd
	after                                              func(time.Duration) <-chan time.Time
}

// observe reads only this execution's new stream, never the preceding resumed segment.
// Only a top-level event is evidence of settlement, not a quoted event in model text.
func (m *settledMonitor) observe() error {
	f, err := os.Open(m.log)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(m.offset, io.SeekStart); err != nil {
		return err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	m.offset += int64(len(b))
	m.revision += int64(len(b))
	m.partial = append(m.partial, b...)
	for {
		idx := bytes.IndexByte(m.partial, '\n')
		if idx < 0 {
			break
		}
		line := m.partial[:idx]
		m.partial = m.partial[idx+1:]
		var e struct {
			Type    string `json:"type"`
			Aborted bool   `json:"aborted"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "agent_settled":
			if !e.Aborted {
				m.settledAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		case "agent_start":
			m.settledAt = ""
			m.ready = false
			m.idle = 0
		}
	}
	return nil
}

func resultFooter(b []byte) bool {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	switch strings.TrimSpace(lines[len(lines)-1]) {
	case "RESULT: done", "RESULT: blocked", "RESULT: failed", "RESULT: trunk-broken":
		return true
	}
	return false
}

// reportReady uses the adapter's own dialect reader. A reusable lane report is not
// evidence: the report must be extracted afresh from this execution's stream.
func (m *settledMonitor) reportReady(ctx context.Context) bool {
	if m.settledAt == "" {
		return false
	}
	if m.checkedRevision == m.revision {
		return m.ready
	}
	m.checkedRevision = m.revision
	stream, err := os.ReadFile(m.log)
	if err != nil || m.startOffset > int64(len(stream)) {
		m.ready = false
		return false
	}
	stream = stream[m.startOffset:]
	if m.backendDir == "" { // Raw fixture commands have a plain-text report.
		m.ready = resultFooter(stream)
		return m.ready
	}
	dir := filepath.Join(m.durable, "settled-probe")
	if os.MkdirAll(dir, 0700) != nil {
		m.ready = false
		return false
	}
	if err := os.Remove(filepath.Join(dir, "report.md")); err != nil && !os.IsNotExist(err) {
		m.ready = false
		return false
	}
	probeLog := filepath.Join(dir, "stream.log")
	if os.WriteFile(probeLog, stream, 0600) != nil {
		m.ready = false
		return false
	}
	readSpend(ctx, m.backendDir, m.adapter, m.backend, dir, probeLog, nil)
	b, err := os.ReadFile(filepath.Join(dir, "report.md"))
	m.ready = err == nil && resultFooter(b)
	return m.ready
}

// groupCPU describes all members of the harness's private process group, including
// helpers, by pid/start time/CPU ticks. Membership changes count as activity too.
// Linux /proc is required for this rule; unreadable counters never authorize a kill.
func groupCPU(pgid int) (string, error) {
	return groupCPUWithStat(pgid, os.ReadFile)
}

func groupCPUWithStat(pgid int, readStat func(string) ([]byte, error)) (string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		raw, err := readStat(filepath.Join("/proc", entry.Name(), "stat"))
		// A process exiting between enumeration and stat can return ESRCH,
		// not just ENOENT. Neither is an unreadable counter for a live process.
		if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return "", err
		}
		end := strings.LastIndexByte(string(raw), ')')
		if end < 0 {
			return "", fmt.Errorf("invalid proc stat")
		}
		fields := strings.Fields(string(raw[end+1:]))
		if len(fields) < 20 {
			return "", fmt.Errorf("short proc stat")
		}
		group, err := strconv.Atoi(fields[2])
		if err != nil {
			return "", err
		}
		if group == pgid {
			fmt.Fprintf(&out, "%s:%s:%s:%s:%s:%s;", entry.Name(), fields[19], fields[11], fields[12], fields[13], fields[14])
		}
	}
	if out.Len() == 0 {
		return "", fmt.Errorf("harness group disappeared")
	}
	return out.String(), nil
}

func (m *settledMonitor) activity(pid int) (string, error) {
	readCPU := m.cpu
	if readCPU == nil {
		readCPU = groupCPU
	}
	cpu, err := readCPU(pid)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(cpu)
	for _, path := range []string{m.log, m.stderr} {
		info, err := os.Stat(path)
		if os.IsNotExist(err) && path == m.stderr {
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
	}
	return out.String(), nil
}

func (m *settledMonitor) sample(ctx context.Context, pid int) bool {
	if m.disabled {
		return false
	}
	if m.observe() != nil || !m.reportReady(ctx) {
		m.idle = 0
		m.lastActivity = ""
		return false
	}
	activity, err := m.activity(pid)
	if err != nil {
		m.idle = 0
		m.lastActivity = ""
		return false
	}
	if activity != m.lastActivity {
		m.idle = 0
	} else {
		m.idle++
	}
	m.lastActivity = activity
	return m.idle == SettledIdleSamples
}
