package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// generate runs the gate's own registry export and returns the path it wrote.
//
// The registry is not committed: its check set is a property of the machine that
// plans it (a host without `node` plans no storybook checks, one without the Pi SDK
// plans no board suite), so a committed copy would be stale on any other machine. What
// is a property of the tree is the shape, and that is what these tests hold.
// The export is cached for the whole test binary: it spawns a fresh `python3` that
// imports the entire Python gate, which costs ~2.4s, and a dozen tests need the same
// bytes. Measured 2026-11-11: 12 uncached exports were ~29s of a 27s suite (the rest
// is the one-time `go build` in TestMain and milliseconds of real work).
var (
	registryOnce sync.Once
	registryOut  []byte
	registryErr  error
)

func generate(t *testing.T) string {
	t.Helper()
	registryOnce.Do(func() {
		cmd := exec.Command("python3", filepath.Join("..", "..", "tools", "fork-check.py"), "--registry-json")
		registryOut, registryErr = cmd.Output()
	})
	if registryErr != nil {
		t.Fatalf("exporting the registry failed: %v", registryErr)
	}
	path := filepath.Join(t.TempDir(), "gate-registry.json")
	if err := os.WriteFile(path, registryOut, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T) *Registry {
	t.Helper()
	reg, err := LoadRegistry(generate(t))
	if err != nil {
		t.Fatalf("loading the registry failed: %v", err)
	}
	return reg
}

// The registry is generated from the gate that runs today, so a check the gate
// plans and the registry does not declare is a check the new planner would silently
// drop. The digest is what a verdict binds, so it must be a function of the file.
//
// This covers the command checks. The gate's own in-process checks have no command
// to spell, and `TestTheRegistryNamesTheChecksItCannotCommand` holds those.
func TestTheRegistryDeclaresEveryCommandCheckTheGatePlans(t *testing.T) {
	reg := load(t)
	if len(reg.Declarations) < 40 {
		t.Fatalf("the registry declares %d checks; the gate plans more than that", len(reg.Declarations))
	}
	seen := map[string]bool{}
	for _, d := range reg.Declarations {
		if d.ID == "" || d.Kind == "" || d.Label == "" || len(d.Command) == 0 || d.TimeoutMS == 0 {
			t.Errorf("declaration %+v is missing an id, a kind, a label, a command, or a timeout", d)
		}
		if seen[d.ID] {
			t.Errorf("the registry declares %q twice", d.ID)
		}
		seen[d.ID] = true
	}
	if reg.Digest == "" {
		t.Fatal("the registry has no digest")
	}
}

// A declaration's id is what a verdict names a check by, so it must not move with a
// document count or with this machine's absolute root: two verdicts over the same check
// would then be incomparable. The negative control is the label form the export used to
// emit as the id, which carries both.
func TestADeclarationIDCarriesNoCountAndNoAbsolutePath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	counted := regexp.MustCompile(`\d+ changed `)
	for _, d := range load(t).Declarations {
		if strings.Contains(d.ID, root) {
			t.Errorf("declaration id %q carries this machine's absolute root", d.ID)
		}
		if counted.MatchString(d.ID) {
			t.Errorf("declaration id %q carries a document count", d.ID)
		}
	}
	for _, old := range []string{
		"578 changed Markdown file(s)",
		"fork pi: build the checkout the liveness harness judges at " + root + "/.forks/candidate/pi",
	} {
		if !strings.Contains(old, root) && !counted.MatchString(old) {
			t.Fatalf("the negative control %q is not the shape this test refuses", old)
		}
	}
}

// The plan is the whole product of the no-store face, so a change under one Go tool
// directory must select that directory's steps and nothing else's. This is the selection
// claim the design's G2 rests on, asserted at the computed instance. The negative control
// is the inverse change: a change under `tools/pi-profiler/` must select the profiler's
// steps and not mq's, so a planner that selected everything would fail one of the two.
//
// The retired board tool and the retired merge queue are not one of the pair: they left
// `GO_TOOL_DIRS` (the board replaced by tillr on 2026-11-10, the merge queue by
// `tools/tillr-land` on 2026-11-13), so neither plans a build step at all.
func TestPlanSelectsTheGoToolSteps(t *testing.T) {
	reg := load(t)
	root := filepath.Join("..", "..")
	lander := reg.Plan([]string{"tools/tillr-land/main.go"}, root)
	if !hasCheck(lander, "go-tool/tools-tillr-land-s-whole-suite-passes") {
		t.Fatalf("a tillr-land-only change did not select the lander's build step: %v", ids(lander))
	}
	if hasCheck(lander, "go-tool/tools-pi-profiler-s-whole-suite-passes") {
		t.Fatalf("a tillr-land-only change selected the pi-profiler build step: %v", ids(lander))
	}
	profiler := reg.Plan([]string{"tools/pi-profiler/main.go"}, root)
	if !hasCheck(profiler, "go-tool/tools-pi-profiler-s-whole-suite-passes") {
		t.Fatalf("a pi-profiler-only change did not select the pi-profiler build step: %v", ids(profiler))
	}
	if hasCheck(profiler, "go-tool/tools-tillr-land-s-whole-suite-passes") {
		t.Fatalf("a pi-profiler-only change selected the mq build step: %v", ids(profiler))
	}
}

// Changed Markdown is now selected and rendered by the change program, not by
// a generated command over the exporter's remembered range.
func TestDocumentsDeclarationDelegatesTheCallerRange(t *testing.T) {
	reg, err := LoadRegistry(filepath.Join("..", "gate", "checks"))
	if err != nil {
		t.Fatal(err)
	}
	checks := reg.Plan([]string{"AGENTS.md"}, filepath.Join("..", ".."))
	for _, check := range checks {
		if check.ID != "documents" {
			continue
		}
		want := []string{"python3", "tools/gate/checks/documents.py"}
		if strings.Join(check.Command, "\x00") != strings.Join(want, "\x00") || check.Scope != "change" {
			t.Fatalf("documents is not a change program: %+v", check)
		}
		return
	}
	t.Fatalf("no documents declaration in %v", ids(checks))
}

// A plan instance carries fields a command list cannot: the environment a suite needs, the
// environment it must not inherit, the checkout it needs, its tier, and whether it runs
// alone. A registry that dropped them would let a planner run a check that cannot answer.
// The negative control is a declaration that declares none of them, so the test cannot pass
// by finding the field on some other check.
func TestPlanCarriesTheFieldsACommandListCannot(t *testing.T) {
	reg := load(t)
	root := filepath.Join("..", "..")
	checks := reg.Plan([]string{
		"tools/tillr-land/main.go",
		"tools/session-liveness/run",
		"tools/roster-storybook/render.mts",
	}, root)
	byID := map[string]Check{}
	for _, check := range checks {
		byID[check.ID] = check
	}
	// The environment-carrying check is the liveness harness, not the board suite: the
	// board suite is planned only where `pi_sdk_env` resolves, so a machine without the
	// Pi SDK would fail this test for a reason no change can clear. The harness is
	// planned wherever `node` is, and carries `PI_FORK` the same way.
	liveness, ok := byID["session-liveness/a-session-returns-to-idle-within-its-bound-and-a-later-send-now-starts-work"]
	if !ok {
		t.Fatalf("the liveness harness was not selected: %v", ids(checks))
	}
	if len(liveness.Env) == 0 {
		t.Fatalf("the liveness harness's declaration carries no environment")
	}
	build, ok := byID["go-tool/tools-tillr-land-s-whole-suite-passes"]
	if !ok {
		t.Fatalf("the mq build step was not selected: %v", ids(checks))
	}
	if len(build.Drop) == 0 || build.Drop[0] != "LAND_BATCH_LIMIT" {
		t.Fatalf("the board build step's drop list is %v", build.Drop)
	}
	if len(build.Env) != 0 {
		t.Fatalf("the negative control failed: the board build step declares an environment %v", build.Env)
	}
	var fork *Check
	for index := range checks {
		if checks[index].Kind == "fork-construct" {
			fork = &checks[index]
		}
	}
	if fork == nil || len(fork.Needs) == 0 {
		t.Fatalf("the fork-construct step carries no needs: %+v", fork)
	}
	storybook, ok := byID["storybook/roster"]
	if !ok {
		t.Fatalf("the roster storybook was not selected: %v", ids(checks))
	}
	if storybook.Tier != "rolling" || len(storybook.Writes) == 0 {
		t.Fatalf("the roster storybook carries tier %q and writes %v", storybook.Tier, storybook.Writes)
	}
}

// The legacy registry retains the in-process TypeScript size warning. The two
// Markdown convention guards now have executable tree declarations, so they must
// not also appear here.
func TestTheRegistryNamesTheChecksItCannotCommand(t *testing.T) {
	reg := load(t)
	want := map[string]bool{"file-size": true}
	got := map[string]bool{}
	for _, c := range reg.InProcess {
		if c.ID == "" || c.Kind == "" || c.Label == "" {
			t.Errorf("in-process check %+v is missing an id, a kind, or a label", c)
		}
		got[c.Kind] = true
	}
	for kind := range want {
		if !got[kind] {
			t.Errorf("the registry does not name the in-process check %q", kind)
		}
	}
	for kind := range got {
		if !want[kind] {
			t.Errorf("the registry names an in-process check %q that this test does not know about; "+
				"if the gate gained one, add it here and say what it checks", kind)
		}
	}
	// The negative control: a check that is selected by nothing and forced by nothing is a
	// check that never runs, so the two document guards must declare the document pattern.
	for _, c := range reg.InProcess {
		if c.Kind == "file-size" {
			continue
		}
		if len(c.Selects) == 0 || len(c.Watches) == 0 {
			t.Errorf("in-process check %q declares no selection or no watches", c.ID)
		}
	}
}

// A check that watches nothing is selected by nothing, which is a check that never
// runs. The gate's own integrity set is the exception and declares its selection.
func TestEveryDeclarationIsSelectedBySomething(t *testing.T) {
	for _, d := range load(t).Declarations {
		if len(d.Selects) == 0 && d.Forced == "" {
			t.Errorf("declaration %q selects nothing and is forced by nothing", d.ID)
		}
	}
}

// The digest is what a verdict binds, so a declarations edit that did not regenerate it
// must refuse rather than load: a verdict bound to a digest the file no longer hashes to
// names a registry that never produced the plan.
func TestLoadRegistryRefusesADeclarationsEditThatDidNotRegenerateTheDigest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatalf("reading the fixture registry failed: %v", err)
	}
	var file struct {
		Digest       string          `json:"digest"`
		Declarations json.RawMessage `json:"declarations"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decoding the fixture registry failed: %v", err)
	}
	var declarations []map[string]any
	if err := json.Unmarshal(file.Declarations, &declarations); err != nil {
		t.Fatalf("decoding the fixture registry's declarations failed: %v", err)
	}
	declarations[0]["timeout_ms"] = 1
	edited, err := json.Marshal(declarations)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gate-registry.json")
	body := `{"digest":` + string(mustJSON(t, file.Digest)) + `,"declarations":` + string(edited) + `}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(path); err == nil {
		t.Fatal("a registry whose declarations no longer hash to its digest loaded")
	}
}

// An id names one check, so two declarations sharing one would make a verdict's per-check
// entry ambiguous. The negative control is the fixture with its second id replaced by the
// first, and a digest regenerated over it, so the refusal is the duplicate and not the
// digest.
func TestLoadRegistryRefusesTwoDeclarationsWithOneID(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatalf("reading the fixture registry failed: %v", err)
	}
	var file struct {
		Declarations json.RawMessage `json:"declarations"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decoding the fixture registry failed: %v", err)
	}
	var declarations []map[string]any
	if err := json.Unmarshal(file.Declarations, &declarations); err != nil {
		t.Fatalf("decoding the fixture registry's declarations failed: %v", err)
	}
	declarations[1]["id"] = declarations[0]["id"]
	edited, err := json.Marshal(declarations)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gate-registry.json")
	body := `{"digest":` + string(mustJSON(t, digestOf(t, edited))) + `,"declarations":` + string(edited) + `}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(path); err == nil {
		t.Fatal("a registry declaring one id twice loaded")
	}
}

// The fixture is the loader's own test input, so it must load: a fixture that does
// not would make the refusal tests above pass for the wrong reason.
func TestTheFixtureRegistryLoads(t *testing.T) {
	reg, err := LoadRegistry(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatalf("loading the fixture registry failed: %v", err)
	}
	if len(reg.Declarations) != 3 {
		t.Fatalf("the fixture holds %d declarations, want 3", len(reg.Declarations))
	}
	// `AGENTS.md` is a real tracked document, so the fixture's `must_exist` pattern selects
	// it; the go-tool pattern is a directory prefix and needs no such file.
	checks := reg.Plan([]string{"tools/first/main.go", "AGENTS.md"}, filepath.Join("..", ".."))
	if len(checks) != 3 {
		t.Fatalf("the fixture planned %d checks, want 3: %v", len(checks), ids(checks))
	}
	if strings.Join(checks[1].Command, " ") != "uvx vantage-check AGENTS.md" {
		t.Fatalf("the fixture's documents command is %v", checks[1].Command)
	}
}

func hasCheck(checks []Check, id string) bool {
	for _, check := range checks {
		if check.ID == id {
			return true
		}
	}
	return false
}

func ids(checks []Check) []string {
	out := make([]string, 0, len(checks))
	for _, check := range checks {
		out = append(out, check.ID)
	}
	return out
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// digestOf is the digest the generator writes: sha256 over the declarations' canonical
// bytes. The duplicate-id test regenerates it so the refusal it asserts is the duplicate.
func digestOf(t *testing.T, declarations []byte) string {
	t.Helper()
	sum := sha256.Sum256(declarations)
	return hex.EncodeToString(sum[:])
}

// The digest is what a verdict binds, so it must cover every declaration a plan can owe --
// including the checks the gate runs as its own code. Those have no command, so they are
// named in `in_process` rather than in `declarations`, and a digest over the command
// declarations alone would let an edit to one of their selections change what a verdict
// owes without moving the digest.
//
// The negative control is the digest the export wrote before it covered `in_process`: the
// edited file below loads, and the test is red.
func TestTheRegistryDigestBindsTheInProcessChecks(t *testing.T) {
	raw, err := os.ReadFile(generate(t))
	if err != nil {
		t.Fatalf("reading the exported registry failed: %v", err)
	}
	var file struct {
		Digest       json.RawMessage `json:"digest"`
		Declarations json.RawMessage `json:"declarations"`
		InProcess    json.RawMessage `json:"in_process"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decoding the exported registry failed: %v", err)
	}
	var inProcess []map[string]any
	if err := json.Unmarshal(file.InProcess, &inProcess); err != nil {
		t.Fatalf("decoding the in-process checks failed: %v", err)
	}
	if len(inProcess) == 0 {
		t.Fatal("the export names no in-process check, so this test would pass for the wrong reason")
	}
	// A selection the gate does not have: the check now owes a change it never ran for.
	inProcess[0]["selects"] = []any{map[string]any{"path": "tools/"}}
	edited, err := json.Marshal(inProcess)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gate-registry.json")
	body := `{"digest":` + string(file.Digest) + `,"declarations":` + string(file.Declarations) +
		`,"in_process":` + string(edited) + `}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(path); err == nil {
		t.Fatal("a registry whose in-process selection no longer hashes to its digest loaded")
	}
}

// Two exports of one tree must produce one digest, or the digest binds nothing: a verdict
// recorded against the first would name a registry the second export no longer produces.
// The one value that moves per process is a per-run scratch base in an `env` entry, which
// the export writes as `run-<pid>` the way it already writes a declared write path.
//
// The negative control is the export that normalised `writes` and not `env`: the two
// digests differ and the test is red.
func TestTheRegistryDigestIsReproducibleAcrossExports(t *testing.T) {
	first, err := LoadRegistry(generate(t))
	if err != nil {
		t.Fatalf("loading the first export failed: %v", err)
	}
	second, err := LoadRegistry(generate(t))
	if err != nil {
		t.Fatalf("loading the second export failed: %v", err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("two exports of one tree carry digests %q and %q", first.Digest, second.Digest)
	}
}

// A fork step watches its whole pack, because a pack's manifest can move every series in
// it, but the gate selects it by the series that changed. A `selects` pattern that is a
// bare pack root is the watch list exported as the selection: it plans the step for any
// file in the pack, which the gate does not run.
//
// The negative control is `selects = watch_patterns(check.watches)`, which is what the
// export wrote before it carried the gate's own selection: every fork step then declares
// `yolo-packs/<pack>/` and the test is red.
func TestAForkStepIsSelectedByItsSeriesAndNotItsPack(t *testing.T) {
	reg := load(t)
	forkSteps := 0
	for _, d := range reg.Declarations {
		if d.Kind != "fork-quality" {
			continue
		}
		forkSteps++
		for _, p := range d.Selects {
			trimmed := strings.TrimSuffix(p.Path, "/")
			if strings.HasPrefix(trimmed, "yolo-packs/") && strings.Count(trimmed, "/") == 1 {
				t.Fatalf("declaration %q is selected by the pack root %q", d.ID, p.Path)
			}
		}
	}
	if forkSteps == 0 {
		t.Fatal("the export declares no fork-quality step, so this test would pass for the wrong reason")
	}
}

// The in-process checks are the one part of the plan the registry cannot spell as a
// command, so their tier is a field a plan could silently drop. It is carried verbatim,
// exactly as a command declaration's tier is: the plan is a declaration, and the shadow is
// where a tier is read.
//
// Negative control: the checks name three different tiers, so a plan that hardcoded one
// (or skipped the field) fails on at least two of them.
func TestAnInProcessCheckCarriesItsDeclaredTierIntoThePlan(t *testing.T) {
	reg := &Registry{
		Digest: "d-1",
		InProcess: []InProcessCheck{
			{ID: "file-size", Kind: "file-size", Label: "no file over the ceiling", Tier: "landing"},
			{ID: "heavy", Kind: "heavy", Label: "a rolling in-process check", Tier: "rolling"},
			{ID: "unnamed", Kind: "unnamed", Label: "a check that names no tier"},
		},
	}
	owed := owedInProcess(reg, nil, ".")
	want := map[string]string{"file-size": "landing", "heavy": "rolling", "unnamed": ""}
	if len(owed) != len(want) {
		t.Fatalf("the plan owes %d in-process check(s), want %d", len(owed), len(want))
	}
	for _, check := range owed {
		if check.Tier != want[check.CheckID] {
			t.Fatalf("the in-process check %q carries tier %q, want %q",
				check.CheckID, check.Tier, want[check.CheckID])
		}
	}
}

// A check that names no tier is the landing tier, never an unclassified third state: the
// design's membership rule makes the unnamed default the safe side, because the worst a
// missing entry can do is make a landing slower, never quieter.
//
// Negative control: `rolling` must stay rolling, so a reader that answered landing for
// every input fails here.
func TestAnUnnamedTierIsTheLandingTier(t *testing.T) {
	if got := tierOf(""); got != TierLanding {
		t.Fatalf("an unnamed tier reads as %q, want %q", got, TierLanding)
	}
	if got := tierOf("nonsense"); got != TierLanding {
		t.Fatalf("an unknown tier reads as %q, want %q", got, TierLanding)
	}
	if got := tierOf("rolling"); got != TierRolling {
		t.Fatalf("the rolling tier reads as %q", got)
	}
	if got := tierOf("landing"); got != TierLanding {
		t.Fatalf("the landing tier reads as %q", got)
	}
}
