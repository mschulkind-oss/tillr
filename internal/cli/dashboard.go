package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Manage custom dashboard configurations",
}

func init() {
	dashboardCmd.AddCommand(dashboardListCmd)
	dashboardCmd.AddCommand(dashboardCreateCmd)
	dashboardCmd.AddCommand(dashboardShowCmd)
	dashboardCmd.AddCommand(dashboardSetDefaultCmd)
	dashboardCmd.AddCommand(dashboardRemoveCmd)
}

// defaultWidgets returns the starter widget set for new dashboards.
func defaultWidgets() []models.DashboardWidget {
	return []models.DashboardWidget{
		{Type: "feature-summary", Title: "Features", Size: "medium"},
		{Type: "milestone-progress", Title: "Milestones", Size: "medium"},
		{Type: "roadmap-overview", Title: "Roadmap", Size: "large"},
		{Type: "recent-activity", Title: "Recent Activity", Size: "small"},
	}
}

var dashboardListCmd = &cobra.Command{
	Use:   "list",
	Short: "List dashboard configurations",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		configs, err := db.ListDashboardConfigs(database, p.ID)
		if err != nil {
			return err
		}

		if jsonOutput {
			if configs == nil {
				configs = []models.DashboardConfig{}
			}
			return printJSON(configs)
		}

		if len(configs) == 0 {
			fmt.Println("No dashboard configurations found.")
			return nil
		}

		for _, c := range configs {
			def := ""
			if c.IsDefault {
				def = " (default)"
			}
			fmt.Printf("%-20s %s%s\n", c.ID, c.Name, def)
		}
		return nil
	},
}

var dashboardCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new dashboard configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		layout, err := json.Marshal(defaultWidgets())
		if err != nil {
			return fmt.Errorf("marshalling default layout: %w", err)
		}

		id := strings.ToLower(strings.ReplaceAll(args[0], " ", "-"))
		dc := &models.DashboardConfig{
			ID:        id,
			ProjectID: p.ID,
			Name:      args[0],
			Layout:    layout,
		}

		if err := db.CreateDashboardConfig(database, dc); err != nil {
			return fmt.Errorf("creating dashboard config %q: %w", args[0], err)
		}

		// Re-read to get created_at from DB default.
		dc, err = db.GetDashboardConfig(database, id)
		if err != nil {
			return fmt.Errorf("reading created dashboard config: %w", err)
		}

		if jsonOutput {
			return printJSON(dc)
		}
		fmt.Printf("✓ Created dashboard %q (id: %s)\n", dc.Name, dc.ID)
		return nil
	},
}

var dashboardShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show dashboard configuration details",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		dc, err := db.GetDashboardConfig(database, args[0])
		if err != nil {
			return fmt.Errorf("dashboard %q not found. Run 'tillr dashboard list' to see available dashboards", args[0])
		}

		if jsonOutput {
			return printJSON(dc)
		}

		def := ""
		if dc.IsDefault {
			def = " (default)"
		}
		fmt.Printf("Dashboard: %s%s\n", dc.Name, def)
		fmt.Printf("  ID:      %s\n", dc.ID)
		fmt.Printf("  Created: %s\n", dc.CreatedAt)

		var widgets []models.DashboardWidget
		if err := json.Unmarshal(dc.Layout, &widgets); err == nil {
			fmt.Printf("  Widgets: %d\n", len(widgets))
			for _, w := range widgets {
				fmt.Printf("    - [%s] %s (%s)\n", w.Type, w.Title, w.Size)
			}
		}
		return nil
	},
}

var dashboardSetDefaultCmd = &cobra.Command{
	Use:   "set-default <id>",
	Short: "Set a dashboard as the default",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		if err := db.SetDefaultDashboard(database, p.ID, args[0]); err != nil {
			return fmt.Errorf("setting default dashboard: %w", err)
		}

		dc, err := db.GetDashboardConfig(database, args[0])
		if err != nil {
			return fmt.Errorf("reading dashboard config: %w", err)
		}

		if jsonOutput {
			return printJSON(dc)
		}
		fmt.Printf("✓ Dashboard %q is now the default\n", dc.Name)
		return nil
	},
}

var dashboardRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a dashboard configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.DeleteDashboardConfig(database, args[0]); err != nil {
			return fmt.Errorf("removing dashboard %q: %w", args[0], err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"deleted": args[0]})
		}
		fmt.Printf("✓ Removed dashboard %q\n", args[0])
		return nil
	},
}
