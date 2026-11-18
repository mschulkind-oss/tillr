package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The journal pi-background-tasks keeps, reduced to the columns the watcher reads.
const fakeJournalSchema = `
CREATE TABLE instances (instance_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, process_id INTEGER NOT NULL, disabled_at INTEGER);
CREATE TABLE tasks (task_key TEXT PRIMARY KEY, started_by_instance TEXT, completion_policy TEXT, status TEXT, ended_at INTEGER);
CREATE TABLE deliveries (delivery_key TEXT PRIMARY KEY, task_key TEXT NOT NULL, wake_requested INTEGER NOT NULL);
CREATE TABLE delivery_events (event_id INTEGER PRIMARY KEY, delivery_key TEXT NOT NULL, event TEXT NOT NULL, via TEXT);
`

// fakeSettledPi answers with a footer, settles, and then never exits on its own: the
// 2026-11-18 shape, where a wake task delivered by the background-task extension's
// fallback path leaked its session hold. Its journal says so. SIGTERM is honoured the
// way pi's print mode does (dispose, then exit 143), after IGNORE_TERMS of them.
const fakeSettledPi = `#!/usr/bin/env python3
import os, signal, sqlite3, sys, time
from pathlib import Path
sys.stdin.read()
db = sqlite3.connect(os.environ["PI_BACKGROUND_TASK_JOURNAL"])
db.executescript(os.environ["FAKE_JOURNAL_SCHEMA"])
now = int(time.time() * 1000)
db.execute("INSERT INTO instances VALUES ('inst', ?, ?, NULL)", (now, os.getpid()))
db.execute("INSERT INTO tasks VALUES ('t1', 'inst', 'wake', 'completed', ?)", (now,))
db.execute("INSERT INTO deliveries VALUES ('d1', 't1', 1)")
db.execute("INSERT INTO delivery_events (delivery_key, event, via) VALUES ('d1', 'fallback-injected', NULL), ('d1', 'observed', 'fallback')")
db.commit(); db.close()
terms = [0]
def term(*_):
    terms[0] += 1
    if terms[0] > int(os.environ["IGNORE_TERMS"]):
        Path("disposed").write_text(str(terms[0]))
        sys.stdout.write('{"type":"session_shutdown_flushed"}\n'); sys.stdout.flush()
        os._exit(143)
signal.signal(signal.SIGTERM, term)
with open(os.environ["FAKE_STREAM"]) as f:
    sys.stdout.write(f.read())
sys.stdout.write('{"type":"agent_settled","aborted":false}\n')
sys.stdout.write('{"type":"session_info_changed","name":"settled"}\n')
sys.stdout.flush()
while True:
    signal.pause()
`

// A settled worker whose pi never exits must still end: its report settled, the run an
// ordinary answered exit, and the adapter's ending recorded as an exception. Nothing in
// this test waits on wall time; the settled-idle samples are withheld, so only the
// adapter's stated ending can end the run.
func TestPiSettledHarnessThatNeverExitsIsEndedByTheAdapter(t *testing.T) {
	for _, ignore := range []string{"0"} {
		t.Run("honors-shutdown", func(t *testing.T) {
			dir := scratch(t, "pi-settled-exit")
			root := filepath.Join(dir, "lanes")
			lane := filepath.Join(root, "lane")
			worktree := filepath.Join(lane, "worktree")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(lane, "session-id"), []byte("12345678-1234-1234-1234-123456789abc\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			binDir := filepath.Join(dir, "bin")
			writeExecutable(t, filepath.Join(binDir, "pi"), fakeSettledPi)
			stream, err := filepath.Abs("backends/pi/testdata/complete.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAKE_STREAM", stream)
			t.Setenv("FAKE_JOURNAL_SCHEMA", fakeJournalSchema)
			t.Setenv("IGNORE_TERMS", ignore)
			t.Setenv("PI_BACKGROUND_TASK_JOURNAL", filepath.Join(dir, "journal.sqlite"))
			t.Setenv("TILLR_SETTLED_EXIT_GRACE_SECONDS", "0")
			t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
			t.Setenv("TILLR_SESSION_MODE", "new")
			t.Setenv("TILLR_TRACE_DIR", filepath.Join(dir, "trace"))
			s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			adapter, err := filepath.Abs("backends/pi/run-pi")
			if err != nil {
				t.Fatal(err)
			}
			backends, err := filepath.Abs("backends")
			if err != nil {
				t.Fatal(err)
			}
			never := make(chan time.Time)
			req := runner.Request{RunID: "run", LaneID: "lane", Backend: "pi", WorkingDir: "worktree", Command: []string{adapter, "--backend", "pi", "--model", "fixture", "--effort", "low"}}
			opts := runner.ExecOptions{LaneRoot: root, BackendsDir: backends, Now: time.Now(), SettledAfter: func(time.Duration) <-chan time.Time { return never }}
			env, err := runner.Exec(context.Background(), s, req, opts)
			if err != nil {
				t.Fatal(err)
			}
			var rec runner.Record
			if err := json.Unmarshal(env.Payload, &rec); err != nil {
				t.Fatal(err)
			}
			if rec.State != runner.StateExited || rec.ExitCode == nil || *rec.ExitCode != 0 || rec.EndCause != runner.EndNormalExit {
				t.Fatalf("record = %s/%v/%s, want an ordinary exit 0", rec.State, rec.ExitCode, rec.EndCause)
			}
			want := []string{"ended at agent_settled", "SIGTERM to pi", "harness exited 143"}
			if strings.Contains(rec.EndingException, "SIGKILL") {
				t.Fatalf("a harness that honours its shutdown was escalated: %q", rec.EndingException)
			}
			for _, w := range want {
				if !strings.Contains(rec.EndingException, w) {
					t.Fatalf("ending_exception %q lacks %q", rec.EndingException, w)
				}
			}
			if _, err := os.Stat(filepath.Join(worktree, "disposed")); err != nil {
				t.Fatal("pi was not given its documented shutdown")
			}
			var status runner.Record
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				got, found, err := runner.StatusRecord(r, "run")
				if err != nil || !found {
					t.Fatalf("status: found=%v err=%v", found, err)
				}
				var body struct {
					Run runner.Record `json:"run"`
				}
				if err := json.Unmarshal(got.Payload, &body); err != nil {
					return err
				}
				status = body.Run
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if status.EndingException != rec.EndingException {
				t.Fatalf("status ending_exception = %q, want %q", status.EndingException, rec.EndingException)
			}
			log, err := os.ReadFile(filepath.Join(lane, "run.log"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(log)), "\n")
			if !strings.Contains(lines[len(lines)-2], "session_shutdown_flushed") || !strings.HasPrefix(lines[len(lines)-1], `{"type": "tillr_harness_ended"`) && !strings.HasPrefix(lines[len(lines)-1], `{"type":"tillr_harness_ended"`) {
				t.Fatalf("stream does not end with the shutdown output and the adapter's ending: %q", lines[len(lines)-2:])
			}
			report, err := os.ReadFile(filepath.Join(lane, "durable", "run", "report.md"))
			if err != nil || !strings.Contains(string(report), "RESULT: done") {
				t.Fatalf("report did not settle: %v %s", err, report)
			}
		})
	}
}

// The watcher's question, against journal shapes. Only a wake-policy task can hold a
// session open, so only one can owe a turn; anything the journal cannot answer is
// "cannot tell" (None), which never ends a session.
func TestPiSettledOwedWorkReadsTheBackgroundTaskJournal(t *testing.T) {
	helper, err := filepath.Abs("backends/pi/stop-at-gap")
	if err != nil {
		t.Fatal(err)
	}
	code := `import os, runpy, sqlite3, sys, tempfile
g = runpy.run_path(sys.argv[1])
owed, journal_path = g["owed_work"], g["journal_path"]
schema = sys.argv[2]
def journal(rows, pid=4242, started=1000, disabled=None):
    path = tempfile.mktemp(dir=sys.argv[3], suffix=".sqlite")
    db = sqlite3.connect(path); db.executescript(schema)
    db.execute("INSERT INTO instances VALUES ('i', ?, ?, ?)", (started, pid, disabled))
    for key, policy, ended, wake, events in rows:
        db.execute("INSERT INTO tasks VALUES (?, 'i', ?, 'x', ?)", (key, policy, ended))
        if wake is not None:
            db.execute("INSERT INTO deliveries VALUES (?, ?, ?)", ("d" + key, key, wake))
            for e in events:
                db.execute("INSERT INTO delivery_events (delivery_key, event) VALUES (?, ?)", ("d" + key, e))
    db.commit(); db.close(); return path
cases = [
    ("leaked fallback hold", journal([("a", "wake", 5, 1, ["fallback-injected", "observed"])]), False),
    ("no tasks at all", journal([]), False),
    ("running wake task", journal([("a", "wake", None, None, [])]), True),
    ("running notify task holds nothing", journal([("a", "notify", None, None, [])]), False),
    ("ended wake, completion not yet decided", journal([("a", "wake", 5, None, [])]), True),
    ("ended wake, completion enqueued not observed", journal([("a", "wake", 5, 1, ["enqueued"])]), True),
    ("stopped wake task", journal([("a", "wake", 5, 0, [])]), False),
    ("enqueue failed", journal([("a", "wake", 5, 1, ["enqueue-failed"])]), False),
    ("another process", journal([], pid=1), None),
    ("an older process with this pid", journal([], started=1), None),
    ("disabled journal", journal([], disabled=7), None),
    ("no journal file", os.path.join(sys.argv[3], "absent.sqlite"), None),
]
for name, path, want in cases:
    got = owed(path, 4242, 500)
    assert got is want, (name, got, want)
assert journal_path({"PI_BACKGROUND_TASK_JOURNAL": "off"}) is None
assert journal_path({"XDG_STATE_HOME": "/s"}) == "/s/pi-background-tasks/journal.sqlite"
print("ok")
`
	cmd := exec.Command("python3", "-I", "-c", code, helper, fakeJournalSchema, scratch(t, "pi-owed"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// Escalation, with a stand-in child and no clock: a pi that does not exit after its
// documented shutdown gets the second SIGTERM pi turns into an immediate exit, and
// only then SIGKILL. A pi that exits on the first is signalled once.
func TestPiSettledShutdownEscalates(t *testing.T) {
	helper, err := filepath.Abs("backends/pi/stop-at-gap")
	if err != nil {
		t.Fatal(err)
	}
	code := `import runpy, signal, subprocess, sys
end = runpy.run_path(sys.argv[1])["end_settled"]
class Child:
    pid = 77
    def __init__(self, survives): self.survives, self.sent = survives, []
    def send_signal(self, sig): self.sent.append(sig)
    def wait(self, timeout=None):
        assert timeout == 5, timeout
        if len(self.sent) <= self.survives: raise subprocess.TimeoutExpired("pi", timeout)
for survives, want in [(0, ["SIGTERM"]), (1, ["SIGTERM", "SIGTERM"]), (2, ["SIGTERM", "SIGTERM", "SIGKILL"])]:
    child = Child(survives)
    if survives == 2:
        child.wait = lambda timeout=None, c=child: (_ for _ in ()).throw(subprocess.TimeoutExpired("pi", 5)) if timeout is not None else None
    got = end(child, 5)
    assert [signal.Signals(s).name for s in child.sent] == want, (survives, child.sent)
    assert got == [f"{name} to pi 77" for name in want], got
print("ok")
`
	if out, err := exec.Command("python3", "-I", "-c", code, helper).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
