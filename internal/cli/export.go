package cli

import (
	"fmt"
	"os"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/export"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export project data in various formats",
	Long:  "Export features, roadmap, decisions, or all data as JSON, Markdown, or CSV.",
}

func init() {
	exportCmd.AddCommand(exportFeaturesCmd)
	exportCmd.AddCommand(exportRoadmapCmd)
	exportCmd.AddCommand(exportDecisionsCmd)
	exportCmd.AddCommand(exportAllCmd)
	exportCmd.AddCommand(exportDiagramCmd)

	exportFeaturesCmd.Flags().String("format", "json", "Output format (json, md, csv)")
	exportRoadmapCmd.Flags().String("format", "json", "Output format (json, md, csv)")
	exportDecisionsCmd.Flags().String("format", "json", "Output format (json, md, csv)")
	exportAllCmd.Flags().String("format", "json", "Output format (json, md)")

	exportDiagramCmd.Flags().String("format", "mermaid", "Output format (mermaid, dot)")
	exportDiagramCmd.Flags().StringP("output", "o", "", "Write to file instead of stdout")
	exportDiagramCmd.Flags().String("milestone", "", "Limit to a specific milestone ID")
	exportDiagramCmd.Flags().String("status", "", "Filter features by status")
}

var exportFeaturesCmd = &cobra.Command{
	Use:   "features",
	Short: "Export features",
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

		features, err := db.ListFeatures(database, p.ID, "", "")
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		return export.Features(features, os.Stdout, format)
	},
}

var exportRoadmapCmd = &cobra.Command{
	Use:   "roadmap",
	Short: "Export roadmap",
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

		items, err := db.ListRoadmapItems(database, p.ID)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		return export.Roadmap(items, os.Stdout, format)
	},
}

var exportDecisionsCmd = &cobra.Command{
	Use:   "decisions",
	Short: "Export decisions",
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		decisions, err := db.ListDecisions(database, "")
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		return export.Decisions(decisions, os.Stdout, format)
	},
}

var exportAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Export all project data",
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

		format, _ := cmd.Flags().GetString("format")
		if format == "csv" {
			return fmt.Errorf("CSV format not supported for 'all' export; use json or md")
		}

		features, _ := db.ListFeatures(database, p.ID, "", "")
		items, _ := db.ListRoadmapItems(database, p.ID)
		decisions, _ := db.ListDecisions(database, "")

		return export.All(p.Name, features, items, decisions, os.Stdout, format)
	},
}

var exportDiagramCmd = &cobra.Command{
	Use:   "diagram",
	Short: "Export architecture diagram as Mermaid or Graphviz DOT",
	Long: `Generate a dependency diagram of project features.

Features are shown as nodes colored by status, with edges representing
dependencies. Milestones are rendered as subgraphs grouping their features.

Output can be pasted into GitHub Markdown (Mermaid) or rendered with Graphviz (DOT).

Examples:
  tillr export diagram                      # Mermaid to stdout
  tillr export diagram --format dot          # Graphviz DOT to stdout
  tillr export diagram -o arch.md            # Write to file
  tillr export diagram --milestone v1        # Only features in milestone v1
  tillr export diagram --status implementing # Only implementing features`,
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

		milestoneFilter, _ := cmd.Flags().GetString("milestone")
		statusFilter, _ := cmd.Flags().GetString("status")
		format, _ := cmd.Flags().GetString("format")
		output, _ := cmd.Flags().GetString("output")

		features, err := db.ListFeatures(database, p.ID, statusFilter, milestoneFilter)
		if err != nil {
			return fmt.Errorf("listing features: %w", err)
		}

		milestones, err := db.ListMilestones(database, p.ID)
		if err != nil {
			return fmt.Errorf("listing milestones: %w", err)
		}

		w := os.Stdout
		if output != "" {
			f, err := os.Create(output)
			if err != nil {
				return fmt.Errorf("creating output file: %w", err)
			}
			defer f.Close() //nolint:errcheck
			w = f
		}

		return export.Diagram(features, milestones, w, format)
	},
}
