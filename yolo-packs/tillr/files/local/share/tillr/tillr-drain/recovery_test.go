package main_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
)

func TestRecoveryFacesAnswerHelp(t *testing.T) {
	for _, face := range []string{"acquit", "examine", "set-aside"} {
		code, out := invoke(t, face, "--help")
		if code != record.ExitOK || !strings.Contains(string(out), "-store") {
			t.Fatalf("%s: %d: %s", face, code, out)
		}
		if face == "examine" {
			for _, flag := range []string{"-brief-template", "-run-bin", "-questions-cmd", "-replay", "-fixer-gate"} {
				if !strings.Contains(string(out), flag) {
					t.Fatalf("examine omits loop input %s: %s", flag, out)
				}
			}
		}
	}
}

func TestRecoveryRequiresIdentityAndExplanation(t *testing.T) {
	for _, args := range [][]string{{"acquit"}, {"acquit", "--run", "run-a"}, {"examine"}, {"set-aside"}, {"set-aside", "--item", "item-a"}} {
		code, out := invoke(t, args...)
		if code != record.ExitUsage {
			t.Fatalf("%v: %d: %s", args, code, out)
		}
	}
}
