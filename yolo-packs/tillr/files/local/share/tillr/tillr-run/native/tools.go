package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxReadLines = 250
	maxReadBytes = 32 * 1024
)

// Toolset executes the in-process file, search, and progress tools behind the
// lane fence and read-only seat policy (Slice 2, D-NAB3).
type Toolset struct {
	policy FencePolicy
}

// NewToolset returns a ToolExecutor enforcing policy on every file, search, and
// progress tool call.
func NewToolset(policy FencePolicy) *Toolset {
	return &Toolset{policy: policy}
}

// Definitions returns the JSON Schema tool definitions for Slice 2's six
// in-process tools.
func (t *Toolset) Definitions() []ToolDef {
	return []ToolDef{
		{
			Name:        "read_file",
			Description: "Read UTF-8 text from a file with optional 1-indexed offset and line limit (capped at 250 lines / 32 KiB).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":250}},"required":["path"],"additionalProperties":false}`),
		},
		{
			Name:        "list_files",
			Description: "List directory entries or glob matches under a directory (capped at 500 entries).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"glob":{"type":"string"}},"additionalProperties":false}`),
		},
		{
			Name:        "search_files",
			Description: "Search UTF-8 file contents by Go regular expression or literal string (capped at 200 matches).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string"},"literal":{"type":"boolean"},"case_insensitive":{"type":"boolean"}},"required":["pattern"],"additionalProperties":false}`),
		},
		{
			Name:        "write_file",
			Description: "Atomically write UTF-8 content to a file inside permitted worktree fence, artifacts, or scratch roots.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
		},
		{
			Name:        "edit_file",
			Description: "Apply exact, unique, non-overlapping text replacements to an existing UTF-8 file inside permitted roots.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"edits":{"type":"array","minItems":1,"items":{"type":"object","properties":{"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["old_text","new_text"],"additionalProperties":false}}},"required":["path","edits"],"additionalProperties":false}`),
		},
		{
			Name:        "report_progress",
			Description: "Append one or more verified criterion IDs (for example c1) to the run's progress file.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"criterion":{"type":"string"},"criteria":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`),
		},
	}
}

// Execute dispatches one tool call and returns (resultText, isError).
func (t *Toolset) Execute(ctx context.Context, call ContentBlock) (string, bool) {
	switch call.Name {
	case "read_file":
		var args readFileArgs
		if err := unmarshalArgs(call.Input, &args); err != nil {
			return fmt.Sprintf("read_file: %v", err), true
		}
		return t.execReadFile(args)
	case "list_files":
		var args listFilesArgs
		if err := unmarshalArgs(call.Input, &args); err != nil {
			return fmt.Sprintf("list_files: %v", err), true
		}
		return t.execListFiles(ctx, args)
	case "search_files":
		var args searchFilesArgs
		if err := unmarshalArgs(call.Input, &args); err != nil {
			return fmt.Sprintf("search_files: %v", err), true
		}
		return t.execSearchFiles(ctx, args)
	case "write_file":
		var args writeFileArgs
		if err := unmarshalArgs(call.Input, &args); err != nil {
			return fmt.Sprintf("write_file: %v", err), true
		}
		return t.execWriteFile(args)
	case "edit_file":
		var args editFileArgs
		if err := unmarshalArgs(call.Input, &args); err != nil {
			return fmt.Sprintf("edit_file: %v", err), true
		}
		return t.execEditFile(args)
	case "report_progress":
		var args reportProgressArgs
		if err := unmarshalArgs(call.Input, &args); err != nil {
			return fmt.Sprintf("report_progress: %v", err), true
		}
		return t.execReportProgress(args)
	default:
		return fmt.Sprintf("unknown tool %q", call.Name), true
	}
}

func unmarshalArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (t *Toolset) execReadFile(args readFileArgs) (string, bool) {
	resolved, err := t.policy.CheckReadTarget(args.Path)
	if err != nil {
		return err.Error(), true
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Sprintf("read_file: %v", err), true
	}
	if info.IsDir() {
		return fmt.Sprintf("read_file: %q is a directory (use list_files)", args.Path), true
	}

	raw, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("read_file: %v", err), true
	}
	sampleLen := len(raw)
	if sampleLen > binaryCheckBytes {
		sampleLen = binaryCheckBytes
	}
	if isBinarySample(raw[:sampleLen]) || !utf8.Valid(raw) {
		return fmt.Sprintf("read_file: refusing binary or non-UTF-8 file %q", args.Path), true
	}

	offset := args.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 || limit > maxReadLines {
		limit = maxReadLines
	}

	text := string(raw)
	if text == "" {
		return "", false
	}
	lines := strings.Split(text, "\n")
	// Avoid counting a trailing empty element caused by a final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines := len(lines)
	if offset > totalLines {
		return fmt.Sprintf("[offset %d is past end of file (%d lines)]", offset, totalLines), false
	}

	startIdx := offset - 1
	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	var b strings.Builder
	writtenLines := 0
	byteTruncated := false
	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		addLen := len(line)
		if writtenLines > 0 {
			addLen++ // newline separator
		}
		if b.Len()+addLen > maxReadBytes {
			if writtenLines == 0 {
				b.WriteString(truncateUTF8(line, maxReadBytes))
				writtenLines = 1
			}
			byteTruncated = true
			break
		}
		if writtenLines > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		writtenLines++
	}

	lastShown := startIdx + writtenLines
	if byteTruncated || lastShown < totalLines {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if byteTruncated {
			fmt.Fprintf(&b, "[truncated at %d bytes; showing lines %d-%d of %d]", maxReadBytes, offset, lastShown, totalLines)
		} else {
			fmt.Fprintf(&b, "[showing lines %d-%d of %d; use offset=%d to continue]", offset, lastShown, totalLines, lastShown+1)
		}
	}
	return b.String(), false
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *Toolset) execWriteFile(args writeFileArgs) (string, bool) {
	resolved, err := t.policy.CheckWriteTarget(args.Path)
	if err != nil {
		return err.Error(), true
	}
	if !utf8.ValidString(args.Content) {
		return "write_file: content must be valid UTF-8", true
	}
	mode := os.FileMode(0o644)
	if info, serr := os.Stat(resolved); serr == nil {
		if info.IsDir() {
			return fmt.Sprintf("write_file: %q is a directory", args.Path), true
		}
		mode = info.Mode().Perm()
	}
	if err := atomicWriteFile(resolved, []byte(args.Content), mode); err != nil {
		return fmt.Sprintf("write_file: %v", err), true
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path), false
}

type textEdit struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type editFileArgs struct {
	Path  string     `json:"path"`
	Edits []textEdit `json:"edits"`
}

func (t *Toolset) execEditFile(args editFileArgs) (string, bool) {
	resolved, err := t.policy.CheckWriteTarget(args.Path)
	if err != nil {
		return err.Error(), true
	}
	if len(args.Edits) == 0 {
		return "edit_file: edits must be non-empty", true
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Sprintf("edit_file: %v", err), true
	}
	if info.IsDir() {
		return fmt.Sprintf("edit_file: %q is a directory", args.Path), true
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("edit_file: %v", err), true
	}
	if !utf8.Valid(raw) {
		return fmt.Sprintf("edit_file: refusing non-UTF-8 file %q", args.Path), true
	}
	orig := string(raw)

	type span struct {
		start int
		end   int
		rep   string
		idx   int
	}
	spans := make([]span, 0, len(args.Edits))
	for i, e := range args.Edits {
		if e.OldText == "" {
			return fmt.Sprintf("edit_file: edit %d has empty old_text", i), true
		}
		if !utf8.ValidString(e.NewText) {
			return fmt.Sprintf("edit_file: edit %d has invalid UTF-8 new_text", i), true
		}
		count := strings.Count(orig, e.OldText)
		if count == 0 {
			return fmt.Sprintf("edit_file: edit %d old_text not found in %s", i, args.Path), true
		}
		if count > 1 {
			return fmt.Sprintf("edit_file: edit %d old_text matches %d locations in %s (must be unique)", i, count, args.Path), true
		}
		start := strings.Index(orig, e.OldText)
		spans = append(spans, span{
			start: start,
			end:   start + len(e.OldText),
			rep:   e.NewText,
			idx:   i,
		})
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].start < spans[j].start
	})
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return fmt.Sprintf("edit_file: edits %d and %d overlap", spans[i-1].idx, spans[i].idx), true
		}
	}

	var b strings.Builder
	cursor := 0
	for _, s := range spans {
		b.WriteString(orig[cursor:s.start])
		b.WriteString(s.rep)
		cursor = s.end
	}
	b.WriteString(orig[cursor:])

	if err := atomicWriteFile(resolved, []byte(b.String()), info.Mode().Perm()); err != nil {
		return fmt.Sprintf("edit_file: %v", err), true
	}
	return fmt.Sprintf("applied %d edit(s) to %s", len(args.Edits), args.Path), false
}

type reportProgressArgs struct {
	Criterion string   `json:"criterion"`
	Criteria  []string `json:"criteria"`
}

func (t *Toolset) execReportProgress(args reportProgressArgs) (string, bool) {
	if strings.TrimSpace(t.policy.ProgressFile) == "" {
		return "report_progress: TILLR_PROGRESS_FILE is not set", true
	}
	var ids []string
	if strings.TrimSpace(args.Criterion) != "" {
		ids = append(ids, strings.TrimSpace(args.Criterion))
	}
	for _, c := range args.Criteria {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	if len(ids) == 0 {
		return "report_progress: at least one criterion id is required", true
	}
	for _, id := range ids {
		if strings.ContainsAny(id, "\r\n") {
			return fmt.Sprintf("report_progress: criterion id %q must be a single line", id), true
		}
	}
	if err := os.MkdirAll(filepath.Dir(t.policy.ProgressFile), 0o755); err != nil {
		return fmt.Sprintf("report_progress: %v", err), true
	}
	f, err := os.OpenFile(t.policy.ProgressFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Sprintf("report_progress: %v", err), true
	}
	var buf strings.Builder
	for _, id := range ids {
		buf.WriteString(id)
		buf.WriteByte('\n')
	}
	_, werr := f.WriteString(buf.String())
	cerr := f.Close()
	if werr != nil {
		return fmt.Sprintf("report_progress: %v", werr), true
	}
	if cerr != nil {
		return fmt.Sprintf("report_progress: %v", cerr), true
	}
	return fmt.Sprintf("recorded progress: %s", strings.Join(ids, ", ")), false
}

// atomicWriteFile writes data to a temporary file in target's parent directory
// and renames it into place so partial writes never appear on disk.
func atomicWriteFile(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tillr-native-write-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}
