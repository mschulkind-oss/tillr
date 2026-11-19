package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Stop at the unrelated backend-policy read: the gate policy must be read and
// reported before the loop can create a store, stage a lane, or dispatch a run.
func gatePolicyStartup(t *testing.T, raw string) (string, string) {
	t.Helper()
	root := scratch(t, "gate-policy")
	if raw != "" {
		path := filepath.Join(root, "config", "tillr", "gate-policy.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, raw)
	}
	code, out := invoke(t, "loop", "--root", root,
		"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")
	if code != 2 {
		t.Fatalf("startup exit %d, want 2: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".tillr", "state.db")); !os.IsNotExist(err) {
		t.Fatalf("startup touched the store: %v", err)
	}
	return string(out), readLoopResult(t, root).Detail
}

func TestGatePolicyStartupValues(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"absent", "", "landing_strategy=before-merge auto_revert=false budget_ms=10000 lane_base=green (file absent)"},
		{"configured", `{"landing_strategy":"after-merge","auto_revert":false,"budget_ms":10000,"lane_base":"green"}`, "landing_strategy=after-merge auto_revert=false budget_ms=10000 lane_base=green"},
		{"missing keys", `{}`, "landing_strategy=before-merge auto_revert=false budget_ms=10000 lane_base=green"},
		{"legacy red_cap ignored", `{"landing_strategy":"before-merge","auto_revert":true,"budget_ms":1,"red_cap":{"landings":20,"minutes":60},"lane_base":"tip"}`, "landing_strategy=before-merge auto_revert=true budget_ms=1 lane_base=tip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, detail := gatePolicyStartup(t, tc.raw)
			if !strings.Contains(out, "loop: gate policy "+tc.want+"\n") {
				t.Fatalf("missing single policy line %q: %s", tc.want, out)
			}
			if !strings.Contains(detail, "backend-policy.json") {
				t.Fatalf("gate policy should pass before backend refusal: %s", detail)
			}
		})
	}
}

func TestGatePolicyRefusesInvalidValuesBeforeDispatch(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"landing_strategy", `"surprise"`}, {"landing_strategy", `null`},
		{"auto_revert", `"false"`}, {"auto_revert", `0`}, {"auto_revert", `null`},
		{"budget_ms", `0`}, {"budget_ms", `-1`}, {"budget_ms", `1.5`},
		{"budget_ms", `"10000"`}, {"budget_ms", `true`}, {"budget_ms", `null`},
		{"lane_base", `"latest"`}, {"lane_base", `null`},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			raw := `{"` + tc.key + `":` + tc.value + `}`
			out, detail := gatePolicyStartup(t, raw)
			want := tc.key + "=" + tc.value
			if !strings.Contains(detail, want) {
				t.Fatalf("refusal does not name %s: %s", want, detail)
			}
			if strings.Contains(out, "reading the policy") || strings.Contains(out, "loop: gate policy ") {
				t.Fatalf("invalid gate policy reached later startup: %s", out)
			}
		})
	}
}

func TestGatePolicyRefusesMalformedDocument(t *testing.T) {
	for _, raw := range []string{`{`, `[]`, `null`, `{} {}`} {
		t.Run(raw, func(t *testing.T) {
			_, detail := gatePolicyStartup(t, raw)
			if !strings.Contains(detail, "gate-policy.json") {
				t.Fatalf("missing policy diagnostic: %s", detail)
			}
			if strings.Contains(detail, "backend-policy.json") {
				t.Fatalf("bad document reached backend read: %s", detail)
			}
		})
	}
}
