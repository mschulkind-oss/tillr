package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planCheck is one planned step as a case writes it. A case names only the fields it is
// about; the rest are the zero value, which is what a plan that carries nothing extra
// looks like.
type planCheck struct {
	Scope     string            `json:"scope,omitempty"`
	BudgetMS  int               `json:"budget_ms,omitempty"`
	Remember  *bool             `json:"remember,omitempty"`
	CheckID   string            `json:"check_id"`
	Command   []string          `json:"command"`
	TimeoutMS int               `json:"timeout_ms"`
	Reads     []string          `json:"reads"`
	Writes    []string          `json:"writes"`
	DependsOn []string          `json:"depends_on"`
	Needs     []string          `json:"needs"`
	Drop      []string          `json:"drop"`
	Env       map[string]string `json:"env"`
	Tier      string            `json:"tier"`
	Exclusive bool              `json:"exclusive"`
}

// planIn is design §5.7's gate.plan payload as a case writes it.
type planIn struct {
	PlanID         string      `json:"plan_id"`
	BaseSHA        string      `json:"base_sha"`
	TreeSHA        string      `json:"tree_sha"`
	RegistryDigest string      `json:"registry_digest"`
	Checks         []planCheck `json:"checks"`
	InProcess      []struct {
		CheckID string `json:"check_id"`
	} `json:"in_process"`
}

// verdictOut is the record the run face emits, decoded. The fields live under `payload`:
// design §5.1 says the envelope carries `payload`, and §5.7's list describes a payload.
type verdictOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		VerdictID      string `json:"verdict_id"`
		PlanID         string `json:"plan_id"`
		RegistryDigest string `json:"registry_digest"`
		BaseSHA        string `json:"base_sha"`
		TreeSHA        string `json:"tree_sha"`
		Result         string `json:"result"`
		Checks         []struct {
			CheckID    string `json:"check_id"`
			Result     string `json:"result"`
			DurationMS int64  `json:"duration_ms"`
			LogRef     string `json:"log_ref"`
		} `json:"checks"`
	} `json:"payload"`
}

// planRecord writes a gate.plan record for one case and returns its path.
func planRecord(t *testing.T, plan planIn) string {
	t.Helper()
	return write(t, "plan.json", envelope(t, "gate.plan", plan.PlanID, plan))
}

// runPlan drives the run face and returns its exit code and the verdict it wrote.
func runPlan(t *testing.T, planPath, worktree string, extra ...string) (int, []byte) {
	t.Helper()
	out := filepath.Join(scratch(t, "verdict"), "verdict.json")
	args := append([]string{"run", "--input", planPath, "--worktree", worktree, "--output", out}, extra...)
	code, stdout := run(t, args...)
	raw, err := os.ReadFile(out)
	if err != nil {
		// A face that refused the plan outright writes no verdict; the caller's case
		// says whether that is the expected outcome.
		return code, stdout
	}
	return code, raw
}

func decodeVerdict(t *testing.T, raw []byte) verdictOut {
	t.Helper()
	var got verdictOut
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the face did not emit a verdict record: %v\n%s", err, raw)
	}
	return got
}

// A check that exits nonzero is a completed negative verdict: exit 1, result `fail`, and
// the verdict binds the tree it examined. A verdict that did not name its tree could not
// be trusted by land, and one that answered 0 for a red would let a failing candidate
// through.
//
// Negative control: the same plan with `exit 0` is the pass case below, so a face that
// answered 1 for every plan fails there and a face that answered 0 for every plan fails
// here.
func TestRunRecordsAFailedCheckAsAVerdict(t *testing.T) {
	plan := planRecord(t, planIn{
		PlanID: "p-1", BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197", RegistryDigest: "d-1",
		Checks: []planCheck{{CheckID: "always-fails", Command: []string{"sh", "-c", "exit 1"}, TimeoutMS: 5000}},
	})
	code, raw := runPlan(t, plan, tree(t))
	if code != 1 {
		t.Fatalf("a failed check exited %d, want 1: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Kind != "gate.verdict" || got.Payload.Result != "fail" {
		t.Fatalf("the verdict is %q/%q", got.Kind, got.Payload.Result)
	}
	if got.Payload.TreeSHA != "c0b4c69197" {
		t.Fatalf("the verdict binds tree %q", got.Payload.TreeSHA)
	}
	if len(got.Payload.Checks) != 1 || got.Payload.Checks[0].Result != "fail" {
		t.Fatalf("the per-check results are %+v", got.Payload.Checks)
	}
	if got.Payload.Checks[0].LogRef == "" {
		t.Fatal("a failed check carries no log reference: a red verdict that cannot be read is a re-run")
	}
}

// A plan whose checks pass is exit 0 and result `pass`, and each check's log holds what
// it wrote. The log is the evidence a verdict points at, so a verdict that named a log
// nothing wrote would be a reference to nothing.
//
// The check also proves the plan's `drop` and `env` reach the child: a runner that
// ignored them would run a different environment than the gate it replaces.
func TestRunPassesAPlanWhoseChecksPass(t *testing.T) {
	plan := planRecord(t, planIn{
		PlanID: "p-2", BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197", RegistryDigest: "d-1",
		Checks: []planCheck{{
			CheckID:   "always-passes",
			Command:   []string{"sh", "-c", `echo "[${DROPPED-unset}][${TILLR_SQLITE_TRACE-unset}][${SET-unset}]"`},
			TimeoutMS: 5000,
			Drop:      []string{"DROPPED"},
			Env:       map[string]string{"SET": "yes"},
		}},
	})
	t.Setenv("DROPPED", "leaked")
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	code, raw := runPlan(t, plan, tree(t))
	if code != 0 {
		t.Fatalf("a passing plan exited %d, want 0: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Payload.Result != "pass" {
		t.Fatalf("the verdict result is %q", got.Payload.Result)
	}
	if len(got.Payload.Checks) != 1 || got.Payload.Checks[0].Result != "pass" {
		t.Fatalf("the per-check results are %+v", got.Payload.Checks)
	}
	logged, err := os.ReadFile(got.Payload.Checks[0].LogRef)
	if err != nil {
		t.Fatalf("reading the check's log failed: %v", err)
	}
	if !strings.Contains(string(logged), "[unset][unset][yes]") {
		t.Fatalf("the check ran with the wrong environment: %q", logged)
	}
}

// A check that cannot start is exit 4, never a failed verdict: the candidate was not
// judged, and reading it as a red would isolate a candidate for a missing tool.
//
// Negative control: the same plan with a command that exists is the pass case, so a face
// that answered 4 for every plan fails there.
func TestRunExitsFourForACheckThatCannotStart(t *testing.T) {
	plan := planRecord(t, planIn{
		PlanID: "p-3", BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197", RegistryDigest: "d-1",
		Checks: []planCheck{{CheckID: "absent", Command: []string{"/nonexistent/checker"}, TimeoutMS: 5000}},
	})
	code, raw := runPlan(t, plan, tree(t))
	if code != 4 {
		t.Fatalf("a check that cannot start exited %d, want 4: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Payload.Result != "error" {
		t.Fatalf("the verdict result is %q, want error", got.Payload.Result)
	}
	if len(got.Payload.Checks) != 1 || got.Payload.Checks[0].Result != "error" {
		t.Fatalf("the per-check results are %+v", got.Payload.Checks)
	}
}

// A check whose `needs` path is not there cannot be run either, and it must not be
// started to find out: the plan says the checkout is required, so its absence is exit 4
// before the command runs.
//
// Negative control: the same check with the path present is the pass case, so a face that
// answered 4 for every `needs` fails there.
func TestRunExitsFourForACheckWhoseNeedsAreMissing(t *testing.T) {
	plan := planRecord(t, planIn{
		PlanID: "p-4", BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197", RegistryDigest: "d-1",
		Checks: []planCheck{{
			CheckID:   "needs-a-checkout",
			Command:   []string{"sh", "-c", "exit 0"},
			TimeoutMS: 5000,
			Needs:     []string{filepath.Join(scratch(t, "absent"), "checkout")},
		}},
	})
	code, raw := runPlan(t, plan, tree(t))
	if code != 4 {
		t.Fatalf("a check whose needs are missing exited %d, want 4: %s", code, raw)
	}
	if got := decodeVerdict(t, raw); got.Payload.Result != "error" {
		t.Fatalf("the verdict result is %q, want error", got.Payload.Result)
	}
}

// A plan that owes an in-process check is `error`/4, never `pass`: the gate's own-code
// checks have no command this face can run, so a verdict from it would omit them and
// assert a transition that did not happen (the next-steps ruling's A-INPROCESS).
//
// Negative control: the same plan with `in_process` empty is the pass case, so a face
// that answered 4 for every plan fails there.
func TestRunRefusesAPlanThatOwesAnInProcessCheck(t *testing.T) {
	plan := planIn{
		PlanID: "p-5", BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197", RegistryDigest: "d-1",
		Checks: []planCheck{{CheckID: "always-passes", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000}},
	}
	plan.InProcess = append(plan.InProcess, struct {
		CheckID string `json:"check_id"`
	}{CheckID: "file-size"})
	code, raw := runPlan(t, planRecord(t, plan), tree(t))
	if code != 4 {
		t.Fatalf("a plan owing an in-process check exited %d, want 4: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Payload.Result != "error" {
		t.Fatalf("the verdict result is %q, want error", got.Payload.Result)
	}
	if len(got.Payload.Checks) != 1 || got.Payload.Checks[0].Result != "error" {
		t.Fatalf("a check that was not run is recorded as %+v", got.Payload.Checks)
	}
}

// The verdict binds the plan it ran and the tree it examined: `plan_id`,
// `registry_digest`, `base_sha` and `tree_sha` are copied from the plan, because those
// four are what `land pass` refuses a mismatch on.
//
// Negative control: two plans differing only in `tree_sha` must produce two different
// verdicts, so a face that dropped or hardcoded the tree fails here.
func TestRunBindsThePlansRegistryDigestAndTree(t *testing.T) {
	check := []planCheck{{CheckID: "always-passes", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000}}
	first := planRecord(t, planIn{
		PlanID: "p-6", BaseSHA: "base-a", TreeSHA: "tree-a", RegistryDigest: "digest-a", Checks: check,
	})
	second := planRecord(t, planIn{
		PlanID: "p-6", BaseSHA: "base-a", TreeSHA: "tree-b", RegistryDigest: "digest-a", Checks: check,
	})
	firstCode, firstRaw := runPlan(t, first, tree(t))
	secondCode, secondRaw := runPlan(t, second, tree(t))
	if firstCode != 0 || secondCode != 0 {
		t.Fatalf("the plans exited %d then %d", firstCode, secondCode)
	}
	got := decodeVerdict(t, firstRaw)
	if got.Payload.PlanID != "p-6" || got.Payload.RegistryDigest != "digest-a" ||
		got.Payload.BaseSHA != "base-a" || got.Payload.TreeSHA != "tree-a" {
		t.Fatalf("the verdict binds %+v", got.Payload)
	}
	if got.Payload.VerdictID == "" {
		t.Fatal("the verdict carries no verdict_id")
	}
	if other := decodeVerdict(t, secondRaw); other.Payload.VerdictID == got.Payload.VerdictID {
		t.Fatal("two plans for two trees produced one verdict_id")
	}
}

// The plan face and the run face compose: a plan the plan face produced from the fixture
// registry owes the registry's in-process check, so running it is `error`/4. This is the
// end-to-end statement of A-INPROCESS -- the real registry's own-code checks are not
// something this face can execute, and the verdict says so rather than passing.
func TestRunRefusesAPlanThePlanFaceProduced(t *testing.T) {
	in := write(t, "change.json", envelope(t, "gate.change", "c-1", change{
		BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197",
		ChangedPaths: []string{"tools/first/main.go"},
	}))
	root := tree(t, "tools/first/main.go")
	planPath := filepath.Join(scratch(t, "plan"), "plan.json")
	if code, out := run(t, "plan", "--registry", fixtureRegistry(t), "--root", root, "--input", in, "--output", planPath); code != 0 {
		t.Fatalf("plan exited %d: %s", code, out)
	}
	code, raw := runPlan(t, planPath, root)
	if code != 4 {
		t.Fatalf("running a plan that owes an in-process check exited %d, want 4: %s", code, raw)
	}
	if got := decodeVerdict(t, raw); got.Payload.Result != "error" {
		t.Fatalf("the verdict result is %q, want error", got.Payload.Result)
	}
}

func TestRunRunsIndependentChecksConcurrentlyWithSameOrderedResults(t *testing.T) {
	dir := scratch(t, "parallel-checks")
	root := tree(t)
	remember := false
	// Two independent checks signal each other via files: check-a creates a.started and
	// waits for b.started; check-b creates b.started and waits for a.started. Serial
	// execution would deadlock; concurrent execution completes both immediately.
	// check-c depends on check-a and fails; check-d shares a write path with check-c
	// and must not overlap it.
	aStarted := filepath.Join(dir, "a.started")
	bStarted := filepath.Join(dir, "b.started")
	waitScript := func(self, other string, exit int) string {
		return "touch '" + self + "'; i=0; while [ ! -f '" + other + "' ]; do i=$((i+1)); if [ $i -gt 200 ]; then exit 2; fi; sleep 0.01; done; exit " + string(rune('0'+exit))
	}
	plan := planRecord(t, planIn{
		PlanID: "p-parallel", BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197", RegistryDigest: "d-1",
		Checks: []planCheck{
			{CheckID: "check-a", Scope: "change", Tier: "fast", BudgetMS: 5000, Remember: &remember, Command: []string{"sh", "-c", waitScript(aStarted, bStarted, 0)}},
			{CheckID: "check-b", Scope: "change", Tier: "fast", BudgetMS: 5000, Remember: &remember, Command: []string{"sh", "-c", waitScript(bStarted, aStarted, 0)}},
			{CheckID: "check-c", Scope: "change", Tier: "fast", BudgetMS: 5000, Remember: &remember, DependsOn: []string{"check-a"}, Writes: []string{"shared/out"}, Command: []string{"sh", "-c", "exit 1"}},
			{CheckID: "check-d", Scope: "change", Tier: "fast", BudgetMS: 5000, Remember: &remember, Writes: []string{"shared/"}, Command: []string{"sh", "-c", "exit 0"}},
		},
	})
	code, raw := runPlan(t, plan, root)
	if code != 1 {
		t.Fatalf("parallel plan with one failure exited %d, want 1: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Payload.Result != "fail" {
		t.Fatalf("verdict result = %q, want fail", got.Payload.Result)
	}
	want := []struct {
		id     string
		result string
	}{
		{"check-a", "pass"},
		{"check-b", "pass"},
		{"check-c", "fail"},
		{"check-d", "pass"},
	}
	if len(got.Payload.Checks) != len(want) {
		t.Fatalf("got %d checks, want %d: %+v", len(got.Payload.Checks), len(want), got.Payload.Checks)
	}
	for i, w := range want {
		c := got.Payload.Checks[i]
		if c.CheckID != w.id || c.Result != w.result {
			t.Fatalf("check[%d] = %+v, want %s/%s", i, c, w.id, w.result)
		}
	}
}
