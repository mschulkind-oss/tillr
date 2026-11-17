package main_test

import (
	"os"
	"os/exec"
	"testing"
)

func TestClaudeQuotaWithoutModelTokens(t *testing.T) {
	for _, path := range []string{"backends/claude/quota", "backends/claude/quota_test.py"} {
		if _, err := os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("python3", "-B", "backends/claude/quota_test.py")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quota adapter: %v\n%s", err, out)
	}
}
