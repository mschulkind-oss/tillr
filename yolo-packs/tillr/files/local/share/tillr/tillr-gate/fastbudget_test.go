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

// The child blocks until the runner signals it. These cases judge recorded state,
// never elapsed time; timeout_ms deliberately differs from the fast budget.
func TestFastBudgetRecordsOverrunWithoutJudgingTree(t *testing.T) {
	for _, scope := range []string{"tree", "change"} {
		t.Run(scope, func(t *testing.T) {
			remember := false
			// A change check is not stopped at its budget; it is stopped at its ceiling,
			// which is its own timeout_ms. A child that never answers reaches it.
			timeout := 3000
			if scope == "change" {
				timeout = 200
			}
			plan := planRecord(t, planIn{PlanID: "fast-budget", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
				Checks: []planCheck{{CheckID: "blocked", Scope: scope, Tier: "fast", BudgetMS: 100, TimeoutMS: timeout, Remember: &remember,
					Command: []string{"python3", "-c", "import signal; signal.pause()"}},
					{CheckID: "after", Command: []string{"true"}}},
			})
			code, raw := runPlan(t, plan, scratch(t, "fast-root"))
			verdict := decodeVerdict(t, raw)
			wantCode, wantResult, wantCheck := 0, "pass", "over-budget"
			if scope == "change" {
				wantCode, wantResult, wantCheck = 4, "error", "error"
			}
			if code != wantCode || verdict.Payload.Result != wantResult || len(verdict.Payload.Checks) != 2 || verdict.Payload.Checks[0].Result != wantCheck || verdict.Payload.Checks[1].Result != "pass" {
				t.Fatalf("fast %s deadline: exit %d: %s", scope, code, raw)
			}
			log, err := os.ReadFile(verdict.Payload.Checks[0].LogRef)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), "budget_ms 100") {
				t.Fatalf("missing budget: %s", log)
			}
			// "deferred" is said only where judgment is deferred: a carried tree check.
			if deferred := strings.Contains(string(log), "deferred"); deferred != (scope == "tree") {
				t.Fatalf("fast %s deadline: deferral wording is wrong: %s", scope, log)
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

func TestFastOverrunDoesNotEraseAnotherFailure(t *testing.T) {
	remember := false
	plan := planRecord(t, planIn{PlanID: "mixed", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
		Checks: []planCheck{{CheckID: "failed", Command: []string{"false"}},
			{CheckID: "blocked", Command: []string{"python3", "-c", "import signal; signal.pause()"}, Scope: "tree", Tier: "fast", BudgetMS: 100, TimeoutMS: 3000, Remember: &remember}},
	})
	code, raw := runPlan(t, plan, scratch(t, "mixed-root"))
	verdict := decodeVerdict(t, raw)
	if code != 1 || verdict.Payload.Result != "fail" || verdict.Payload.Checks[1].Result != "over-budget" {
		t.Fatalf("overrun erased failure: exit %d: %s", code, raw)
	}
}
