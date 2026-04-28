// Package persona implements the conductor + persona pattern's
// storage layer: persona discovery (from .claude/agents/), context
// file reads/appends/compaction (under swarf/agents/<name>/), retro
// listing (under swarf/retros/), and conductor-state file access
// (swarf/conductor.md).
//
// All file paths are project-relative and resolved against the tillr
// project root (where .tillr.json lives).
package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultCompactThreshold = 20_000 // words
	contextFilename         = "context.md"
)

// Persona is a discovered agent role.
type Persona struct {
	Name           string `json:"name"`
	DefinitionPath string `json:"definition_path"` // relative path to .claude/agents/<name>.md
	ContextPath    string `json:"context_path"`    // relative path to swarf/agents/<name>/context.md
	ContextWords   int    `json:"context_words"`
	ContextBytes   int64  `json:"context_bytes"`
	UpdatedAt      string `json:"updated_at,omitempty"` // RFC3339; "" if file does not exist
}

// Retro is a discovered retro file.
type Retro struct {
	Name      string `json:"name"` // basename without extension
	Path      string `json:"path"` // relative path under swarf/retros/
	Bytes     int64  `json:"bytes"`
	UpdatedAt string `json:"updated_at"` // RFC3339
}

// List returns all personas discovered in projectRoot/.claude/agents/.
// Each .md file (excluding README.md) becomes a persona.
func List(projectRoot string) ([]Persona, error) {
	agentsDir := filepath.Join(projectRoot, ".claude", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Persona{}, nil
		}
		return nil, err
	}
	var out []Persona
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		base := strings.TrimSuffix(name, ".md")
		if base == "README" || base == "readme" || strings.HasPrefix(base, ".") {
			continue
		}
		p, err := Get(projectRoot, base)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get loads a single persona's metadata. Returns an error if the
// .claude/agents/<name>.md definition does not exist; the context
// file's absence is fine (zero-state metadata returned).
func Get(projectRoot, name string) (*Persona, error) {
	defPath := filepath.Join(".claude", "agents", name+".md")
	if _, err := os.Stat(filepath.Join(projectRoot, defPath)); err != nil {
		return nil, fmt.Errorf("persona %q has no definition at %s", name, defPath)
	}
	ctxPath := filepath.Join("swarf", "agents", name, contextFilename)
	p := &Persona{
		Name:           name,
		DefinitionPath: defPath,
		ContextPath:    ctxPath,
	}
	if info, err := os.Stat(filepath.Join(projectRoot, ctxPath)); err == nil {
		p.ContextBytes = info.Size()
		p.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
		// Word count is approximate: split on whitespace.
		if data, err := os.ReadFile(filepath.Join(projectRoot, ctxPath)); err == nil {
			p.ContextWords = len(strings.Fields(string(data)))
		}
	}
	return p, nil
}

// ContextRead returns the persona's context file contents. Returns
// "" (no error) if the file does not exist yet.
func ContextRead(projectRoot, name string) (string, error) {
	if _, err := Get(projectRoot, name); err != nil {
		return "", err
	}
	path := filepath.Join(projectRoot, "swarf", "agents", name, contextFilename)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Append adds a timestamped block to the persona's context file. The
// directory is created if needed. Format:
//
//	## YYYY-MM-DDTHH:MMZ — <one-line summary>
//
//	<body>
//
// If summary is empty, only the timestamp is used.
func Append(projectRoot, name, summary, body string) error {
	if _, err := Get(projectRoot, name); err != nil {
		return err
	}
	dir := filepath.Join(projectRoot, "swarf", "agents", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, contextFilename)

	header := "## " + time.Now().UTC().Format("2006-01-02T15:04Z")
	if strings.TrimSpace(summary) != "" {
		header += " — " + strings.TrimSpace(summary)
	}
	entry := "\n" + header + "\n\n" + strings.TrimRight(body, "\n") + "\n"

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_, err = f.WriteString(entry)
	return err
}

// CompactResult reports the outcome of a compact run.
type CompactResult struct {
	BackupPath  string `json:"backup_path"`
	WordsBefore int    `json:"words_before"`
	WordsAfter  int    `json:"words_after"`
	BlocksKept  int    `json:"blocks_kept"`
	BlocksMoved int    `json:"blocks_moved"`
}

// Compact archives older blocks of a persona's context file to a
// backup, keeping the most recent N blocks verbatim. Blocks are split
// on lines starting with "## " (markdown H2). The backup is written
// at swarf/agents/<name>/context.md.pre-compact-<RFC3339>.
//
// keep is the number of recent blocks to retain. Default 20 if 0.
func Compact(projectRoot, name string, keep int) (*CompactResult, error) {
	if keep <= 0 {
		keep = 20
	}
	if _, err := Get(projectRoot, name); err != nil {
		return nil, err
	}
	path := filepath.Join(projectRoot, "swarf", "agents", name, contextFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading context: %w", err)
	}
	wordsBefore := len(strings.Fields(string(data)))
	blocks := splitBlocks(string(data))

	if len(blocks) <= keep {
		return &CompactResult{
			BlocksKept:  len(blocks),
			WordsBefore: wordsBefore,
			WordsAfter:  wordsBefore,
		}, nil
	}

	moved := blocks[:len(blocks)-keep]
	kept := blocks[len(blocks)-keep:]

	backupName := fmt.Sprintf("%s.pre-compact-%s",
		contextFilename, time.Now().UTC().Format("2006-01-02T15-04-05Z"))
	backupPath := filepath.Join(projectRoot, "swarf", "agents", name, backupName)
	if err := os.WriteFile(backupPath, []byte(strings.Join(moved, "")), 0o644); err != nil {
		return nil, fmt.Errorf("writing backup: %w", err)
	}

	stub := fmt.Sprintf(
		"<!-- compacted on %s; %d earlier block(s) archived to %s -->\n\n",
		time.Now().UTC().Format(time.RFC3339), len(moved), backupName,
	)
	newBody := stub + strings.Join(kept, "")
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
		return nil, fmt.Errorf("writing compacted file: %w", err)
	}

	return &CompactResult{
		BackupPath:  filepath.Join("swarf", "agents", name, backupName),
		BlocksKept:  len(kept),
		BlocksMoved: len(moved),
		WordsBefore: wordsBefore,
		WordsAfter:  len(strings.Fields(newBody)),
	}, nil
}

// CompactThreshold returns the configured threshold (in words) at
// which a compact is recommended. Defaults to 20_000 if no override.
// Per-persona override mechanism is open question Q42.
func CompactThreshold() int { return defaultCompactThreshold }

// splitBlocks divides a markdown document into "## "-delimited blocks.
// Each returned block starts with "## ". A leading non-"## " preamble
// (whitespace, comments, etc.) is dropped because it is not itself a
// content block — for compaction purposes only blocks with a header
// are countable.
func splitBlocks(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	var blocks []string
	var current strings.Builder
	inBlock := false
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			if inBlock {
				blocks = append(blocks, current.String())
				current.Reset()
			}
			inBlock = true
		}
		if inBlock {
			current.WriteString(l)
		}
	}
	if current.Len() > 0 {
		blocks = append(blocks, current.String())
	}
	return blocks
}

// ConductorRead returns swarf/conductor.md contents; "" if not present.
func ConductorRead(projectRoot string) (string, error) {
	path := filepath.Join(projectRoot, "swarf", "conductor.md")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ConductorAppend appends a timestamped block to swarf/conductor.md.
func ConductorAppend(projectRoot, summary, body string) error {
	dir := filepath.Join(projectRoot, "swarf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "conductor.md")
	header := "## " + time.Now().UTC().Format("2006-01-02T15:04Z")
	if strings.TrimSpace(summary) != "" {
		header += " — " + strings.TrimSpace(summary)
	}
	entry := "\n" + header + "\n\n" + strings.TrimRight(body, "\n") + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_, err = f.WriteString(entry)
	return err
}

// ListRetros returns retro files under swarf/retros/, newest first.
func ListRetros(projectRoot string) ([]Retro, error) {
	dir := filepath.Join(projectRoot, "swarf", "retros")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Retro{}, nil
		}
		return nil, err
	}
	var out []Retro
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Retro{
			Name:      strings.TrimSuffix(name, ".md"),
			Path:      filepath.Join("swarf", "retros", name),
			Bytes:     info.Size(),
			UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// ReadRetro returns the contents of a retro file by name (without .md).
func ReadRetro(projectRoot, name string) (string, error) {
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid retro name %q", name)
	}
	path := filepath.Join(projectRoot, "swarf", "retros", name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteRetro creates a retro file at swarf/retros/<name>.md.
// Returns the path (relative to project root).
func WriteRetro(projectRoot, name, body string) (string, error) {
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid retro name %q", name)
	}
	dir := filepath.Join(projectRoot, "swarf", "retros")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	rel := filepath.Join("swarf", "retros", name+".md")
	if err := os.WriteFile(filepath.Join(projectRoot, rel), []byte(body), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}
