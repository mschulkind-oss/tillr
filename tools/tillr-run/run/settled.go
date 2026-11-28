package run

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	records                                            func() error
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
	// harnessEnded is the adapter's statement that it ended a settled harness.
	harnessEnded string
	// repeating is the call pi named when it ended the session for repeating it.
	repeating *RepeatingCall
	// loopRecovery summarizes any in-run loop recoveries pi performed.
	loopRecovery *LoopRecovery
	// providerError is the provider's message when this execution's last assistant turn
	// ended with stopReason "error"; a later assistant turn that did not clears it.
	// parked says a landing-call offer was staged, which is an ending of its own.
	providerError string
	parked        bool
	after         func(time.Duration) <-chan time.Time
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
			Type         string          `json:"type"`
			Aborted      bool            `json:"aborted"`
			After        string          `json:"after"`
			Reason       string          `json:"reason"`
			Actions      []string        `json:"actions"`
			Exit         *int            `json:"harness_exit"`
			Tool         string          `json:"toolName"`
			Args         json.RawMessage `json:"args"`
			Streak       int             `json:"streak"`
			Action       string          `json:"action"`
			Shape        string          `json:"shape"`
			TokensBefore int64           `json:"tokensBefore"`
			TokensAfter  int64           `json:"tokensAfter"`
			Message      *providerTurn   `json:"message"`
			Messages     []providerTurn  `json:"messages"`
			Success      *bool           `json:"success"`
			FinalError   string          `json:"finalError"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "message", "message_end", "turn_end":
			if e.Message != nil {
				m.noteTurn(*e.Message)
			}
		case "agent_end":
			for i := len(e.Messages) - 1; i >= 0; i-- {
				if e.Messages[i].Role == "assistant" {
					m.noteTurn(e.Messages[i])
					break
				}
			}
		case "auto_retry_end":
			if e.Success != nil && !*e.Success && m.providerError == "" {
				m.providerError = providerMessage("", e.FinalError)
			}
		case "parked":
			m.parked = true
		case "agent_settled":
			if !e.Aborted {
				m.settledAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		case HarnessEndedEvent:
			m.harnessEnded = describeHarnessEnded(e.After, e.Reason, e.Actions, e.Exit)
		case LoopDetectedEvent:
			if e.Action == "recovered" {
				if m.loopRecovery == nil {
					m.loopRecovery = &LoopRecovery{}
				}
				m.loopRecovery.Count++
				if e.Shape != "" {
					m.loopRecovery.Shape = e.Shape
				}
				m.loopRecovery.TokensBefore = e.TokensBefore
				m.loopRecovery.TokensAfter = e.TokensAfter
			} else {
				m.repeating = repeatingCall(e.Tool, e.Action, e.Shape, e.Args, e.Streak)
			}
		case "agent_start":
			m.settledAt = ""
			m.ready = false
			m.idle = 0
		}
	}
	return nil
}

// providerTurn is the part of a Pi message that says how an assistant turn ended.
type providerTurn struct {
	Role         string `json:"role"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
}

// noteTurn follows the latest assistant turn: one that ended in error holds the
// provider's message, and a later one that did not clears it.
func (m *settledMonitor) noteTurn(t providerTurn) {
	if t.Role != "assistant" {
		return
	}
	if t.StopReason == "error" {
		m.providerError = providerMessage(t.ErrorMessage, m.providerError)
		return
	}
	m.providerError = ""
}

func providerMessage(message, fallback string) string {
	if text := strings.TrimSpace(message); text != "" {
		return text
	}
	if text := strings.TrimSpace(fallback); text != "" {
		return text
	}
	return "the provider reported an error without a message"
}

// HarnessEndedEvent is the stream event an adapter appends when it ended a harness
// whose session had settled and that did not exit (tillr-run pi-stop-at-gap).
const HarnessEndedEvent = "tillr_harness_ended"

// repeatingCall is the record's statement of one `loop_detected` event. The arguments
// are re-encoded with sorted keys and no whitespace, so the digest does not depend on how
// the harness spaced them.
func repeatingCall(tool, action, shape string, raw json.RawMessage, streak int) *RepeatingCall {
	if tool == "" && shape != "no-tool" {
		tool = "unnamed tool"
	}
	text := "null"
	var args any
	if len(raw) > 0 && json.Unmarshal(raw, &args) == nil {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if enc.Encode(args) == nil {
			text = strings.TrimSuffix(b.String(), "\n")
		}
	}
	quoted := text
	if len(quoted) > RepeatingCallArgsLimit {
		quoted = strings.ToValidUTF8(quoted[:RepeatingCallArgsLimit], "") + "…"
	}
	return &RepeatingCall{
		Tool:       tool,
		Args:       quoted,
		ArgsDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(text))),
		Streak:     streak,
		Action:     action,
		Shape:      shape,
	}
}

func describeHarnessEnded(after, reason string, actions []string, exit *int) string {
	if after == "" {
		after = "an unnamed event"
	}
	text := "ended at " + after
	if reason != "" {
		text += "; " + reason
	}
	if len(actions) > 0 {
		text += "; sent " + strings.Join(actions, ", then ")
	}
	if exit != nil {
		text += fmt.Sprintf("; harness exited %d", *exit)
	}
	return text
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
