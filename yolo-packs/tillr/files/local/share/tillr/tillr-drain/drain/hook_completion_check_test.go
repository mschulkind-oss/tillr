package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionRequiresItemVerdictFlagAtStartup(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-item", true: "current-item"}[supported], func(t *testing.T) {
			hooks, log := hookStub(t, []string{"complete-item"}, "")
			bin := filepath.Join(filepath.Dir(log), "item")
			flags := "--store --item --state --now --reason --by --resolves-via"
			if supported {
				flags += " --require-verdicts"
			}
			writeForkFile(t, bin, "#!/bin/sh\n[ \"$*\" = 'state --help' ] || exit 99\necho '"+flags+"'\n")
			if err := os.Chmod(bin, 0755); err != nil {
				t.Fatal(err)
			}
			steps := &ProcessSteps{Root: filepath.Dir(log), ItemBin: bin}
			steps.ConfigureHooks(hooks, nil)
			err := steps.CheckPrograms(context.Background(), nil)
			if supported && err != nil {
				t.Fatal(err)
			}
			if !supported && (err == nil || !strings.Contains(err.Error(), "--require-verdicts")) {
				t.Fatalf("startup: %v", err)
			}
		})
	}
}
