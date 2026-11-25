package gitidentity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type checkFixture struct {
	t          *testing.T
	tmp        string
	repo       string
	policyPath string
	env        []string
}

func newCheckFixture(t *testing.T, userName, userEmail string, allowed [][]string) *checkFixture {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	cfgPath := filepath.Join(tmp, "gitconfig")
	var cfgContent strings.Builder
	if userName != "" || userEmail != "" {
		cfgContent.WriteString("[user]\n")
		if userName != "" {
			fmt.Fprintf(&cfgContent, " name = %s\n", userName)
		}
		if userEmail != "" {
			fmt.Fprintf(&cfgContent, " email = %s\n", userEmail)
		}
	}
	if err := os.WriteFile(cfgPath, []byte(cfgContent.String()), 0o644); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}

	policyPath := filepath.Join(tmp, "allowed.json")
	if allowed == nil {
		allowed = [][]string{{"Allowed Owner", "owner@example.invalid"}}
	}
	rawPolicy, err := json.Marshal(allowed)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	if err := os.WriteFile(policyPath, rawPolicy, 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	var env []string
	for _, entry := range os.Environ() {
		k, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(k, "GIT_") || k == planBaseEnv || k == policyEnv {
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"GIT_CONFIG_GLOBAL="+cfgPath,
		"GIT_CONFIG_NOSYSTEM=1",
		policyEnv+"="+policyPath,
	)

	f := &checkFixture{
		t:          t,
		tmp:        tmp,
		repo:       repo,
		policyPath: policyPath,
		env:        env,
	}
	f.gitMust("init", "-q", "-b", "main")
	return f
}

func (f *checkFixture) gitMust(args ...string) string {
	f.t.Helper()
	return f.gitEnvMust(nil, args...)
}

func (f *checkFixture) gitEnvMust(extra map[string]string, args ...string) string {
	f.t.Helper()
	fullArgs := append([]string{"-C", f.repo}, args...)
	cmd := exec.Command("git", fullArgs...)
	env := append([]string(nil), f.env...)
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *checkFixture) run(extra ...string) (int, string) {
	f.t.Helper()
	env := append([]string(nil), f.env...)
	env = append(env, extra...)
	var out bytes.Buffer
	code := CheckIdentity(f.repo, env, &out)
	return code, out.String()
}

func TestCheckIdentityMissingGlobalIdentity(t *testing.T) {
	f := newCheckFixture(t, "", "", nil)
	code, out := f.run()
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "the landing identity is not configured") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCheckIdentityLocalOverrideRefused(t *testing.T) {
	f := newCheckFixture(t, "Allowed Owner", "owner@example.invalid", nil)
	f.gitMust("config", "user.name", "Local Override")
	code, out := f.run()
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "this repository's own config sets the landing identity") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, "local user.name overrides the deployed identity") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCheckIdentityNoDiffBase(t *testing.T) {
	f := newCheckFixture(t, "Allowed Owner", "owner@example.invalid", nil)
	f.gitMust("commit", "--allow-empty", "-qm", "base")
	code, out := f.run()
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "no diff base, so no commit can be called added by this change") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCheckIdentityApprovedCommitsWithPlanBase(t *testing.T) {
	f := newCheckFixture(t, "Allowed Owner", "owner@example.invalid", nil)
	f.gitMust("commit", "--allow-empty", "-qm", "base")
	base := f.gitMust("rev-parse", "HEAD")
	f.gitMust("commit", "--allow-empty", "-qm", "second")

	code, out := f.run(planBaseEnv + "=" + base)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "1 commit(s) in "+base[:10]+"..HEAD carry only approved identities") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCheckIdentityDisallowedCommitAndTruncation(t *testing.T) {
	f := newCheckFixture(t, "Allowed Owner", "owner@example.invalid", nil)
	f.gitMust("commit", "--allow-empty", "-qm", "base")
	base := f.gitMust("rev-parse", "HEAD")

	// 6 commits with both author and committer foreign -> 12 findings (> maxReported=10)
	for i := 0; i < 6; i++ {
		f.gitEnvMust(map[string]string{
			"GIT_AUTHOR_NAME":     "Foreign",
			"GIT_AUTHOR_EMAIL":    "foreign@example.invalid",
			"GIT_COMMITTER_NAME":  "Foreign",
			"GIT_COMMITTER_EMAIL": "foreign@example.invalid",
		}, "commit", "--allow-empty", "-qm", fmt.Sprintf("bad-%d", i))
	}

	code, out := f.run(planBaseEnv + "=" + base)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "12 disallowed author/committer pair(s)") {
		t.Fatalf("expected 12 findings, got: %s", out)
	}
	if !strings.Contains(out, "... and 2 more") {
		t.Fatalf("expected truncation line, got: %s", out)
	}
}

func TestCheckIdentityOriginMainMergeBase(t *testing.T) {
	f := newCheckFixture(t, "Allowed Owner", "owner@example.invalid", nil)
	f.gitMust("commit", "--allow-empty", "-qm", "base")
	f.gitMust("update-ref", "refs/remotes/origin/main", "HEAD")
	f.gitMust("commit", "--allow-empty", "-qm", "lane-commit")

	code, out := f.run()
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "1 commit(s) in ") || !strings.Contains(out, "carry only approved identities") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCheckIdentityInvalidPolicyReturns2(t *testing.T) {
	f := newCheckFixture(t, "Allowed Owner", "owner@example.invalid", nil)
	f.gitMust("commit", "--allow-empty", "-qm", "base")
	base := f.gitMust("rev-parse", "HEAD")

	missingPolicy := filepath.Join(f.tmp, "missing.json")
	code, out := f.run(planBaseEnv+"="+base, policyEnv+"="+missingPolicy)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d: %s", code, out)
	}
	if !strings.Contains(out, "cannot read the repository identity policy") {
		t.Fatalf("unexpected output: %s", out)
	}
}
