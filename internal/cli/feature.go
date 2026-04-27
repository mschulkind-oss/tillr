package cli

import (
	"fmt"
	"strconv"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

var featureCmd = &cobra.Command{
	Use:     "feature",
	Aliases: []string{"f"},
	Short:   "Manage features",
}

var featureAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Create a new feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		project, err := db.GetProject(database)
		if err != nil {
			return fmt.Errorf("loading project: %w", err)
		}

		desc, _ := cmd.Flags().GetString("description")
		feature, err := db.AddFeature(database, project.ID, args[0], desc)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(feature)
		}
		fmt.Printf("#%d  %s  [%s]\n", feature.ID, feature.Title, feature.Status)
		return nil
	},
}

var featureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List features",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		project, err := db.GetProject(database)
		if err != nil {
			return fmt.Errorf("loading project: %w", err)
		}

		features, err := db.ListFeatures(database, project.ID)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(features)
		}
		if len(features) == 0 {
			fmt.Println("No features yet. Add one with 'tillr feature add \"Title\"'.")
			return nil
		}
		for _, f := range features {
			fmt.Printf("#%-4d  %-12s  %s\n", f.ID, f.Status, f.Title)
		}
		return nil
	},
}

var featureShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a feature and its comment thread",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid feature ID %q", args[0])
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		feature, err := db.GetFeature(database, id)
		if err != nil {
			return err
		}
		comments, err := db.ListComments(database, "feature", strconv.FormatInt(id, 10))
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"feature":  feature,
				"comments": comments,
			})
		}
		fmt.Printf("#%d  %s  [%s]\n", feature.ID, feature.Title, feature.Status)
		if feature.Description != "" {
			fmt.Printf("\n%s\n", feature.Description)
		}
		if len(comments) == 0 {
			fmt.Println("\n(no comments yet)")
			return nil
		}
		fmt.Printf("\n%d comment(s):\n", len(comments))
		for _, c := range comments {
			author := c.AuthorType
			if c.AuthorRole != "" {
				author = author + "/" + c.AuthorRole
			}
			fmt.Printf("\n[%s — %s]\n%s\n", author, c.CreatedAt.Format("2006-01-02 15:04"), c.Body)
		}
		return nil
	},
}

func init() {
	featureAddCmd.Flags().StringP("description", "d", "", "Feature description")

	featureCmd.AddCommand(featureAddCmd)
	featureCmd.AddCommand(featureListCmd)
	featureCmd.AddCommand(featureShowCmd)
}
