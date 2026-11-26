package main_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

var bin string

// binDir holds this module's binary.
var binDir string

// Scratch lives under $YOLO_DURABLE_DIR, never /tmp: a suite that writes to /tmp loses
// its evidence when the box is cleaned, and the owner refuses it.
func scratchRoot() string {
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", ".yolo", "durable")
	}
	if !filepath.IsAbs(root) {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	return root
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
	// The stand-in `pi` programs these cases write do not answer `pi --features`, so the
	// features they stand for are stated to the adapter instead of probed.
	os.Setenv("TILLR_PI_FEATURES", "parked-tool-calls")
	if os.Getenv("RUN_OUTPUT_HELPER") == "1" {
		return m.Run()
	}
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 1
	}
	dir, err := os.MkdirTemp(root, "tillr-drain-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	// All default fixture commits use this deployed identity. Keep the config
	// private so identity observation never depends on the host's Git identity.
	config := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(config, []byte("[user]\n name = Fixture Landing\n email = landing@example.invalid\n"), 0600); err != nil {
		return 1
	}
	os.Setenv("GIT_CONFIG_GLOBAL", config)
	binDir = dir
	bin = filepath.Join(dir, "tillr-drain")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}
	return m.Run()
}

// invoke drives the binary with the state home scrubbed, so a face that derives a store
// path instead of taking --store cannot reach the owner's board.
func invoke(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out
}

func envelope(kind, id string, payload any) string {
	raw, err := json.Marshal(map[string]any{
		"record_version": 1,
		"kind":           kind,
		"id":             id,
		"producer":       map[string]string{"system": "tillr", "utility": "drain", "version": "0.1.0"},
		"created_at":     "2026-11-10T00:00:00Z",
		"payload":        payload,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func itemRecord(itemID, state string, blockedBy ...string) string {
	return itemRecordRanked(itemID, state, "", blockedBy...)
}

func itemRecordRanked(itemID, state, rank string, blockedBy ...string) string {
	return itemRecordFenced(itemID, state, rank, []string{"items/" + itemID + "/"}, blockedBy...)
}

func itemRecordFenced(itemID, state, rank string, allowed []string, blockedBy ...string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    blockedBy,
		"rank":          rank,
		"allowed_paths": allowed,
		"criteria":      []any{},
	})
}

func fixture(t *testing.T, records ...string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "store"), "board.sqlite3")
	ctx := context.Background()
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("opening the fixture store failed: %v", err)
	}
	defer s.Close()
	raw, err := json.Marshal(map[string]any{
		"kind":    "fixture.seed",
		"payload": map[string]any{"records": rawRecords(records)},
	})
	if err != nil {
		t.Fatalf("building the seed bundle failed: %v", err)
	}
	if err := s.Seed(ctx, raw); err != nil {
		t.Fatalf("seeding the fixture store failed: %v", err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s failed: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

func rawRecords(records []string) []json.RawMessage {
	raw := make([]json.RawMessage, len(records))
	for i, record := range records {
		raw[i] = json.RawMessage(record)
	}
	return raw
}

// The retry face's help names both forms — a build retry and a design run's re-raise — and
// that every refusal names the command it would accept.
func TestRetryHelpNamesTheDesignRunForm(t *testing.T) {
	code, out := invoke(t, "retry", "--help")
	if code != 0 {
		t.Fatalf("retry --help exited %d: %s", code, out)
	}
	for _, want := range []string{"design run", "raises the item's conflict again", "names the exact command it would accept"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("retry --help does not say %q:\n%s", want, out)
		}
	}
}

// TestHelpIsTheFaceSurface pins the layer-1 surface (docs/reference/tillr-distribution.md,
// "Documentation split"): `--help` names the faces and a face's `--help` names that face's flags, both
// on stdout and both exit 0.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := invoke(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, out)
	}
	for _, face := range []string{"plan", "once", "retry", "boundary", "loop"} {
		if !strings.Contains(string(out), face) {
			t.Errorf("--help does not name the face %q: %s", face, out)
		}
	}
	code, out = invoke(t, "plan", "--help")
	if code != 0 {
		t.Fatalf("plan --help exited %d, want 0: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of tillr-drain plan") {
		t.Errorf("plan --help does not name the face's flags: %s", out)
	}
	if code, out = invoke(t, "bogus", "--help"); code != 2 {
		t.Errorf("a face that does not exist exited %d, want 2: %s", code, out)
	}
}

func tick(t *testing.T, now string, lanes []string, maxDispatches int, policyRef string) string {
	t.Helper()
	return tickPayload(t, map[string]any{
		"now":             now,
		"available_lanes": lanes,
		"max_dispatches":  maxDispatches,
		"policy_ref":      policyRef,
	})
}

func tickPayload(t *testing.T, payload map[string]any) string {
	t.Helper()
	path := filepath.Join(scratch(t, "tick"), "tick.json")
	if err := os.WriteFile(path, []byte(envelope(drain.PassKind, "tick-1", payload)), 0o644); err != nil {
		t.Fatalf("writing the tick failed: %v", err)
	}
	return path
}

func policyDoc(t *testing.T, document map[string]any) string {
	t.Helper()
	path := filepath.Join(scratch(t, "policy"), "policy.json")
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("building the policy failed: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the policy failed: %v", err)
	}
	return path
}

func twoBackendPolicy(t *testing.T, maxFailovers int, overrides map[string]string) string {
	t.Helper()
	document := map[string]any{
		"default_backend": "first",
		"preference":      []string{"first", "second"},
		"max_failovers":   maxFailovers,
		"backends": map[string]any{
			"first": map[string]any{
				"adapter":         "/bin/true",
				"models":          []string{"route-a", "route-b"},
				"effort":          "high",
				"timeout_seconds": 60,
			},
			"second": map[string]any{
				"adapter":         "/bin/true",
				"model":           "route-z",
				"timeout_seconds": 60,
			},
		},
	}
	if overrides != nil {
		document["overrides"] = overrides
	}
	return policyDoc(t, document)
}

// A tick the face cannot read is malformed input: exit 2, and no output file, because a
// plan that failed half way must not leave a file a caller would read as an answer.
func TestPlanRefusesAMalformedTick(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))

	cases := map[string]map[string]any{
		"unparsable now": {"now": "yesterday", "available_lanes": []string{"lane-1"}, "max_dispatches": 1},
		"negative max":   {"now": "2026-11-10T00:00:00Z", "available_lanes": []string{"lane-1"}, "max_dispatches": -1},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(scratch(t, "refused"), "decisions.json")
			code, output := invoke(t, "plan", "--store", storePath, "--input", tickPayload(t, payload), "--output", out)
			if code != 2 {
				t.Fatalf("plan exited %d, want 2: %s", code, output)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a refused tick left an output file at %s", out)
			}
		})
	}

	t.Run("wrong record kind", func(t *testing.T) {
		path := filepath.Join(scratch(t, "wrong-kind"), "tick.json")
		raw := envelope("drain.tock", "tick-1", map[string]any{
			"now": "2026-11-10T00:00:00Z", "available_lanes": []string{"lane-1"}, "max_dispatches": 1,
		})
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatalf("writing the tick failed: %v", err)
		}
		out := filepath.Join(scratch(t, "wrong-kind"), "decisions.json")
		if code, output := invoke(t, "plan", "--store", storePath, "--input", path, "--output", out); code != 2 {
			t.Fatalf("plan exited %d, want 2: %s", code, output)
		}
	})

	t.Run("policy names no backends", func(t *testing.T) {
		empty := policyDoc(t, map[string]any{"backends": map[string]any{}})
		out := filepath.Join(scratch(t, "empty-policy"), "decisions.json")
		code, output := invoke(t, "plan", "--store", storePath,
			"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, empty), "--output", out)
		if code != 2 {
			t.Fatalf("plan exited %d, want 2: %s", code, output)
		}
	})

	t.Run("policy that is not there", func(t *testing.T) {
		out := filepath.Join(scratch(t, "missing-policy"), "decisions.json")
		code, output := invoke(t, "plan", "--store", storePath,
			"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, filepath.Join(scratch(t, "missing-policy"), "nope.json")),
			"--output", out)
		if code != 2 {
			t.Fatalf("plan exited %d, want 2: %s", code, output)
		}
	})
}

// A backend whose timeout_seconds is negative refuses the plan by name instead of emitting
// a request run would refuse at claim. Zero is not refused: it is an unbounded run whose
// lane lease carries no expiry (design D-RW1, docs/reference/run-wall-ruling.md).
func TestPlanRefusesAPolicyWhoseBackendHasANegativeTimeout(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	policyPath := policyDoc(t, map[string]any{
		"default_backend": "first",
		"preference":      []string{"first"},
		"backends": map[string]any{
			"first": map[string]any{"adapter": "/bin/true", "model": "route-a", "timeout_seconds": -1},
		},
	})
	out := filepath.Join(scratch(t, "negative-timeout"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath,
		"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath), "--output", out)
	if code != 2 {
		t.Fatalf("plan exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "timeout_seconds") {
		t.Fatalf("the refusal %q does not name the field", output)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused policy left an output file at %s", out)
	}
}

// An override naming an item the store does not hold is refused rather than ignored: the
// override map names items, so a stale entry is exactly the rot design §2 refuses
// mechanically instead of letting it fall through a silent default.
func TestPlanRefusesAnOverrideNamingAnUnknownItem(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	policyPath := twoBackendPolicy(t, 1, map[string]string{"item_999": "second"})
	out := filepath.Join(scratch(t, "stale-override"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath,
		"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath), "--output", out)
	if code != 2 {
		t.Fatalf("plan exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "item_999") {
		t.Fatalf("the refusal %q does not name the stale override", output)
	}
}

// On 2026-11-23 the live drain loop held 3.8 GB resident because loopFace enabled
// store.CacheCommittedEvents(), which retained all 1.39M events in memory (1.37 GB in
// store.(*eventCache), 92% unselected run.child-session worker records). loopFace must
// enable the narrow selected-event cache instead.
func TestLoopUsesSelectedEventCache(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var foundSelected, foundCommitted bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "loopFace" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "store" {
				return true
			}
			switch sel.Sel.Name {
			case "CacheSelectedEvents":
				foundSelected = true
			case "CacheCommittedEvents":
				foundCommitted = true
			}
			return true
		})
	}
	if !foundSelected {
		t.Error("loopFace does not call store.CacheSelectedEvents()")
	}
	if foundCommitted {
		t.Error("loopFace calls store.CacheCommittedEvents(), which retains the whole event log")
	}
}

func TestRetryDefaultsNowToCurrentTime(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		envelope("run.record", "run-1", map[string]any{
			"run_id":      "run-1",
			"item_id":     "item_042",
			"lane_id":     "lane-1",
			"backend":     "pi",
			"state":       "exited",
			"exit_code":   2,
			"started_at":  "2026-11-10T00:00:00Z",
			"finished_at": "2026-11-10T00:01:00Z",
		}),
	)

	before := time.Now().UTC().Truncate(time.Second)
	code, out := invoke(t, "retry",
		"--store", storePath,
		"--item", "item_042",
		"--run", "run-1",
		"--by", "operator",
		"--why", "retrying without --now",
	)
	after := time.Now().UTC().Add(time.Second)
	if code != 0 {
		t.Fatalf("retry without --now exited %d: %s", code, out)
	}
	var got struct {
		Kind      string    `json:"kind"`
		CreatedAt time.Time `json:"created_at"`
		Payload   struct {
			ItemID          string    `json:"item_id"`
			SupersedesRunID string    `json:"supersedes_run_id"`
			RequestedAt     time.Time `json:"requested_at"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding retry record: %v\n%s", err, out)
	}
	if got.Kind != "drain.retry" || got.Payload.ItemID != "item_042" || got.Payload.SupersedesRunID != "run-1" {
		t.Fatalf("unexpected retry record: %+v", got)
	}
	if got.Payload.RequestedAt.Before(before) || got.Payload.RequestedAt.After(after) {
		t.Fatalf("requested_at %s outside [%s, %s]", got.Payload.RequestedAt, before, after)
	}

	if code, out := invoke(t, "retry",
		"--store", storePath,
		"--item", "item_042",
		"--run", "run-1",
		"--by", "operator",
		"--why", "bad clock",
		"--now", "not-a-timestamp",
	); code != 2 {
		t.Fatalf("retry with malformed --now exited %d, want 2: %s", code, out)
	}
}
