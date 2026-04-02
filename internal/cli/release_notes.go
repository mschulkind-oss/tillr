package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

var releaseNotesCmd = &cobra.Command{
	Use:   "release-notes",
	Short: "Generate release notes from completed features",
	Long: `Generate release notes from tillr project data.

Collects features with status "done" and formats them into beautiful
release notes grouped by milestone. Supports markdown and JSON output.

EXAMPLES
  tillr release-notes --version v0.2.0
  tillr release-notes --milestone v1.0-production --version v1.0.0
  tillr release-notes --since 2025-01-01 --format markdown
  tillr release-notes --version v0.2.0 -o RELEASE_NOTES.md`,
	RunE: runReleaseNotes,
}

func init() {
	releaseNotesCmd.Flags().String("since", "", "Only include features updated after this date (YYYY-MM-DD)")
	releaseNotesCmd.Flags().String("milestone", "", "Filter to a specific milestone ID")
	releaseNotesCmd.Flags().String("format", "markdown", "Output format: markdown, json")
	releaseNotesCmd.Flags().String("version", "", "Version label for the release (e.g. v0.2.0)")
	releaseNotesCmd.Flags().StringP("output", "o", "", "Write output to file instead of stdout")
}

type releaseNotesData struct {
	Version     string              `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	Project     string              `json:"project"`
	Milestone   *models.Milestone   `json:"milestone,omitempty"`
	Groups      []releaseNotesGroup `json:"groups"`
	Stats       releaseNotesStats   `json:"stats"`
}

type releaseNotesGroup struct {
	MilestoneID   string           `json:"milestone_id"`
	MilestoneName string           `json:"milestone_name"`
	Features      []models.Feature `json:"features"`
}

type releaseNotesStats struct {
	TotalFeatures int `json:"total_features"`
}

func runReleaseNotes(cmd *cobra.Command, _ []string) error {
	database, _, err := openDB()
	if err != nil {
		return err
	}
	defer database.Close() //nolint:errcheck

	p, err := db.GetProject(database)
	if err != nil {
		return fmt.Errorf("getting project: %w", err)
	}

	since, _ := cmd.Flags().GetString("since")
	milestoneID, _ := cmd.Flags().GetString("milestone")
	format, _ := cmd.Flags().GetString("format")
	version, _ := cmd.Flags().GetString("version")
	outputFile, _ := cmd.Flags().GetString("output")

	// Validate --since date format if provided
	if since != "" {
		if _, err := time.Parse("2006-01-02", since); err != nil {
			return fmt.Errorf("invalid --since date %q: expected YYYY-MM-DD format", since)
		}
	}

	// Validate format
	if format != "markdown" && format != "json" {
		return fmt.Errorf("unsupported format %q: use markdown or json", format)
	}

	// Query done features, optionally filtered by milestone
	features, err := db.ListFeatures(database, p.ID, "done", milestoneID)
	if err != nil {
		return fmt.Errorf("listing features: %w", err)
	}

	// Apply --since filter on updated_at
	if since != "" {
		features = filterFeaturesSince(features, since)
	}

	// Get milestone details if requested
	var milestone *models.Milestone
	if milestoneID != "" {
		milestone, err = db.GetMilestone(database, milestoneID)
		if err != nil {
			return fmt.Errorf("milestone %q not found: %w", milestoneID, err)
		}
	}

	// Group features by milestone
	groups := groupByMilestone(features)

	data := releaseNotesData{
		Version:     version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Project:     p.Name,
		Milestone:   milestone,
		Groups:      groups,
		Stats: releaseNotesStats{
			TotalFeatures: len(features),
		},
	}

	// Build output string
	var output string
	if jsonOutput || format == "json" {
		return writeOutput(outputFile, "", data, true)
	}
	output = renderMarkdown(data)

	return writeOutput(outputFile, output, nil, false)
}

func filterFeaturesSince(features []models.Feature, since string) []models.Feature {
	var filtered []models.Feature
	for _, f := range features {
		if f.UpdatedAt >= since {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

func groupByMilestone(features []models.Feature) []releaseNotesGroup {
	order := []string{}
	groups := map[string]*releaseNotesGroup{}

	for _, f := range features {
		key := f.MilestoneID
		name := f.MilestoneName
		if key == "" {
			key = "_uncategorized"
			name = "Uncategorized"
		}
		g, ok := groups[key]
		if !ok {
			g = &releaseNotesGroup{
				MilestoneID:   f.MilestoneID,
				MilestoneName: name,
			}
			groups[key] = g
			order = append(order, key)
		}
		g.Features = append(g.Features, f)
	}

	var result []releaseNotesGroup
	for _, key := range order {
		result = append(result, *groups[key])
	}
	return result
}

func renderMarkdown(data releaseNotesData) string {
	var sb strings.Builder

	// Title
	if data.Version != "" {
		fmt.Fprintf(&sb, "# Release Notes — %s\n\n", data.Version)
	} else if data.Milestone != nil {
		fmt.Fprintf(&sb, "# Release Notes — %s\n\n", data.Milestone.Name)
	} else {
		sb.WriteString("# Release Notes\n\n")
	}

	// Features section
	sb.WriteString("## 🎯 Features\n\n")

	if len(data.Groups) == 0 {
		sb.WriteString("_No completed features found._\n\n")
	} else {
		for _, g := range data.Groups {
			fmt.Fprintf(&sb, "### %s\n\n", g.MilestoneName)
			for _, f := range g.Features {
				if f.Description != "" {
					fmt.Fprintf(&sb, "- **%s** — %s\n", f.Name, f.Description)
				} else {
					fmt.Fprintf(&sb, "- **%s**\n", f.Name)
				}
			}
			sb.WriteString("\n")
		}
	}

	// Stats section
	sb.WriteString("## 📊 Stats\n\n")
	fmt.Fprintf(&sb, "- %d features shipped\n", data.Stats.TotalFeatures)

	// Footer
	fmt.Fprintf(&sb, "\n---\n_Generated by Tillr on %s_\n", time.Now().UTC().Format("2006-01-02"))

	return sb.String()
}

func writeOutput(outputFile, text string, jsonData any, isJSON bool) error {
	if isJSON {
		if outputFile != "" {
			f, err := os.Create(outputFile)
			if err != nil {
				return fmt.Errorf("creating output file: %w", err)
			}
			defer f.Close() //nolint:errcheck
			return writeJSON(f, jsonData)
		}
		return printJSON(jsonData)
	}

	if outputFile != "" {
		if err := os.WriteFile(outputFile, []byte(text), 0o644); err != nil {
			return fmt.Errorf("writing output file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Release notes written to %s\n", outputFile)
		return nil
	}

	fmt.Print(text)
	return nil
}

func writeJSON(f *os.File, v any) error {
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
