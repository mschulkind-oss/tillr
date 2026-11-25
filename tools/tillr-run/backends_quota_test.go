package main_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestClaudeQuotaWithoutModelTokens(t *testing.T) {
	// Usage error on unexpected arguments exits 2.
	cmd := exec.Command(bin, "claude-quota", "extra")
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("claude-quota extra: got err=%v out=%s, want exit 2", err, out)
	}
	if !strings.Contains(string(out), "usage: quota") {
		t.Fatalf("claude-quota extra stderr = %q", out)
	}

	// Missing auth exits 1 without starting any model process.
	emptyHome := scratch(t, "quota-home")
	cmd = exec.Command(bin, "claude-quota")
	cmd.Env = []string{"HOME=" + emptyHome, "PATH=" + os.Getenv("PATH")}
	out, err = cmd.CombinedOutput()
	exit, ok = err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("claude-quota missing auth: got err=%v out=%s, want exit 1", err, out)
	}
	if !strings.Contains(string(out), "subscription access token") {
		t.Fatalf("claude-quota missing auth stderr = %q", out)
	}
}
