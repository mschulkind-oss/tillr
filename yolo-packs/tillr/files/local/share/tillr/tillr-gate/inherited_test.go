package main

import (
	"testing"
)

func TestApplyInheritedFailures(t *testing.T) {
	baseVerdict := func(result string, checks ...VerdictCheck) VerdictPayload {
		return VerdictPayload{
			VerdictID:      "v-1",
			PlanID:         "p-1",
			RegistryDigest: "d-1",
			BaseSHA:        "base-sha",
			TreeSHA:        "tree-sha",
			Result:         result,
			Checks:         checks,
		}
	}

	t.Run("promotes fail verdict when all failing checks are inherited", func(t *testing.T) {
		v := baseVerdict(ResultFail,
			VerdictCheck{CheckID: "go-test", Result: ResultPass, LogRef: "go.log"},
			VerdictCheck{CheckID: "pack-render", Result: ResultFail, LogRef: "pack.log"},
		)
		inherited := []InheritedFailure{
			{CheckID: "pack-render", TipSHA: "tip-1", LogRef: "tip-pack.log", Output: "render failed on tip"},
		}
		got, ok := ApplyInheritedFailures(v, inherited)
		if !ok {
			t.Fatal("ApplyInheritedFailures = false, want true")
		}
		if got.Result != ResultPassInherited {
			t.Fatalf("Result = %q, want %q", got.Result, ResultPassInherited)
		}
		if got.Checks[1].Result != ResultInherited {
			t.Fatalf("Checks[1].Result = %q, want %q", got.Checks[1].Result, ResultInherited)
		}
		if len(got.InheritedFailures) != 1 || got.InheritedFailures[0].Output != "render failed on tip" {
			t.Fatalf("InheritedFailures = %+v", got.InheritedFailures)
		}
	})

	t.Run("does not promote when an uninherited check failed", func(t *testing.T) {
		v := baseVerdict(ResultFail,
			VerdictCheck{CheckID: "pack-render", Result: ResultFail},
			VerdictCheck{CheckID: "unit-test", Result: ResultFail},
		)
		inherited := []InheritedFailure{
			{CheckID: "pack-render", TipSHA: "tip-1", Output: "render failed on tip"},
		}
		got, ok := ApplyInheritedFailures(v, inherited)
		if ok {
			t.Fatal("ApplyInheritedFailures = true, want false")
		}
		if got.Result != ResultFail {
			t.Fatalf("Result = %q, want %q", got.Result, ResultFail)
		}
	})

	t.Run("does not promote error or pass verdicts", func(t *testing.T) {
		for _, res := range []string{ResultError, ResultStalled, ResultPass} {
			v := baseVerdict(res, VerdictCheck{CheckID: "pack-render", Result: res})
			got, ok := ApplyInheritedFailures(v, []InheritedFailure{{CheckID: "pack-render", Output: "x"}})
			if ok || got.Result != res {
				t.Fatalf("ApplyInheritedFailures(%q) = (%v, %q), want (false, %q)", res, ok, got.Result, res)
			}
		}
	})
}
