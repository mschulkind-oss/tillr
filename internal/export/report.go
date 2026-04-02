package export

import (
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/mschulkind/tillr/internal/models"
)

// ReportData holds all the data needed to generate a project report.
type ReportData struct {
	ProjectName string               `json:"project_name"`
	Generated   string               `json:"generated"`
	Milestones  []models.Milestone   `json:"milestones"`
	Features    []models.Feature     `json:"features"`
	Roadmap     []models.RoadmapItem `json:"roadmap"`
}

// Report generates a project status report in the given format (md or html).
func Report(data ReportData, w io.Writer, format string) error {
	data.Generated = time.Now().Format("January 2, 2006 15:04")
	switch format {
	case "html":
		return ReportHTML(data, w)
	default:
		return ReportMarkdown(data, w)
	}
}

// ReportMarkdown generates a clean Markdown project status report.
func ReportMarkdown(data ReportData, w io.Writer) error {
	pr := newPrinter(w)

	pr.printf("# %s — Project Status Report\n\n", data.ProjectName)
	pr.printf("*Generated: %s*\n\n", data.Generated)
	pr.println("---")
	pr.println()

	// Summary stats
	pr.println("## Summary")
	pr.println()
	featuresByStatus := countByStatus(data.Features)
	total := len(data.Features)
	done := featuresByStatus["done"]
	var pct float64
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}
	pr.printf("- **Total Features:** %d\n", total)
	pr.printf("- **Completed:** %d (%.0f%%)\n", done, pct)
	pr.printf("- **In Progress:** %d\n", featuresByStatus["implementing"])
	pr.printf("- **Milestones:** %d\n", len(data.Milestones))
	pr.printf("- **Roadmap Items:** %d\n", len(data.Roadmap))
	pr.println()

	// Milestone progress
	if len(data.Milestones) > 0 {
		pr.println("## Milestone Progress")
		pr.println()
		for _, m := range data.Milestones {
			var pctDone float64
			if m.TotalFeatures > 0 {
				pctDone = float64(m.DoneFeatures) / float64(m.TotalFeatures) * 100
			}
			bar := progressBar(pctDone, 20)
			pr.printf("### %s\n\n", m.Name)
			pr.printf("  %s %.0f%% (%d/%d features)\n\n", bar, pctDone, m.DoneFeatures, m.TotalFeatures)
		}
	}

	// Features by status
	pr.println("## Features")
	pr.println()
	if err := FeaturesMarkdown(data.Features, w); err != nil {
		return err
	}

	// Roadmap overview
	pr.println()
	if err := RoadmapMarkdown(data.Roadmap, w); err != nil {
		return err
	}

	return pr.err
}

// ReportHTML generates an HTML project status report with inline CSS for printing.
func ReportHTML(data ReportData, w io.Writer) error {
	featuresByStatus := countByStatus(data.Features)
	total := len(data.Features)
	done := featuresByStatus["done"]
	var pct float64
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}

	pr := newPrinter(w)
	pr.println(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">`)
	pr.printf("<title>%s — Project Status Report</title>\n", escapeHTML(data.ProjectName))
	pr.println(`<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; line-height: 1.6; color: #1a1a2e; max-width: 900px; margin: 0 auto; padding: 40px 20px; }
  h1 { font-size: 1.8em; margin-bottom: 4px; }
  h2 { font-size: 1.3em; margin-top: 28px; margin-bottom: 12px; border-bottom: 2px solid #e0e0e0; padding-bottom: 4px; }
  h3 { font-size: 1.1em; margin-top: 16px; margin-bottom: 8px; }
  .meta { color: #666; font-size: 0.9em; margin-bottom: 20px; }
  .summary-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 12px; margin-bottom: 20px; }
  .stat-card { background: #f8f9fa; border: 1px solid #e0e0e0; border-radius: 8px; padding: 12px; text-align: center; }
  .stat-card .number { font-size: 1.8em; font-weight: 700; color: #16213e; }
  .stat-card .label { font-size: 0.85em; color: #666; }
  .progress-bar { background: #e0e0e0; border-radius: 4px; height: 20px; overflow: hidden; margin: 4px 0 12px 0; }
  .progress-fill { background: #4caf50; height: 100%; border-radius: 4px; transition: width 0.3s; }
  .milestone { margin-bottom: 16px; }
  .milestone-header { display: flex; justify-content: space-between; align-items: center; }
  .milestone-pct { font-weight: 600; color: #666; font-size: 0.9em; }
  table { width: 100%; border-collapse: collapse; margin: 12px 0; }
  th, td { padding: 8px 12px; text-align: left; border-bottom: 1px solid #e0e0e0; }
  th { background: #f8f9fa; font-weight: 600; font-size: 0.85em; text-transform: uppercase; letter-spacing: 0.05em; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 0.75em; font-weight: 600; }
  .badge-done { background: #c8e6c9; color: #2e7d32; }
  .badge-implementing { background: #bbdefb; color: #1565c0; }
  .badge-draft { background: #f5f5f5; color: #757575; }
  .badge-planning { background: #fff9c4; color: #f57f17; }
  .badge-blocked { background: #ffcdd2; color: #c62828; }
  .badge-agent-qa, .badge-human-qa { background: #e1bee7; color: #6a1b9a; }
  .roadmap-item { padding: 8px 0; border-bottom: 1px solid #f0f0f0; }
  .roadmap-title { font-weight: 600; }
  .roadmap-meta { font-size: 0.85em; color: #666; }
  .priority-critical { color: #c62828; }
  .priority-high { color: #e65100; }
  .priority-medium { color: #f9a825; }
  .priority-low { color: #2e7d32; }
  @media print {
    body { padding: 20px; max-width: 100%; }
    .stat-card { border: 1px solid #ccc; break-inside: avoid; }
    h2 { break-after: avoid; }
    table { break-inside: auto; }
    tr { break-inside: avoid; }
  }
</style>
</head>
<body>`)
	pr.printf("<h1>%s</h1>\n", escapeHTML(data.ProjectName))
	pr.printf("<p class=\"meta\">Project Status Report — Generated %s</p>\n", escapeHTML(data.Generated))

	// Summary cards
	pr.println(`<h2>Summary</h2>
<div class="summary-grid">`)
	pr.printf("<div class=\"stat-card\"><div class=\"number\">%d</div><div class=\"label\">Total Features</div></div>\n", total)
	pr.printf("<div class=\"stat-card\"><div class=\"number\">%d</div><div class=\"label\">Completed</div></div>\n", done)
	pr.printf("<div class=\"stat-card\"><div class=\"number\">%.0f%%</div><div class=\"label\">Completion</div></div>\n", pct)
	pr.printf("<div class=\"stat-card\"><div class=\"number\">%d</div><div class=\"label\">In Progress</div></div>\n", featuresByStatus["implementing"])
	pr.printf("<div class=\"stat-card\"><div class=\"number\">%d</div><div class=\"label\">Milestones</div></div>\n", len(data.Milestones))
	pr.printf("<div class=\"stat-card\"><div class=\"number\">%d</div><div class=\"label\">Roadmap Items</div></div>\n", len(data.Roadmap))
	pr.println("</div>")

	// Milestones
	if len(data.Milestones) > 0 {
		pr.println("<h2>Milestone Progress</h2>")
		for _, m := range data.Milestones {
			var pctDone float64
			if m.TotalFeatures > 0 {
				pctDone = float64(m.DoneFeatures) / float64(m.TotalFeatures) * 100
			}
			pr.println("<div class=\"milestone\">")
			pr.printf("<div class=\"milestone-header\"><h3>%s</h3><span class=\"milestone-pct\">%d/%d (%.0f%%)</span></div>\n",
				escapeHTML(m.Name), m.DoneFeatures, m.TotalFeatures, pctDone)
			pr.printf("<div class=\"progress-bar\"><div class=\"progress-fill\" style=\"width:%.0f%%\"></div></div>\n", pctDone)
			pr.println("</div>")
		}
	}

	// Features table
	pr.println("<h2>Features</h2>")
	if len(data.Features) > 0 {
		pr.println("<table><thead><tr><th>Name</th><th>Status</th><th>Priority</th><th>Milestone</th></tr></thead><tbody>")
		for _, f := range data.Features {
			badgeClass := "badge-" + f.Status
			pr.printf("<tr><td>%s</td><td><span class=\"badge %s\">%s</span></td><td>%d</td><td>%s</td></tr>\n",
				escapeHTML(f.Name), badgeClass, escapeHTML(f.Status), f.Priority, escapeHTML(f.MilestoneName))
		}
		pr.println("</tbody></table>")
	} else {
		pr.println("<p><em>No features.</em></p>")
	}

	// Roadmap
	pr.println("<h2>Roadmap</h2>")
	if len(data.Roadmap) > 0 {
		for _, r := range data.Roadmap {
			priClass := "priority-" + r.Priority
			pr.println("<div class=\"roadmap-item\">")
			pr.printf("<div class=\"roadmap-title\">%s</div>\n", escapeHTML(r.Title))
			pr.printf("<div class=\"roadmap-meta\"><span class=\"%s\">%s</span> · %s · %s</div>\n",
				priClass, escapeHTML(r.Priority), escapeHTML(r.Status), escapeHTML(r.Category))
			pr.println("</div>")
		}
	} else {
		pr.println("<p><em>No roadmap items.</em></p>")
	}

	pr.println("</body>\n</html>")
	return pr.err
}

// ReportJSON writes report data as JSON.
func ReportJSON(data ReportData, w io.Writer) error {
	data.Generated = time.Now().Format(time.RFC3339)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func countByStatus(features []models.Feature) map[string]int {
	m := make(map[string]int)
	for _, f := range features {
		m[f.Status]++
	}
	return m
}

func progressBar(pct float64, width int) string {
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
