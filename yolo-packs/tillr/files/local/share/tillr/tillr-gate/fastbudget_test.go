package main_test

import (
	"os"
	"strings"
	"testing"
)

// The child blocks until the runner signals it. These cases judge recorded state,
// never elapsed time; timeout_ms deliberately differs from the fast budget.
func TestFastBudgetRecordsOverrunWithoutJudgingTree(t *testing.T) {
	for _, scope := range []string{"tree", "change"} {
		t.Run(scope, func(t *testing.T) {
			remember := false
			plan := planRecord(t, planIn{PlanID: "fast-budget", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "registry",
				Checks: []planCheck{{CheckID: "blocked", Scope: scope, Tier: "fast", BudgetMS: 100, TimeoutMS: 3000, Remember: &remember,
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
