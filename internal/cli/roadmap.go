package cli

import (
	"fmt"
	"strings"

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

	roadmapShowCmd.Flags().String("format", "table", "Output format (table, json, markdown)")

	roadmapAddCmd.Flags().String("description", "", "Item description")
	roadmapAddCmd.Flags().String("category", "", "Item category")
	roadmapAddCmd.Flags().String("priority", "medium", "Priority (critical, high, medium, low, nice-to-have)")
	roadmapAddCmd.Flags().Int("order", 0, "Sort order")

	roadmapEditCmd.Flags().String("title", "", "New title")
	roadmapEditCmd.Flags().String("description", "", "New description")
	roadmapEditCmd.Flags().String("category", "", "New category")
	roadmapEditCmd.Flags().String("priority", "", "New priority")
	roadmapEditCmd.Flags().String("status", "", "New status")

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

		// Table format
		fmt.Printf("%-20s %-12s %-12s %-12s %s\n", "ID", "PRIORITY", "STATUS", "CATEGORY", "TITLE")
		fmt.Println(strings.Repeat("─", 80))
		for _, r := range items {
			cat := r.Category
			if cat == "" {
				cat = "—"
			}
			fmt.Printf("%-20s %-12s %-12s %-12s %s\n", r.ID, r.Priority, r.Status, cat, r.Title)
		}
		return nil
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
		order, _ := cmd.Flags().GetInt("order")

		id := engine.Slug(args[0])
		r := &models.RoadmapItem{
			ID:          id,
			ProjectID:   p.ID,
			Title:       args[0],
			Description: desc,
			Category:    category,
			Priority:    priority,
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
			return printJSON(items)
		}

		return printRoadmapMarkdown(p.Name, items)
	},
}

func printRoadmapMarkdown(projectName string, items []models.RoadmapItem) error {
	fmt.Printf("# %s — Roadmap\n\n", projectName)

	groups := map[string][]models.RoadmapItem{}
	priorities := []string{"critical", "high", "medium", "low", "nice-to-have"}
	for _, r := range items {
		groups[r.Priority] = append(groups[r.Priority], r)
	}

	for _, pri := range priorities {
		if ritems, ok := groups[pri]; ok && len(ritems) > 0 {
			fmt.Printf("## %s %s\n\n", priorityEmoji(pri), strings.Title(pri)) //nolint:staticcheck
			for _, r := range ritems {
				check := " "
				if r.Status == "done" {
					check = "x"
				}
				fmt.Printf("- [%s] **%s**", check, r.Title)
				if r.Category != "" {
					fmt.Printf(" _%s_", r.Category)
				}
				if r.Description != "" {
					fmt.Printf(" — %s", r.Description)
				}
				fmt.Println()
			}
			fmt.Println()
		}
	}
	return nil
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
