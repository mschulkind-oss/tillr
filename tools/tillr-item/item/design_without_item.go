package item

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/pelletier/go-toml/v2"
)

// designWithoutItems uses the same tracked documents and block parser as sync.
// One warning per document keeps the waiting list stable when several roadmaps link it;
// Sorted roadmap paths and then line order select the displayed source link.
func designWithoutItems(docs []SyncDocument, held map[string]storedState, stages map[string]string) []SyncNote {
	byPath := map[string]SyncDocument{}
	var roadmaps []string
	covered := map[string]bool{}
	for _, d := range docs {
		byPath[d.Path] = d
		if strings.HasPrefix(d.Path, "docs/") && path.Base(d.Path) == "roadmap.md" {
			roadmaps = append(roadmaps, d.Path)
		}
		blocks, _ := parseItemBlocks(d.Path, d.Content)
		if len(blocks) > 0 {
			covered[d.Path] = true
		}
	}
	for _, st := range held {
		if st.state != "landed" && st.designDoc != "" {
			target, _, _ := strings.Cut(st.designDoc, "#")
			covered[path.Clean(target)] = true
		}
	}
	sort.Strings(roadmaps)
	var notes []SyncNote
	for _, roadmap := range roadmaps {
		fence := fenceState{}
		heading := ""
		for i, line := range splitLines(byPath[roadmap].Content) {
			if fence.step(line) {
				continue
			}
			if m := headingLine.FindStringSubmatch(line); m != nil {
				heading = m[2]
			}
			for _, m := range linkTarget.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if strings.Contains(target, ":") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") {
					continue
				}
				target, _, _ = strings.Cut(target, "#")
				target = path.Clean(path.Join(path.Dir(roadmap), target))
				if !strings.HasPrefix(target, "docs/design/") || covered[target] {
					continue
				}
				d, ok := byPath[target]
				if !ok {
					continue
				}
				stage := designStage(d.Content)
				if role, ok := stages[stage]; ok {
					stage = role
				}
				if stage == "built" || stage == "done" {
					continue
				}
				covered[target] = true
				notes = append(notes, SyncNote{File: target, Roadmap: roadmap, Heading: heading, Line: i + 1, Rule: "design-without-item", Message: fmt.Sprintf("%s:%d links this design, which has no item block or non-landed item; owner must file its item block", roadmap, i+1)})
			}
		}
	}
	return notes
}

// designStageRoles resolves a repository's optional Vantage stage vocabulary.
// Without that file, the literal built/done roles still mean completed work.
// A present but unreadable or invalid configuration refuses sync, not its warnings.
func designStageRoles(root string) (map[string]string, error) {
	if root == "" {
		return nil, nil
	}
	file := filepath.Join(root, ".vantage.toml")
	content, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, Externalf("reading %s: %v", file, err)
	}
	var config struct {
		Planning struct {
			Stages map[string]string `toml:"stages"`
		} `toml:"planning"`
	}
	if err := toml.Unmarshal(content, &config); err != nil {
		return nil, Externalf("reading %s: %v", file, err)
	}
	stages := map[string]string{}
	for label, role := range config.Planning.Stages {
		key := strings.ToLower(label)
		if _, exists := stages[key]; exists {
			return nil, Externalf("reading %s: duplicate stage label ignoring case: %s", file, label)
		}
		stages[key] = strings.ToLower(role)
	}
	return stages, nil
}

// designStage reads the repository's top-level scalar frontmatter convention,
// not stage-like text in the body or a nested metadata block.
func designStage(content []byte) string {
	lines := splitLines(content)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return ""
	}
	for _, line := range lines[1:end] {
		if !strings.HasPrefix(line, "stage:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "stage:"))
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		return strings.ToLower(value)
	}
	return ""
}

// replaceDesignWithoutItems runs in the caller's sync transaction. Insert or update keeps
// first_seen while replacement removes findings that no longer hold.
func replaceDesignWithoutItems(tx *store.Tx, warnings []SyncNote, at int64) error {
	keep := map[string]bool{}
	for _, n := range warnings {
		if n.Rule != "design-without-item" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO design_without_item (document,roadmap,roadmap_line,first_seen) VALUES (?,?,?,?) ON CONFLICT(document) DO UPDATE SET roadmap=excluded.roadmap,roadmap_line=excluded.roadmap_line`, n.File, n.Roadmap, n.Line, at); err != nil {
			return Internalf("storing design-without-item: %v", err)
		}
		keep[n.File] = true
	}
	rows, err := tx.Reader().Query(`SELECT document FROM design_without_item`)
	if err != nil {
		return Internalf("reading design-without-item: %v", err)
	}
	var removed []string
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			rows.Close()
			return Internalf("reading design-without-item: %v", err)
		}
		if !keep[document] {
			removed = append(removed, document)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Internalf("reading design-without-item: %v", err)
	}
	for _, document := range removed {
		if _, err := tx.Exec(`DELETE FROM design_without_item WHERE document=?`, document); err != nil {
			return Internalf("removing design-without-item: %v", err)
		}
	}
	return nil
}
