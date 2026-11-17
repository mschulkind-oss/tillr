package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var gateBin string

// Scratch lives under $YOLO_DURABLE_DIR, never /tmp: a suite that writes to /tmp loses
// its evidence when the box is cleaned, and the owner refuses it.
func scratchRoot() string {
	if root := os.Getenv("YOLO_DURABLE_DIR"); root != "" {
		return root
	}
	return filepath.Join("..", "..", ".yolo", "durable")
}

func scratch(t *testing.T, prefix string) string {
	t.Helper()
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, prefix+"-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns before TestMain exits, so cleanup also runs after setup or test failure.
func runTests(m *testing.M) int {
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 1
	}
	dir, err := os.MkdirTemp(root, "tillr-gate-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	gateBin = filepath.Join(dir, "tillr-gate")
	build := exec.Command("go", "build", "-o", gateBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}
	return m.Run()
}

// run drives the binary with the state home scrubbed, so a face that derives a store
// path instead of taking --store cannot reach the owner's board.
func run(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(gateBin, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out
}

// change is design §5.7's gate.change payload. A case varies changed_paths and leaves
// the rest, so a selection difference is attributable to the paths the case names.
type change struct {
	BaseSHA      string   `json:"base_sha"`
	TreeSHA      string   `json:"tree_sha"`
	ChangedPaths []string `json:"changed_paths"`
}

// envelope wraps a payload in design §5.1's envelope. The clock is the caller's, so
// two runs of the same case produce the same bytes.
func envelope(t *testing.T, kind, id string, payload any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"record_version": 1,
		"kind":           kind,
		"id":             id,
		"producer":       map[string]string{"system": "tillr", "utility": "gate", "version": "0.1.0"},
		"created_at":     "2026-11-10T00:00:00Z",
		"payload":        payload,
	})
	if err != nil {
		t.Fatalf("building the input record failed: %v", err)
	}
	return string(raw)
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "input"), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", name, err)
	}
	return path
}

// planOut is the record the face emits, decoded. The fields live under `payload`:
// design §5.1 says the envelope carries `payload`, and §5.7's list describes a
// payload, not the envelope. The plan's WS4-2 block unmarshalled them at the top
// level; that is the wave-2 defect this brief carries the fix for.
type planOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		PlanID         string `json:"plan_id"`
		BaseSHA        string `json:"base_sha"`
		TreeSHA        string `json:"tree_sha"`
		RegistryDigest string `json:"registry_digest"`
		Checks         []struct {
			CheckID   string            `json:"check_id"`
			Command   []string          `json:"command"`
			TimeoutMS int               `json:"timeout_ms"`
			Reads     []string          `json:"reads"`
			Writes    []string          `json:"writes"`
			DependsOn []string          `json:"depends_on"`
			Kind      string            `json:"kind"`
			Label     string            `json:"label"`
			Env       map[string]string `json:"env"`
			Drop      []string          `json:"drop"`
			Needs     []string          `json:"needs"`
			Tier      string            `json:"tier"`
			Exclusive bool              `json:"exclusive"`
			Forced    string            `json:"forced"`
		} `json:"checks"`
	} `json:"payload"`
}

func decode(t *testing.T, out []byte) planOut {
	t.Helper()
	var got planOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
	return got
}

// plan runs the face against one change record, the fixture registry, and a root the
// case built. The root is explicit because the documents declaration's pattern carries
// `must_exist`: a plan computed against a root where the changed paths do not exist
// selects nothing, and a test that let that happen would pass for the wrong reason.
func plan(t *testing.T, registry, root, changePath string) (int, []byte) {
	t.Helper()
	return run(t, "plan", "--registry", registry, "--root", root, "--input", changePath)
}

// tree creates a scratch worktree holding the named paths, so a `must_exist` pattern
// has something to find.
func tree(t *testing.T, paths ...string) string {
	t.Helper()
	root := scratch(t, "root")
	for _, path := range paths {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("creating %s failed: %v", path, err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("writing %s failed: %v", path, err)
		}
	}
	return root
}

// fixtureRegistry is GATE-1's three-check fixture: a go-tool declaration selected by
// `tools/first/`, the documents declaration selected by a Markdown regex, and the
// forced integrity declaration. A plan over it is small enough to assert whole.
func fixtureRegistry(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatalf("resolving the fixture registry failed: %v", err)
	}
	return path
}

// The plan is the whole product of the no-store face, so a change under one module
// must select that module's step and nothing else's. This is the selection claim the
// design's G2 rests on, asserted at the record.
func TestPlanSelectsTheChangedModulesStepAndNotAnothers(t *testing.T) {
	reg := fixtureRegistry(t)
	in := write(t, "change.json", envelope(t, "gate.change", "c-1", change{
		BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197",
		ChangedPaths: []string{"tools/first/main.go"},
	}))
	code, out := plan(t, reg, tree(t, "tools/first/main.go"), in)
	if code != 0 {
		t.Fatalf("plan exited %d: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "gate.plan" {
		t.Fatalf("plan emitted kind %q", got.Kind)
	}
	if got.Payload.RegistryDigest == "" {
		t.Fatal("the plan carries no registry digest")
	}
	var sawFirst, sawDocuments bool
	for _, c := range got.Payload.Checks {
		switch c.CheckID {
		case "go-tool/tools-first-s-whole-suite-passes":
			sawFirst = true
		case "documents":
			sawDocuments = true
		}
	}
	if !sawFirst {
		t.Fatalf("a change under tools/first/ did not select its build step: %+v", got.Payload.Checks)
	}
	if sawDocuments {
		t.Fatal("a Go-only change selected the documents check")
	}
}

// The documents check's argv is computed for this change, not replayed from the
// export: two Markdown paths select exactly those two, and a path added after the
// export is selected too. A planner that replayed the export's instance would run the
// checker over every tracked document whatever the change.
func TestPlanComputesTheDocumentsCommandForItsOwnChange(t *testing.T) {
	reg := fixtureRegistry(t)
	in := write(t, "change.json", envelope(t, "gate.change", "c-1", change{
		BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197",
		ChangedPaths: []string{"docs/a.md", "docs/b.md", "tools/first/main.go"},
	}))
	code, out := plan(t, reg, tree(t, "docs/a.md", "docs/b.md", "tools/first/main.go"), in)
	if code != 0 {
		t.Fatalf("plan exited %d: %s", code, out)
	}
	for _, c := range decode(t, out).Payload.Checks {
		if c.CheckID != "documents" {
			continue
		}
		want := []string{"uvx", "vantage-check", "docs/a.md", "docs/b.md"}
		if len(c.Command) != len(want) {
			t.Fatalf("the documents command is %v, want %v", c.Command, want)
		}
		for i := range want {
			if c.Command[i] != want[i] {
				t.Fatalf("the documents command is %v, want %v", c.Command, want)
			}
		}
		return
	}
	t.Fatal("a change touching two Markdown files did not select the documents check")
}

// A forced declaration is planned for every change, including one that selects
// nothing: the integrity set is not a function of the diff.
func TestPlanCarriesTheForcedDeclarationForAnEmptyChange(t *testing.T) {
	reg := fixtureRegistry(t)
	in := write(t, "change.json", envelope(t, "gate.change", "c-1", change{
		BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197", ChangedPaths: []string{},
	}))
	code, out := plan(t, reg, tree(t), in)
	if code != 0 {
		t.Fatalf("plan exited %d: %s", code, out)
	}
	got := decode(t, out)
	if len(got.Payload.Checks) != 1 {
		t.Fatalf("an empty change planned %d checks, want the one forced declaration", len(got.Payload.Checks))
	}
	if got.Payload.Checks[0].Forced != "always" {
		t.Fatalf("the planned check carries forced %q", got.Payload.Checks[0].Forced)
	}
}

// The plan carries the fields a command list cannot: env, drop, needs, tier and
// exclusivity are what let a runner reproduce the gate it replaces, and a plan that
// drops them attests less than the gate while binding a digest.
func TestPlanCarriesTheFieldsACommandListCannot(t *testing.T) {
	reg := fixtureRegistry(t)
	in := write(t, "change.json", envelope(t, "gate.change", "c-1", change{
		BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197",
		ChangedPaths: []string{"tools/first/main.go"},
	}))
	code, out := plan(t, reg, tree(t, "tools/first/main.go"), in)
	if code != 0 {
		t.Fatalf("plan exited %d: %s", code, out)
	}
	for _, c := range decode(t, out).Payload.Checks {
		if c.CheckID != "go-tool/tools-first-s-whole-suite-passes" {
			continue
		}
		if c.Kind != "go-tool" || c.Tier != "rolling" || c.TimeoutMS != 60000 {
			t.Fatalf("the planned check carries kind %q, tier %q, timeout %d", c.Kind, c.Tier, c.TimeoutMS)
		}
		if len(c.Drop) != 1 || c.Drop[0] != "LAND_BATCH_LIMIT" {
			t.Fatalf("the planned check carries drop %v", c.Drop)
		}
		if c.Label == "" {
			t.Fatal("the planned check carries no label")
		}
		return
	}
	t.Fatal("the change did not select the go-tool declaration")
}

// A malformed change record is a usage error, not a plan with no checks: exit 2, no
// record, nothing written. A caller that cannot tell the two apart plans nothing and
// reads it as a clean gate.
func TestPlanRefusesAMalformedChange(t *testing.T) {
	reg := fixtureRegistry(t)
	in := write(t, "change.json", `{"kind":"gate.change"}`)
	code, out := plan(t, reg, tree(t), in)
	if code != 2 {
		t.Fatalf("plan exited %d, want 2: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("a usage error emitted a record: %s", out)
	}
}

// A registry whose digest does not match its declarations is an incompatible input,
// not a usage error: exit 3. The digest is what a verdict binds, so a registry edited
// without regenerating it would bind a verdict to a registry that never produced the
// plan.
func TestPlanRefusesARegistryWhoseDigestDoesNotMatch(t *testing.T) {
	raw, err := os.ReadFile(fixtureRegistry(t))
	if err != nil {
		t.Fatalf("reading the fixture registry failed: %v", err)
	}
	var file map[string]any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decoding the fixture registry failed: %v", err)
	}
	file["digest"] = "0000000000000000000000000000000000000000000000000000000000000000"
	edited, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("re-encoding the fixture registry failed: %v", err)
	}
	reg := write(t, "registry.json", string(edited))
	in := write(t, "change.json", envelope(t, "gate.change", "c-1", change{
		BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197",
		ChangedPaths: []string{"tools/first/main.go"},
	}))
	code, out := plan(t, reg, tree(t, "tools/first/main.go"), in)
	if code != 3 {
		t.Fatalf("plan exited %d, want 3: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("an incompatible registry emitted a record: %s", out)
	}
}

// The plan is a function of the change and the registry and nothing else: two runs
// produce the same bytes, so a plan can be compared and a digest can bind it.
func TestPlanIsReproducible(t *testing.T) {
	reg := fixtureRegistry(t)
	in := write(t, "change.json", envelope(t, "gate.change", "c-1", change{
		BaseSHA: "c0b4c69197", TreeSHA: "c0b4c69197",
		ChangedPaths: []string{"docs/a.md", "tools/first/main.go"},
	}))
	root := tree(t, "docs/a.md", "tools/first/main.go")
	firstCode, first := plan(t, reg, root, in)
	secondCode, second := plan(t, reg, root, in)
	if firstCode != 0 || secondCode != 0 {
		t.Fatalf("plan exited %d then %d", firstCode, secondCode)
	}
	if string(first) != string(second) {
		t.Fatalf("two plans of one change differ:\n%s\n%s", first, second)
	}
}

// TestHelpIsTheFaceSurface pins the layer-1 surface (docs/design/tillr-distribution.md
// Section 3): `--help` names the faces and a face's `--help` names that face's flags, both
// on stdout and both exit 0.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := run(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, out)
	}
	for _, face := range []string{"change", "dualrun", "plan", "run", "shadow"} {
		if !strings.Contains(string(out), face) {
			t.Errorf("--help does not name the face %q: %s", face, out)
		}
	}
	code, out = run(t, "plan", "--help")
	if code != 0 {
		t.Fatalf("plan --help exited %d, want 0: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of plan") {
		t.Errorf("plan --help does not name the face's flags: %s", out)
	}
	if code, out = run(t, "bogus", "--help"); code != 2 {
		t.Errorf("a face that does not exist exited %d, want 2: %s", code, out)
	}
}
