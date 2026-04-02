package cli

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/export"
	"github.com/spf13/cobra"
)

func init() {
	exportCmd.AddCommand(exportPDFCmd)
	exportCmd.AddCommand(exportPNGCmd)

	exportPDFCmd.Flags().StringP("output", "o", "", "Output file path (default: stdout)")
	exportPNGCmd.Flags().StringP("output", "o", "", "Output file path (required)")
}

var exportPDFCmd = &cobra.Command{
	Use:   "pdf <type>",
	Short: "Export project data as a printable HTML document (PDF-ready)",
	Long: `Generate a styled HTML document suitable for printing to PDF.

Supported export types:
  roadmap    - Project roadmap with priorities and status
  features   - Feature list grouped by status with specs
  stats      - Project statistics and completion metrics

The output is self-contained HTML with inline CSS, optimized for
print-to-PDF via any browser. Open the file and use Ctrl+P / Cmd+P.

Examples:
  tillr export pdf roadmap                      # HTML to stdout
  tillr export pdf features -o features.html    # Save to file
  tillr export pdf stats -o stats.html          # Stats report`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"roadmap", "features", "stats"},
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		output, _ := cmd.Flags().GetString("output")
		w := os.Stdout
		if output != "" {
			f, err := os.Create(output)
			if err != nil {
				return fmt.Errorf("creating output file: %w", err)
			}
			defer f.Close() //nolint:errcheck
			w = f
		}

		switch args[0] {
		case "roadmap":
			items, err := db.ListRoadmapItems(database, p.ID)
			if err != nil {
				return fmt.Errorf("listing roadmap items: %w", err)
			}
			milestones, _ := db.ListMilestones(database, p.ID)
			features, _ := db.ListFeatures(database, p.ID, "", "")
			data := export.ReportData{
				ProjectName: p.Name,
				Milestones:  milestones,
				Features:    features,
				Roadmap:     items,
			}
			if err := export.ReportHTML(data, w); err != nil {
				return err
			}

		case "features":
			features, err := db.ListFeatures(database, p.ID, "", "")
			if err != nil {
				return fmt.Errorf("listing features: %w", err)
			}
			milestones, _ := db.ListMilestones(database, p.ID)
			data := export.ReportData{
				ProjectName: p.Name,
				Milestones:  milestones,
				Features:    features,
			}
			if err := export.ReportHTML(data, w); err != nil {
				return err
			}

		case "stats":
			features, _ := db.ListFeatures(database, p.ID, "", "")
			milestones, _ := db.ListMilestones(database, p.ID)
			roadmap, _ := db.ListRoadmapItems(database, p.ID)
			data := export.ReportData{
				ProjectName: p.Name,
				Milestones:  milestones,
				Features:    features,
				Roadmap:     roadmap,
			}
			if err := export.ReportHTML(data, w); err != nil {
				return err
			}

		default:
			return fmt.Errorf("unknown export type: %s (use: roadmap, features, stats)", args[0])
		}

		if output != "" {
			fmt.Fprintf(os.Stderr, "Wrote PDF-ready HTML to %s\n", output)
			fmt.Fprintf(os.Stderr, "Open in browser and print to PDF (Ctrl+P / Cmd+P)\n")
		}
		return nil
	},
}

var exportPNGCmd = &cobra.Command{
	Use:   "png <type>",
	Short: "Export project charts as SVG (viewable as image)",
	Long: `Generate SVG chart images for project data.

Supported chart types:
  roadmap    - Roadmap items by priority (horizontal bar chart)
  features   - Feature count by status (bar chart)
  stats      - Project completion overview (pie-style chart)

SVG files can be viewed in any browser or image viewer, and embedded
in documents or rendered to PNG using tools like Inkscape or rsvg-convert.

Examples:
  tillr export png features -o features.svg
  tillr export png roadmap -o roadmap.svg
  tillr export png stats -o stats.svg`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"roadmap", "features", "stats"},
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		output, _ := cmd.Flags().GetString("output")
		w := os.Stdout
		if output != "" {
			f, err := os.Create(output)
			if err != nil {
				return fmt.Errorf("creating output file: %w", err)
			}
			defer f.Close() //nolint:errcheck
			w = f
		}

		switch args[0] {
		case "features":
			features, err := db.ListFeatures(database, p.ID, "", "")
			if err != nil {
				return fmt.Errorf("listing features: %w", err)
			}
			counts := map[string]int{}
			for _, f := range features {
				counts[f.Status]++
			}
			if err := export.BarChartSVG(w, p.Name+" - Features by Status", counts); err != nil {
				return err
			}

		case "roadmap":
			items, err := db.ListRoadmapItems(database, p.ID)
			if err != nil {
				return fmt.Errorf("listing roadmap items: %w", err)
			}
			counts := map[string]int{}
			for _, r := range items {
				counts[r.Priority]++
			}
			if err := export.BarChartSVG(w, p.Name+" - Roadmap by Priority", counts); err != nil {
				return err
			}

		case "stats":
			features, _ := db.ListFeatures(database, p.ID, "", "")
			counts := map[string]int{}
			for _, f := range features {
				counts[f.Status]++
			}
			if err := export.BarChartSVG(w, p.Name+" - Project Stats", counts); err != nil {
				return err
			}

		default:
			return fmt.Errorf("unknown chart type: %s (use: roadmap, features, stats)", args[0])
		}

		if output != "" {
			fmt.Fprintf(os.Stderr, "Wrote SVG chart to %s\n", output)
		}
		return nil
	},
}
