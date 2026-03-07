package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mschulkind/lifecycle/internal/models"
)

// p is a write helper that tracks the first error.
type p struct {
	w   io.Writer
	err error
}

func newPrinter(w io.Writer) *p { return &p{w: w} }

func (p *p) printf(format string, a ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, a...)
}

func (p *p) println(a ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintln(p.w, a...)
}

// Features exports features in the given format (json, md/markdown, csv).
func Features(features []models.Feature, w io.Writer, format string) error {
	switch format {
	case "csv":
		return FeaturesCSV(features, w)
	case "md", "markdown":
		return FeaturesMarkdown(features, w)
	default:
		return FeaturesJSON(features, w)
	}
}

// Roadmap exports roadmap items in the given format.
func Roadmap(items []models.RoadmapItem, w io.Writer, format string) error {
	switch format {
	case "csv":
		return RoadmapCSV(items, w)
	case "md", "markdown":
		return RoadmapMarkdown(items, w)
	default:
		return RoadmapJSON(items, w)
	}
}

// Decisions exports decisions in the given format.
func Decisions(decisions []models.Decision, w io.Writer, format string) error {
	switch format {
	case "csv":
		return DecisionsCSV(decisions, w)
	case "md", "markdown":
		return DecisionsMarkdown(decisions, w)
	default:
		return DecisionsJSON(decisions, w)
	}
}

// All exports features, roadmap, and decisions together (json or markdown only).
func All(projectName string, features []models.Feature, roadmap []models.RoadmapItem, decisions []models.Decision, w io.Writer, format string) error {
	switch format {
	case "md", "markdown":
		pr := newPrinter(w)
		pr.printf("# %s — Full Project Export\n\n", projectName)
		pr.printf("*Generated: %s*\n\n", time.Now().Format("January 2, 2006"))
		pr.println("---")
		pr.println()
		if pr.err != nil {
			return pr.err
		}
		if err := FeaturesMarkdown(features, w); err != nil {
			return err
		}
		pr.printf("\n---\n\n")
		if pr.err != nil {
			return pr.err
		}
		if err := RoadmapMarkdown(roadmap, w); err != nil {
			return err
		}
		pr.printf("\n---\n\n")
		if pr.err != nil {
			return pr.err
		}
		return DecisionsMarkdown(decisions, w)
	default:
		return AllJSON(projectName, features, roadmap, decisions, w)
	}
}

// --- JSON exports ---

// FeaturesJSON writes features as pretty-printed JSON.
func FeaturesJSON(features []models.Feature, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(features)
}

// RoadmapJSON writes roadmap items as pretty-printed JSON.
func RoadmapJSON(items []models.RoadmapItem, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

// DecisionsJSON writes decisions as pretty-printed JSON.
func DecisionsJSON(decisions []models.Decision, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(decisions)
}

// AllJSON writes a combined export as JSON.
func AllJSON(projectName string, features []models.Feature, roadmap []models.RoadmapItem, decisions []models.Decision, w io.Writer) error {
	data := struct {
		Project   string               `json:"project"`
		Generated string               `json:"generated"`
		Features  []models.Feature     `json:"features"`
		Roadmap   []models.RoadmapItem `json:"roadmap"`
		Decisions []models.Decision    `json:"decisions"`
	}{
		Project:   projectName,
		Generated: time.Now().Format(time.RFC3339),
		Features:  features,
		Roadmap:   roadmap,
		Decisions: decisions,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// --- Markdown exports ---

// FeaturesMarkdown writes features as a markdown document.
func FeaturesMarkdown(features []models.Feature, w io.Writer) error {
	pr := newPrinter(w)
	pr.println("## Features")
	pr.println()

	if len(features) == 0 {
		pr.println("*No features.*")
		return pr.err
	}

	// Group by status
	groups := map[string][]models.Feature{}
	for _, f := range features {
		groups[f.Status] = append(groups[f.Status], f)
	}

	statuses := []string{"implementing", "planning", "draft", "agent-qa", "human-qa", "done", "blocked"}
	for _, status := range statuses {
		items, ok := groups[status]
		if !ok || len(items) == 0 {
			continue
		}
		pr.printf("### %s\n\n", titleCase(status))
		for _, f := range items {
			check := " "
			prefix := ""
			if f.Status == "done" {
				check = "x"
				prefix = "~~"
			}
			line := fmt.Sprintf("- [%s] %s%s%s", check, prefix, f.Name, prefix)
			if f.Status == "done" {
				line += " ✅"
			}
			if f.Priority > 0 {
				line += fmt.Sprintf(" (priority: %d)", f.Priority)
			}
			pr.println(line)
		}
		pr.println()
	}
	return pr.err
}

// RoadmapMarkdown writes roadmap items as a presentation-ready markdown document.
func RoadmapMarkdown(items []models.RoadmapItem, w io.Writer) error {
	pr := newPrinter(w)
	pr.println("## Project Roadmap")
	pr.println()

	if len(items) == 0 {
		pr.println("*No roadmap items.*")
		return pr.err
	}

	// Group by priority
	groups := map[string][]models.RoadmapItem{}
	for _, r := range items {
		groups[r.Priority] = append(groups[r.Priority], r)
	}

	priorities := []string{"critical", "high", "medium", "low", "nice-to-have"}
	for _, pri := range priorities {
		ritems, ok := groups[pri]
		if !ok || len(ritems) == 0 {
			continue
		}

		pr.printf("### %s %s\n\n", priorityEmoji(pri), priorityLabel(pri))
		for _, r := range ritems {
			check := " "
			prefix := ""
			suffix := ""
			if r.Status == "done" || r.Status == "completed" {
				check = "x"
				prefix = "~~"
				suffix = "~~ ✅"
			}
			if suffix != "" {
				pr.printf("- [%s] %s%s%s\n", check, prefix, r.Title, suffix)
			} else {
				pr.printf("- [%s] %s\n", check, r.Title)
			}
		}
		pr.println()
	}
	return pr.err
}

// DecisionsMarkdown writes decisions as a markdown document.
func DecisionsMarkdown(decisions []models.Decision, w io.Writer) error {
	pr := newPrinter(w)
	pr.println("## Architecture Decisions")
	pr.println()

	if len(decisions) == 0 {
		pr.println("*No decisions recorded.*")
		return pr.err
	}

	for i, d := range decisions {
		statusBadge := strings.ToUpper(d.Status)
		pr.printf("### %d. %s [%s]\n\n", i+1, d.Title, statusBadge)
		if d.Context != "" {
			pr.printf("**Context:** %s\n\n", d.Context)
		}
		if d.Decision != "" {
			pr.printf("**Decision:** %s\n\n", d.Decision)
		}
		if d.Consequences != "" {
			pr.printf("**Consequences:** %s\n\n", d.Consequences)
		}
	}
	return pr.err
}

// --- CSV exports ---

// FeaturesCSV writes features as CSV.
func FeaturesCSV(features []models.Feature, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{"id", "name", "status", "priority", "milestone", "description", "created_at"}); err != nil {
		return err
	}
	for _, f := range features {
		if err := cw.Write([]string{
			f.ID, f.Name, f.Status, fmt.Sprintf("%d", f.Priority),
			f.MilestoneName, f.Description, f.CreatedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// RoadmapCSV writes roadmap items as CSV.
func RoadmapCSV(items []models.RoadmapItem, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{"id", "title", "priority", "status", "category", "effort", "description", "created_at"}); err != nil {
		return err
	}
	for _, r := range items {
		if err := cw.Write([]string{
			r.ID, r.Title, r.Priority, r.Status, r.Category,
			r.Effort, r.Description, r.CreatedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// DecisionsCSV writes decisions as CSV.
func DecisionsCSV(decisions []models.Decision, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{"id", "title", "status", "context", "decision", "consequences", "created_at"}); err != nil {
		return err
	}
	for _, d := range decisions {
		if err := cw.Write([]string{
			d.ID, d.Title, d.Status, d.Context, d.Decision,
			d.Consequences, d.CreatedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ---

func priorityEmoji(p string) string {
	switch p {
	case "critical":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	case "low":
		return "🟢"
	case "nice-to-have":
		return "🔵"
	default:
		return "⚪"
	}
}

func priorityLabel(p string) string {
	switch p {
	case "critical":
		return "Critical Priority"
	case "high":
		return "High Priority"
	case "medium":
		return "Medium Priority"
	case "low":
		return "Low Priority"
	case "nice-to-have":
		return "Nice to Have"
	default:
		return p
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}
