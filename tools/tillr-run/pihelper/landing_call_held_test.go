package pihelper

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lchEntry(id string, parent any, message map[string]any) map[string]any {
	return map[string]any{
		"type":     "message",
		"id":       id,
		"parentId": parent,
		"message":  message,
	}
}

func lchJSONL(t *testing.T, records []map[string]any) string {
	t.Helper()
	var lines []string
	session, err := json.Marshal(map[string]any{"type": "session", "id": "s"})
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, string(session))
	for _, rec := range records {
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestLandingCallHeldTranscripts(t *testing.T) {
	call := lchEntry("a", nil, map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "toolCall", "id": "call"}},
	})
	result := lchEntry("b", "a", map[string]any{
		"role":       "toolResult",
		"toolCallId": "call",
	})
	other := lchEntry("c", "b", map[string]any{
		"role":       "toolResult",
		"toolCallId": "other",
	})
	onlyOther := lchEntry("c", "a", map[string]any{
		"role":       "toolResult",
		"toolCallId": "other",
	})
	compaction := map[string]any{
		"type":     "compaction",
		"id":       "c",
		"parentId": "a",
	}

	cases := []struct {
		name     string
		records  []map[string]any
		raw      *string
		expected int
	}{
		{name: "active call", records: []map[string]any{call}, expected: 0},
		{name: "call+result", records: []map[string]any{call, result}, expected: 0},
		{name: "call+result+other", records: []map[string]any{call, result, other}, expected: 1},
		{name: "call+only_other", records: []map[string]any{call, onlyOther}, expected: 1},
		{name: "call+compaction", records: []map[string]any{call, compaction}, expected: 1},
		{name: "non-JSON", raw: ptrString("not json"), expected: 1},
		{name: "missing file", expected: 1},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(path)
			if tc.records != nil {
				if err := os.WriteFile(path, []byte(lchJSONL(t, tc.records)), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if tc.raw != nil {
				if err := os.WriteFile(path, []byte(*tc.raw), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := RunLandingCallHeld([]string{path, "call"}, io.Discard)
			if got != tc.expected {
				t.Fatalf("RunLandingCallHeld() = %d, want %d", got, tc.expected)
			}
		})
	}
}

func TestLandingCallHeldBranchAndInvalidInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	call := lchEntry("a", nil, map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "toolCall", "id": "call"}},
	})
	other := lchEntry("b", nil, map[string]any{
		"role":    "assistant",
		"content": []any{},
	})

	t.Run("sibling branch", func(t *testing.T) {
		if err := os.WriteFile(path, []byte(lchJSONL(t, []map[string]any{call, other})), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := RunLandingCallHeld([]string{path, "call"}, io.Discard); got != 1 {
			t.Fatalf("RunLandingCallHeld() = %d, want 1", got)
		}
	})

	t.Run("missing parentId", func(t *testing.T) {
		missingParent := map[string]any{"type": "message", "id": "b", "parentId": "missing"}
		if err := os.WriteFile(path, []byte(lchJSONL(t, []map[string]any{call, missingParent})), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := RunLandingCallHeld([]string{path, "call"}, io.Discard); got != 1 {
			t.Fatalf("RunLandingCallHeld() = %d, want 1", got)
		}
	})

	t.Run("trailing broken JSON line", func(t *testing.T) {
		content := lchJSONL(t, []map[string]any{call}) + "{broken\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := RunLandingCallHeld([]string{path, "call"}, io.Discard); got != 1 {
			t.Fatalf("RunLandingCallHeld() = %d, want 1", got)
		}
	})
}

func ptrString(s string) *string { return &s }

func TestLandingCallHeldHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var out bytes.Buffer
		if got := RunLandingCallHeld([]string{flag}, &out); got != 0 {
			t.Fatalf("RunLandingCallHeld(%q) = %d, want 0", flag, got)
		}
		if !strings.Contains(out.String(), "pi-landing-call-held") {
			t.Fatalf("help output %q does not name pi-landing-call-held", out.String())
		}
	}
}
