package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func writeGateVerdictFixture(t *testing.T, root, runID, triagePath string) {
	t.Helper()
	runDir := RunDir(root, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"verdict_id": "verdict-" + runID,
		"result":     "error",
		"checks": []map[string]any{{
			"check_id": "gate:python3",
			"result":   "error",
			"log_ref":  triagePath,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          "gate.verdict",
		ID:            "verdict-" + runID,
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
		Payload:       payload,
	}
	raw, err := record.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, GateVerdictRecord), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A gate that exits before judging because its program is missing in the candidate's
// tree (with the cause recorded in the gate's own triage log or stderr) is classified
// as a start failure naming the program and its error, while an interrupted gate or
// one that failed after starting stays an ordinary re-gated no-verdict.
func TestProcessStepsGateClassifiesStartFailure(t *testing.T) {
	f, l := passFixture(t, "item-a")
	c := prepareReadyCandidate(t, f, l, "item-a")
	steps := &ProcessSteps{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: l.cfg.Now}

	missingPath := filepath.Join(c.Worktree, "tools", "gate", "change-gate.py")
	triagePath := filepath.Join(RunDir(f.root, c.RunID), "logs", "00-gate-python3.triage.log")
	if err := os.MkdirAll(filepath.Dir(triagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	triageBody := "tillr-gate: gate program \"python3 " + missingPath + " --base a --head b\" in " + c.Worktree + ": exit 2 after 0.0s; this is a bounded triage of 1 output line(s), the whole output is 00-gate-python3.log\n" +
		"python3: can't open file '" + missingPath + "': [Errno 2] No such file or directory\n"
	if err := os.WriteFile(triagePath, []byte(triageBody), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGateVerdictFixture(t, f.root, c.RunID, triagePath)

	steps.GateProgram = executable(t,
		`echo "tillr-gate run: the gate program exited 2, which is not a verdict: it refused to judge, so the tree was neither passed nor failed: `+triagePath+`" >&2; `+
			`echo "tillr-loop gate: tillr-gate run answered exit 4: no judgment was made" >&2; exit 4`)
	got, err := steps.Gate(context.Background(), c)
	if err != nil || got.Outcome != GateErrored {
		t.Fatalf("missing gate program answered %+v / %v, want GateErrored", got, err)
	}
	if !strings.Contains(got.ErrorDetail, missingPath) || !strings.Contains(got.ErrorDetail, "No such file or directory") {
		t.Fatalf("ErrorDetail = %q, want missing program path %q and its error", got.ErrorDetail, missingPath)
	}

	// A gate killed by a signal was interrupted and must stay re-gated gate-no-verdict.
	interruptedTriage := "tillr-gate: gate program \"python3 " + missingPath + "\" in " + c.Worktree + ": no exit: killed by a signal (signal: terminated) after 1.2s; this is a bounded triage of 0 output line(s), the whole output is 00-gate-python3.log\n"
	if err := os.WriteFile(triagePath, []byte(interruptedTriage), 0o644); err != nil {
		t.Fatal(err)
	}
	steps.GateProgram = executable(t,
		`echo "tillr-gate run: the gate program was killed by a signal rather than answering (signal: terminated); nothing was judged: `+triagePath+`" >&2; exit 4`)
	got, err = steps.Gate(context.Background(), c)
	if err != nil || got.Outcome != GateErrored || got.ErrorDetail != "" {
		t.Fatalf("interrupted gate answered %+v / %v, want re-gated no-verdict (empty ErrorDetail)", got, err)
	}

	// A gate that already started judging (printed progress markers) stays gate-no-verdict.
	startedTriage := "tillr-gate: gate program \"python3 " + missingPath + "\" in " + c.Worktree + ": exit 2 after 3.0s; this is a bounded triage of 2 output line(s), the whole output is 00-gate-python3.log\n" +
		"tillr-gate checks: go-build: ran 100 ms\n" +
		"sh: 0: cannot open transient.tmp: No such file\n"
	if err := os.WriteFile(triagePath, []byte(startedTriage), 0o644); err != nil {
		t.Fatal(err)
	}
	steps.GateProgram = executable(t, `echo "tillr-loop gate: tillr-gate run answered exit 4: no judgment was made" >&2; exit 4`)
	got, err = steps.Gate(context.Background(), c)
	if err != nil || got.Outcome != GateErrored || got.ErrorDetail != "" {
		t.Fatalf("gate that failed after starting answered %+v / %v, want re-gated no-verdict", got, err)
	}
}

func TestGateStartErrorDetailCases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
		triage string
		want   string
	}{
		{
			name:   "tillr-gate could not start",
			stderr: `tillr-gate run: the gate program "./tools/gate/change-gate.py --base a" could not be started: fork/exec ./tools/gate/change-gate.py: no such file or directory; its triage is /tmp/t.log`,
			want:   "./tools/gate/change-gate.py",
		},
		{
			name:   "tillr-loop missing runner",
			stderr: `installed tillr-gate runner /opt/tillr/tillr-gate/change-gate.py is missing or not a regular file`,
			want:   "/opt/tillr/tillr-gate/change-gate.py",
		},
		{
			name:   "tillr-loop relative install root",
			stderr: `TILLR_INSTALL_ROOT must be absolute: relative/tillr`,
			want:   "TILLR_INSTALL_ROOT must be absolute: relative/tillr",
		},
		{
			name:   "sh cannot open script in triage",
			triage: "tillr-gate: gate program \"sh tools/gate/run.sh\" in /w: exit 127 after 0.0s; this is a bounded triage of 1 output line(s), the whole output is /w/log\nsh: 0: cannot open tools/gate/run.sh: No such file\n",
			want:   "tools/gate/run.sh",
		},
		{
			name:   "bash missing script in stderr",
			stderr: "bash: tools/gate/run.sh: No such file or directory\n",
			want:   "tools/gate/run.sh",
		},
		{
			name:   "transient disk error stays no-verdict",
			stderr: "the disk is full\n",
			want:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := gateStartErrorDetail(tc.stderr, tc.triage)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("got %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// startErrorSteps fails to start the gate program for one candidate commit (failingHead),
// and passes for any other commit.
type startErrorSteps struct {
	recordingSteps
	mu          sync.Mutex
	failingHead string
	detail      string
	gated       []string
}

func (s *startErrorSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gated = append(s.gated, c.HeadSHA)
	if c.HeadSHA == s.failingHead {
		return GateOutcome{Outcome: GateErrored, ErrorDetail: s.detail}, nil
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

// When the gate program cannot start against a candidate, the loop holds the item once
// with the program's path and error (visible in `tillr drain why` and recorded as
// `drain.item-fault`), does not gate the same commit again on later passes, and gates
// again only when an operator clears the hold or the candidate's commit changes.
func TestGateStartFailureHoldsOnceUntilClearedOrCommitChanges(t *testing.T) {
	f, l := passFixture(t, "item-a")
	c := prepareReadyCandidate(t, f, l, "item-a")
	missingPath := filepath.Join(c.Worktree, "tools", "gate", "change-gate.py")
	detail := gateStartErrorDetail("", "python3: can't open file '"+missingPath+"': [Errno 2] No such file or directory\n")
	steps := &startErrorSteps{failingHead: c.HeadSHA, detail: detail}
	l.cfg.Steps = steps

	// Pass 1: gates once and holds the item.
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(steps.gated) != 1 {
		t.Fatalf("gated %d times on first run, want 1", len(steps.gated))
	}

	// Pass 2 (same candidate commit, hold untouched): must NOT gate again.
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(steps.gated) != 1 {
		t.Fatalf("same candidate was re-gated (%d times total), want still 1", len(steps.gated))
	}

	// Verify fault record, zero gate-no-verdict waits, item-fault event, and `tillr drain why`.
	var faultSeq int64
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, held := faults["item-a"]
		if !held || fault.Reason != StopGateHarnessError || fault.HeadSHA != c.HeadSHA {
			t.Fatalf("item-a fault = %+v (held %v), want %s bound to head %s", fault, held, StopGateHarnessError, c.HeadSHA)
		}
		if !strings.Contains(fault.Detail, missingPath) || !strings.Contains(fault.Detail, "No such file or directory") {
			t.Errorf("fault detail = %q, want program path %q and error", fault.Detail, missingPath)
		}
		faultSeq = fault.Seq

		var waits int
		if err := r.QueryRow(`SELECT count(*) FROM dispatches WHERE item_id='item-a' AND reason=?`, ReasonGateNoVerdict).Scan(&waits); err != nil {
			return err
		}
		if waits != 0 {
			t.Errorf("recorded %d gate-no-verdict waits, want 0", waits)
		}
		events, err := r.EventsOfKind(0, itemFaultKind)
		if err != nil {
			return err
		}
		if len(events) != 1 {
			t.Errorf("recorded %d %s events, want 1", len(events), itemFaultKind)
		}

		why, err := Why(r, Pass{StorePath: f.storePath, Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		found := false
		for _, row := range why.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			found = true
			if row.Reason != StopGateHarnessError || !strings.Contains(row.Why, missingPath) || !strings.Contains(row.Why, "No such file or directory") {
				t.Errorf("why row = %+v, want %s naming %q and error", row, StopGateHarnessError, missingPath)
			}
		}
		if !found {
			t.Errorf("item-a missing from Why rows")
		}
		return nil
	})

	// Operator clears the hold: the next pass gates the same candidate again (and re-holds once).
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return ClearItemFault(tx, "item-a", faultSeq, l.cfg.Now())
	})
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("run after clear-fault: %v", err)
	}
	if len(steps.gated) != 2 {
		t.Fatalf("after operator clear-fault, gated %d times, want 2", len(steps.gated))
	}

	// Candidate's commit changes (without operator clear-fault): the loop releases the old
	// commit's hold, gates the new commit, and lands it.
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "updated candidate\n")
	runGit(t, c.Worktree, "add", "candidate.txt")
	runGit(t, c.Worktree, "commit", "-qm", "updated candidate")
	newHead := runGit(t, c.Worktree, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", pinRef(c.RunID), newHead)
	updated := c
	updated.HeadSHA = newHead
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateReadyKind, updated.RunID, updated, l.cfg.Now())
	})
	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("run after candidate commit change: %v", err)
	}
	if len(steps.gated) != 3 || steps.gated[2] != newHead {
		t.Fatalf("gated heads = %v, want third gate on new commit %s", steps.gated, newHead)
	}
	if res.Landed != 1 {
		t.Fatalf("after candidate commit change, landed = %d, want 1 (stop=%s)", res.Landed, res.Stop)
	}
}

// A retired or prepared candidate's drain.retirement record carries the worker head while
// the gate runs on the maintenance commit. Its gate-harness-error hold must stay bound to
// that candidate across passes rather than looking like a changed commit.
func TestGateStartFailureHoldsRetiredCandidateOnce(t *testing.T) {
	f, l := passFixture(t, "item-a")
	c := prepareReadyCandidate(t, f, l, "item-a")
	workerHead := c.HeadSHA

	// Simulate prepareCandidate: persist drain.retirement before the maintenance commit
	// (so its recorded HeadSHA is workerHead) and advance the lane to the retired commit.
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "retired\n")
	runGit(t, c.Worktree, "add", "candidate.txt")
	tree := runGit(t, c.Worktree, "write-tree")
	c.WorkerHeadSHA = workerHead
	c.RetirementTreeSHA = tree
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateRetirementKind, c.RunID, c, l.cfg.Now())
	})
	runGit(t, c.Worktree, "commit", "-qm", "chore: retire landed document block")
	retiredHead := runGit(t, c.Worktree, "rev-parse", "HEAD")

	missingPath := filepath.Join(c.Worktree, "tools", "gate", "change-gate.py")
	detail := gateStartErrorDetail("", "python3: can't open file '"+missingPath+"': [Errno 2] No such file or directory\n")
	steps := &startErrorSteps{failingHead: retiredHead, detail: detail}
	l.cfg.Steps = steps

	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(steps.gated) != 1 || steps.gated[0] != retiredHead {
		t.Fatalf("gated = %v, want [%s]", steps.gated, retiredHead)
	}

	// Second pass with the same retired candidate must not clear the hold or re-gate.
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(steps.gated) != 1 {
		t.Fatalf("retired candidate was re-gated (%v), want still 1", steps.gated)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, held := faults["item-a"]
		if !held || fault.Reason != StopGateHarnessError || fault.HeadSHA != retiredHead || fault.WorkerHeadSHA != workerHead {
			t.Fatalf("item-a fault = %+v (held %v), want %s with head=%s worker_head=%s", fault, held, StopGateHarnessError, retiredHead, workerHead)
		}
		return nil
	})
}

// When the gate command exits 4 because it could not start (for example its runner file is
// missing), the loop holds the candidate with that cause, raises the gate-start-failed
// alarm once naming the missing file, does not run the gate again across many passes until
// the cause changes (one gate call per change of cause, with no count or time limit on
// waiting), and clears the alarm when a gate next starts.
func TestGateStartFailureRaisesAlarmOnceAndClearsWhenGateStarts(t *testing.T) {
	f, l := passFixture(t, "item-a")
	c := prepareReadyCandidate(t, f, l, "item-a")
	var report bytes.Buffer
	l.cfg.Report = &report

	missingV1 := "/opt/tillr/v1/tillr-gate/change-gate.py"
	missingV2 := "/opt/tillr/v2/tillr-gate/change-gate.py"
	countFile := filepath.Join(t.TempDir(), "gate-calls")
	modeFile := filepath.Join(t.TempDir(), "gate-mode")
	writeForkFile(t, modeFile, "missing-v1")

	gateBin := executable(t, `printf x >> `+countFile+`
mode=$(cat `+modeFile+`)
case "$mode" in
  missing-v1)
    echo "installed tillr-gate runner `+missingV1+` is missing or not a regular file" >&2
    exit 4
    ;;
  missing-v2)
    echo "installed tillr-gate runner `+missingV2+` is missing or not a regular file" >&2
    exit 4
    ;;
  pass)
    exit 0
    ;;
esac
`)
	procSteps := &ProcessSteps{
		Root:        f.root,
		Store:       f.storePath,
		LaneRoot:    f.laneRoot,
		Now:         l.cfg.Now,
		GateProgram: gateBin,
	}
	l.cfg.Steps = &gateCallbackSteps{
		gate: procSteps.Gate,
	}

	gateCalls := func() int {
		t.Helper()
		raw, err := os.ReadFile(countFile)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return len(raw)
	}

	// Drive 10 passes while the runner file is missing: must call the gate once.
	for pass := 0; pass < 10; pass++ {
		if _, err := l.Run(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
	}
	if got := gateCalls(); got != 1 {
		t.Fatalf("gate called %d times across 10 passes with unchanged missing runner, want 1", got)
	}

	var faultSeq int64
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, held := faults["item-a"]
		if !held || fault.Reason != StopGateHarnessError || !strings.Contains(fault.Detail, missingV1) {
			t.Fatalf("item-a fault = %+v (held %v), want %s naming %q", fault, held, StopGateHarnessError, missingV1)
		}
		faultSeq = fault.Seq

		var alarmEvents int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, alarms.GateStartFailedKind).Scan(&alarmEvents); err != nil {
			return err
		}
		if alarmEvents != 1 {
			t.Errorf("%s event count = %d, want 1", alarms.GateStartFailedKind, alarmEvents)
		}

		why, err := Why(r, Pass{StorePath: f.storePath, Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(why.Alarms) != 1 || why.Alarms[0].Kind != alarms.GateStartFailed || !strings.Contains(why.Alarms[0].Cause, missingV1) {
			t.Fatalf("why alarms = %+v, want one %s naming %q", why.Alarms, alarms.GateStartFailed, missingV1)
		}
		foundRow := false
		for _, row := range why.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			foundRow = true
			if row.Reason != StopGateHarnessError || !strings.Contains(row.Why, missingV1) {
				t.Errorf("why row = %+v, want %s naming %q", row, StopGateHarnessError, missingV1)
			}
		}
		if !foundRow {
			t.Errorf("item-a missing from why rows")
		}
		return nil
	})
	if got := strings.Count(report.String(), "alarm started: ALARM "+alarms.GateStartFailed+" (gate)"); got != 1 {
		t.Fatalf("alarm started count = %d, want 1:\n%s", got, report.String())
	}

	// Change cause #1 (different missing runner path) and clear the fault: 10 more passes
	// must run the gate exactly once more (total 2 calls).
	writeForkFile(t, modeFile, "missing-v2")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return ClearItemFault(tx, "item-a", faultSeq, l.cfg.Now())
	})
	for pass := 0; pass < 10; pass++ {
		if _, err := l.Run(context.Background()); err != nil {
			t.Fatalf("second-cause pass %d: %v", pass+1, err)
		}
	}
	if got := gateCalls(); got != 2 {
		t.Fatalf("gate called %d times after first cause change + 10 passes, want 2", got)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, held := faults["item-a"]
		if !held || !strings.Contains(fault.Detail, missingV2) {
			t.Fatalf("item-a fault after cause change = %+v (held %v), want %q", fault, held, missingV2)
		}
		faultSeq = fault.Seq
		why, err := Why(r, Pass{StorePath: f.storePath, Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(why.Alarms) != 1 || why.Alarms[0].Kind != alarms.GateStartFailed || !strings.Contains(why.Alarms[0].Cause, missingV2) {
			t.Fatalf("why alarms after cause change = %+v, want one %s naming %q", why.Alarms, alarms.GateStartFailed, missingV2)
		}
		return nil
	})

	// Change cause #2 (gate can now start and pass) and clear the fault: gate runs once
	// more (total 3 calls), candidate lands, and the gate-start-failed alarm clears.
	writeForkFile(t, modeFile, "pass")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return ClearItemFault(tx, "item-a", faultSeq, l.cfg.Now())
	})
	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("recovery pass: %v", err)
	}
	if got := gateCalls(); got != 3 {
		t.Fatalf("gate called %d times after recovery, want 3", got)
	}
	if res.Landed != 1 {
		t.Fatalf("landed = %d, want 1 (stop=%s)", res.Landed, res.Stop)
	}
	_ = c
	fixtureRead(t, f, func(r *store.Reader) error {
		active, err := alarms.Read(r, l.cfg.Now(), nil)
		if err != nil {
			return err
		}
		for _, a := range active {
			if a.Kind == alarms.GateStartFailed {
				t.Fatalf("gate-start-failed alarm still active after gate started: %+v", active)
			}
		}
		return nil
	})
	if got := strings.Count(report.String(), "alarm cleared: "+alarms.GateStartFailed+":gate"); got != 1 {
		t.Fatalf("alarm cleared count = %d, want 1:\n%s", got, report.String())
	}
}

// The gate-start-failed alarm clears as soon as any gate next starts (even on another
// candidate whose gate reaches no verdict), while the earlier item's hold remains.
func TestGateStartAlarmClearsWhenAnotherCandidateGateStarts(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ca := prepareReadyCandidate(t, f, l, "item-a")
	cb := prepareReadyCandidate(t, f, l, "item-b")
	missingPath := "/opt/tillr/tillr-gate/change-gate.py"
	detail := gateStartErrorDetail("installed tillr-gate runner "+missingPath+" is missing or not a regular file", "")

	var mu sync.Mutex
	allowB := false
	bStartedCalls := 0
	var alarmsAfterNoVerdict []alarms.Alarm
	steps := &gateCallbackSteps{
		gate: func(ctx context.Context, c Candidate) (GateOutcome, error) {
			mu.Lock()
			okB := allowB
			if okB && c.ItemID == cb.ItemID {
				bStartedCalls++
			}
			call := bStartedCalls
			mu.Unlock()
			if c.ItemID == ca.ItemID {
				return GateOutcome{Outcome: GateErrored, ErrorDetail: detail, StartError: true}, nil
			}
			if !okB {
				return GateOutcome{Outcome: GateErrored, ErrorDetail: detail, StartError: true}, nil
			}
			if call == 1 {
				// First time item-b's gate starts, it runs and returns ordinary gate-no-verdict.
				return GateOutcome{Outcome: GateErrored}, nil
			}
			// On the re-gate pass, verify the gate-start-failed alarm was already cleared by
			// the previous started gate, then pass so l.Run completes.
			fixtureRead(t, f, func(r *store.Reader) error {
				var err error
				alarmsAfterNoVerdict, err = alarms.Read(r, l.cfg.Now(), nil)
				return err
			})
			return GateOutcome{Outcome: GatePassed}, nil
		},
	}
	l.cfg.Steps = steps

	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	var seqB int64
	fixtureRead(t, f, func(r *store.Reader) error {
		active, err := alarms.Read(r, l.cfg.Now(), nil)
		if err != nil {
			return err
		}
		if len(active) != 1 || active[0].Kind != alarms.GateStartFailed {
			t.Fatalf("alarms after first run = %+v, want one %s", active, alarms.GateStartFailed)
		}
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		seqB = faults[cb.ItemID].Seq
		return nil
	})

	// Clear only item-b's fault and let item-b's gate start (returning ordinary no-verdict).
	mu.Lock()
	allowB = true
	mu.Unlock()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return ClearItemFault(tx, cb.ItemID, seqB, l.cfg.Now())
	})
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(alarmsAfterNoVerdict) != 0 {
		t.Fatalf("alarms after item-b gate started (no-verdict) = %+v, want none", alarmsAfterNoVerdict)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		active, err := alarms.Read(r, l.cfg.Now(), nil)
		if err != nil {
			return err
		}
		if len(active) != 0 {
			t.Fatalf("alarms after another gate started = %+v, want none", active)
		}
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if _, held := faults[ca.ItemID]; !held {
			t.Fatalf("item-a fault should remain held after item-b's gate started")
		}
		return nil
	})
}

type gateCallbackSteps struct {
	recordingSteps
	gate func(context.Context, Candidate) (GateOutcome, error)
}

func (s *gateCallbackSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	return s.gate(ctx, c)
}
