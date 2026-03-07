package cli

import (
	"fmt"
	"os"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/export"
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

	exportFeaturesCmd.Flags().String("format", "json", "Output format (json, md, csv)")
	exportRoadmapCmd.Flags().String("format", "json", "Output format (json, md, csv)")
	exportDecisionsCmd.Flags().String("format", "json", "Output format (json, md, csv)")
	exportAllCmd.Flags().String("format", "json", "Output format (json, md)")
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
