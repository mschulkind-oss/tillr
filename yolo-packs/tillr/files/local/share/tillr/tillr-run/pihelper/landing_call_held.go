package pihelper

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// LandingCallHeld reports whether the active pi transcript at path can replay
// the landing tool call with callID. It fails closed on any I/O, UTF-8, JSON,
// type, cycle, missing parent, duplicate id, or compaction error.
func LandingCallHeld(path, callID string) bool {
	if callID == "" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(raw) {
		return false
	}
	lines := splitLines(string(raw))
	if len(lines) == 0 {
		return false
	}
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var obj any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return false
		}
		m, ok := obj.(map[string]any)
		if !ok {
			return false
		}
		entries = append(entries, m)
	}
	if typ, _ := entries[0]["type"].(string); typ != "session" {
		return false
	}

	byID := make(map[string]map[string]any, len(entries)-1)
	var leaf any
	for _, entry := range entries[1:] {
		id, ok := entry["id"].(string)
		if !ok || id == "" {
			return false
		}
		if _, dup := byID[id]; dup {
			return false
		}
		byID[id] = entry
		leaf = id
	}

	var lastMessage map[string]any
	saved := false
	visited := make(map[string]struct{})
	for leaf != nil {
		leafID, ok := leaf.(string)
		if !ok {
			return false
		}
		entry, exists := byID[leafID]
		if !exists {
			return false
		}
		if _, seen := visited[leafID]; seen {
			return false
		}
		visited[leafID] = struct{}{}

		typ, _ := entry["type"].(string)
		if typ == "compaction" {
			return false
		}
		if typ == "message" {
			msg, ok := entry["message"].(map[string]any)
			if !ok {
				return false
			}
			if lastMessage == nil {
				lastMessage = msg
			}
			role, _ := msg["role"].(string)
			if role == "toolResult" {
				if tcID, ok := msg["toolCallId"].(string); ok && tcID == callID {
					saved = true
				}
			}
			if role == "assistant" {
				content, ok := msg["content"].([]any)
				if !ok || !hasToolCall(content, callID) {
					return false
				}
				if lastRole, _ := lastMessage["role"].(string); lastRole == "toolResult" {
					tcID, ok := lastMessage["toolCallId"].(string)
					return ok && tcID == callID
				}
				return !saved
			}
		}

		parentVal, hasParent := entry["parentId"]
		if !hasParent || parentVal == nil {
			leaf = nil
		} else {
			leaf = parentVal
		}
	}
	return false
}

func hasToolCall(content []any, callID string) bool {
	for _, part := range content {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "toolCall" {
			continue
		}
		if id, _ := m["id"].(string); id == callID {
			return true
		}
	}
	return false
}

// splitLines mimics Python str.splitlines() for UTF-8 text.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			lines = append(lines, s[start:i])
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		} else if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// RunLandingCallHeld implements the pi-landing-call-held face.
// It exits 0 on -h/--help or when len(args)==2 and LandingCallHeld(args[0], args[1]) is true, else 1.
func RunLandingCallHeld(args []string, stdout io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "usage: tillr-run pi-landing-call-held <transcript> <call_id>")
		return 0
	}
	if len(args) == 2 && LandingCallHeld(args[0], args[1]) {
		return 0
	}
	return 1
}
