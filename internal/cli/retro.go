package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/persona"
	"github.com/spf13/cobra"
)

var retroCmd = &cobra.Command{
	Use:   "retro",
	Short: "Generate a retrospective from the most recent Claude session transcript",
	Long: `Analyze the most recent Claude Code session transcript for friction
signals (tool errors, retries, course corrections) and write a markdown
retrospective to swarf/retros/<timestamp>.md.

By default reads the project's transcript directory at:
  ~/.claude/projects/<repo-slug>/transcripts/  (or similar)

Override with --transcript <path> to point at a specific transcript file.

The output is a *scaffold* — statistical signals plus headings for
qualitative analysis. The conductor (or human) fills in the
recommendations.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, cfg, err := openDB()
		if err != nil {
			return err
		}

		path, _ := cmd.Flags().GetString("transcript")
		if path == "" {
			path, err = findRecentTranscript(cfg.ProjectDir)
			if err != nil {
				return fmt.Errorf("locating transcript: %w (use --transcript <path> to override)", err)
			}
		}

		analysis, err := analyzeTranscript(path)
		if err != nil {
			return fmt.Errorf("analyzing %s: %w", path, err)
		}

		now := time.Now().UTC()
		retroName := now.Format("2006-01-02T15-04-05Z")
		body := renderRetro(retroName, path, analysis)

		rel, err := persona.WriteRetro(cfg.ProjectDir, retroName, body)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"path":     rel,
				"analysis": analysis,
			})
		}
		fmt.Printf("Wrote retro to %s\n", rel)
		fmt.Printf("  Source transcript: %s\n", path)
		fmt.Printf("  Tool uses:    %d\n", analysis.ToolUses)
		fmt.Printf("  Tool errors:  %d\n", analysis.ToolErrors)
		fmt.Printf("  User msgs:    %d\n", analysis.UserMessages)
		fmt.Printf("  Assistant:    %d\n", analysis.AssistantMessages)
		fmt.Println()
		fmt.Println("Open the retro and fill in the qualitative sections, or invoke")
		fmt.Println("the conductor to do the analysis.")
		return nil
	},
}

func init() {
	retroCmd.Flags().StringP("transcript", "t", "",
		"Path to a Claude session transcript (default: most recent in ~/.claude/projects/...)")
	rootCmd.AddCommand(retroCmd)
}

// Analysis is the structured statistics extracted from a transcript.
type Analysis struct {
	TranscriptPath    string   `json:"transcript_path"`
	ToolUses          int      `json:"tool_uses"`
	ToolErrors        int      `json:"tool_errors"`
	UserMessages      int      `json:"user_messages"`
	AssistantMessages int      `json:"assistant_messages"`
	StartedAt         string   `json:"started_at,omitempty"`
	EndedAt           string   `json:"ended_at,omitempty"`
	Friction          []string `json:"friction_samples"`
}

// analyzeTranscript reads a JSONL transcript (one JSON object per line)
// and counts the statistics relevant to a retro. It tolerates unknown
// fields and unknown line shapes — the goal is to surface signals,
// not to validate every transcript schema.
func analyzeTranscript(path string) (*Analysis, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	a := &Analysis{TranscriptPath: path}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<24) // up to 16MB lines

	frictionLimit := 8
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		// Track timestamp range
		if ts := stringField(msg, "timestamp"); ts != "" {
			if a.StartedAt == "" {
				a.StartedAt = ts
			}
			a.EndedAt = ts
		}

		// Categorize by message type/role
		switch stringField(msg, "type") {
		case "user":
			a.UserMessages++
		case "assistant":
			a.AssistantMessages++
			scanForToolUses(msg, a)
		}

		// Friction samples — short snippets of user "let me try again"
		// / "that didn't work" / "actually" etc.
		if a.UserMessages > 0 && stringField(msg, "type") == "user" {
			content := stringField(msg, "message_content_text")
			if content == "" {
				if c := nestedString(msg, "message", "content"); c != "" {
					content = c
				}
			}
			if isFrictionSample(content) && len(a.Friction) < frictionLimit {
				if len(content) > 200 {
					content = content[:200] + "…"
				}
				a.Friction = append(a.Friction, strings.TrimSpace(content))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return a, nil
}

func scanForToolUses(msg map[string]any, a *Analysis) {
	// Best-effort scan — Claude Code transcripts use varied shapes.
	// Look for common indicators of tool_use blocks and tool_result errors.
	walkAny(msg, func(k string, v any) {
		switch k {
		case "type":
			if s, ok := v.(string); ok {
				if s == "tool_use" {
					a.ToolUses++
				}
				if s == "tool_result" {
					if isErr := nestedBool(msg, "is_error"); isErr {
						a.ToolErrors++
					}
				}
			}
		}
	})
}

func walkAny(v any, visit func(k string, v any)) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			visit(k, val)
			walkAny(val, visit)
		}
	case []any:
		for _, e := range t {
			walkAny(e, visit)
		}
	}
}

func stringField(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func nestedString(m map[string]any, path ...string) string {
	cur := any(m)
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

func nestedBool(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func isFrictionSample(text string) bool {
	if text == "" {
		return false
	}
	low := strings.ToLower(text)
	signals := []string{
		"let me try", "let's try", "actually", "wait", "no, that's wrong",
		"that's not", "didn't work", "doesn't work", "broke", "regression",
	}
	for _, s := range signals {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// findRecentTranscript walks ~/.claude/projects/*/<repo>/ and returns
// the most recently modified .jsonl file. Falls back to scanning by
// project name if a perfect match isn't found.
func findRecentTranscript(projectRoot string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(root); err != nil {
		return "", fmt.Errorf("no transcripts directory at %s", root)
	}
	repoSlug := filepath.Base(projectRoot)

	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if !strings.Contains(p, repoSlug) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		cands = append(cands, cand{p, info.ModTime()})
		return nil
	})
	if len(cands) == 0 {
		return "", fmt.Errorf("no transcripts found for project %s under %s", repoSlug, root)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	return cands[0].path, nil
}

func renderRetro(name, transcript string, a *Analysis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Retrospective — %s\n\n", name)
	fmt.Fprintf(&b, "Source transcript: `%s`\n\n", transcript)
	if a.StartedAt != "" {
		fmt.Fprintf(&b, "Window: %s → %s\n\n", a.StartedAt, a.EndedAt)
	}

	b.WriteString("## Statistics\n\n")
	fmt.Fprintf(&b, "- User messages: %d\n", a.UserMessages)
	fmt.Fprintf(&b, "- Assistant messages: %d\n", a.AssistantMessages)
	fmt.Fprintf(&b, "- Tool uses: %d\n", a.ToolUses)
	fmt.Fprintf(&b, "- Tool errors: %d\n\n", a.ToolErrors)

	if len(a.Friction) > 0 {
		b.WriteString("## Friction signals\n\n")
		b.WriteString("Sampled messages that look like course corrections:\n\n")
		for _, s := range a.Friction {
			fmt.Fprintf(&b, "- %q\n", s)
		}
		b.WriteString("\n")
	}

	b.WriteString("## What worked\n\n")
	b.WriteString("_(fill in)_\n\n")
	b.WriteString("## What didn't\n\n")
	b.WriteString("_(fill in)_\n\n")
	b.WriteString("## Recommendations\n\n")
	b.WriteString("Each recommendation should be actionable. Targets:\n")
	b.WriteString("- A persona's prompt (`.claude/agents/<name>.md`)\n")
	b.WriteString("- A persona's context file (`swarf/agents/<name>/context.md`)\n")
	b.WriteString("- A tillr config setting (`tillr config set ...`)\n")
	b.WriteString("- A new feature (`tillr feature add --persona ... \"...\"`)\n\n")
	b.WriteString("1. _(fill in)_\n")

	return b.String()
}
