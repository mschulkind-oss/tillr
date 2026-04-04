package cli

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	exportCmd.AddCommand(exportGitCmd)
	importCmd.AddCommand(importGitCmd)

	importGitCmd.Flags().Bool("dry-run", false, "Show what would be imported without writing to the database")
}

var exportGitCmd = &cobra.Command{
	Use:   "git",
	Short: "Export project data to git-trackable files",
	Long: `Export core entities to .tillr/export/ as human-readable, git-trackable files.

Features, milestones, roadmap items, and workstreams are written as individual
markdown files with YAML frontmatter. Lightweight tables (feature_deps,
workstream_links, workstream_notes) are written as YAML list files.

The export is idempotent — running twice produces identical output.
FTS indexes are NOT exported; they are rebuilt on import.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		return runExportGit(database, p)
	},
}

var importGitCmd = &cobra.Command{
	Use:   "git",
	Short: "Import project data from git-trackable export files",
	Long: `Rebuild the database from files previously exported with 'tillr export git'.

This is intended for disaster recovery. It reads markdown and YAML files
from .tillr/export/ and inserts them into the database.

Use --dry-run to preview what would be imported without writing.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		return runImportGit(database, p, dryRun)
	},
}

// ---------------------------------------------------------------------------
// Export
// ---------------------------------------------------------------------------

func runExportGit(database *sql.DB, p *models.Project) error {
	root, err := findExportRoot()
	if err != nil {
		return err
	}

	// Create directory structure
	dirs := []string{"features", "milestones", "roadmap", "workstreams"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}

	// Export features
	features, err := db.ListFeatures(database, p.ID, "", "")
	if err != nil {
		return fmt.Errorf("listing features: %w", err)
	}
	// ListFeatures loads deps but not tags; load tags per feature
	for i := range features {
		tags, _ := db.GetFeatureTags(database, features[i].ID)
		features[i].Tags = tags
	}
	for _, f := range features {
		if err := writeFeatureFile(root, &f); err != nil {
			return fmt.Errorf("writing feature %s: %w", f.ID, err)
		}
	}
	// Clean up removed features
	if err := cleanRemovedFiles(filepath.Join(root, "features"), features, func(f models.Feature) string { return f.ID }); err != nil {
		return fmt.Errorf("cleaning features: %w", err)
	}

	// Export milestones
	milestones, err := db.ListMilestones(database, p.ID)
	if err != nil {
		return fmt.Errorf("listing milestones: %w", err)
	}
	for _, m := range milestones {
		if err := writeMilestoneFile(root, &m); err != nil {
			return fmt.Errorf("writing milestone %s: %w", m.ID, err)
		}
	}
	if err := cleanRemovedFiles(filepath.Join(root, "milestones"), milestones, func(m models.Milestone) string { return m.ID }); err != nil {
		return fmt.Errorf("cleaning milestones: %w", err)
	}

	// Export roadmap items
	roadmap, err := db.ListRoadmapItems(database, p.ID)
	if err != nil {
		return fmt.Errorf("listing roadmap items: %w", err)
	}
	for _, r := range roadmap {
		if err := writeRoadmapFile(root, &r); err != nil {
			return fmt.Errorf("writing roadmap item %s: %w", r.ID, err)
		}
	}
	if err := cleanRemovedFiles(filepath.Join(root, "roadmap"), roadmap, func(r models.RoadmapItem) string { return r.ID }); err != nil {
		return fmt.Errorf("cleaning roadmap: %w", err)
	}

	// Export workstreams
	workstreams, err := db.ListWorkstreams(database, p.ID, "")
	if err != nil {
		return fmt.Errorf("listing workstreams: %w", err)
	}
	for _, w := range workstreams {
		if err := writeWorkstreamFile(root, &w); err != nil {
			return fmt.Errorf("writing workstream %s: %w", w.ID, err)
		}
	}
	if err := cleanRemovedFiles(filepath.Join(root, "workstreams"), workstreams, func(w models.Workstream) string { return w.ID }); err != nil {
		return fmt.Errorf("cleaning workstreams: %w", err)
	}

	// Export lightweight tables as YAML
	if err := exportFeatureDeps(database, root); err != nil {
		return fmt.Errorf("exporting feature_deps: %w", err)
	}
	if err := exportWorkstreamLinks(database, root, workstreams); err != nil {
		return fmt.Errorf("exporting workstream_links: %w", err)
	}
	if err := exportWorkstreamNotes(database, root, workstreams); err != nil {
		return fmt.Errorf("exporting workstream_notes: %w", err)
	}

	fmt.Printf("Exported to %s\n", root)
	fmt.Printf("  %d features, %d milestones, %d roadmap items, %d workstreams\n",
		len(features), len(milestones), len(roadmap), len(workstreams))
	return nil
}

func findExportRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".tillr", "export"), nil
}

// cleanRemovedFiles removes .md files from dir that don't correspond to any entity.
func cleanRemovedFiles[T any](dir string, entities []T, idFunc func(T) string) error {
	ids := make(map[string]bool, len(entities))
	for _, e := range entities {
		ids[idFunc(e)+".md"] = true
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if !ids[entry.Name()] {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Feature export
// ---------------------------------------------------------------------------

type featureFrontmatter struct {
	ID             string   `yaml:"id"`
	Status         string   `yaml:"status"`
	Priority       int      `yaml:"priority"`
	MilestoneID    string   `yaml:"milestone_id,omitempty"`
	RoadmapItemID  string   `yaml:"roadmap_item_id,omitempty"`
	AssignedCycle  string   `yaml:"assigned_cycle,omitempty"`
	Tags           []string `yaml:"tags,omitempty"`
	DependsOn      []string `yaml:"depends_on,omitempty"`
	EstimatePoints int      `yaml:"estimate_points,omitempty"`
	EstimateSize   string   `yaml:"estimate_size,omitempty"`
	CreatedAt      string   `yaml:"created_at"`
	UpdatedAt      string   `yaml:"updated_at"`
}

func writeFeatureFile(root string, f *models.Feature) error {
	fm := featureFrontmatter{
		ID:             f.ID,
		Status:         f.Status,
		Priority:       f.Priority,
		MilestoneID:    f.MilestoneID,
		RoadmapItemID:  f.RoadmapItemID,
		AssignedCycle:  f.AssignedCycle,
		Tags:           f.Tags,
		DependsOn:      f.DependsOn,
		EstimatePoints: f.EstimatePoints,
		EstimateSize:   f.EstimateSize,
		CreatedAt:      f.CreatedAt,
		UpdatedAt:      f.UpdatedAt,
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return err
	}
	buf.WriteString("---\n")
	buf.WriteString("# " + f.Name + "\n")

	if f.Description != "" {
		buf.WriteString("\n" + f.Description + "\n")
	}

	if f.Spec != "" {
		buf.WriteString("\n## Spec\n\n" + f.Spec + "\n")
	}

	return os.WriteFile(filepath.Join(root, "features", f.ID+".md"), []byte(buf.String()), 0o644)
}

// ---------------------------------------------------------------------------
// Milestone export
// ---------------------------------------------------------------------------

type milestoneFrontmatter struct {
	ID        string `yaml:"id"`
	Status    string `yaml:"status"`
	SortOrder int    `yaml:"sort_order"`
	CreatedAt string `yaml:"created_at"`
	UpdatedAt string `yaml:"updated_at"`
}

func writeMilestoneFile(root string, m *models.Milestone) error {
	fm := milestoneFrontmatter{
		ID:        m.ID,
		Status:    m.Status,
		SortOrder: m.SortOrder,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return err
	}
	buf.WriteString("---\n")
	buf.WriteString("# " + m.Name + "\n")

	if m.Description != "" {
		buf.WriteString("\n" + m.Description + "\n")
	}

	return os.WriteFile(filepath.Join(root, "milestones", m.ID+".md"), []byte(buf.String()), 0o644)
}

// ---------------------------------------------------------------------------
// Roadmap export
// ---------------------------------------------------------------------------

type roadmapFrontmatter struct {
	ID        string `yaml:"id"`
	Category  string `yaml:"category,omitempty"`
	Priority  string `yaml:"priority"`
	Status    string `yaml:"status"`
	Effort    string `yaml:"effort,omitempty"`
	SortOrder int    `yaml:"sort_order"`
	CreatedAt string `yaml:"created_at"`
	UpdatedAt string `yaml:"updated_at"`
}

func writeRoadmapFile(root string, r *models.RoadmapItem) error {
	fm := roadmapFrontmatter{
		ID:        r.ID,
		Category:  r.Category,
		Priority:  r.Priority,
		Status:    r.Status,
		Effort:    r.Effort,
		SortOrder: r.SortOrder,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return err
	}
	buf.WriteString("---\n")
	buf.WriteString("# " + r.Title + "\n")

	if r.Description != "" {
		buf.WriteString("\n" + r.Description + "\n")
	}

	return os.WriteFile(filepath.Join(root, "roadmap", r.ID+".md"), []byte(buf.String()), 0o644)
}

// ---------------------------------------------------------------------------
// Workstream export
// ---------------------------------------------------------------------------

type workstreamFrontmatter struct {
	ID        string `yaml:"id"`
	ParentID  string `yaml:"parent_id,omitempty"`
	Status    string `yaml:"status"`
	Tags      string `yaml:"tags,omitempty"`
	SortOrder int    `yaml:"sort_order"`
	CreatedAt string `yaml:"created_at"`
	UpdatedAt string `yaml:"updated_at"`
}

func writeWorkstreamFile(root string, w *models.Workstream) error {
	fm := workstreamFrontmatter{
		ID:        w.ID,
		ParentID:  w.ParentID,
		Status:    w.Status,
		Tags:      w.Tags,
		SortOrder: w.SortOrder,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return err
	}
	buf.WriteString("---\n")
	buf.WriteString("# " + w.Name + "\n")

	if w.Description != "" {
		buf.WriteString("\n" + w.Description + "\n")
	}

	return os.WriteFile(filepath.Join(root, "workstreams", w.ID+".md"), []byte(buf.String()), 0o644)
}

// ---------------------------------------------------------------------------
// Lightweight table exports (YAML)
// ---------------------------------------------------------------------------

type featureDepEntry struct {
	FeatureID string `yaml:"feature_id"`
	DependsOn string `yaml:"depends_on"`
}

func exportFeatureDeps(database *sql.DB, root string) error {
	rows, err := database.Query("SELECT feature_id, depends_on FROM feature_deps ORDER BY feature_id, depends_on")
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck

	var deps []featureDepEntry
	for rows.Next() {
		var d featureDepEntry
		if err := rows.Scan(&d.FeatureID, &d.DependsOn); err != nil {
			return err
		}
		deps = append(deps, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	return writeYAMLFile(filepath.Join(root, "feature_deps.yaml"), deps)
}

type workstreamLinkEntry struct {
	WorkstreamID string `yaml:"workstream_id"`
	LinkType     string `yaml:"link_type"`
	TargetID     string `yaml:"target_id,omitempty"`
	TargetURL    string `yaml:"target_url,omitempty"`
	Label        string `yaml:"label,omitempty"`
	CreatedAt    string `yaml:"created_at"`
}

func exportWorkstreamLinks(database *sql.DB, root string, workstreams []models.Workstream) error {
	var all []workstreamLinkEntry
	for _, w := range workstreams {
		links, err := db.ListWorkstreamLinks(database, w.ID)
		if err != nil {
			return err
		}
		for _, l := range links {
			all = append(all, workstreamLinkEntry{
				WorkstreamID: l.WorkstreamID,
				LinkType:     l.LinkType,
				TargetID:     l.TargetID,
				TargetURL:    l.TargetURL,
				Label:        l.Label,
				CreatedAt:    l.CreatedAt,
			})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].WorkstreamID != all[j].WorkstreamID {
			return all[i].WorkstreamID < all[j].WorkstreamID
		}
		if all[i].LinkType != all[j].LinkType {
			return all[i].LinkType < all[j].LinkType
		}
		if all[i].TargetID != all[j].TargetID {
			return all[i].TargetID < all[j].TargetID
		}
		if all[i].TargetURL != all[j].TargetURL {
			return all[i].TargetURL < all[j].TargetURL
		}
		return all[i].CreatedAt < all[j].CreatedAt
	})
	return writeYAMLFile(filepath.Join(root, "workstream_links.yaml"), all)
}

type workstreamNoteEntry struct {
	WorkstreamID string `yaml:"workstream_id"`
	Content      string `yaml:"content"`
	NoteType     string `yaml:"note_type"`
	Source       string `yaml:"source,omitempty"`
	Resolved     int    `yaml:"resolved"`
	CreatedAt    string `yaml:"created_at"`
}

func exportWorkstreamNotes(database *sql.DB, root string, workstreams []models.Workstream) error {
	var all []workstreamNoteEntry
	for _, w := range workstreams {
		notes, err := db.ListWorkstreamNotes(database, w.ID)
		if err != nil {
			return err
		}
		for _, n := range notes {
			all = append(all, workstreamNoteEntry{
				WorkstreamID: n.WorkstreamID,
				Content:      n.Content,
				NoteType:     n.NoteType,
				Source:       n.Source,
				Resolved:     n.Resolved,
				CreatedAt:    n.CreatedAt,
			})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].WorkstreamID != all[j].WorkstreamID {
			return all[i].WorkstreamID < all[j].WorkstreamID
		}
		if all[i].CreatedAt != all[j].CreatedAt {
			return all[i].CreatedAt < all[j].CreatedAt
		}
		return all[i].Content < all[j].Content
	})
	return writeYAMLFile(filepath.Join(root, "workstream_notes.yaml"), all)
}

func writeYAMLFile(path string, data any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(data); err != nil {
		return err
	}
	return enc.Close()
}

// ---------------------------------------------------------------------------
// Import
// ---------------------------------------------------------------------------

func runImportGit(database *sql.DB, p *models.Project, dryRun bool) error {
	root, err := findExportRoot()
	if err != nil {
		return err
	}

	if _, err := os.Stat(root); os.IsNotExist(err) {
		return fmt.Errorf("export directory not found: %s\nRun 'tillr export git' first", root)
	}

	// Import milestones first (features reference them)
	milestones, err := importMilestoneFiles(filepath.Join(root, "milestones"))
	if err != nil {
		return fmt.Errorf("reading milestones: %w", err)
	}

	// Import roadmap items
	roadmapItems, err := importRoadmapFiles(filepath.Join(root, "roadmap"))
	if err != nil {
		return fmt.Errorf("reading roadmap items: %w", err)
	}

	// Import features
	features, err := importFeatureFiles(filepath.Join(root, "features"))
	if err != nil {
		return fmt.Errorf("reading features: %w", err)
	}

	// Import workstreams
	workstreams, err := importWorkstreamFiles(filepath.Join(root, "workstreams"))
	if err != nil {
		return fmt.Errorf("reading workstreams: %w", err)
	}

	// Read lightweight tables
	var deps []featureDepEntry
	if data, err := os.ReadFile(filepath.Join(root, "feature_deps.yaml")); err == nil {
		if err := yaml.Unmarshal(data, &deps); err != nil {
			return fmt.Errorf("parsing feature_deps.yaml: %w", err)
		}
	}

	var wsLinks []workstreamLinkEntry
	if data, err := os.ReadFile(filepath.Join(root, "workstream_links.yaml")); err == nil {
		if err := yaml.Unmarshal(data, &wsLinks); err != nil {
			return fmt.Errorf("parsing workstream_links.yaml: %w", err)
		}
	}

	var wsNotes []workstreamNoteEntry
	if data, err := os.ReadFile(filepath.Join(root, "workstream_notes.yaml")); err == nil {
		if err := yaml.Unmarshal(data, &wsNotes); err != nil {
			return fmt.Errorf("parsing workstream_notes.yaml: %w", err)
		}
	}

	if dryRun {
		fmt.Println("Dry run — no changes will be made.")
		fmt.Printf("  %d milestones\n", len(milestones))
		fmt.Printf("  %d roadmap items\n", len(roadmapItems))
		fmt.Printf("  %d features\n", len(features))
		fmt.Printf("  %d workstreams\n", len(workstreams))
		fmt.Printf("  %d feature deps\n", len(deps))
		fmt.Printf("  %d workstream links\n", len(wsLinks))
		fmt.Printf("  %d workstream notes\n", len(wsNotes))
		return nil
	}

	// Write to database using INSERT OR REPLACE for idempotency
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, m := range milestones {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO milestones (id, project_id, name, description, sort_order, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.Milestone.ID, p.ID, m.Milestone.Name, m.Milestone.Description, m.Milestone.SortOrder, m.Milestone.Status, m.Meta.CreatedAt, m.Meta.UpdatedAt,
		); err != nil {
			return fmt.Errorf("inserting milestone %s: %w", m.Milestone.ID, err)
		}
	}

	for _, r := range roadmapItems {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO roadmap_items (id, project_id, title, description, category, priority, status, effort, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Item.ID, p.ID, r.Item.Title, r.Item.Description, r.Item.Category, r.Item.Priority, r.Item.Status, r.Item.Effort, r.Item.SortOrder, r.Meta.CreatedAt, r.Meta.UpdatedAt,
		); err != nil {
			return fmt.Errorf("inserting roadmap item %s: %w", r.Item.ID, err)
		}
	}

	for _, f := range features {
		milestoneID := f.Meta.MilestoneID
		if milestoneID == "" {
			milestoneID = "" // explicit null
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO features (id, project_id, milestone_id, name, description, spec, status, priority, assigned_cycle, roadmap_item_id, estimate_points, estimate_size, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.Meta.ID, p.ID, nullStrGit(milestoneID), f.Feature.Name, f.Feature.Description, f.Feature.Spec, f.Meta.Status, f.Meta.Priority, f.Meta.AssignedCycle, f.Meta.RoadmapItemID, f.Meta.EstimatePoints, f.Meta.EstimateSize, f.Meta.CreatedAt, f.Meta.UpdatedAt,
		); err != nil {
			return fmt.Errorf("inserting feature %s: %w", f.Meta.ID, err)
		}

		// Tags
		if _, err := tx.Exec("DELETE FROM feature_tags WHERE feature_id = ?", f.Meta.ID); err != nil {
			return fmt.Errorf("clearing tags for %s: %w", f.Meta.ID, err)
		}
		for _, tag := range f.Meta.Tags {
			if _, err := tx.Exec("INSERT OR IGNORE INTO feature_tags (feature_id, tag) VALUES (?, ?)", f.Meta.ID, tag); err != nil {
				return fmt.Errorf("inserting tag %s for %s: %w", tag, f.Meta.ID, err)
			}
		}
	}

	for _, w := range workstreams {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO workstreams (id, project_id, parent_id, name, description, status, tags, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			w.Meta.ID, p.ID, nullStrGit(w.Meta.ParentID), w.Workstream.Name, w.Workstream.Description, w.Meta.Status, w.Meta.Tags, w.Meta.SortOrder, w.Meta.CreatedAt, w.Meta.UpdatedAt,
		); err != nil {
			return fmt.Errorf("inserting workstream %s: %w", w.Meta.ID, err)
		}
	}

	// Feature deps: clear all and re-insert
	if _, err := tx.Exec("DELETE FROM feature_deps"); err != nil {
		return fmt.Errorf("clearing feature_deps: %w", err)
	}
	for _, d := range deps {
		if _, err := tx.Exec("INSERT OR IGNORE INTO feature_deps (feature_id, depends_on) VALUES (?, ?)", d.FeatureID, d.DependsOn); err != nil {
			return fmt.Errorf("inserting feature dep %s -> %s: %w", d.FeatureID, d.DependsOn, err)
		}
	}

	// Workstream links: clear and re-insert
	if _, err := tx.Exec("DELETE FROM workstream_links"); err != nil {
		return fmt.Errorf("clearing workstream_links: %w", err)
	}
	for _, l := range wsLinks {
		if _, err := tx.Exec(
			"INSERT INTO workstream_links (workstream_id, link_type, target_id, target_url, label, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			l.WorkstreamID, l.LinkType, l.TargetID, l.TargetURL, l.Label, l.CreatedAt,
		); err != nil {
			return fmt.Errorf("inserting workstream link: %w", err)
		}
	}

	// Workstream notes: clear and re-insert
	if _, err := tx.Exec("DELETE FROM workstream_notes"); err != nil {
		return fmt.Errorf("clearing workstream_notes: %w", err)
	}
	for _, n := range wsNotes {
		if _, err := tx.Exec(
			"INSERT INTO workstream_notes (workstream_id, content, note_type, source, resolved, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			n.WorkstreamID, n.Content, n.NoteType, n.Source, n.Resolved, n.CreatedAt,
		); err != nil {
			return fmt.Errorf("inserting workstream note: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	fmt.Println("Import complete.")
	fmt.Printf("  %d milestones, %d roadmap items, %d features, %d workstreams\n",
		len(milestones), len(roadmapItems), len(features), len(workstreams))
	return nil
}

// ---------------------------------------------------------------------------
// Markdown parsing helpers
// ---------------------------------------------------------------------------

// parseFrontmatter reads a markdown file, splits YAML frontmatter from body.
func parseFrontmatter(path string) (frontmatter []byte, title string, body string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", "", err
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	var (
		inFM    bool
		fmLines []string
		rest    []string
		pastFM  bool
	)

	for scanner.Scan() {
		line := scanner.Text()
		if !inFM && !pastFM && strings.TrimSpace(line) == "---" {
			inFM = true
			continue
		}
		if inFM && strings.TrimSpace(line) == "---" {
			inFM = false
			pastFM = true
			continue
		}
		if inFM {
			fmLines = append(fmLines, line)
		} else if pastFM {
			rest = append(rest, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", "", err
	}

	fm := []byte(strings.Join(fmLines, "\n") + "\n")

	// Extract title from first "# Title" line
	bodyLines := rest
	for i, line := range bodyLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			title = strings.TrimPrefix(trimmed, "# ")
			bodyLines = append(bodyLines[:i], bodyLines[i+1:]...)
			break
		}
	}

	// Trim leading/trailing blank lines from body
	bodyStr := strings.TrimSpace(strings.Join(bodyLines, "\n"))

	return fm, title, bodyStr, nil
}

// ---------------------------------------------------------------------------
// Import file parsers
// ---------------------------------------------------------------------------

type importedFeature struct {
	Meta    featureFrontmatter
	Feature struct {
		Name        string
		Description string
		Spec        string
	}
}

func importFeatureFiles(dir string) ([]importedFeature, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []importedFeature
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		fm, title, body, err := parseFrontmatter(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}

		var meta featureFrontmatter
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return nil, fmt.Errorf("parsing frontmatter in %s: %w", entry.Name(), err)
		}

		f := importedFeature{Meta: meta}
		f.Feature.Name = title

		// Split body into description and spec
		if idx := strings.Index(body, "## Spec\n"); idx >= 0 {
			f.Feature.Description = strings.TrimSpace(body[:idx])
			f.Feature.Spec = strings.TrimSpace(body[idx+len("## Spec\n"):])
		} else if idx := strings.Index(body, "## Spec\r\n"); idx >= 0 {
			f.Feature.Description = strings.TrimSpace(body[:idx])
			f.Feature.Spec = strings.TrimSpace(body[idx+len("## Spec\r\n"):])
		} else {
			f.Feature.Description = body
		}

		out = append(out, f)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Meta.ID < out[j].Meta.ID })
	return out, nil
}

type importedMilestone struct {
	Meta      milestoneFrontmatter
	Milestone struct {
		ID          string
		Name        string
		Description string
		SortOrder   int
		Status      string
	}
}

func importMilestoneFiles(dir string) ([]importedMilestone, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []importedMilestone
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		fm, title, body, err := parseFrontmatter(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}

		var meta milestoneFrontmatter
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return nil, fmt.Errorf("parsing frontmatter in %s: %w", entry.Name(), err)
		}

		m := importedMilestone{Meta: meta}
		m.Milestone.ID = meta.ID
		m.Milestone.Name = title
		m.Milestone.Description = body
		m.Milestone.SortOrder = meta.SortOrder
		m.Milestone.Status = meta.Status

		out = append(out, m)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Meta.ID < out[j].Meta.ID })
	return out, nil
}

type importedRoadmap struct {
	Meta roadmapFrontmatter
	Item struct {
		ID          string
		Title       string
		Description string
		Category    string
		Priority    string
		Status      string
		Effort      string
		SortOrder   int
	}
}

func importRoadmapFiles(dir string) ([]importedRoadmap, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []importedRoadmap
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		fm, title, body, err := parseFrontmatter(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}

		var meta roadmapFrontmatter
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return nil, fmt.Errorf("parsing frontmatter in %s: %w", entry.Name(), err)
		}

		r := importedRoadmap{Meta: meta}
		r.Item.ID = meta.ID
		r.Item.Title = title
		r.Item.Description = body
		r.Item.Category = meta.Category
		r.Item.Priority = meta.Priority
		r.Item.Status = meta.Status
		r.Item.Effort = meta.Effort
		r.Item.SortOrder = meta.SortOrder

		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Meta.ID < out[j].Meta.ID })
	return out, nil
}

type importedWorkstream struct {
	Meta       workstreamFrontmatter
	Workstream struct {
		Name        string
		Description string
	}
}

func importWorkstreamFiles(dir string) ([]importedWorkstream, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []importedWorkstream
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		fm, title, body, err := parseFrontmatter(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}

		var meta workstreamFrontmatter
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return nil, fmt.Errorf("parsing frontmatter in %s: %w", entry.Name(), err)
		}

		w := importedWorkstream{Meta: meta}
		w.Workstream.Name = title
		w.Workstream.Description = body

		out = append(out, w)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Meta.ID < out[j].Meta.ID })
	return out, nil
}

// nullStrGit converts an empty string to nil for nullable columns.
func nullStrGit(s string) any {
	if s == "" {
		return nil
	}
	return s
}
