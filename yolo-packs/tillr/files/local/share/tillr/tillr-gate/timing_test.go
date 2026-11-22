package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckWallTimeDistinguishesFreshReusedAndNotRun(t *testing.T) {
	root, tree := memoryRepo(t)
	check := PlanCheck{CheckID: "tree-check", Scope: "tree", Reads: []string{"input/"}, Command: []string{"true"}}
	unavailable := check
	unavailable.Needs = []string{"ref:refs/heads/patch/missing"}
	cannotStart := PlanCheck{CheckID: "absent", Command: []string{"/nonexistent-gate-test-command"}}
	for _, test := range []struct {
		check PlanCheck
		want  string
		red   bool
	}{{check, "ran", false}, {check, "reused", false}, {unavailable, "not-run", false}, {cannotStart, "not-run", true}} {
		want := test.want
		record, runErr := RunWith(memoryPlan(t, tree, test.check), root, t.TempDir(), RunOptions{measureLanding: true})
		if (runErr != nil) != test.red {
			t.Fatalf("%s: %v", want, runErr)
		}
		var verdict struct {
			WallMS *int64 `json:"wall_ms"`
			Checks []struct {
				Execution  string `json:"execution"`
				WallMS     *int64 `json:"wall_ms"`
				DurationMS int64  `json:"duration_ms"`
			} `json:"checks"`
		}
		if err := json.Unmarshal(record.Payload, &verdict); err != nil {
			t.Fatal(err)
		}
		if verdict.WallMS == nil || len(verdict.Checks) != 1 {
			t.Fatalf("missing run measurements: %s", record.Payload)
		}
		got := verdict.Checks[0]
		if got.Execution != want || (got.WallMS == nil) != (want == "not-run") ||
			got.WallMS != nil && (*got.WallMS < 0 || *got.WallMS > *verdict.WallMS) ||
			want == "reused" && got.DurationMS != 0 {
			t.Fatalf("want %s: %s", want, record.Payload)
		}
	}
}

func TestFastTreeCheckCannotInheritGateHandoffs(t *testing.T) {
	for _, name := range []string{"TILLR_ORDINARY_GATE_TIMING", "TILLR_FAST_TREE_TIMING", "TILLR_GATE_SPANS_OUT", "TILLR_FAST_VERDICT_OUT"} {
		t.Setenv(name, "parent")
	}
	check := PlanCheck{CheckID: "nested-gate", measureLanding: true,
		Env: map[string]string{"TILLR_FAST_TREE_TIMING": "1"}, Command: []string{"sh", "-c", `
		test -z "${TILLR_ORDINARY_GATE_TIMING+x}${TILLR_FAST_TREE_TIMING+x}${TILLR_GATE_SPANS_OUT+x}${TILLR_FAST_VERDICT_OUT+x}"
	`}}
	if got := runCheck(check, t.TempDir(), t.TempDir(), 0); got.Result != ResultPass || got.Execution != "ran" {
		t.Fatalf("fast tree check inherited a parent handoff: %+v", got)
	}
}

func TestGoBuildEvidenceIsPrivateToTheCheckInvocation(t *testing.T) {
	root, logs := t.TempDir(), t.TempDir()
	inherited := filepath.Join(root, "inherited.json")
	t.Setenv(goBuildEvidenceEnv, inherited)
	check := PlanCheck{CheckID: "go-build", measureLanding: true, Command: []string{"sh", "-c", `printf '%s\n' '[{"module":"tools/fixture","execution":"ran","mode":"compile-only","packages":[{"import_path":"fixture/tool","test_cache":"hit"}]},{"module":"tools/reused","execution":"reused"}]' > "$TILLR_GO_BUILD_EVIDENCE_OUT"`}}
	got := runCheck(check, root, logs, 0)
	if got.Result != ResultPass || len(got.GoBuild) != 2 || got.GoBuild[0].Mode != "compile-only" ||
		got.GoBuild[0].Packages == nil || len(*got.GoBuild[0].Packages) != 1 || (*got.GoBuild[0].Packages)[0].TestCache != "hit" ||
		got.GoBuild[1].Execution != "reused" || got.GoBuild[1].Packages != nil {
		t.Fatalf("go-build package evidence not transferred: %+v", got)
	}
	// A nonpass still contributes inventory, without changing classification.
	for exit, result := range map[string]string{"1": ResultFail, "4": ResultError} {
		check.Command = []string{"sh", "-c", `printf '%s\n' '[{"module":"tools/fixture","execution":"ran","mode":"compile-only","discovery":"partial","packages":[{"import_path":"fixture/tool","test_cache":"not-started"}]}]' > "$TILLR_GO_BUILD_EVIDENCE_OUT"; exit ` + exit}
		got = runCheck(check, root, logs, 0)
		if got.Result != result || len(got.GoBuild) != 1 || got.GoBuild[0].Discovery != "partial" ||
			got.GoBuild[0].Packages == nil || len(*got.GoBuild[0].Packages) != 1 || (*got.GoBuild[0].Packages)[0].TestCache != "not-started" {
			t.Fatalf("exit %s: partial discovery evidence not transferred: %+v", exit, got)
		}
	}
	// A runner paired with an older check gets missing evidence, not a stale hit.
	check.Command = []string{"true"}
	got = runCheck(check, root, logs, 0)
	if got.Result != ResultPass || got.GoBuild != nil {
		t.Fatalf("absent package evidence must not be fabricated: %+v", got)
	}
	if _, err := os.Stat(inherited); !os.IsNotExist(err) {
		t.Fatalf("inherited sidecar must not be used: %v", err)
	}
	if paths, err := filepath.Glob(filepath.Join(logs, "go-build-evidence-*")); err != nil || len(paths) != 0 {
		t.Fatalf("private sidecars must be cleaned up: %v (%v)", paths, err)
	}
}
