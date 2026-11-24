package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
func load(t *testing.T) *Registry {
	t.Helper()
	reg, err := LoadRegistry(filepath.Join("..", "gate", "checks"))
	if err != nil {
		t.Fatalf("loading the registry failed: %v", err)
	}
	return reg
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

// A change under one Go tool directory must select that directory's suite and not an
// unrelated one's. The negative control is the inverse change, so a planner that
// selected everything would fail one of the two.
func TestPlanSelectsTheGoToolSteps(t *testing.T) {
	reg := load(t)
	root := filepath.Join("..", "..")
	lander := reg.Plan([]string{"tools/tillr-land/main.go"}, root)
	if !hasCheck(lander, "go-test-tillr-land") {
		t.Fatalf("a tillr-land-only change did not select the lander's suite: %v", ids(lander))
	}
	if hasCheck(lander, "go-test-tillr-cli") {
		t.Fatalf("a tillr-land-only change selected the CLI's suite: %v", ids(lander))
	}
	cli := reg.Plan([]string{"tools/tillr-cli/main.go"}, root)
	if !hasCheck(cli, "go-test-tillr-cli") {
		t.Fatalf("a tillr-cli-only change did not select the CLI's suite: %v", ids(cli))
	}
	if hasCheck(cli, "go-test-tillr-land") {
		t.Fatalf("a tillr-cli-only change selected the lander's suite: %v", ids(cli))
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
