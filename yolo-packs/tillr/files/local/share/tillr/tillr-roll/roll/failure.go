package roll

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	shardFailureRe     = regexp.MustCompile(`^(?:go-test-shards:\s+)?\S+ shard \d+ (?:failed|timed out|cannot start|missing completion)\b`)
	goFailRe           = regexp.MustCompile(`^---\s*FAIL:\s*(\S+)`)
	shardFailedTestsRe = regexp.MustCompile(`failed \(exit -?\d+\):\s*(.+)$`)
)

func isSlotNotice(line string) bool {
	return strings.HasPrefix(line, "go-test-shards: ") &&
		(strings.Contains(line, " waiting for a test slot") || strings.Contains(line, " yielding to a waiting "))
}

func hasTracePrefix(line string) bool {
	for _, p := range TracePrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func dedupOrdered(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// FailureDetail extracts a concise failure summary from an error (especially *ExitError).
func FailureDetail(cause error) string {
	if cause == nil {
		return ""
	}
	var exitErr *ExitError
	if !errors.As(cause, &exitErr) {
		return cause.Error()
	}
	var parts []string
	if exitErr.Stdout != "" {
		parts = append(parts, exitErr.Stdout)
	}
	if exitErr.Stderr != "" {
		parts = append(parts, exitErr.Stderr)
	}
	raw := strings.Join(parts, "\n")
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" || hasTracePrefix(line) || isSlotNotice(line) {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return cause.Error()
	}

	var shardLines []string
	var failTests []string
	var diagnostics []string
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if shardFailureRe.MatchString(stripped) ||
			(strings.HasPrefix(stripped, "go-test-shards: ") && !strings.Contains(stripped, " tests passed,")) {
			normalized := stripped
			if !strings.HasPrefix(stripped, "go-test-shards: ") {
				normalized = "go-test-shards: " + stripped
			}
			shardLines = append(shardLines, normalized)
			if m := shardFailedTestsRe.FindStringSubmatch(normalized); m != nil {
				for _, name := range strings.Split(m[1], ",") {
					name = strings.TrimSpace(name)
					if name != "" {
						failTests = append(failTests, name)
					}
				}
			}
			continue
		}
		if m := goFailRe.FindStringSubmatch(stripped); m != nil {
			failTests = append(failTests, m[1])
			diagnostics = append(diagnostics, stripped)
			continue
		}
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) &&
			!strings.HasPrefix(stripped, "=== ") &&
			!strings.HasPrefix(stripped, "--- PASS:") {
			diagnostics = append(diagnostics, stripped)
		}
	}

	if len(shardLines) > 0 || len(failTests) > 0 {
		summary := dedupOrdered(shardLines)
		orderedTests := dedupOrdered(failTests)
		joined := strings.Join(summary, "\n")
		var missingTests []string
		for _, name := range orderedTests {
			if !strings.Contains(joined, name) {
				missingTests = append(missingTests, name)
			}
		}
		if len(missingTests) > 0 {
			summary = append(summary, "failing tests: "+strings.Join(missingTests, ", "))
		}
		diag := dedupOrdered(diagnostics)
		if len(diag) > 12 {
			diag = diag[:12]
		}
		summary = append(summary, diag...)
		return strings.Join(summary, "\n")
	}
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	return strings.Join(lines, "\n")
}

// Stepped executes fn and wraps any error in *RollFailedError.
// If step is "loop check" and fn returns an *ExitError with ExitCode == ExitIncompatible,
// the step is mapped to "store check".
func Stepped(step string, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if step == "loop check" && errors.As(err, &exitErr) && exitErr.ExitCode == ExitIncompatible {
		step = "store check"
	}
	return &RollFailedError{
		Step:   step,
		Cause:  err,
		Detail: FailureDetail(err),
	}
}

type rollFailedFact struct {
	Commit string `json:"commit"`
	Step   string `json:"step"`
	Detail string `json:"detail"`
	Mover  string `json:"mover,omitempty"`
	Ts     int64  `json:"ts,omitempty"`
}

// RecordFailure appends a drain.roll-failed event to the store (best effort).
func RecordFailure(store, commit, step, detail string, stderr io.Writer) {
	if stderr == nil {
		stderr = os.Stderr
	}
	fact := rollFailedFact{
		Commit: commit,
		Step:   step,
		Detail: strings.Join(strings.Fields(detail), " "),
	}
	at := time.Now().UnixMilli()
	if step == "store check" {
		fact.Mover = "operator"
	}
	payloadBytes, err := json.Marshal(fact)
	if err != nil {
		short := commit
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Fprintf(stderr, "tillr-roll: could not record the failed roll of %s: %v\n", short, err)
		return
	}

	if err := insertRollFailed(store, at, commit, string(payloadBytes)); err != nil {
		short := commit
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Fprintf(stderr, "tillr-roll: could not record the failed roll of %s: %v\n", short, err)
		if step == "store check" {
			fact.Ts = at
			alarmBytes, mErr := json.Marshal(fact)
			if mErr != nil {
				fmt.Fprintf(stderr, "tillr-roll: could not save the store roll alarm: %v; mover: operator\n", mErr)
				return
			}
			tmpDir, mkErr := os.MkdirTemp(filepath.Dir(store), ".roll-alarm-")
			if mkErr != nil {
				fmt.Fprintf(stderr, "tillr-roll: could not save the store roll alarm: %v; mover: operator\n", mkErr)
				return
			}
			defer os.RemoveAll(tmpDir)
			alarmPath := filepath.Join(tmpDir, "alarm.json")
			if wErr := os.WriteFile(alarmPath, alarmBytes, 0o644); wErr != nil {
				fmt.Fprintf(stderr, "tillr-roll: could not save the store roll alarm: %v; mover: operator\n", wErr)
				return
			}
			if rErr := os.Rename(alarmPath, store+".roll-alarm.json"); rErr != nil {
				fmt.Fprintf(stderr, "tillr-roll: could not save the store roll alarm: %v; mover: operator\n", rErr)
			}
		}
	}
}

func insertRollFailed(store string, at int64, commit, payload string) error {
	absStore, err := filepath.Abs(store)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+absStore+"?mode=rw&_pragma=busy_timeout(30000)")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(
		"INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, ?, ?, ?, ?)",
		at, "drain.roll-failed", "commit", commit, payload,
	)
	return err
}
