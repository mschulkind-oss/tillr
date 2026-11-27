package native

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const interruptedToolResultText = "Tool execution was interrupted before completion; inspect state before retrying."

// SessionHeader is the first JSONL record written to <artifactDir>/sessions/<session-id>.jsonl.
type SessionHeader struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	StartedAt string `json:"started_at"`
	Backend   string `json:"backend"`
	Model     string `json:"model"`
	Effort    string `json:"effort,omitempty"`
}

// SessionEntry is one persisted conversation message in the JSONL transcript.
type SessionEntry struct {
	Type      string  `json:"type"`
	Timestamp string  `json:"timestamp"`
	Message   Message `json:"message"`
}

// Session holds the canonical provider-neutral transcript on disk.
type Session struct {
	Path     string
	Header   SessionHeader
	Messages []Message
}

// ValidateUUID reports whether s is a canonical 36-character hex UUID.
func ValidateUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// TranscriptPath returns <artifactDir>/sessions/<sessionID>.jsonl, outside workDir.
func TranscriptPath(artifactDir, sessionID string) string {
	return filepath.Join(artifactDir, "sessions", sessionID+".jsonl")
}

// OpenNewSession creates a fresh canonical transcript for sessionID, failing
// closed when sessionID is missing or not a valid UUID.
func OpenNewSession(artifactDir string, hdr SessionHeader) (*Session, error) {
	if !ValidateUUID(hdr.SessionID) {
		return nil, fmt.Errorf("invalid session id %q: must be a UUID", hdr.SessionID)
	}
	path := TranscriptPath(artifactDir, hdr.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating sessions directory: %w", err)
	}
	hdr.Type = "session"
	if hdr.StartedAt == "" {
		hdr.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(hdr)
	if err != nil {
		return nil, fmt.Errorf("encoding session header: %w", err)
	}
	line = append(line, '\n')
	if err := os.WriteFile(path, line, 0o600); err != nil {
		return nil, fmt.Errorf("writing session transcript %s: %w", path, err)
	}
	return &Session{
		Path:   path,
		Header: hdr,
	}, nil
}

// LoadSession reads an existing canonical transcript from disk, failing closed
// when the file is missing, empty, or corrupt.
func LoadSession(artifactDir, sessionID string) (*Session, error) {
	if !ValidateUUID(sessionID) {
		return nil, fmt.Errorf("invalid session id %q: must be a UUID", sessionID)
	}
	path := TranscriptPath(artifactDir, sessionID)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening session transcript %s: %w", path, err)
	}
	defer f.Close()

	s := &Session{Path: path}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		lineNo++
		if lineNo == 1 {
			var hdr SessionHeader
			if err := json.Unmarshal([]byte(raw), &hdr); err != nil {
				return nil, fmt.Errorf("corrupt session header in %s: %w", path, err)
			}
			if hdr.Type != "session" || hdr.SessionID == "" {
				return nil, fmt.Errorf("corrupt session header in %s: missing session type or id", path)
			}
			if hdr.SessionID != sessionID {
				return nil, fmt.Errorf("session transcript %s belongs to %q, not %q", path, hdr.SessionID, sessionID)
			}
			s.Header = hdr
			continue
		}
		var entry SessionEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, fmt.Errorf("corrupt session entry on line %d of %s: %w", lineNo, path, err)
		}
		if entry.Type != "message" || entry.Message.Role == "" {
			return nil, fmt.Errorf("corrupt session entry on line %d of %s: invalid message record", lineNo, path)
		}
		s.Messages = append(s.Messages, entry.Message)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading session transcript %s: %w", path, err)
	}
	if lineNo == 0 {
		return nil, errors.New("session transcript is empty")
	}
	return s, nil
}

// AppendMessage appends one message to the in-memory history and flushes it to disk.
func (s *Session) AppendMessage(msg Message) error {
	now := time.Now().UTC()
	if msg.Timestamp == 0 {
		msg.Timestamp = now.UnixMilli()
	}
	entry := SessionEntry{
		Type:      "message",
		Timestamp: now.Format(time.RFC3339Nano),
		Message:   msg,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encoding session entry: %w", err)
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(s.Path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("appending to session transcript %s: %w", s.Path, err)
	}
	_, werr := f.Write(raw)
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("writing session transcript %s: %w", s.Path, werr)
	}
	if cerr != nil {
		return fmt.Errorf("closing session transcript %s: %w", s.Path, cerr)
	}
	s.Messages = append(s.Messages, msg)
	return nil
}

// RepairInterruptedTools checks whether the transcript ends with an assistant
// message containing unanswered tool_use blocks (from a crash or SIGKILL) and
// appends synthetic is_error tool_result blocks so strict provider alternation
// holds on resume.
func (s *Session) RepairInterruptedTools() (bool, error) {
	if len(s.Messages) == 0 {
		return false, nil
	}
	last := s.Messages[len(s.Messages)-1]
	if last.Role != RoleAssistant {
		return false, nil
	}
	var results []ContentBlock
	for _, blk := range last.Content {
		if blk.Type == BlockToolUse && blk.ID != "" {
			results = append(results, ContentBlock{
				Type:      BlockToolResult,
				ToolUseID: blk.ID,
				Content:   interruptedToolResultText,
				IsError:   true,
			})
		}
	}
	if len(results) == 0 {
		return false, nil
	}
	if err := s.AppendMessage(Message{
		Role:    RoleUser,
		Content: results,
	}); err != nil {
		return false, err
	}
	return true, nil
}
