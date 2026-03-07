package vcs

import (
	"testing"
)

func TestDetectVCS(t *testing.T) {
	// This test runs inside the lifecycle repo which has both .jj and .git
	result := DetectVCS()
	if result != "jj" && result != "git" && result != "" {
		t.Errorf("DetectVCS() returned unexpected value: %q", result)
	}
}

func TestGetLog(t *testing.T) {
	vcsType, commits, err := GetLog(5)
	if err != nil {
		t.Fatalf("GetLog(5) returned error: %v", err)
	}
	if vcsType == "" {
		t.Skip("No VCS detected, skipping log test")
	}
	if len(commits) == 0 {
		t.Fatal("Expected at least one commit")
	}
	// Verify commit fields are populated
	c := commits[0]
	if c.Hash == "" {
		t.Error("First commit has empty hash")
	}
	if c.Message == "" {
		t.Error("First commit has empty message")
	}
}

func TestGetBranches(t *testing.T) {
	vcsType, branches, err := GetBranches()
	if err != nil {
		t.Fatalf("GetBranches() returned error: %v", err)
	}
	if vcsType == "" {
		t.Skip("No VCS detected, skipping branches test")
	}
	if len(branches) == 0 {
		t.Fatal("Expected at least one branch")
	}
	b := branches[0]
	if b.Name == "" {
		t.Error("First branch has empty name")
	}
}

func TestIsAlphaNum(t *testing.T) {
	tests := []struct {
		input byte
		want  bool
	}{
		{'a', true},
		{'z', true},
		{'A', true},
		{'Z', true},
		{'0', true},
		{'9', true},
		{' ', false},
		{'|', false},
	}
	for _, tc := range tests {
		if got := isAlphaNum(tc.input); got != tc.want {
			t.Errorf("isAlphaNum(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestFirstRune(t *testing.T) {
	r, size := firstRune("abc")
	if r != 'a' || size != 1 {
		t.Errorf("firstRune(\"abc\") = (%c, %d), want ('a', 1)", r, size)
	}
	r, size = firstRune("◆test")
	if r != '◆' || size != 3 {
		t.Errorf("firstRune(\"◆test\") = (%c, %d), want ('◆', 3)", r, size)
	}
	r, size = firstRune("")
	if r != 0 || size != 0 {
		t.Errorf("firstRune(\"\") = (%c, %d), want (0, 0)", r, size)
	}
}
