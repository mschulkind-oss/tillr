package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

// prURLPattern matches GitHub PR URLs: https://github.com/owner/repo/pull/123
var prURLPattern = regexp.MustCompile(`^https?://github\.com/([^/]+/[^/]+)/pull/(\d+)(?:/.*)?$`)

// ParsePRURL extracts repo (owner/repo) and PR number from a GitHub PR URL.
func ParsePRURL(url string) (repo string, number int, err error) {
	m := prURLPattern.FindStringSubmatch(url)
	if m == nil {
		return "", 0, fmt.Errorf("invalid GitHub PR URL: %q (expected https://github.com/owner/repo/pull/123)", url)
	}
	n, _ := strconv.Atoi(m[2])
	return m[1], n, nil
}

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Manage pull request links for features",
	Long: `Associate GitHub pull requests with features for traceability.

  lifecycle pr link <feature-id> <pr-url>    Link a PR to a feature
  lifecycle pr list [--feature <id>]          List PR links
  lifecycle pr unlink <feature-id> <pr-url>   Remove a PR link`,
}

var prLinkCmd = &cobra.Command{
	Use:   "link <feature-id> <pr-url>",
	Short: "Link a pull request to a feature",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID := args[0]
		prURL := args[1]

		// Validate feature exists
		if _, err := db.GetFeature(database, featureID); err != nil {
			return fmt.Errorf("feature %q not found", featureID)
		}

		repo, number, err := ParsePRURL(prURL)
		if err != nil {
			return err
		}

		pr := &models.FeaturePR{
			FeatureID: featureID,
			PRURL:     prURL,
			PRNumber:  number,
			Repo:      repo,
			Status:    "open",
		}
		if err := db.LinkFeaturePR(database, pr); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint") {
				return fmt.Errorf("PR %s is already linked to feature %s", prURL, featureID)
			}
			return fmt.Errorf("linking PR: %w", err)
		}

		// Record event
		p, _ := db.GetProject(database)
		if p != nil {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: featureID,
				EventType: "pr_linked",
				Data:      fmt.Sprintf(`{"pr_url":%q,"pr_number":%d,"repo":%q}`, prURL, number, repo),
			})
		}

		if jsonOutput {
			return printJSON(pr)
		}
		fmt.Printf("Linked PR #%d (%s) → feature %s\n", number, repo, featureID)
		return nil
	},
}

var prListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pull request links",
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID, _ := cmd.Flags().GetString("feature")

		var prs []models.FeaturePR
		if featureID != "" {
			prs, err = db.ListFeaturePRs(database, featureID)
		} else {
			prs, err = db.ListAllPRs(database)
		}
		if err != nil {
			return fmt.Errorf("listing PRs: %w", err)
		}
		if prs == nil {
			prs = []models.FeaturePR{}
		}

		if jsonOutput {
			return printJSON(prs)
		}

		if len(prs) == 0 {
			fmt.Println("No PR links found.")
			return nil
		}

		fmt.Printf("%-20s %-8s %-30s %-8s %s\n", "FEATURE", "PR#", "REPO", "STATUS", "URL")
		for _, pr := range prs {
			fmt.Printf("%-20s %-8d %-30s %-8s %s\n",
				pr.FeatureID, pr.PRNumber, pr.Repo, pr.Status, pr.PRURL)
		}
		return nil
	},
}

var prUnlinkCmd = &cobra.Command{
	Use:   "unlink <feature-id> <pr-url>",
	Short: "Remove a pull request link from a feature",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID := args[0]
		prURL := args[1]

		if err := db.UnlinkFeaturePR(database, featureID, prURL); err != nil {
			return err
		}

		// Record event
		p, _ := db.GetProject(database)
		if p != nil {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: featureID,
				EventType: "pr_unlinked",
				Data:      fmt.Sprintf(`{"pr_url":%q}`, prURL),
			})
		}

		if jsonOutput {
			return printJSON(map[string]string{
				"status":     "unlinked",
				"feature_id": featureID,
				"pr_url":     prURL,
			})
		}
		fmt.Printf("Unlinked PR %s from feature %s\n", prURL, featureID)
		return nil
	},
}

func init() {
	prListCmd.Flags().StringP("feature", "f", "", "Filter by feature ID")
	prCmd.AddCommand(prLinkCmd)
	prCmd.AddCommand(prListCmd)
	prCmd.AddCommand(prUnlinkCmd)
}
