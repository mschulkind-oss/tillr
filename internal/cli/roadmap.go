package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var roadmapCmd = &cobra.Command{
	Use:   "roadmap",
	Short: "Manage roadmap",
}

func init() {
	roadmapCmd.AddCommand(roadmapShowCmd)
	roadmapCmd.AddCommand(roadmapAddCmd)
	roadmapCmd.AddCommand(roadmapEditCmd)
	roadmapCmd.AddCommand(roadmapPrioritizeCmd)
	roadmapCmd.AddCommand(roadmapExportCmd)
	roadmapCmd.AddCommand(roadmapStatsCmd)

	roadmapShowCmd.Flags().String("format", "table", "Output format (table, json, markdown)")

	roadmapAddCmd.Flags().String("description", "", "Item description")
	roadmapAddCmd.Flags().String("category", "", "Item category")
	roadmapAddCmd.Flags().String("priority", "medium", "Priority (critical, high, medium, low, nice-to-have)")
	roadmapAddCmd.Flags().String("effort", "", "Effort estimate (xs, s, m, l, xl)")
	roadmapAddCmd.Flags().Int("order", 0, "Sort order")

	roadmapEditCmd.Flags().String("title", "", "New title")
	roadmapEditCmd.Flags().String("description", "", "New description")
	roadmapEditCmd.Flags().String("category", "", "New category")
	roadmapEditCmd.Flags().String("priority", "", "New priority")
	roadmapEditCmd.Flags().String("status", "", "New status")
	roadmapEditCmd.Flags().String("effort", "", "Effort estimate (xs, s, m, l, xl)")

	roadmapExportCmd.Flags().String("format", "markdown", "Export format (markdown, json)")
}

var roadmapShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display roadmap",
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

		items, err := db.ListRoadmapItems(database, p.ID)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" || jsonOutput {
			return printJSON(items)
		}

		if len(items) == 0 {
			fmt.Println("No roadmap items. Use 'lifecycle roadmap add' to create one.")
			return nil
		}

		if format == "markdown" {
			return printRoadmapMarkdown(p.Name, items)
		}

		// Table format — enhanced with box-drawing and priority indicators
		return printRoadmapTable(items)
	},
}

var roadmapAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Add a roadmap item",
	Args:  cobra.ExactArgs(1),
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

		desc, _ := cmd.Flags().GetString("description")
		category, _ := cmd.Flags().GetString("category")
		priority, _ := cmd.Flags().GetString("priority")
		effort, _ := cmd.Flags().GetString("effort")
		order, _ := cmd.Flags().GetInt("order")

		if effort != "" {
			validEfforts := map[string]bool{"xs": true, "s": true, "m": true, "l": true, "xl": true}
			if !validEfforts[effort] {
				return fmt.Errorf("invalid effort %q: must be one of xs, s, m, l, xl", effort)
			}
		}

		id := engine.Slug(args[0])
		r := &models.RoadmapItem{
			ID:          id,
			ProjectID:   p.ID,
			Title:       args[0],
			Description: desc,
			Category:    category,
			Priority:    priority,
			Effort:      effort,
			SortOrder:   order,
		}

		if err := db.CreateRoadmapItem(database, r); err != nil {
			return err
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "roadmap.item_added",
			Data:      fmt.Sprintf(`{"title":%q,"priority":%q}`, args[0], priority),
		})

		if jsonOutput {
			return printJSON(r)
		}
		fmt.Printf("✓ Added roadmap item %q (id: %s, priority: %s)\n", r.Title, r.ID, r.Priority)
		return nil
	},
}

var roadmapEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Edit a roadmap item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		updates := make(map[string]any)
		if v, _ := cmd.Flags().GetString("title"); v != "" {
			updates["title"] = v
		}
		if v, _ := cmd.Flags().GetString("description"); v != "" {
			updates["description"] = v
		}
		if v, _ := cmd.Flags().GetString("category"); v != "" {
			updates["category"] = v
		}
		if v, _ := cmd.Flags().GetString("priority"); v != "" {
			updates["priority"] = v
		}
		if v, _ := cmd.Flags().GetString("status"); v != "" {
			updates["status"] = v
		}
		if cmd.Flags().Changed("effort") {
			v, _ := cmd.Flags().GetString("effort")
			if v != "" {
				validEfforts := map[string]bool{"xs": true, "s": true, "m": true, "l": true, "xl": true}
				if !validEfforts[v] {
					return fmt.Errorf("invalid effort %q: must be one of xs, s, m, l, xl", v)
				}
			}
			updates["effort"] = v
		}

		if len(updates) == 0 {
			return fmt.Errorf("no changes specified")
		}

		if err := db.UpdateRoadmapItem(database, args[0], updates); err != nil {
			return err
		}

		if jsonOutput {
			r, _ := db.GetRoadmapItem(database, args[0])
			return printJSON(r)
		}
		fmt.Printf("✓ Updated roadmap item %s\n", args[0])
		return nil
	},
}

var roadmapPrioritizeCmd = &cobra.Command{
	Use:   "prioritize",
	Short: "Show items grouped by priority for review",
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

		items, err := db.ListRoadmapItems(database, p.ID)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(items)
		}

		groups := map[string][]models.RoadmapItem{}
		priorities := []string{"critical", "high", "medium", "low", "nice-to-have"}
		for _, r := range items {
			groups[r.Priority] = append(groups[r.Priority], r)
		}

		for _, pri := range priorities {
			if items, ok := groups[pri]; ok && len(items) > 0 {
				fmt.Printf("\n%s %s\n", priorityEmoji(pri), strings.ToUpper(pri))
				fmt.Println(strings.Repeat("─", 40))
				for i, r := range items {
					fmt.Printf("  %d. [%s] %s\n", i+1, r.Status, r.Title)
				}
			}
		}
		return nil
	},
}

var roadmapExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export roadmap",
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

		items, err := db.ListRoadmapItems(database, p.ID)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return printRoadmapJSON(p.Name, items)
		}

		return printRoadmapMarkdown(p.Name, items)
	},
}

var roadmapStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show roadmap health and progress statistics",
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

		stats, err := db.GetRoadmapStats(database, p.ID)
		if err != nil {
			return err
		}

		health, healthEmoji := roadmapHealthLabel(stats)

		if jsonOutput {
			return printJSON(map[string]any{
				"total":       stats.Total,
				"health":      health,
				"by_priority": stats.ByPriority,
				"by_category": stats.ByCategory,
				"by_status":   stats.ByStatus,
			})
		}

		// Human-readable box output
		fmt.Println("╔═══════════════════════════════╗")
		fmt.Println("║     📊 Roadmap Statistics     ║")
		fmt.Println("╠═══════════════════════════════╣")
		fmt.Printf("║ Total Items:  %-16d║\n", stats.Total)
		fmt.Printf("║ Health:       %s %-13s║\n", healthEmoji, health)
		fmt.Println("╠═══════════════════════════════╣")
		fmt.Println("║ By Priority:                  ║")
		priorities := []string{"critical", "high", "medium", "low", "nice-to-have"}
		for _, pri := range priorities {
			if count, ok := stats.ByPriority[pri]; ok && count > 0 {
				fmt.Printf("║   %s %-14s%-12d║\n", priorityEmoji(pri), strings.Title(pri), count) //nolint:staticcheck
			}
		}
		fmt.Println("╠═══════════════════════════════╣")
		fmt.Println("║ By Category:                  ║")
		if len(stats.ByCategory) == 0 {
			fmt.Println("║   (none)                      ║")
		} else {
			for cat, count := range stats.ByCategory {
				fmt.Printf("║   %-16s%-12d║\n", cat, count)
			}
		}
		fmt.Println("╠═══════════════════════════════╣")
		fmt.Println("║ By Status:                    ║")
		if len(stats.ByStatus) == 0 {
			fmt.Println("║   all proposed                ║")
		} else {
			for status, count := range stats.ByStatus {
				fmt.Printf("║   %-16s%-12d║\n", status, count)
			}
		}
		fmt.Println("╚═══════════════════════════════╝")
		return nil
	},
}

func roadmapHealthLabel(stats *db.RoadmapStats) (string, string) {
	switch {
	case stats.Total == 0:
		return "Empty", "⚪"
	case stats.Total < 3:
		return "Needs work", "🟡"
	case stats.Total > 10:
		return "Comprehensive", "🔵"
	default:
		if len(stats.ByPriority) > 1 {
			return "Good", "🟢"
		}
		return "Needs work", "🟡"
	}
}

func printRoadmapMarkdown(projectName string, items []models.RoadmapItem) error {
	// Title
	fmt.Printf("# 🗺️ Project Roadmap — %s\n\n", projectName)
	fmt.Printf("*Generated: %s*\n\n", time.Now().Format("January 2, 2006"))

	if len(items) == 0 {
		fmt.Println("*No roadmap items yet.*")
		return nil
	}

	// Summary stats
	fmt.Println("---")
	fmt.Println()
	fmt.Println("## 📊 Summary")
	fmt.Println()

	priorityCounts := map[string]int{}
	statusCounts := map[string]int{}
	categoryCounts := map[string]int{}
	for _, r := range items {
		priorityCounts[r.Priority]++
		statusCounts[r.Status]++
		cat := r.Category
		if cat == "" {
			cat = "Uncategorized"
		}
		categoryCounts[cat]++
	}

	fmt.Printf("| Metric | Count |\n")
	fmt.Printf("|:-------|------:|\n")
	fmt.Printf("| **Total Items** | %d |\n", len(items))

	priorities := []string{"critical", "high", "medium", "low", "nice-to-have"}
	for _, pri := range priorities {
		if c, ok := priorityCounts[pri]; ok {
			fmt.Printf("| %s %s | %d |\n", priorityEmoji(pri), priorityLabel(pri), c)
		}
	}

	statuses := []string{"proposed", "accepted", "in-progress", "done", "deferred", "rejected"}
	for _, s := range statuses {
		if c, ok := statusCounts[s]; ok {
			fmt.Printf("| Status: %s | %d |\n", titleCase(s), c)
		}
	}
	fmt.Println()

	// Group by priority
	groups := map[string][]models.RoadmapItem{}
	for _, r := range items {
		groups[r.Priority] = append(groups[r.Priority], r)
	}

	fmt.Println("---")
	fmt.Println()

	itemNum := 1
	for _, pri := range priorities {
		ritems, ok := groups[pri]
		if !ok || len(ritems) == 0 {
			continue
		}

		fmt.Printf("## %s %s\n\n", priorityEmoji(pri), priorityLabel(pri))

		for _, r := range ritems {
			fmt.Printf("### %d. %s\n\n", itemNum, r.Title)

			if r.Category != "" {
				fmt.Printf("- **Category:** %s\n", r.Category)
			}
			fmt.Printf("- **Status:** %s\n", titleCase(r.Status))
			fmt.Printf("- **Priority:** %s %s\n", priorityEmoji(r.Priority), titleCase(r.Priority))
			if r.Description != "" {
				fmt.Printf("- **Description:** %s\n", r.Description)
			}
			fmt.Println()
			itemNum++
		}
	}

	// Category index
	if len(categoryCounts) > 0 {
		fmt.Println("---")
		fmt.Println()
		fmt.Println("## 📂 Category Index")
		fmt.Println()

		catGroups := map[string][]models.RoadmapItem{}
		for _, r := range items {
			cat := r.Category
			if cat == "" {
				cat = "Uncategorized"
			}
			catGroups[cat] = append(catGroups[cat], r)
		}

		// Collect and sort category names for deterministic output
		catNames := make([]string, 0, len(catGroups))
		for cat := range catGroups {
			catNames = append(catNames, cat)
		}
		sortStrings(catNames)

		for _, cat := range catNames {
			citems := catGroups[cat]
			fmt.Printf("**%s** (%d)\n\n", cat, len(citems))
			for _, r := range citems {
				check := " "
				if r.Status == "done" {
					check = "x"
				}
				fmt.Printf("- [%s] %s — *%s*\n", check, r.Title, titleCase(r.Status))
			}
			fmt.Println()
		}
	}

	return nil
}

func printRoadmapJSON(projectName string, items []models.RoadmapItem) error {
	priorityCounts := map[string]int{}
	statusCounts := map[string]int{}
	categoryCounts := map[string]int{}
	for _, r := range items {
		priorityCounts[r.Priority]++
		statusCounts[r.Status]++
		cat := r.Category
		if cat == "" {
			cat = "uncategorized"
		}
		categoryCounts[cat]++
	}

	export := struct {
		Project    string               `json:"project"`
		Generated  string               `json:"generated"`
		TotalItems int                  `json:"total_items"`
		ByPriority map[string]int       `json:"by_priority"`
		ByStatus   map[string]int       `json:"by_status"`
		ByCategory map[string]int       `json:"by_category"`
		Items      []models.RoadmapItem `json:"items"`
	}{
		Project:    projectName,
		Generated:  time.Now().Format(time.RFC3339),
		TotalItems: len(items),
		ByPriority: priorityCounts,
		ByStatus:   statusCounts,
		ByCategory: categoryCounts,
		Items:      items,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(export)
}

func printRoadmapTable(items []models.RoadmapItem) error {
	// Group by priority
	groups := map[string][]models.RoadmapItem{}
	priorities := []string{"critical", "high", "medium", "low", "nice-to-have"}
	for _, r := range items {
		groups[r.Priority] = append(groups[r.Priority], r)
	}

	first := true
	for _, pri := range priorities {
		ritems, ok := groups[pri]
		if !ok || len(ritems) == 0 {
			continue
		}

		if !first {
			fmt.Println()
		}
		first = false

		header := fmt.Sprintf(" %s %s (%d)", priorityIndicator(pri), strings.ToUpper(priorityLabel(pri)), len(ritems))
		fmt.Printf("┌%s┐\n", strings.Repeat("─", 78))
		fmt.Printf("│%-78s│\n", header)
		fmt.Printf("├%s┤\n", strings.Repeat("─", 78))

		for _, r := range ritems {
			cat := r.Category
			if cat == "" {
				cat = "—"
			}
			statusBadge := fmt.Sprintf("[%s]", r.Status)
			titlePart := fmt.Sprintf("  %s  %s", r.Title, cat)

			// Pad title to right-align status badge
			padLen := 78 - len(titlePart) - len(statusBadge)
			if padLen < 1 {
				padLen = 1
			}
			line := titlePart + strings.Repeat(" ", padLen) + statusBadge
			// Truncate if too long
			if len(line) > 78 {
				line = line[:78]
			}
			fmt.Printf("│%-78s│\n", line)
		}
		fmt.Printf("└%s┘\n", strings.Repeat("─", 78))
	}
	return nil
}

func priorityIndicator(p string) string {
	switch p {
	case "critical":
		return "● CRIT"
	case "high":
		return "● HIGH"
	case "medium":
		return "● MED "
	case "low":
		return "● LOW "
	case "nice-to-have":
		return "● NICE"
	default:
		return "○     "
	}
}

func priorityLabel(p string) string {
	switch p {
	case "critical":
		return "Critical"
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

// sortStrings sorts a slice of strings in place (simple insertion sort to avoid extra imports).
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

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
