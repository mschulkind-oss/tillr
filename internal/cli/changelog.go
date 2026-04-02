package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/spf13/cobra"
)

var changelogCmd = &cobra.Command{
	Use:   "changelog",
	Short: "Generate a changelog from project events",
	Long: `Generate a changelog from tillr events in Keep a Changelog format.

Events are categorized into Added, Changed, Fixed, and Removed sections,
grouped by date. Supports markdown (default) and JSON output formats.

EXAMPLES
  tillr changelog
  tillr changelog --since 2025-01-01
  tillr changelog --since 2025-01-01 --until 2025-06-30
  tillr changelog --format json
  tillr changelog -o CHANGELOG.md`,
	RunE: runChangelog,
}

func init() {
	changelogCmd.Flags().String("since", "", "Only include events after this date (YYYY-MM-DD)")
	changelogCmd.Flags().String("until", "", "Only include events before this date (YYYY-MM-DD)")
	changelogCmd.Flags().String("format", "markdown", "Output format: markdown, json")
	changelogCmd.Flags().StringP("output", "o", "", "Write output to file instead of stdout")
}

// changelogCategory represents one of the Keep a Changelog categories.
type changelogCategory string

const (
	categoryAdded   changelogCategory = "Added"
	categoryChanged changelogCategory = "Changed"
	categoryFixed   changelogCategory = "Fixed"
	categoryRemoved changelogCategory = "Removed"
)

type changelogEntry struct {
	Category  changelogCategory `json:"category"`
	Summary   string            `json:"summary"`
	EventType string            `json:"event_type"`
	FeatureID string            `json:"feature_id,omitempty"`
	Timestamp string            `json:"timestamp"`
}

type changelogDay struct {
	Date    string           `json:"date"`
	Entries []changelogEntry `json:"entries"`
}

type changelogData struct {
	Title       string         `json:"title"`
	GeneratedAt string         `json:"generated_at"`
	Project     string         `json:"project"`
	Since       string         `json:"since,omitempty"`
	Until       string         `json:"until,omitempty"`
	Days        []changelogDay `json:"days"`
}

func runChangelog(cmd *cobra.Command, _ []string) error {
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
	until, _ := cmd.Flags().GetString("until")
	format, _ := cmd.Flags().GetString("format")
	outputFile, _ := cmd.Flags().GetString("output")

	if since != "" {
		if _, err := time.Parse("2006-01-02", since); err != nil {
			return fmt.Errorf("invalid --since date %q: expected YYYY-MM-DD format", since)
		}
	}
	if until != "" {
		if _, err := time.Parse("2006-01-02", until); err != nil {
			return fmt.Errorf("invalid --until date %q: expected YYYY-MM-DD format", until)
		}
	}
	if format != "markdown" && format != "json" {
		return fmt.Errorf("unsupported format %q: use markdown or json", format)
	}

	events, err := db.ListEventsFiltered(database, p.ID, "", "", since, until, 0)
	if err != nil {
		return fmt.Errorf("listing events: %w", err)
	}

	entries := categorizEvents(events)
	days := groupEntriesByDate(entries)

	data := changelogData{
		Title:       "Changelog",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Project:     p.Name,
		Since:       since,
		Until:       until,
		Days:        days,
	}

	if jsonOutput || format == "json" {
		return writeOutput(outputFile, "", data, true)
	}

	md := renderChangelogMarkdown(data)
	return writeOutput(outputFile, md, nil, false)
}

// categorizeEvent maps an event to a changelog category and human-readable summary.
// Returns empty category for events that should not appear in the changelog.
func categorizeEvent(e models.Event) (changelogCategory, string) {
	var dataMap map[string]any
	if e.Data != "" {
		_ = json.Unmarshal([]byte(e.Data), &dataMap)
	}
	getString := func(key string) string {
		if dataMap == nil {
			return ""
		}
		v, _ := dataMap[key].(string)
		return v
	}

	switch e.EventType {
	case "feature.created":
		name := getString("name")
		desc := getString("description")
		if desc != "" {
			return categoryAdded, fmt.Sprintf("Feature: %q — %s", name, desc)
		}
		return categoryAdded, fmt.Sprintf("Feature: %q", name)

	case "milestone.created":
		name := getString("name")
		return categoryAdded, fmt.Sprintf("Milestone: %q", name)

	case "roadmap.item_added":
		title := getString("title")
		return categoryAdded, fmt.Sprintf("Roadmap item: %q", title)

	case "discussion.created":
		title := getString("title")
		return categoryAdded, fmt.Sprintf("Discussion: %q", title)

	case "idea.submitted":
		title := getString("title")
		return categoryAdded, fmt.Sprintf("Idea: %q", title)

	case "feature.status_changed":
		from := getString("from")
		to := getString("to")
		if to == "done" {
			return categoryFixed, fmt.Sprintf("Completed feature (was %s)", from)
		}
		return categoryChanged, fmt.Sprintf("Status %s → %s", from, to)

	case "cycle.started":
		cycleType := getString("cycle_type")
		return categoryChanged, fmt.Sprintf("Started %s cycle", cycleType)

	case "cycle.scored":
		return categoryChanged, "Cycle step scored"

	case "cycle.advanced":
		return categoryChanged, "Cycle advanced to next step"

	case "work.completed":
		workType := getString("work_type")
		if workType != "" {
			return categoryChanged, fmt.Sprintf("Completed %s work", workType)
		}
		return categoryChanged, "Work completed"

	case "qa.approved":
		return categoryChanged, "QA approved"

	case "qa.rejected":
		return categoryChanged, "QA rejected — sent back for rework"

	case "idea.approved":
		return categoryChanged, "Idea approved"

	case "idea.rejected":
		return categoryRemoved, "Idea rejected"

	case "discussion.resolved":
		return categoryChanged, "Discussion resolved"

	case "feature.cascade_blocked":
		return categoryChanged, "Feature blocked (cascade)"

	case "feature.cascade_unblocked":
		return categoryChanged, "Feature unblocked (cascade)"

	default:
		return "", ""
	}
}

func categorizEvents(events []models.Event) []changelogEntry {
	var entries []changelogEntry
	for _, e := range events {
		cat, summary := categorizeEvent(e)
		if cat == "" {
			continue
		}
		entry := changelogEntry{
			Category:  cat,
			Summary:   summary,
			EventType: e.EventType,
			FeatureID: e.FeatureID,
			Timestamp: e.CreatedAt,
		}
		entries = append(entries, entry)
	}
	return entries
}

func groupEntriesByDate(entries []changelogEntry) []changelogDay {
	order := []string{}
	byDate := map[string][]changelogEntry{}
	for _, e := range entries {
		date := e.Timestamp
		if len(date) >= 10 {
			date = date[:10]
		}
		if _, exists := byDate[date]; !exists {
			order = append(order, date)
		}
		byDate[date] = append(byDate[date], e)
	}
	// Sort dates descending (most recent first)
	sort.Sort(sort.Reverse(sort.StringSlice(order)))

	var days []changelogDay
	for _, date := range order {
		days = append(days, changelogDay{
			Date:    date,
			Entries: byDate[date],
		})
	}
	return days
}

func renderChangelogMarkdown(data changelogData) string {
	var sb strings.Builder

	sb.WriteString("# Changelog\n\n")

	if len(data.Days) == 0 {
		sb.WriteString("_No changelog entries found._\n")
		return sb.String()
	}

	categoryOrder := []changelogCategory{categoryAdded, categoryChanged, categoryFixed, categoryRemoved}

	for _, day := range data.Days {
		fmt.Fprintf(&sb, "## [%s]\n\n", day.Date)

		// Group entries by category within the day
		byCategory := map[changelogCategory][]changelogEntry{}
		for _, e := range day.Entries {
			byCategory[e.Category] = append(byCategory[e.Category], e)
		}

		for _, cat := range categoryOrder {
			entries, ok := byCategory[cat]
			if !ok {
				continue
			}
			fmt.Fprintf(&sb, "### %s\n", cat)
			for _, e := range entries {
				if e.FeatureID != "" {
					fmt.Fprintf(&sb, "- %s (%s)\n", e.Summary, e.FeatureID)
				} else {
					fmt.Fprintf(&sb, "- %s\n", e.Summary)
				}
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
