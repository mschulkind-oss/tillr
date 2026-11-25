package pihelper

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStopAtGapParkedExit(t *testing.T) {
	runCase := func(t *testing.T, script string) (int, string, string) {
		t.Helper()
		dir := t.TempDir()
		pausePath := filepath.Join(dir, "pause")
		var out, errBuf bytes.Buffer
		env := append(os.Environ(), "TILLR_HARNESS_EXIT_BOUND_SECONDS=0.5")
		code := RunStopAtGap([]string{pausePath, "sh", "-c", script}, env, &out, &errBuf)
		return code, out.String(), errBuf.String()
	}

	t.Run("land and sibling toolResult finish batch", func(t *testing.T) {
		script := `printf '{"type":"parked","toolCallId":"land"}\n'; sleep 0.2; printf '{"type":"message_end","message":{"role":"toolResult","toolCallId":"sibling"}}\n'; printf '{"type":"agent_end"}\n'`
		code, stdout, stderr := runCase(t, script)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr)
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		var types []string
		for _, line := range lines {
			var ev struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("invalid JSON line %q: %v", line, err)
			}
			types = append(types, ev.Type)
		}
		want := []string{"parked", "message_end", "agent_end"}
		if !reflect.DeepEqual(types, want) {
			t.Fatalf("event types = %v, want %v", types, want)
		}
	})

	t.Run("incomplete batch has distinct failure", func(t *testing.T) {
		script := `printf '{"type":"parked"}\n'; exec sleep 5`
		code, stdout, _ := runCase(t, script)
		if code == 0 {
			t.Fatalf("expected non-zero exit for incomplete batch, got 0 (stdout=%q)", stdout)
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		var ev struct {
			After string `json:"after"`
		}
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
			t.Fatalf("invalid JSON last line %q: %v", lines[len(lines)-1], err)
		}
		if ev.After != "parked-batch-incomplete" {
			t.Fatalf("after = %q, want %q", ev.After, "parked-batch-incomplete")
		}
	})

	t.Run("parked process that does not exit is signaled at bound", func(t *testing.T) {
		script := `printf '{"type":"parked"}\n'; printf '{"type":"agent_end"}\n'; exec sleep 5`
		code, stdout, stderr := runCase(t, script)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr)
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		var ev struct {
			After string `json:"after"`
		}
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
			t.Fatalf("invalid JSON last line %q: %v", lines[len(lines)-1], err)
		}
		if ev.After != "parked" {
			t.Fatalf("after = %q, want %q", ev.After, "parked")
		}
	})
}

func TestStopAtGapDrainsFinalOutput(t *testing.T) {
	dir := t.TempDir()
	pausePath := filepath.Join(dir, "pause")
	var out, errBuf bytes.Buffer
	code := RunStopAtGap(
		[]string{pausePath, "sh", "-c", `sleep 0.02; printf 'final output\n'; exit 17`},
		os.Environ(),
		&out,
		&errBuf,
	)
	if code != 17 {
		t.Fatalf("expected exit 17, got %d (stderr=%q)", code, errBuf.String())
	}
	if out.String() != "final output\n" {
		t.Fatalf("stdout = %q, want %q", out.String(), "final output\n")
	}
}

const testJournalSchema = `
CREATE TABLE instances (instance_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, process_id INTEGER NOT NULL, disabled_at INTEGER, pi_session_id TEXT);
CREATE TABLE tasks (task_key TEXT PRIMARY KEY, started_by_instance TEXT, completion_policy TEXT, status TEXT, ended_at INTEGER);
CREATE TABLE deliveries (delivery_key TEXT PRIMARY KEY, task_key TEXT NOT NULL, wake_requested INTEGER NOT NULL);
CREATE TABLE delivery_events (event_id INTEGER PRIMARY KEY, delivery_key TEXT NOT NULL, event TEXT NOT NULL, via TEXT);
`

func TestStopAtGapOwedWorkReadsBackgroundTaskJournal(t *testing.T) {
	type taskRow struct {
		key    string
		policy string
		ended  *int64
		wake   *int
		events []string
	}
	int64Ptr := func(v int64) *int64 { return &v }
	intPtr := func(v int) *int { return &v }
	boolPtr := func(b bool) *bool { return &b }

	dir := t.TempDir()
	makeJournal := func(t *testing.T, name string, pid int, started int64, disabled *int64, rows, priorRows []taskRow) string {
		t.Helper()
		path := filepath.Join(dir, name+".sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(testJournalSchema); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			"INSERT INTO instances VALUES ('i', ?, ?, ?, 'session')",
			started, pid, disabled,
		); err != nil {
			t.Fatal(err)
		}
		if len(priorRows) > 0 {
			if _, err := db.Exec(
				"INSERT INTO instances VALUES ('prev', ?, ?, NULL, 'session')",
				100, 9999,
			); err != nil {
				t.Fatal(err)
			}
		}
		insertBatch := func(inst string, batch []taskRow) {
			for _, r := range batch {
				if _, err := db.Exec(
					"INSERT INTO tasks VALUES (?, ?, ?, 'x', ?)",
					r.key, inst, r.policy, r.ended,
				); err != nil {
					t.Fatal(err)
				}
				if r.wake != nil {
					dKey := "d" + r.key
					if _, err := db.Exec(
						"INSERT INTO deliveries VALUES (?, ?, ?)",
						dKey, r.key, *r.wake,
					); err != nil {
						t.Fatal(err)
					}
					for _, ev := range r.events {
						if _, err := db.Exec(
							"INSERT INTO delivery_events (delivery_key, event) VALUES (?, ?)",
							dKey, ev,
						); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
		insertBatch("i", rows)
		insertBatch("prev", priorRows)
		return path
	}

	cases := []struct {
		name string
		path string
		want *bool
	}{
		{
			name: "leaked fallback hold",
			path: makeJournal(t, "c1", 4242, 1000, nil, []taskRow{
				{key: "a", policy: "wake", ended: int64Ptr(5), wake: intPtr(1), events: []string{"fallback-injected", "observed"}},
			}, nil),
			want: boolPtr(false),
		},
		{
			name: "no tasks at all",
			path: makeJournal(t, "c2", 4242, 1000, nil, nil, nil),
			want: boolPtr(false),
		},
		{
			name: "running wake task",
			path: makeJournal(t, "c3", 4242, 1000, nil, []taskRow{
				{key: "a", policy: "wake", ended: nil, wake: nil},
			}, nil),
			want: boolPtr(true),
		},
		{
			name: "running notify task holds nothing",
			path: makeJournal(t, "c4", 4242, 1000, nil, []taskRow{
				{key: "a", policy: "notify", ended: nil, wake: nil},
			}, nil),
			want: boolPtr(false),
		},
		{
			name: "ended wake, completion not yet decided",
			path: makeJournal(t, "c5", 4242, 1000, nil, []taskRow{
				{key: "a", policy: "wake", ended: int64Ptr(5), wake: nil},
			}, nil),
			want: boolPtr(true),
		},
		{
			name: "ended wake, completion enqueued not observed",
			path: makeJournal(t, "c6", 4242, 1000, nil, []taskRow{
				{key: "a", policy: "wake", ended: int64Ptr(5), wake: intPtr(1), events: []string{"enqueued"}},
			}, nil),
			want: boolPtr(true),
		},
		{
			name: "ended wake in earlier process, never observed",
			path: makeJournal(t, "c7", 4242, 1000, nil, nil, []taskRow{
				{key: "p", policy: "wake", ended: int64Ptr(5), wake: nil},
			}),
			want: boolPtr(false),
		},
		{
			name: "ended wake in earlier process, enqueued not observed",
			path: makeJournal(t, "c8", 4242, 1000, nil, nil, []taskRow{
				{key: "p", policy: "wake", ended: int64Ptr(5), wake: intPtr(1), events: []string{"enqueued"}},
			}),
			want: boolPtr(false),
		},
		{
			name: "running wake in earlier process still holds",
			path: makeJournal(t, "c9", 4242, 1000, nil, nil, []taskRow{
				{key: "p", policy: "wake", ended: nil, wake: nil},
			}),
			want: boolPtr(true),
		},
		{
			name: "stopped wake task",
			path: makeJournal(t, "c10", 4242, 1000, nil, []taskRow{
				{key: "a", policy: "wake", ended: int64Ptr(5), wake: intPtr(0)},
			}, nil),
			want: boolPtr(false),
		},
		{
			name: "enqueue failed",
			path: makeJournal(t, "c11", 4242, 1000, nil, []taskRow{
				{key: "a", policy: "wake", ended: int64Ptr(5), wake: intPtr(1), events: []string{"enqueue-failed"}},
			}, nil),
			want: boolPtr(false),
		},
		{
			name: "another process",
			path: makeJournal(t, "c12", 1, 1000, nil, nil, nil),
			want: nil,
		},
		{
			name: "an older process with this pid",
			path: makeJournal(t, "c13", 4242, 1, nil, nil, nil),
			want: nil,
		},
		{
			name: "disabled journal",
			path: makeJournal(t, "c14", 4242, 1000, int64Ptr(7), nil, nil),
			want: nil,
		},
		{
			name: "no journal file",
			path: filepath.Join(dir, "absent.sqlite"),
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := OwedWork(tc.path, 4242, 500)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("OwedWork() = %v, want nil", *got)
				}
			} else if got == nil || *got != *tc.want {
				t.Fatalf("OwedWork() = %v, want %v", got, *tc.want)
			}
		})
	}

	if got := JournalPath(map[string]string{"PI_BACKGROUND_TASK_JOURNAL": "off"}); got != "" {
		t.Fatalf("JournalPath(off) = %q, want empty", got)
	}
	if got := JournalPath(map[string]string{"XDG_STATE_HOME": "/s"}); got != "/s/pi-background-tasks/journal.sqlite" {
		t.Fatalf("JournalPath(XDG_STATE_HOME) = %q, want /s/pi-background-tasks/journal.sqlite", got)
	}
}

func TestEndSettledHelperProcess(t *testing.T) {
	val := os.Getenv("TILLR_TEST_END_SETTLED_SURVIVES")
	if val == "" {
		return
	}
	survives, err := strconv.Atoi(val)
	if err != nil {
		os.Exit(2)
	}
	sigCh := make(chan os.Signal, 10)
	signal.Notify(sigCh, syscall.SIGTERM)
	fmt.Println("ready")
	count := 0
	for range sigCh {
		count++
		if count > survives {
			os.Exit(0)
		}
	}
}

func TestStopAtGapSettledShutdownEscalates(t *testing.T) {
	cases := []struct {
		survives int
		wantSigs []string
	}{
		{survives: 0, wantSigs: []string{"SIGTERM"}},
		{survives: 1, wantSigs: []string{"SIGTERM", "SIGTERM"}},
		{survives: 2, wantSigs: []string{"SIGTERM", "SIGTERM", "SIGKILL"}},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("survives=%d", tc.survives), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestEndSettledHelperProcess$")
			cmd.Env = append(os.Environ(), fmt.Sprintf("TILLR_TEST_END_SETTLED_SURVIVES=%d", tc.survives))
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waitCh := make(chan error, 1)
			go func() {
				waitCh <- cmd.Wait()
			}()

			reader := bufio.NewReader(stdout)
			line, err := reader.ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "ready" {
				_ = cmd.Process.Kill()
				<-waitCh
				t.Fatalf("helper failed to signal readiness: line=%q err=%v", line, err)
			}

			got := EndSettled(cmd.Process, waitCh, 50*time.Millisecond)
			want := make([]string, len(tc.wantSigs))
			for i, sig := range tc.wantSigs {
				want[i] = fmt.Sprintf("%s to pi %d", sig, cmd.Process.Pid)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("EndSettled() = %v, want %v", got, want)
			}
		})
	}
}
