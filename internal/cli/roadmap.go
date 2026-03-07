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
	roadmapCmd.AddCommand(roadmapRebalanceCmd)
	roadmapCmd.AddCommand(roadmapSummaryCmd)
	roadmapCmd.AddCommand(roadmapNextCmd)

	roadmapNextCmd.Flags().Float64("effort-weight", 1.0, "Multiplier for effort bonus (0=ignore effort, 2=strongly favor easy)")
	roadmapNextCmd.Flags().String("category", "", "Filter to specific category")
	roadmapNextCmd.Flags().Int("count", 1, "Number of top items to show")
	roadmapNextCmd.Flags().String("exclude-status", "done,rejected,deferred", "Comma-separated statuses to exclude")

	roadmapRebalanceCmd.Flags().Bool("dry-run", true, "Preview changes without applying (default)")
	roadmapRebalanceCmd.Flags().Bool("apply", false, "Apply priority bumps")
	roadmapRebalanceCmd.Flags().Int("max-bump", 1, "Maximum number of priority levels to bump")

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

var roadmapRebalanceCmd = &cobra.Command{
	Use:   "rebalance",
	Short: "Auto-bump priority of easy items",
	Long:  "Analyzes roadmap and bumps priority of low-effort items that are stuck at low priority.",
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

		apply, _ := cmd.Flags().GetBool("apply")
		maxBump, _ := cmd.Flags().GetInt("max-bump")

		bumps := computeRebalanceBumps(items, maxBump)

		if jsonOutput {
			if apply {
				for _, b := range bumps {
					if err := db.UpdateRoadmapItem(database, b.item.ID, map[string]any{"priority": b.newPriority}); err != nil {
						return fmt.Errorf("updating %s: %w", b.item.ID, err)
					}
				}
			}

			type jsonBump struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				OldPriority string `json:"old_priority"`
				NewPriority string `json:"new_priority"`
				Effort      string `json:"effort"`
				Reason      string `json:"reason"`
				Applied     bool   `json:"applied"`
			}
			jb := make([]jsonBump, len(bumps))
			for i, b := range bumps {
				jb[i] = jsonBump{
					ID:          b.item.ID,
					Title:       b.item.Title,
					OldPriority: b.oldPriority,
					NewPriority: b.newPriority,
					Effort:      b.item.Effort,
					Reason:      b.reason,
					Applied:     apply,
				}
			}
			return printJSON(map[string]any{
				"bumps":   jb,
				"count":   len(jb),
				"applied": apply,
			})
		}

		if len(bumps) == 0 {
			fmt.Println("No items eligible for priority bump.")
			return nil
		}

		if !apply {
			fmt.Println("🔄 Roadmap Rebalance Preview")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			fmt.Println()
			fmt.Printf("%d items would be bumped:\n", len(bumps))
			for _, b := range bumps {
				fmt.Println()
				fmt.Printf("  ↑ %s\n", b.item.Title)
				fmt.Printf("    %s/%s → %s/%s (%s)\n", b.oldPriority, b.item.Effort, b.newPriority, b.item.Effort, b.reason)
			}
			fmt.Println()
			fmt.Println("Run with --apply to make these changes.")
		} else {
			for _, b := range bumps {
				if err := db.UpdateRoadmapItem(database, b.item.ID, map[string]any{"priority": b.newPriority}); err != nil {
					return fmt.Errorf("updating %s: %w", b.item.ID, err)
				}
			}
			fmt.Printf("✓ Bumped %d roadmap items:\n", len(bumps))
			for _, b := range bumps {
				fmt.Printf("  ↑ %s: %s → %s\n", b.item.Title, b.oldPriority, b.newPriority)
			}
		}
		return nil
	},
}

type rebalanceBump struct {
	item        models.RoadmapItem
	oldPriority string
	newPriority string
	reason      string
}

// priorityRank maps priority strings to numeric rank (higher = more important).
var priorityRank = map[string]int{
	"nice-to-have": 0,
	"low":          1,
	"medium":       2,
	"high":         3,
	"critical":     4,
}

// priorityFromRank is the inverse of priorityRank.
var priorityFromRank = []string{"nice-to-have", "low", "medium", "high", "critical"}

func computeRebalanceBumps(items []models.RoadmapItem, maxBump int) []rebalanceBump {
	var bumps []rebalanceBump
	for _, item := range items {
		if item.Status != "proposed" && item.Status != "accepted" {
			continue
		}

		currentRank, ok := priorityRank[item.Priority]
		if !ok {
			continue
		}

		targetRank := currentRank

		// Rule 1: xs/s effort at low/nice-to-have → target medium
		if (item.Effort == "xs" || item.Effort == "s") && (item.Priority == "low" || item.Priority == "nice-to-have") {
			targetRank = priorityRank["medium"]
		}

		// Rule 2: xs effort at medium → target high
		if item.Effort == "xs" && item.Priority == "medium" {
			targetRank = priorityRank["high"]
		}

		if targetRank <= currentRank {
			continue
		}

		// Cap at max-bump levels
		jump := targetRank - currentRank
		if jump > maxBump {
			jump = maxBump
		}

		newRank := currentRank + jump
		if newRank > priorityRank["critical"] {
			newRank = priorityRank["critical"]
		}

		if newRank == currentRank {
			continue
		}

		reason := "easy item deserves higher priority"
		if item.Effort == "xs" && jump > 1 {
			reason = "very easy item, double bump"
		} else if item.Effort == "xs" {
			reason = "very easy item deserves higher priority"
		}

		bumps = append(bumps, rebalanceBump{
			item:        item,
			oldPriority: item.Priority,
			newPriority: priorityFromRank[newRank],
			reason:      reason,
		})
	}
	return bumps
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

// effectivePriorityScore computes a combined score factoring priority and effort.
func effectivePriorityScore(item models.RoadmapItem, effortWeight float64) float64 {
	priorityValues := map[string]float64{
		"critical": 10, "high": 7, "medium": 5, "low": 3, "nice-to-have": 1,
	}
	effortBonus := map[string]float64{
		"xs": 4, "s": 3, "m": 1, "l": 0, "xl": -1,
	}
	pv := priorityValues[item.Priority]
	eb := effortBonus[item.Effort]
	return pv + eb*effortWeight
}

var roadmapSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Human+agent-friendly roadmap overview",
	Long:  "Shows counts by priority, effort breakdown, and top actionable items scored by effective priority. Designed so you never need to pipe roadmap JSON through python.",
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

		done := 0
		remaining := 0
		byPriority := map[string][2]int{} // [remaining, done]
		byEffort := map[string]int{}
		byCategory := map[string]int{}
		for _, item := range items {
			if item.Status == "done" {
				done++
			} else {
				remaining++
			}
			key := item.Priority
			counts := byPriority[key]
			if item.Status == "done" {
				counts[1]++
			} else {
				counts[0]++
			}
			byPriority[key] = counts
			if item.Effort != "" {
				byEffort[item.Effort]++
			}
			cat := item.Category
			if cat == "" {
				cat = "uncategorized"
			}
			if item.Status != "done" {
				byCategory[cat]++
			}
		}

		// Compute top actionable items
		type scored struct {
			item  models.RoadmapItem
			score float64
		}
		var actionable []scored
		for _, item := range items {
			if item.Status == "done" || item.Status == "rejected" || item.Status == "deferred" {
				continue
			}
			actionable = append(actionable, scored{item, effectivePriorityScore(item, 1.0)})
		}
		// Sort descending by score
		for i := 1; i < len(actionable); i++ {
			for j := i; j > 0 && actionable[j].score > actionable[j-1].score; j-- {
				actionable[j], actionable[j-1] = actionable[j-1], actionable[j]
			}
		}
		top5 := actionable
		if len(top5) > 5 {
			top5 = top5[:5]
		}

		if jsonOutput {
			result := map[string]any{
				"total":       len(items),
				"done":        done,
				"remaining":   remaining,
				"by_priority": byPriority,
				"by_effort":   byEffort,
				"by_category": byCategory,
			}
			topItems := make([]map[string]any, len(top5))
			for i, s := range top5 {
				topItems[i] = map[string]any{
					"id":              s.item.ID,
					"title":           s.item.Title,
					"priority":        s.item.Priority,
					"effort":          s.item.Effort,
					"category":        s.item.Category,
					"effective_score": s.score,
				}
			}
			result["top_actionable"] = topItems
			return printJSON(result)
		}

		fmt.Println("📋 Roadmap Summary")
		fmt.Println("━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Printf("Total: %d items (%d done, %d remaining)\n", len(items), done, remaining)
		fmt.Println()
		fmt.Println("By Priority:")
		for _, pri := range []string{"critical", "high", "medium", "low", "nice-to-have"} {
			counts, ok := byPriority[pri]
			if !ok {
				continue
			}
			fmt.Printf("  %s %-12s %d remaining (%d done)\n", priorityEmoji(pri), titleCase(pri)+":", counts[0], counts[1])
		}
		fmt.Println()
		fmt.Println("By Effort:")
		effortLine := " "
		for _, e := range []string{"XS", "S", "M", "L", "XL"} {
			c := byEffort[strings.ToLower(e)]
			if c > 0 {
				effortLine += fmt.Sprintf(" %s: %d ", e, c)
			}
		}
		fmt.Println(effortLine)
		fmt.Println()
		if len(top5) > 0 {
			fmt.Printf("Top %d Actionable (by effective priority):\n", len(top5))
			for i, s := range top5 {
				cat := s.item.Category
				if cat == "" {
					cat = "—"
				}
				fmt.Printf("  %d. [%s/%s] %s — %s (score: %.0f)\n", i+1, s.item.Priority, s.item.Effort, s.item.Title, cat, s.score)
			}
		}

		if len(byCategory) > 0 {
			fmt.Println()
			fmt.Print("Categories:")
			cats := make([]string, 0, len(byCategory))
			for c := range byCategory {
				cats = append(cats, c)
			}
			sortStrings(cats)
			for _, c := range cats {
				fmt.Printf(" %s (%d)", c, byCategory[c])
			}
			fmt.Println()
		}
		return nil
	},
}

var roadmapNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Pick the best roadmap item to work on next",
	Long:  "Selects the highest effective-priority item factoring both importance and effort. Easy items get a boost so they get done instead of lingering.",
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

		effortWeight, _ := cmd.Flags().GetFloat64("effort-weight")
		category, _ := cmd.Flags().GetString("category")
		count, _ := cmd.Flags().GetInt("count")
		excludeStr, _ := cmd.Flags().GetString("exclude-status")

		excludeSet := map[string]bool{}
		for _, s := range strings.Split(excludeStr, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				excludeSet[s] = true
			}
		}

		type scored struct {
			Item           models.RoadmapItem `json:"item"`
			EffectiveScore float64            `json:"effective_score"`
			PriorityValue  float64            `json:"priority_value"`
			EffortBonus    float64            `json:"effort_bonus"`
		}

		var candidates []scored
		priorityValues := map[string]float64{
			"critical": 10, "high": 7, "medium": 5, "low": 3, "nice-to-have": 1,
		}
		effortBonusMap := map[string]float64{
			"xs": 4, "s": 3, "m": 1, "l": 0, "xl": -1,
		}

		for _, item := range items {
			if excludeSet[item.Status] {
				continue
			}
			if category != "" && item.Category != category {
				continue
			}
			pv := priorityValues[item.Priority]
			eb := effortBonusMap[item.Effort]
			candidates = append(candidates, scored{
				Item:           item,
				EffectiveScore: pv + eb*effortWeight,
				PriorityValue:  pv,
				EffortBonus:    eb,
			})
		}

		// Sort descending by effective score
		for i := 1; i < len(candidates); i++ {
			for j := i; j > 0 && candidates[j].EffectiveScore > candidates[j-1].EffectiveScore; j-- {
				candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
			}
		}

		if len(candidates) == 0 {
			fmt.Println("No actionable roadmap items found.")
			return nil
		}

		if count > len(candidates) {
			count = len(candidates)
		}
		top := candidates[:count]

		if jsonOutput {
			return printJSON(top)
		}

		for i, s := range top {
			if i == 0 {
				fmt.Printf("→ Next: %s\n", s.Item.Title)
			} else {
				fmt.Printf("\n  %d. %s\n", i+1, s.Item.Title)
			}
			cat := s.Item.Category
			if cat == "" {
				cat = "—"
			}
			fmt.Printf("  Priority: %s | Effort: %s | Category: %s\n", s.Item.Priority, s.Item.Effort, cat)
			fmt.Printf("  Effective score: %.0f (priority=%.0f, effort_bonus=%.0f × %.1f)\n", s.EffectiveScore, s.PriorityValue, s.EffortBonus, effortWeight)
			if i == 0 && s.Item.Description != "" {
				desc := s.Item.Description
				if len(desc) > 200 {
					desc = desc[:200] + "..."
				}
				fmt.Printf("\n  %s\n", desc)
			}
		}
		return nil
	},
}
