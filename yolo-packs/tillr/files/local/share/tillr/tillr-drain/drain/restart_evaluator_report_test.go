package drain_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyAfterRestartOverLiveEvaluator(t *testing.T) {
	t.Run("first segment", func(t *testing.T) { whyAfterRestartOverLiveEvaluator(t, false) })
	t.Run("resumed segment", func(t *testing.T) { whyAfterRestartOverLiveEvaluator(t, true) })
}

func whyAfterRestartOverLiveEvaluator(t *testing.T, resume bool) {
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	harness := `[ "${1:-}" != --features ] || exit 2
` + harnessCount + `
if [ "${TILLR_NODE_ID:-}" = evaluate ]; then
    echo "$$ $TILLR_RUN_ID" >> "$STUB_PIDS"
    sleep 600 & child=$!
    trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null; exit 143' TERM
    wait "$child"
else
    cat >/dev/null
` + committingAdapterBody + `    printf '%s\n' '{"type":"agent_end","messages":[{"role":"assistant","content":[{"type":"text","text":"RESULT: done"}]}]}'
fi
`
	s := startConfiguredRoll(t, 1, run.PauseWhereItStands, func(f loopFixture) {
		const workflow = `{"name":"evaluate","version":1,"stages":[{"id":"work","kind":"agent","seat":"build","access":"write"},{"id":"evaluate","kind":"agent","seat":"review","access":"read_only","judge_classes":["verification-only"]}],"edges":[{"from_node":"work","to_node":"evaluate"}],"exit":{"requires_nodes":["work","evaluate"],"evaluator_slot":"evaluate"}}`
		dir := filepath.Join(f.root, "config/tillr/workflows")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "evaluate@v1.json"), workflow)
		git(t, f.root, "add", "config")
		git(t, f.root, "commit", "-m", "evaluator workflow")
		f.policy.Seats = map[string]string{"build": "pi", "review": "pi"}
		ctx := context.Background()
		st, err := store.Open(ctx, f.store, true)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET workflow='evaluate@v1' WHERE item_id='item_041'`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}, harness)
	t.Setenv("TILLR_SQLITE_TRACE", "")
	s.waitFor(t, "the first-segment evaluator", func() bool { return len(lines(t, s.pids)) == 1 })
	fields := strings.Fields(lines(t, s.pids)[0])
	pid, err := strconv.Atoi(fields[0])
	if err != nil || !alive(pid) {
		t.Fatalf("evaluator not alive: %v", fields)
	}
	id := fields[1]
	segments := 1
	if resume {
		// First replace the loop to detach its in-memory evaluator waiter.
		// Pause that inherited segment and explicitly continue it before
		// letting the replacement observe the paused finish.
		s.replaceBinary(t)
		if answer := s.restart(t); !strings.Contains(answer, "restarted") {
			t.Fatalf("first restart: %s", answer)
		}
		s.waitForSuccessorPass(t)
		ctx := context.Background()
		st, err := store.Open(ctx, s.f.store, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.cmd.Process.Signal(syscall.SIGCONT) })
		// Take the writer while freezing so the loop cannot be stopped mid-write,
		// which would leave the store locked when RequestPause runs below.
		if err := st.WriteTx(ctx, func(*store.Tx) error {
			if err := s.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
				return err
			}
			s.waitFor(t, "loop to stop before pausing the evaluator", func() bool {
				return strings.HasPrefix(procStatus(t, s.cmd.Process.Pid, "State"), "T")
			})
			return nil
		}); err != nil {
			st.Close()
			t.Fatal(err)
		}
		err = run.RequestPause(ctx, st, id, 2*time.Second, time.Now())
		st.Close()
		if err != nil {
			t.Fatal(err)
		}
		s.waitFor(t, "the paused evaluator finish", func() bool {
			for _, e := range eventsOf(t, s.f.store) {
				if e.Kind == run.FinishKind && e.ObjectID != nil && *e.ObjectID == id {
					return strings.Contains(string(e.Payload), `"state":"paused"`)
				}
			}
			return false
		})
		resumeCmd := exec.Command(s.exe, "resume", "--root", s.f.root, "--store", s.f.store, "--lane-root", s.f.laneRoot, "--run", id, "--run-bin", sibling(t, "run"))
		var output bytes.Buffer
		resumeCmd.Stdout, resumeCmd.Stderr = &output, &output
		if err := resumeCmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		resumeDone := make(chan struct{})
		go func() {
			done <- resumeCmd.Wait()
			close(resumeDone)
		}()
		t.Cleanup(func() {
			_ = resumeCmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-resumeDone:
			case <-time.After(hangGuard):
				_ = resumeCmd.Process.Kill()
				<-resumeDone
			}
		})
		segments++
		s.waitFor(t, "the resumed evaluator", func() bool {
			select {
			case err := <-done:
				t.Fatalf("resume quit: %v: %s", err, output.String())
			default:
			}
			return len(lines(t, s.pids)) == segments
		})
		if err := s.cmd.Process.Signal(syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
		fields = strings.Fields(lines(t, s.pids)[1])
		pid, err = strconv.Atoi(fields[0])
		if err != nil || !alive(pid) || fields[1] != id {
			t.Fatalf("same evaluator did not resume: %v", fields)
		}
	}
	before := eventsOf(t, s.f.store)
	s.assertLiveWorkerWhy(t, id, resume, false)
	s.assertAcrossReplacements(t, id, resume)
	assertWhyAcrossProcessDomains(t, s, id, resume)
	if !alive(pid) || len(lines(t, s.pids)) != segments {
		t.Errorf("successor ended or duplicated the evaluator: %v", lines(t, s.pids))
	}
	for _, e := range eventsOf(t, s.f.store)[len(before):] {
		if e.Kind == run.AllocateKind || (e.ObjectID != nil && *e.ObjectID == id && (e.Kind == run.FinishKind || e.Kind == run.ResumeKind)) {
			t.Errorf("successor changed the live evaluator: %s %s", e.Kind, e.Payload)
		}
	}
}

func assertWhyAcrossProcessDomains(t *testing.T, s *rollScenario, id string, resumed bool) {
	t.Helper()
	// Keep the real replacement and evaluator alive. The snapshot has a
	// different store identity (no local run face), and labels the dispatch
	// owner as outside the reporter's process domain.
	foreign := *s
	foreign.f.store = filepath.Join(t.TempDir(), "foreign.sqlite3")
	snapshotForeignWorker(t, s.f.store, foreign.f.store, id)
	const unobservable = "worker process is not observable here"
	foreign.assertLiveWorkerWhy(t, id, resumed, true, unobservable)
	assertWorkerView(t, &foreign, id, unobservable)
	foreign.assertWhyWithIndependentWait(t, id, resumed, unobservable)
	assertWorkerView(t, &foreign, id, unobservable, "next dispatch waits on owner: await approval")
	s.assertWhyWithIndependentWait(t, id, resumed)
	assertWorkerView(t, s, id, "the worker continues executing; the replacement loop does not take over that execution")
}

func assertWorkerView(t *testing.T, s *rollScenario, id string, details ...string) {
	t.Helper()
	raw, err := exec.Command(sibling(t, "view"), "waiting", "--store", s.f.store, "--root", s.f.root).CombinedOutput()
	out := string(raw)
	if err != nil || strings.Contains(out, "no-lane") {
		t.Errorf("view waiting: %v\n%s", err, out)
	}
	for _, want := range append(details, "moving (running)", "worker "+id+" on lane-1", "waits for its recorded finish") {
		if !strings.Contains(out, want) {
			t.Errorf("view waiting misses %q:\n%s", want, out)
		}
	}
}

func snapshotForeignWorker(t *testing.T, source, dest, runID string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+source+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO ?`, dest); err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	if _, err := copyDB.Exec(`DROP TRIGGER events_no_update`); err != nil {
		t.Fatal(err)
	}
	res, err := copyDB.Exec(`UPDATE events SET payload=json_set(payload, '$.loop_owner.domain', 'different-process-domain')
		WHERE seq=(SELECT MAX(seq) FROM events WHERE kind='drain.started' AND object_type='run' AND object_id=?)`, runID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("foreign dispatch fixture: %d rows, %v", n, err)
	}
}
