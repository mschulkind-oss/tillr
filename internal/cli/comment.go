package cli

import (
	"fmt"
	"strconv"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

// commentCmd is the Stage 1 (Layer 1) entry point: humans and agents
// add comments to features, the substrate for everything else in
// docs/consulting-firm/implementation-layers.md.
var commentCmd = &cobra.Command{
	Use:   "comment <feature-id> <body>",
	Short: "Add a comment to a feature",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		featureID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid feature ID %q", args[0])
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Verify the feature exists.
		if _, err := db.GetFeature(database, featureID); err != nil {
			return fmt.Errorf("feature #%d not found", featureID)
		}

		role, _ := cmd.Flags().GetString("role")
		authorType := "human"
		if role != "" {
			authorType = "agent"
		}

		comment, err := db.AddComment(database, &models.Comment{
			EntityType: "feature",
			EntityID:   strconv.FormatInt(featureID, 10),
			AuthorType: authorType,
			AuthorRole: role,
			Body:       args[1],
		})
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(comment)
		}
		author := comment.AuthorType
		if comment.AuthorRole != "" {
			author = author + "/" + comment.AuthorRole
		}
		fmt.Printf("Comment added on #%d by %s\n", featureID, author)
		return nil
	},
}

func init() {
	commentCmd.Flags().StringP("role", "r", "",
		"Agent role (e.g. 'implementer', 'reviewer'). If set, author_type is 'agent'.")
}
