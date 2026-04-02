package cli

import (
	"os"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/export"
	"github.com/spf13/cobra"
)

func init() {
	exportCmd.AddCommand(exportReportCmd)
	exportReportCmd.Flags().String("format", "md", "Output format (md, html)")
}

var exportReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate a project status report",
	Long: `Generate a comprehensive project status report including:
  - Project summary with completion stats
  - Milestone progress bars
  - Feature list grouped by status
  - Roadmap overview

Formats:
  md    Clean Markdown (default)
  html  Styled HTML with inline CSS, suitable for printing to PDF

Examples:
  tillr export report                    # Markdown to stdout
  tillr export report --format html      # HTML to stdout
  tillr export report --format html > report.html  # Save as HTML file`,
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

		milestones, err := db.ListMilestones(database, p.ID)
		if err != nil {
			return err
		}

		features, err := db.ListFeatures(database, p.ID, "", "")
		if err != nil {
			return err
		}

		roadmap, err := db.ListRoadmapItems(database, p.ID)
		if err != nil {
			return err
		}

		data := export.ReportData{
			ProjectName: p.Name,
			Milestones:  milestones,
			Features:    features,
			Roadmap:     roadmap,
		}

		format, _ := cmd.Flags().GetString("format")

		if jsonOutput {
			return export.ReportJSON(data, os.Stdout)
		}

		return export.Report(data, os.Stdout, format)
	},
}
