package drain

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The item id a red sha names is `fixer-main-<red_sha12>`: twelve hex characters, lower
// case, and no more. It is the deduplication key, so two spellings of one sha that
// disagreed would file two fixers that fight over one branch.
func TestTrunkFixerIDNamesTwelveHexCharacters(t *testing.T) {
	sha := "9083ABC6F62ED2C584BF31419BCC59FBC0C2FF0E"
	want := "fixer-main-9083abc6f62e"
	if got := TrunkFixerID(sha); got != want {
		t.Fatalf("TrunkFixerID(%q) = %q, want %q", sha, got, want)
	}
	if !IsTrunkFixer(want) {
		t.Fatalf("%q is not recognised as a fixer", want)
	}
	if got := TrunkFixerSHA(want); got != "9083abc6f62e" {
		t.Fatalf("TrunkFixerSHA(%q) = %q, want the sha prefix", want, got)
	}
	if IsTrunkFixer("item_041") {
		t.Fatal("an ordinary item is read as a fixer")
	}
	// A short or empty sha still produces a readable id rather than panicking on the slice.
	if got := TrunkFixerID("abc"); got != "fixer-main-abc" {
		t.Fatalf("a short sha produced %q", got)
	}
}

// The gate's own code is never in a fixer's fence. This is the recursion exemption made
// real: a fixer that could edit the gate could make the red disappear by weakening the
// judge, which is the one repair that must never be available.
func TestGateCodePathsAreNeverInAFixerFence(t *testing.T) {
	for _, path := range []string{
		"tools/fork-check.py",
		"tests/fork-check.test.py",
		"tools/tillr-gate/run.go",
		"tools/tillr-loop/tillr-loop",
		"tools/preflight-declarations.json",
	} {
		if !GateCode(path) {
			t.Fatalf("%q is not recognised as the gate's own code", path)
		}
	}
	if GateCode("tools/tillr-drain/drain/loop.go") {
		t.Fatal("the drain's own code is read as the gate's")
	}
	fence := TrunkFixerFence(
		[]string{"tools/fork-check.py", "tools/tillr-drain/drain/loop.go", "src/app.go"},
		[]string{"tests/fork-check.test.py", "src/app.go", "src/lib.go"},
	)
	want := []string{"src/app.go", "src/lib.go", "tools/tillr-drain/drain/loop.go"}
	if !reflect.DeepEqual(fence, want) {
		t.Fatalf("the fence is %v, want %v (union, deduplicated, gate code removed, sorted)", fence, want)
	}
	if len(TrunkFixerFence([]string{"tools/fork-check.py"}, []string{"tests/fork-check.test.py"})) != 0 {
		t.Fatal("a fence naming only gate code survived the subtraction")
	}
}

func TestFixerFenceDropsAbsoluteAndTraversingPaths(t *testing.T) {
	fence, dropped := trunkFixerPaths(
		[]string{"src/app.go", "/durable/output", "../outside", "src/../../escape", "src/app.go"},
		[]string{"src/", "/durable/output", "./src/", ".", ".."},
	)
	if want := []string{"src/", "src/app.go"}; !reflect.DeepEqual(fence, want) {
		t.Fatalf("fence = %v, want %v", fence, want)
	}
	want := []string{".", "..", "../outside", "./src/", "/durable/output", "src/../../escape"}
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped = %v, want %v", dropped, want)
	}
}

// The watched paths of the failing checks are taken in check-id order, so the same failure
// produces the same fence twice.
func TestCheckWatchesIsStableAndDeduplicated(t *testing.T) {
	watches := map[string][]string{
		"b": {"src/b.go", "src/shared.go"},
		"a": {"src/a.go", "src/shared.go"},
	}
	got := CheckWatches(watches, []string{"b", "a"})
	want := []string{"src/a.go", "src/shared.go", "src/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckWatches = %v, want %v", got, want)
	}
}

// An open fixer is the first fixer item the store holds in a non-terminal state; a landed
// fixer releases the queue.
func TestTrunkFixerOpenReadsOnlyNonTerminalFixers(t *testing.T) {
	if got := trunkFixerOpen([]itemRow{{ItemID: "item_041", State: StateInQueue}}); got != "" {
		t.Fatalf("an ordinary item was read as an open fixer: %q", got)
	}
	if got := trunkFixerOpen([]itemRow{{ItemID: "fixer-main-abc", State: StateLanded}}); got != "" {
		t.Fatalf("a landed fixer still holds the queue: %q", got)
	}
	if got := trunkFixerOpen([]itemRow{
		{ItemID: "item_041", State: StateInQueue},
		{ItemID: "fixer-main-abc", State: StateInQueue},
	}); got != "fixer-main-abc" {
		t.Fatalf("an open fixer was not found: %q", got)
	}
}

// The gate's own verdict names the failing checks, and the plan names what they watch. The
// fixer's fence is built from both, so this read is the hinge between the gate and the
// repair.
func TestGateFailureContextReadsTheVerdictAndPlan(t *testing.T) {
	root := t.TempDir()
	runID := "run-1"
	dir := filepath.Join(root, ".tillr", "loop", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the record directory: %v", err)
	}
	verdict := `{"record_version":1,"kind":"gate.verdict","id":"v1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":{"result":"fail","checks":[{"check_id":"unit","result":"fail"},{"check_id":"lint","result":"pass"}]}}`
	plan := `{"record_version":1,"kind":"gate.plan","id":"p1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":{"checks":[{"check_id":"unit","reads":["src/"],"writes":["out/"]}]}}`
	if err := os.WriteFile(filepath.Join(dir, GateVerdictRecord), []byte(verdict), 0o644); err != nil {
		t.Fatalf("writing the verdict: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, GatePlanRecord), []byte(plan), 0o644); err != nil {
		t.Fatalf("writing the plan: %v", err)
	}
	steps := &ProcessSteps{Root: root}
	checks, _, watches := steps.gateFailureContext(Candidate{RunID: runID})
	if !reflect.DeepEqual(checks, []string{"unit"}) {
		t.Fatalf("the failing checks are %v, want [unit]", checks)
	}
	if !reflect.DeepEqual(watches["unit"], []string{"src/", "out/"}) {
		t.Fatalf("the unit check watches %v, want [src/ out/]", watches["unit"])
	}
}

// A fixer runs the full, untiered gate over the range since the last green, never the
// landing tier the ordinary gate uses: the landing tier's deferred kinds are exactly the
// sibling breakage a fixer must prove green. With no watermark yet the range is the whole
// tree, because a range from an empty base would plan nothing and pass.
func TestFixerGateRunsSinceTheLastGreenOrTheWholeTree(t *testing.T) {
	steps := &ProcessSteps{FixerGateProgram: "python3 tools/fork-check.py {green} --lock-timeout 30"}
	got := steps.fixerGate(Candidate{ItemID: "fixer-main-deadbeef", FixerBaseSHA: "0123456789abcdef"})
	if !strings.Contains(got, "--since 0123456789abcdef") {
		t.Fatalf("the fixer gate is %q, want --since <last green>", got)
	}
	got = steps.fixerGate(Candidate{ItemID: "fixer-main-deadbeef"})
	if !strings.Contains(got, "--all") {
		t.Fatalf("a first fixer's gate is %q, want --all", got)
	}
}
