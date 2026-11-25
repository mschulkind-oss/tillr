package main

import (
	"path/filepath"
	"testing"
)

func TestParseCanaryRunLog(t *testing.T) {
	parsed := parseRunLog(filepath.Join("testdata", "canary-run.log"), &TraceContext{
		RunID: "run-canary-1",
	}, false)
	if parsed.State != "ok" {
		t.Fatalf("state = %q (%s), want ok", parsed.State, parsed.Detail)
	}
	if parsed.Summary.Turns != 7 {
		t.Fatalf("turns = %d, want 7", parsed.Summary.Turns)
	}
	if parsed.Summary.TokensIn != 36196 {
		t.Fatalf("tokens_in = %d, want 36196", parsed.Summary.TokensIn)
	}
	if parsed.Summary.TokensOut != 1917 {
		t.Fatalf("tokens_out = %d, want 1917", parsed.Summary.TokensOut)
	}
	if parsed.Summary.CacheRead != 216192 {
		t.Fatalf("cache_read = %d, want 216192", parsed.Summary.CacheRead)
	}
	if parsed.Summary.Reasoning != 438 {
		t.Fatalf("reasoning = %d, want 438", parsed.Summary.Reasoning)
	}
	if parsed.Summary.ToolCalls != 6 {
		t.Fatalf("tool_calls = %d, want 6", parsed.Summary.ToolCalls)
	}
}

func TestModuleOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/w/lane-1/worktree/tools/tillr-store/read.go", "tools/tillr-store"},
		{"/w/lane-1/worktree/yolo-packs/tillr/SKILL.md", "yolo-packs/tillr"},
		{"/w/lane-1/worktree/tests/pack-sync.test.py", "tests"},
		{"/w/lane-1/worktree/AGENTS.md", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := moduleOf(tc.in); got != tc.want {
			t.Errorf("moduleOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
