package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The overrun is on the face's own output, beside the summary, so a slow check is seen by
// whoever reads the gate's stderr and not only by whoever opens its log.
func TestRunPrintsTheOverrunOfASlowChangeCheck(t *testing.T) {
	remember := false
	plan := planRecord(t, planIn{PlanID: "slow-change-note", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
		Checks: []planCheck{{CheckID: "slow", Scope: "change", Tier: "fast", BudgetMS: 1, Remember: &remember,
			Command: []string{"python3", "-c", "import time; time.sleep(0.02)"}}},
	})
	cmd := exec.Command(gateBin, "run", "--input", plan, "--worktree", scratch(t, "slow-note-root"),
		"--output", filepath.Join(scratch(t, "slow-note"), "verdict.json"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("slow change check: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "ms over its budget_ms; its verdict (pass) stands") {
		t.Fatalf("overrun not printed: %s", out)
	}
}

// Until a heavy runner exists, carrying a tree check means dropping its judgment: the
// verdict would stay pass and nothing would ever run the check again. So by default a
// fast tree check that runs past its budget keeps its own verdict while it progresses,
// as a change check does -- a failing one fails the gate -- and the overrun is recorded.
// The 2026-11-16 review measured `go-build` at 5.6-5.7 s against its 2,700 ms budget at
// ten concurrent gates: carried, a candidate that did not compile could land.
func TestFastTreeOverrunKeepsItsVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, exit, result string
		code               int
	}{{"pass", "0", "pass", 0}, {"fail", "1", "fail", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			remember := false
			plan := planRecord(t, planIn{PlanID: "slow-tree-" + tc.name, BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
				Checks: []planCheck{{CheckID: "slow", Scope: "tree", Tier: "fast", BudgetMS: 1, Remember: &remember,
					Command: []string{"python3", "-c", "import sys, time; time.sleep(0.02); sys.exit(" + tc.exit + ")"}}},
			})
			code, raw := runPlan(t, plan, scratch(t, "slow-tree-root"))
			var verdict struct {
				Payload struct {
					Result string `json:"result"`
					Checks []struct {
						Result       string `json:"result"`
						DurationMS   int64  `json:"duration_ms"`
						OverBudgetMS int64  `json:"over_budget_ms"`
						LogRef       string `json:"log_ref"`
					} `json:"checks"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(raw, &verdict); err != nil {
				t.Fatalf("no verdict: %v: %s", err, raw)
			}
			if code != tc.code || verdict.Payload.Result != tc.result || len(verdict.Payload.Checks) != 1 {
				t.Fatalf("slow tree check: exit %d: %s", code, raw)
			}
			check := verdict.Payload.Checks[0]
			if check.Result != tc.result || check.OverBudgetMS <= 0 || check.OverBudgetMS != check.DurationMS-1 {
				t.Fatalf("overrun not recorded against the check: %s", raw)
			}
			log, err := os.ReadFile(check.LogRef)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), "over its budget_ms 1") || strings.Contains(string(log), "deferred") {
				t.Fatalf("overrun log wording: %s", log)
			}
		})
	}
}

// A change check that runs past its budget is not "could not judge": it ran, so its own
// answer is the verdict, and the overrun is recorded against the check. The budget is 1 ms
// and the child sleeps 20 ms, so the overrun is certain without any elapsed-time assertion:
// a slower machine only makes it larger. The case is the 2026-11-16 drain fault, where
// `documents` ran past 2000 ms under load and the item was held as gate-harness-error.
func TestFastChangeOverrunKeepsItsVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, exit, result string
		code               int
	}{{"pass", "0", "pass", 0}, {"fail", "1", "fail", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			remember := false
			plan := planRecord(t, planIn{PlanID: "slow-change-" + tc.name, BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
				Checks: []planCheck{{CheckID: "slow", Scope: "change", Tier: "fast", BudgetMS: 1, Remember: &remember,
					Command: []string{"python3", "-c", "import sys, time; time.sleep(0.02); sys.exit(" + tc.exit + ")"}}},
			})
			code, raw := runPlan(t, plan, scratch(t, "slow-change-root"))
			var verdict struct {
				Payload struct {
					Result string `json:"result"`
					Checks []struct {
						Result       string `json:"result"`
						DurationMS   int64  `json:"duration_ms"`
						OverBudgetMS int64  `json:"over_budget_ms"`
						LogRef       string `json:"log_ref"`
					} `json:"checks"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(raw, &verdict); err != nil {
				t.Fatalf("no verdict: %v: %s", err, raw)
			}
			if code != tc.code || verdict.Payload.Result != tc.result || len(verdict.Payload.Checks) != 1 {
				t.Fatalf("slow change check: exit %d: %s", code, raw)
			}
			check := verdict.Payload.Checks[0]
			if check.Result != tc.result || check.OverBudgetMS <= 0 || check.OverBudgetMS != check.DurationMS-1 {
				t.Fatalf("overrun not recorded against the check: %s", raw)
			}
			log, err := os.ReadFile(check.LogRef)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), "over its budget_ms 1") || strings.Contains(string(log), "deferred") {
				t.Fatalf("overrun log wording: %s", log)
			}
		})
	}
}

func TestDeclarationExitFourIsNoJudgment(t *testing.T) {
	for _, scope := range []string{"tree", "change"} {
		t.Run(scope, func(t *testing.T) {
			remember := false
			plan := planRecord(t, planIn{PlanID: "no-judgment", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
				Checks: []planCheck{{CheckID: "unavailable", Scope: scope, Tier: "fast", BudgetMS: 1000, Remember: &remember, Command: []string{"sh", "-c", "exit 4"}}},
			})
			code, raw := runPlan(t, plan, scratch(t, "unavailable-root"))
			if code != 4 || decodeVerdict(t, raw).Payload.Result != "error" {
				t.Fatalf("unavailable %s judge: exit %d: %s", scope, code, raw)
			}
		})
	}
}
