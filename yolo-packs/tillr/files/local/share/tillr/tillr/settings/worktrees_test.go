package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreesDefaultsAndEffective(t *testing.T) {
	dir := t.TempDir()
	s, err := Read(dir)
	if err != nil {
		t.Fatalf("Read empty dir: %v", err)
	}
	if s.Worktrees.DiskBudgetSet || s.Worktrees.MaxCountSet {
		t.Fatalf("expected unset worktree bounds, got %+v", s.Worktrees)
	}
	if got := s.Worktrees.EffectiveDiskBudgetBytes(20); got != 20*DefaultWorktreeDiskBudgetPerAgentBytes {
		t.Fatalf("EffectiveDiskBudgetBytes(20) = %d, want %d", got, 20*DefaultWorktreeDiskBudgetPerAgentBytes)
	}
	if got := s.Worktrees.EffectiveMaxCount(20); got != 40 {
		t.Fatalf("EffectiveMaxCount(20) = %d, want 40", got)
	}
	if got := s.Worktrees.EffectiveDiskBudgetBytes(0); got != 0 {
		t.Fatalf("EffectiveDiskBudgetBytes(0) = %d, want 0", got)
	}
	if got := s.Worktrees.EffectiveMaxCount(0); got != 0 {
		t.Fatalf("EffectiveMaxCount(0) = %d, want 0", got)
	}
}

func TestWorktreesValidConfig(t *testing.T) {
	dir := t.TempDir()
	content := "[worktrees]\ndisk_budget_bytes = 10737418240\nmax_count = 12\n"
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Read(dir)
	if err != nil {
		t.Fatalf("Read valid [worktrees]: %v", err)
	}
	if !s.Worktrees.DiskBudgetSet || s.Worktrees.DiskBudgetBytes != 10737418240 {
		t.Fatalf("unexpected DiskBudget: %+v", s.Worktrees)
	}
	if !s.Worktrees.MaxCountSet || s.Worktrees.MaxCount != 12 {
		t.Fatalf("unexpected MaxCount: %+v", s.Worktrees)
	}
	if got := s.Worktrees.EffectiveDiskBudgetBytes(20); got != 10737418240 {
		t.Fatalf("EffectiveDiskBudgetBytes override = %d, want 10737418240", got)
	}
	if got := s.Worktrees.EffectiveMaxCount(20); got != 12 {
		t.Fatalf("EffectiveMaxCount override = %d, want 12", got)
	}
	line := s.StartupLine()
	if !strings.Contains(line, "worktrees.disk_budget_bytes=10737418240") || !strings.Contains(line, "worktrees.max_count=12") {
		t.Fatalf("StartupLine missing worktrees settings: %s", line)
	}
}

func TestWorktreesInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "zero disk budget",
			toml: "[worktrees]\ndisk_budget_bytes = 0\n",
			want: "worktrees.disk_budget_bytes",
		},
		{
			name: "negative disk budget",
			toml: "[worktrees]\ndisk_budget_bytes = -1\n",
			want: "worktrees.disk_budget_bytes",
		},
		{
			name: "zero max count",
			toml: "[worktrees]\nmax_count = 0\n",
			want: "worktrees.max_count",
		},
		{
			name: "negative max count",
			toml: "[worktrees]\nmax_count = -5\n",
			want: "worktrees.max_count",
		},
		{
			name: "unknown key",
			toml: "[worktrees]\nfoo = 1\n",
			want: "worktrees.foo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(tc.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Read(dir)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q missing %q", err.Error(), tc.want)
			}
		})
	}
}
