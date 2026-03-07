package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage context entries",
}

func init() {
	contextCmd.AddCommand(contextAddCmd)
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextShowCmd)
	contextCmd.AddCommand(contextSearchCmd)

	contextAddCmd.Flags().String("feature", "", "Feature ID to associate with")
	contextAddCmd.Flags().String("type", "note", "Context type (note, source-analysis, doc, spec)")
	contextAddCmd.Flags().String("title", "", "Title (required)")
	contextAddCmd.Flags().String("content", "", "Markdown content (required)")
	contextAddCmd.Flags().String("author", "agent", "Author")
	contextAddCmd.Flags().String("tags", "", "Comma-separated tags")

	contextListCmd.Flags().String("feature", "", "Filter by feature ID")
	contextListCmd.Flags().String("type", "", "Filter by context type")
}

var contextAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a context entry",
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		if title == "" {
			return fmt.Errorf("--title is required")
		}
		content, _ := cmd.Flags().GetString("content")
		if content == "" {
			return fmt.Errorf("--content is required")
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		feature, _ := cmd.Flags().GetString("feature")
		contextType, _ := cmd.Flags().GetString("type")
		author, _ := cmd.Flags().GetString("author")
		tags, _ := cmd.Flags().GetString("tags")

		e := &models.ContextEntry{
			ProjectID:   p.ID,
			FeatureID:   feature,
			ContextType: contextType,
			Title:       title,
			ContentMD:   content,
			Author:      author,
			Tags:        tags,
		}

		if err := db.InsertContext(database, e); err != nil {
			return fmt.Errorf("adding context: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: feature,
			EventType: "context.added",
			Data:      fmt.Sprintf(`{"context_id":%d,"title":%q,"type":%q}`, e.ID, title, contextType),
		})

		if jsonOutput {
			return printJSON(e)
		}
		fmt.Printf("✓ Added context #%d: %s\n", e.ID, title)
		return nil
	},
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List context entries",
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

		feature, _ := cmd.Flags().GetString("feature")
		contextType, _ := cmd.Flags().GetString("type")

		entries, err := db.ListContext(database, p.ID, feature)
		if err != nil {
			return err
		}

		// Filter by type client-side since ListContext doesn't support it
		if contextType != "" {
			var filtered []models.ContextEntry
			for _, e := range entries {
				if e.ContextType == contextType {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		if jsonOutput {
			return printJSON(entries)
		}

		if len(entries) == 0 {
			fmt.Println("No context entries found.")
			return nil
		}

		fmt.Printf("%-6s %-16s %-10s %-8s %s\n", "ID", "TYPE", "FEATURE", "AUTHOR", "TITLE")
		fmt.Println(strings.Repeat("─", 65))
		for _, e := range entries {
			feature := e.FeatureID
			if feature == "" {
				feature = "-"
			}
			fmt.Printf("%-6d %-16s %-10s %-8s %s\n", e.ID, e.ContextType, feature, e.Author, e.Title)
		}
		return nil
	},
}

var contextShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show context entry details",
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

		// List all and find by ID (no GetContext function available)
		entries, err := db.ListContext(database, p.ID, "")
		if err != nil {
			return err
		}

		var found *models.ContextEntry
		for i := range entries {
			if fmt.Sprintf("%d", entries[i].ID) == args[0] {
				found = &entries[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("context entry not found: %s", args[0])
		}

		if jsonOutput {
			return printJSON(found)
		}

		fmt.Printf("Context #%d: %s\n", found.ID, found.Title)
		fmt.Printf("  Type:    %s\n", found.ContextType)
		fmt.Printf("  Author:  %s\n", found.Author)
		if found.FeatureID != "" {
			fmt.Printf("  Feature: %s\n", found.FeatureID)
		}
		if found.Tags != "" {
			fmt.Printf("  Tags:    %s\n", found.Tags)
		}
		fmt.Printf("  Created: %s\n", found.CreatedAt)
		fmt.Printf("\n%s\n", found.ContentMD)
		return nil
	},
}

var contextSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search context entries",
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

		results, err := db.SearchContext(database, p.ID, args[0])
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(results)
		}

		if len(results) == 0 {
			fmt.Println("No matching context entries.")
			return nil
		}

		fmt.Printf("%-6s %-16s %-8s %s\n", "ID", "TYPE", "AUTHOR", "TITLE")
		fmt.Println(strings.Repeat("─", 55))
		for _, e := range results {
			fmt.Printf("%-6d %-16s %-8s %s\n", e.ID, e.ContextType, e.Author, e.Title)
		}
		return nil
	},
}
