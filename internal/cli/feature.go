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
	Short:   "Manage features (the unit of work)",
	Long: `Features are tillr's unit of trackable work. Each has a title,
optional description, status, and optional target_persona. Features
flow through statuses: draft → claimed → done | blocked | human-qa.

The orchestrator daemon claims pending features by persona and
dispatches them to claude -p — see 'tillr orchestrator --help'.`,
}

var featureAddCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Create a new feature",
	Long: `Create a new feature. By default it lands in 'draft' status with
no target persona. Use --persona to type the work for a specific
persona (so the orchestrator routes it correctly), --description for
a longer spec, --status to set an initial state.`,
	Example: `  tillr feature add "User authentication"
  tillr feature add "Implement OAuth" --persona implementer --description "Use coreos/go-oidc"
  tillr feature add "Investigate caching strategies" --persona researcher
  tillr --json feature add "Quick task"`,
	Args: cobra.ExactArgs(1),
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
		persona, _ := cmd.Flags().GetString("persona")
		status, _ := cmd.Flags().GetString("status")
		feature, err := db.AddFeature(database, project.ID, args[0], desc, persona)
		if err != nil {
			return err
		}
		if status != "" && status != feature.Status {
			feature, err = db.SetFeatureStatus(database, feature.ID, status)
			if err != nil {
				return err
			}
		}

		if jsonOutput {
			return printJSON(feature)
		}
		ref := Code(fmt.Sprintf("#%d", feature.ID))
		st := "[" + Status(feature.Status) + "]"
		var personaPart string
		if feature.TargetPersona != "" {
			personaPart = "  " + Dim("→") + Persona(feature.TargetPersona)
		}
		fmt.Printf("%s %s  %s%s\n", ref, st, feature.Title, personaPart)
		return nil
	},
}

var featureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List features",
	Long: `List features in the current project, newest first. Filter by
target persona or status.`,
	Example: `  tillr feature list
  tillr feature list --persona implementer
  tillr feature list --status draft
  tillr --json feature list`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		project, err := db.GetProject(database)
		if err != nil {
			return fmt.Errorf("loading project: %w", err)
		}

		filter := db.ListFeaturesFilter{}
		filter.Persona, _ = cmd.Flags().GetString("persona")
		filter.Status, _ = cmd.Flags().GetString("status")

		features, err := db.ListFeatures(database, project.ID, filter)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(features)
		}
		if len(features) == 0 {
			fmt.Println(Dim("No features match. Add one with 'tillr feature add \"Title\"'."))
			return nil
		}
		for _, f := range features {
			ref := Code(fmt.Sprintf("#%-4d", f.ID))
			// Pad status before colorizing so widths line up.
			padded := fmt.Sprintf("%-12s", f.Status)
			st := Status(padded)
			var personaPart string
			if f.TargetPersona != "" {
				personaPart = Dim("→") + Persona(f.TargetPersona) + "  "
			}
			fmt.Printf("%s  %s  %s%s\n", ref, st, personaPart, f.Title)
		}
		return nil
	},
}

var featureShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a feature and its comment thread",
	Long: `Display a feature with its full description and comment thread.
Comments are sorted oldest first; agents and humans appear inline.`,
	Example: `  tillr feature show 4
  tillr --json feature show 4`,
	Args: cobra.ExactArgs(1),
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

		ref := Code(fmt.Sprintf("#%d", feature.ID))
		st := "[" + Status(feature.Status) + "]"
		var personaPart string
		if feature.TargetPersona != "" {
			personaPart = "  " + Dim("→") + Persona(feature.TargetPersona)
		}
		fmt.Printf("%s %s %s%s\n", ref, Bold(feature.Title), st, personaPart)
		fmt.Println(Dim(fmt.Sprintf("Created: %s   Updated: %s",
			feature.CreatedAt.Format("2006-01-02 15:04"),
			feature.UpdatedAt.Format("2006-01-02 15:04"))))
		if feature.Description != "" {
			fmt.Printf("\n%s\n", feature.Description)
		}
		if len(comments) == 0 {
			fmt.Println("\n" + Dim("(no comments yet)"))
			return nil
		}
		fmt.Printf("\n%s\n", Header(fmt.Sprintf("Comments (%d)", len(comments))))
		for _, c := range comments {
			authorLabel := c.AuthorType
			if c.AuthorRole != "" {
				authorLabel = c.AuthorType + "/" + Persona(c.AuthorRole)
			}
			ts := c.CreatedAt.Format("2006-01-02 15:04")
			fmt.Printf("\n%s %s\n%s\n",
				Dim("["+ts+"]"), authorLabel, c.Body)
		}
		return nil
	},
}

var featureStatusCmd = &cobra.Command{
	Use:   "status <id> <new-status>",
	Short: "Set a feature's status (e.g. draft, claimed, done, blocked)",
	Long: `Transition a feature to a new status. The orchestrator handles
status transitions automatically per run; this command is for manual
correction (e.g. closing out a feature whose work landed before the
orchestrator picked it up).`,
	Example: `  tillr feature status 4 done
  tillr feature status 4 blocked
  tillr --json feature status 4 done`,
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid feature ID %q", args[0])
		}
		newStatus := args[1]
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck
		feature, err := db.SetFeatureStatus(database, id, newStatus)
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(feature)
		}
		fmt.Printf("%s %s  %s  [%s]\n",
			Success("✓"),
			Code(fmt.Sprintf("#%d", feature.ID)),
			feature.Title,
			Status(feature.Status))
		return nil
	},
}

var featureDoneCmd = &cobra.Command{
	Use:     "done <id>",
	Short:   "Mark a feature done (alias for 'feature status <id> done')",
	Example: `  tillr feature done 4`,
	Args:    cobra.ExactArgs(1),
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
		feature, err := db.SetFeatureStatus(database, id, "done")
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(feature)
		}
		fmt.Printf("%s %s  %s  [%s]\n",
			Success("✓"),
			Code(fmt.Sprintf("#%d", feature.ID)),
			feature.Title,
			Status(feature.Status))
		return nil
	},
}

func init() {
	featureAddCmd.Flags().StringP("description", "d", "", "Feature description")
	featureAddCmd.Flags().StringP("persona", "p", "",
		"Target persona (e.g. implementer, researcher, reviewer)")
	featureAddCmd.Flags().StringP("status", "s", "",
		"Initial status (default: draft)")

	featureListCmd.Flags().StringP("persona", "p", "", "Filter by target persona")
	featureListCmd.Flags().StringP("status", "s", "", "Filter by status")

	featureCmd.AddCommand(featureAddCmd)
	featureCmd.AddCommand(featureStatusCmd)
	featureCmd.AddCommand(featureDoneCmd)
	featureCmd.AddCommand(featureListCmd)
	featureCmd.AddCommand(featureShowCmd)
}
