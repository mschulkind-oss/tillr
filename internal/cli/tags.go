package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/spf13/cobra"
)

var tagCmd = &cobra.Command{
	Use:   "tag",
	Short: "Manage project-wide tags",
	Long: `Manage tags that can be applied to features for categorization.

  tillr tag add <name>    Add a tag (idempotent)
  tillr tag list          List all tags with feature counts`,
}

var tagAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a tag by applying it to enable it for use",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		tagName := strings.TrimSpace(args[0])
		if tagName == "" {
			return fmt.Errorf("tag name cannot be empty")
		}

		if jsonOutput {
			return printJSON(map[string]string{"tag": tagName, "status": "available"})
		}
		fmt.Printf("Tag %q is available for use. Apply it with: tillr feature tag <feature-id> %s\n", tagName, tagName)
		return nil
	},
}

var tagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tags with feature counts",
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

		tags, err := db.ListAllTags(database, p.ID)
		if err != nil {
			return fmt.Errorf("listing tags: %w", err)
		}

		if jsonOutput {
			if tags == nil {
				tags = []models.TagCount{}
			}
			return printJSON(tags)
		}

		if len(tags) == 0 {
			fmt.Println("No tags found. Add tags with: tillr feature tag <feature-id> <tag>")
			return nil
		}

		fmt.Printf("%-30s %s\n", "TAG", "FEATURES")
		fmt.Printf("%-30s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 8))
		for _, t := range tags {
			fmt.Printf("%-30s %d\n", t.Tag, t.Count)
		}
		return nil
	},
}

func init() {
	tagCmd.AddCommand(tagAddCmd)
	tagCmd.AddCommand(tagListCmd)
}
