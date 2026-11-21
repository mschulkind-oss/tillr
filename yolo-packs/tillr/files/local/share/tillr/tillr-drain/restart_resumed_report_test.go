package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A resumed worker is still alive when its loop is replaced again. The successor
// cannot wait on the old loop's child, and must not report its own lease as a
// shortage of lanes or launch a second worker in that lane.
func TestWhyAfterRestartOverLiveResumedRun(t *testing.T) {
	dir := scratch(t, "resumed-report")
	hold := filepath.Join(dir, "hold")
	writeFile(t, hold, "hold\n")
	t.Setenv("STUB_HOLD", hold)
	// A trace of the loop's planning transaction lets the test wait for a
	// completed successor pass without sleeping or changing the fixture state.
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	t.Cleanup(func() { os.Remove(hold) })
	harness := `[ "${1:-}" != --features ] || exit 2
` + harnessCount + `
item=$(basename "$(dirname "$PWD")")
seg_file="$STUB_SEGMENT.$item"
n=0; [ ! -f "$seg_file" ] || n=$(cat "$seg_file")
n=$((n+1)); echo "$n" > "$seg_file"
echo "$$ $item $n" >> "$STUB_PIDS"
if [ "$n" -eq 1 ]; then
    sleep 600 & child=$!
    trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null; exit 143' TERM
    wait "$child"
else
    cat
    while [ -f "$STUB_HOLD" ]; do sleep 0.05; done
fi
`
	s := startRoll(t, 1, run.PauseWhereItStands, harness)
	// Only the loop needs tracing; the test's own store handles need not log.
	t.Setenv("TILLR_SQLITE_TRACE", "")
	t.Cleanup(func() {
		// Stop only this fixture's loop and its latest recorded harness. This
		// also cleans up if the test fails before it can pause the first segment.
		s.cmd.Process.Kill()
		pids := lines(t, s.pids)
		if len(pids) > 0 {
			fields := strings.Fields(pids[len(pids)-1])
			if len(fields) > 0 {
				if pid, err := strconv.Atoi(fields[0]); err == nil {
					syscall.Kill(pid, syscall.SIGTERM)
				}
			}
		}
	})
	s.inFlight(t, 1)
	const id = "run-item_041-a1"
	ctx := context.Background()
	st, err := store.Open(ctx, s.f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	err = run.RequestPause(ctx, st, id, 2*time.Second, time.Now())
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	s.waitFor(t, "the resumed segment", func() bool {
		return len(lines(t, s.pids)) >= 2
	})
	before := eventsOf(t, s.f.store)
	var relevant []string
	for _, e := range before {
		if e.ObjectID != nil && *e.ObjectID == id && (e.Kind == run.FinishKind || e.Kind == run.ResumeKind) {
			relevant = append(relevant, e.Kind)
			if e.Kind == run.FinishKind {
				var rec run.Record
				if err := json.Unmarshal(e.Payload, &rec); err != nil || rec.State != run.StatePaused || rec.EndCause != run.EndRestart {
					t.Fatalf("first finish is not paused/restart: %s (%v)", e.Payload, err)
				}
			}
		}
	}
	if strings.Join(relevant, ",") != run.FinishKind+","+run.ResumeKind {
		t.Fatalf("expected finish then resume of %s, got %v", id, relevant)
	}
	workerPID, err := strconv.Atoi(strings.Fields(lines(t, s.pids)[1])[0])
	if err != nil || !alive(workerPID) {
		t.Fatalf("resumed worker is not alive: %v", lines(t, s.pids))
	}
	s.assertLiveWorkerWhy(t, id, true, false)
	s.assertAcrossReplacements(t, id, true)
	s.replaceLoopProcess(t)
	s.assertLiveWorkerWhy(t, id, true, true)
	assertWorkerView(t, s, id, "the worker continues executing; the replacement loop does not take over that execution")
	if !alive(workerPID) || len(lines(t, s.pids)) != 2 {
		t.Errorf("successor ended or duplicated the resumed worker: %v", lines(t, s.pids))
	}
	for _, e := range eventsOf(t, s.f.store)[len(before):] {
		if e.ObjectID != nil && *e.ObjectID == id && (e.Kind == run.FinishKind || e.Kind == run.ResumeKind) {
			t.Errorf("successor changed the live run's last event: %s", e.Kind)
		}
		if e.Kind == run.AllocateKind {
			t.Errorf("successor allocated another run: %s", e.Payload)
		}
	}
	s.assertWhyWithIndependentWait(t, id, true)
}

// A new loop process opens its session before it reaps the dead predecessor.
// The live run must still be reported as executing its existing segment.
func (s *rollScenario) replaceLoopProcess(t *testing.T) {
	t.Helper()
	old := s.cmd
	if err := old.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = old.Wait()
	s.out = filepath.Join(filepath.Dir(s.out), "replacement.out")
	out, err := os.Create(s.out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	next := exec.Command(old.Path, old.Args[1:]...)
	next.Stdout, next.Stderr = out, out
	if err := next.Start(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	t.Cleanup(func() { _ = next.Process.Kill(); _ = next.Wait() })
	if next.Process.Pid == old.Process.Pid {
		t.Fatal("replacement must be a different process")
	}
	s.cmd = next
	s.waitFor(t, "the new process to complete two planning transactions", func() bool {
		return completedPlanningTransactions(s.output(t)) >= 2
	})
}

func (s *rollScenario) assertAcrossReplacements(t *testing.T, id string, resumed bool) {
	t.Helper()
	for _, build := range []string{"\n# first report replacement\n", "\n# second report replacement\n"} {
		s.replaceBinaryWith(t, build)
		if answer := s.restart(t); !strings.Contains(answer, "restarted") {
			t.Fatalf("restart: %s", answer)
		}
		s.waitForSuccessorPass(t)
		s.assertLiveWorkerWhy(t, id, resumed, true)
	}
}

func (s *rollScenario) waitForSuccessorPass(t *testing.T) {
	t.Helper()
	s.waitFor(t, "successor to complete a planning pass", func() bool {
		out := s.output(t)
		start := strings.LastIndex(out, "loop: restarted in place onto build")
		if start < 0 {
			return false
		}
		// The second planning transaction can start only after the first pass
		// has had the opportunity to dispatch any duplicate.
		return completedPlanningTransactions(out[start:]) >= 2
	})
}

func completedPlanningTransactions(out string) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "(*Loop).passPlansMode.func1") && strings.Contains(line, "phase=end") {
			count++
		}
	}
	return count
}

func (s *rollScenario) assertWhyWithIndependentWait(t *testing.T, id string, resumed bool, details ...string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, s.f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, "item_041", drain.StateBlocked, time.Now(), item.WaitingOn{Waiter: "owner", Reason: "await approval"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.assertLiveWorkerWhy(t, id, resumed, true, append(details, "next dispatch waits on owner: await approval")...)
}

func (s *rollScenario) assertLiveWorkerWhy(t *testing.T, id string, resumed, survived bool, details ...string) {
	t.Helper()
	policy := filepath.Join(scratch(t, "restart-why"), "policy.json")
	raw, err := json.Marshal(s.f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	args := []string{"--root", s.f.root, "--store", s.f.store, "--policy", policy, "--lanes", "1"}
	code, out := invoke(t, append([]string{"why", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var answer drain.WhyAnswer
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatalf("why JSON: %v: %s", err, out)
	}
	if len(answer.Rows) != 1 {
		t.Fatalf("why rows: %s", out)
	}
	row := answer.Rows[0]
	code, detail := invoke(t, append([]string{"why", "item_041", "--json"}, args...)...)
	var itemRow drain.WhyRow
	if err := json.Unmarshal(detail, &itemRow); code != 0 || err != nil {
		t.Fatalf("why item_041: %d %s (%v)", code, detail, err)
	}
	if itemRow.Position != "running" || itemRow.Reason != drain.ReasonRunLive || itemRow.Why != row.Why || itemRow.Mover != row.Mover {
		t.Errorf("item-specific why disagrees with the running row: %+v", itemRow)
	}
	if answer.Summary.LanesWorkers != 1 || answer.Summary.LanesExamining != 0 {
		t.Errorf("why counts a live worker as an examination: %+v", answer.Summary)
	}
	code, text := invoke(t, append([]string{"why"}, args...)...)
	if code != 0 {
		t.Fatalf("why text: %d %s", code, text)
	}
	for _, want := range []string{"item_041", "running", id, "lane-1"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("public why text misses %q: %s", want, text)
		}
	}
	if strings.Contains(strings.ToLower(string(text)), "no-lane") {
		t.Errorf("public why text says the running item waits for its lane: %s", text)
	}
	if row.ItemID != "item_041" || row.Position != "running" || row.Reason != drain.ReasonRunLive || row.Decision.RunRequest == nil || row.Decision.RunRequest.RunID != id || row.Decision.LaneID != "lane-1" {
		t.Errorf("why must identify the running item, run and lane: %+v", row)
	}
	if strings.Contains(strings.ToLower(string(out)), "no-lane") || strings.Contains(strings.ToLower(row.Why), "waiting for a lane") || strings.Contains(row.Why, "lane kept") || len(row.WaitingOn) != 0 {
		t.Errorf("why says the running item waits for its own lane: reason=%q why=%q mover=%q", row.Reason, row.Why, row.Mover)
	}
	if !strings.HasPrefix(row.Mover, "worker "+id+" on lane-1") {
		t.Errorf("why must name the live worker as the next mover: %q", row.Mover)
	}
	if strings.Contains(row.Why, "(resumed)") != resumed {
		t.Errorf("resumed=%t: %s", resumed, row.Why)
	}
	for _, want := range details {
		if !strings.Contains(row.Why, want) || !strings.Contains(string(text), want) {
			t.Errorf("why misses %q in the explanation or CLI row:\n%s\n%s", want, row.Why, text)
		}
	}
	// Locally witnessed survival across exec is not just a timestamp inference.
	foreign := slices.Contains(details, "worker process is not observable here")
	for _, report := range []string{string(text), row.Why} {
		if strings.Contains(report, "waits for its recorded finish") != survived ||
			strings.Contains(report, "timing does not establish takeover") != (survived && foreign) ||
			strings.Contains(report, "the worker continues executing; the replacement loop does not take over that execution") != (survived && !foreign) {
			t.Errorf("survived=%t: %s", survived, report)
		}
	}
}
