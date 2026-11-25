package pihelper

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	endedEvent          = "tillr_harness_ended"
	defaultExitBound    = 120.0
	owedRecheckInterval = 1 * time.Second
	defaultSettledGrace = 30.0
	journalEnv          = "PI_BACKGROUND_TASK_JOURNAL"
)

var journalDisabled = map[string]bool{
	"0":        true,
	"false":    true,
	"off":      true,
	"no":       true,
	"none":     true,
	"disabled": true,
}

// JournalPath resolves the pi-background-tasks SQLite journal path from env.
func JournalPath(env map[string]string) string {
	configured := strings.TrimSpace(env[journalEnv])
	if journalDisabled[strings.ToLower(configured)] {
		return ""
	}
	if configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return configured
		}
		return abs
	}
	state := strings.TrimSpace(env["XDG_STATE_HOME"])
	if state == "" {
		home := env["HOME"]
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "pi-background-tasks", "journal.sqlite")
}

// OwedWakes reports whether the session in process pid still owes a wake turn.
// The second return value is false when it cannot tell (missing/disabled/unreadable
// journal, missing or ambiguous instance row).
func OwedWakes(path string, pid int, sinceMS int64) ([]string, bool) {
	if path == "" {
		return nil, false
	}
	if _, err := os.Stat(path); err != nil {
		return nil, false
	}
	uri := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=1000", path)
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, false
	}
	defer db.Close()

	rows, err := db.Query(
		"SELECT instance_id, pi_session_id, disabled_at FROM instances WHERE process_id=? AND started_at>=?",
		pid, sinceMS,
	)
	if err != nil {
		return nil, false
	}
	var currentIDs []any
	sessions := make(map[string]struct{})
	hasInvalid := false
	for rows.Next() {
		var instID sql.NullString
		var sessID sql.NullString
		var disabledAt sql.NullString
		if err := rows.Scan(&instID, &sessID, &disabledAt); err != nil {
			rows.Close()
			return nil, false
		}
		if disabledAt.Valid || !sessID.Valid || sessID.String == "" || !instID.Valid {
			hasInvalid = true
		} else {
			sessions[sessID.String] = struct{}{}
		}
		currentIDs = append(currentIDs, instID.String)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false
	}
	rows.Close()

	if len(currentIDs) == 0 || hasInvalid || len(sessions) != 1 {
		return nil, false
	}
	var session string
	for s := range sessions {
		session = s
	}

	instRows, err := db.Query("SELECT instance_id FROM instances WHERE pi_session_id=?", session)
	if err != nil {
		return nil, false
	}
	var ids []any
	for instRows.Next() {
		var id sql.NullString
		if err := instRows.Scan(&id); err != nil {
			instRows.Close()
			return nil, false
		}
		ids = append(ids, id.String)
	}
	if err := instRows.Err(); err != nil {
		instRows.Close()
		return nil, false
	}
	instRows.Close()
	if len(ids) == 0 {
		return nil, false
	}

	marks := placeholders(len(ids))
	currentMarks := placeholders(len(currentIDs))
	query := fmt.Sprintf(`
            SELECT t.task_key FROM tasks t
            WHERE t.started_by_instance IN (%s) AND t.completion_policy='wake' AND (
              t.ended_at IS NULL OR (
                t.started_by_instance IN (%s) AND NOT EXISTS (
                  SELECT 1 FROM deliveries d WHERE d.task_key=t.task_key AND (
                    d.wake_requested=0 OR EXISTS (
                      SELECT 1 FROM delivery_events e WHERE e.delivery_key=d.delivery_key
                      AND e.event IN ('observed','enqueue-failed'))))))`, marks, currentMarks)

	qArgs := make([]any, 0, len(ids)+len(currentIDs))
	qArgs = append(qArgs, ids...)
	qArgs = append(qArgs, currentIDs...)

	taskRows, err := db.Query(query, qArgs...)
	if err != nil {
		return nil, false
	}
	defer taskRows.Close()
	keys := []string{}
	for taskRows.Next() {
		var key string
		if err := taskRows.Scan(&key); err != nil {
			return nil, false
		}
		keys = append(keys, key)
	}
	if err := taskRows.Err(); err != nil {
		return nil, false
	}
	return keys, true
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// OwedWork returns nil if OwedWakes cannot tell, or a pointer to len(wakes) > 0.
func OwedWork(path string, pid int, sinceMS int64) *bool {
	wakes, ok := OwedWakes(path, pid, sinceMS)
	if !ok {
		return nil
	}
	b := len(wakes) > 0
	return &b
}

// EndSettled ends a settled pi process with SIGTERM, SIGTERM, SIGKILL, waiting up
// to bound between signals, and returns the list of actions taken.
func EndSettled(proc *os.Process, waitCh <-chan error, bound time.Duration) []string {
	actions := []string{}
	if proc == nil {
		return actions
	}
	sigs := []syscall.Signal{syscall.SIGTERM, syscall.SIGTERM, syscall.SIGKILL}
	for _, sig := range sigs {
		if err := proc.Signal(sig); err != nil {
			if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
				break
			}
			break
		}
		actions = append(actions, fmt.Sprintf("%s to pi %d", sigName(sig), proc.Pid))
		if sig == syscall.SIGKILL {
			<-waitCh
			break
		}
		timer := time.NewTimer(bound)
		select {
		case <-waitCh:
			timer.Stop()
			return actions
		case <-timer.C:
		}
	}
	return actions
}

// EndParked enforces the absolute park deadline without extending it for disposal.
func EndParked(proc *os.Process, waitCh <-chan error) []string {
	actions := []string{}
	if proc == nil {
		return actions
	}
	sigs := []syscall.Signal{syscall.SIGTERM, syscall.SIGTERM, syscall.SIGKILL}
	for _, sig := range sigs {
		select {
		case <-waitCh:
			return actions
		default:
		}
		if err := proc.Signal(sig); err != nil {
			if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
				break
			}
			break
		}
		actions = append(actions, fmt.Sprintf("%s to pi %d", sigName(sig), proc.Pid))
	}
	<-waitCh
	return actions
}

func sigName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	default:
		return sig.String()
	}
}

func parseEnvSlice(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok {
			m[k] = v
		}
	}
	return m
}

func secondsFromEnv(env map[string]string, name string, def float64) time.Duration {
	raw, ok := env[name]
	if !ok {
		return time.Duration(def * float64(time.Second))
	}
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return time.Duration(def * float64(time.Second))
	}
	if val < 0 {
		val = 0
	}
	return time.Duration(val * float64(time.Second))
}

// RunStopAtGap implements the pi-stop-at-gap face.
func RunStopAtGap(args []string, env []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "usage: tillr-run pi-stop-at-gap [--owed-wakes <pid> <since_ms> | <pause-request-path> <cmd> [args...]]")
		return 0
	}
	envMap := parseEnvSlice(env)
	if len(args) >= 1 && args[0] == "--owed-wakes" {
		if len(args) < 3 {
			fmt.Fprintln(stderr, "stop-at-gap: --owed-wakes requires <pid> <since_ms>")
			return 2
		}
		pid, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "stop-at-gap: invalid pid %q: %v\n", args[1], err)
			return 2
		}
		sinceMS, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "stop-at-gap: invalid since_ms %q: %v\n", args[2], err)
			return 2
		}
		wakes, ok := OwedWakes(JournalPath(envMap), pid, sinceMS)
		if !ok {
			_, _ = fmt.Fprintln(stdout, "null")
			return 0
		}
		encoded, _ := json.Marshal(wakes)
		_, _ = fmt.Fprintln(stdout, string(encoded))
		return 0
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "stop-at-gap: requires <pause-request-path> <cmd> [args...]")
		return 2
	}

	requestPath := args[0]
	cmdArgs := args[1:]
	launchedMS := time.Now().UnixMilli() - 1000
	journal := JournalPath(envMap)
	grace := secondsFromEnv(envMap, "TILLR_SETTLED_EXIT_GRACE_SECONDS", defaultSettledGrace)
	bound := secondsFromEnv(envMap, "TILLR_HARNESS_EXIT_BOUND_SECONDS", defaultExitBound)

	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(stderr, "stop-at-gap: %v\n", err)
		return 1
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = w
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		fmt.Fprintf(stderr, "stop-at-gap: %v\n", err)
		return 1
	}
	_ = w.Close()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	dataCh := make(chan []byte, 32)
	go func() {
		defer close(dataCh)
		buf := make([]byte, 65536)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				dataCh <- chunk
			}
			if readErr != nil {
				return
			}
		}
	}()

	ready := false
	sent := false
	var pending []byte
	oversized := false
	var head []byte
	settled := false
	var parkedAt time.Time
	parkedBatchDone := false
	var nextCheck time.Time
	var ended []string
	didEnd := false
	endedAfter := "agent_settled"
	exited := false
	var waitErr error
	dataClosed := false

	processChunk := func(data []byte) {
		_, _ = stdout.Write(data)
		for _, part := range splitKeepEnds(data) {
			if len(head) < 64 {
				need := 64 - len(head)
				if len(part) < need {
					need = len(part)
				}
				head = append(head, part[:need]...)
			}
			if len(part) > 0 && part[len(part)-1] == '\n' {
				if bytes.HasPrefix(head, []byte(`{"type":"parked"`)) {
					settled = false
					if parkedAt.IsZero() {
						parkedAt = time.Now()
					}
				} else if !parkedAt.IsZero() && bytes.HasPrefix(head, []byte(`{"type":"agent_end"`)) {
					parkedBatchDone = true
				} else if parkedAt.IsZero() && bytes.HasPrefix(head, []byte(`{"type":"agent_settled"`)) {
					settled = true
					nextCheck = time.Now().Add(grace)
				} else if bytes.HasPrefix(head, []byte(`{"type":"agent_start"`)) {
					settled = false
				}
				head = head[:0]
			}
		}
		if !ready {
			for _, part := range splitKeepEnds(data) {
				if len(pending)+len(part) > 65536 {
					oversized = true
					pending = pending[:0]
				}
				if !oversized {
					pending = append(pending, part...)
				}
				if len(part) > 0 && part[len(part)-1] == '\n' {
					if !oversized && utf8.Valid(pending) {
						var header map[string]any
						if json.Unmarshal(pending, &header) == nil {
							if typ, _ := header["type"].(string); typ == "session" {
								ready = true
							}
						}
					}
					pending = pending[:0]
					oversized = false
					if ready {
						break
					}
				}
			}
		}
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		if !exited {
			select {
			case err := <-waitCh:
				waitErr = err
				exited = true
			default:
			}
		}

		if exited {
			// Drain any buffered output without blocking indefinitely if a grandchild holds the pipe.
			drainTimer := time.NewTimer(10 * time.Millisecond)
		drainExit:
			for {
				select {
				case data, ok := <-dataCh:
					if !ok {
						dataClosed = true
						break drainExit
					}
					processChunk(data)
				case <-drainTimer.C:
					break drainExit
				}
			}
			drainTimer.Stop()
			break loop
		}

		select {
		case err := <-waitCh:
			waitErr = err
			exited = true
			continue loop
		case data, ok := <-dataCh:
			if !ok {
				dataClosed = true
			} else {
				processChunk(data)
			}
		case <-ticker.C:
		}

		if parkedAt.IsZero() && ready && !sent && !exited {
			if _, err := os.Stat(requestPath); err == nil {
				_ = cmd.Process.Signal(syscall.SIGUSR1)
				sent = true
			}
		}
		if !parkedAt.IsZero() && !exited && !time.Now().Before(parkedAt.Add(bound)) {
			ended = EndParked(cmd.Process, waitCh)
			didEnd = true
			if parkedBatchDone {
				endedAfter = "parked"
			} else {
				endedAfter = "parked-batch-incomplete"
			}
			exited = true
			break loop
		}
		if settled && !sent && !exited && !time.Now().Before(nextCheck) {
			nextCheck = time.Now().Add(owedRecheckInterval)
			if ow := OwedWork(journal, cmd.Process.Pid, launchedMS); ow != nil && !*ow {
				ended = EndSettled(cmd.Process, waitCh, bound)
				didEnd = true
				settled = false
				exited = true
				break loop
			}
		}
		if dataClosed && exited {
			break loop
		}
	}

	if didEnd && !dataClosed {
		// Forward what pi flushed during its shutdown before the closing event.
		drainTimer := time.NewTimer(10 * time.Millisecond)
	drainEnded:
		for {
			select {
			case chunk, ok := <-dataCh:
				if !ok {
					break drainEnded
				}
				_, _ = stdout.Write(chunk)
				if len(chunk) > 0 {
					if chunk[len(chunk)-1] == '\n' {
						head = head[:0]
					} else {
						head = []byte{'x'}
					}
				}
			case <-drainTimer.C:
				break drainEnded
			}
		}
		drainTimer.Stop()
	}

	_ = r.Close()
	if !exited {
		waitErr = <-waitCh
	}
	_ = waitErr

	code := 0
	if cmd.ProcessState != nil {
		if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				code = 128 + int(ws.Signal())
			} else {
				code = ws.ExitStatus()
			}
		} else {
			code = cmd.ProcessState.ExitCode()
		}
	}

	if didEnd {
		var reason string
		switch endedAfter {
		case "parked":
			reason = "pi did not exit after parking"
		case "parked-batch-incomplete":
			reason = "parked batch did not finish before the exit bound"
		default:
			reason = "pi did not exit after its session settled with no background work owed"
		}
		if ended == nil {
			ended = []string{}
		}
		payload := struct {
			Type        string   `json:"type"`
			After       string   `json:"after"`
			Reason      string   `json:"reason"`
			Actions     []string `json:"actions"`
			HarnessExit int      `json:"harness_exit"`
		}{
			Type:        endedEvent,
			After:       endedAfter,
			Reason:      reason,
			Actions:     ended,
			HarnessExit: code,
		}
		line, _ := json.Marshal(payload)
		if len(head) > 0 {
			_, _ = stdout.Write([]byte{'\n'})
		}
		_, _ = stdout.Write(append(line, '\n'))
		if endedAfter == "parked-batch-incomplete" {
			return 1
		}
		return 0
	}
	return code
}

func splitKeepEnds(data []byte) [][]byte {
	var parts [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			parts = append(parts, data[start:i+1])
			start = i + 1
		}
	}
	if start < len(data) {
		parts = append(parts, data[start:])
	}
	return parts
}
