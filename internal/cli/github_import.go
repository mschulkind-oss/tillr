package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/engine"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/spf13/cobra"
)

// GitHubIssue represents a GitHub issue from the gh CLI JSON output.
type GitHubIssue struct {
	Number    int              `json:"number"`
	Title     string           `json:"title"`
	Body      string           `json:"body"`
	State     string           `json:"state"`
	Labels    []GitHubLabel    `json:"labels"`
	Milestone *GitHubMilestone `json:"milestone"`
	Assignees []GitHubUser     `json:"assignees"`
}

// GitHubLabel represents a GitHub label.
type GitHubLabel struct {
	Name string `json:"name"`
}

// GitHubMilestone represents a GitHub milestone.
type GitHubMilestone struct {
	Title string `json:"title"`
}

// GitHubUser represents a GitHub user.
type GitHubUser struct {
	Login string `json:"login"`
}

// ImportResult represents the result of importing a single issue.
type ImportResult struct {
	IssueNumber int      `json:"issue_number"`
	Title       string   `json:"title"`
	FeatureID   string   `json:"feature_id"`
	Status      string   `json:"status"`
	Tags        []string `json:"tags,omitempty"`
	MilestoneID string   `json:"milestone_id,omitempty"`
	Skipped     bool     `json:"skipped,omitempty"`
	SkipReason  string   `json:"skip_reason,omitempty"`
}

// ImportSummary represents the overall import summary.
type ImportSummary struct {
	Repo     string         `json:"repo"`
	Total    int            `json:"total"`
	Imported int            `json:"imported"`
	Skipped  int            `json:"skipped"`
	DryRun   bool           `json:"dry_run"`
	Results  []ImportResult `json:"results"`
}

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import data from external sources",
}

var githubImportCmd = &cobra.Command{
	Use:   "github <owner/repo>",
	Short: "Import GitHub issues as tillr features",
	Long: `Import issues from a GitHub repository as tillr features.

Requires the GitHub CLI (gh) to be installed and authenticated.
Install it from: https://cli.github.com/

Each imported issue becomes a feature with:
  - Title from issue title
  - Description from issue body
  - Status mapped: open → draft, closed → done
  - GitHub labels mapped to tillr tags
  - GitHub milestone matched to tillr milestone (if exists)

An event is recorded for each imported issue.`,
	Example: `  # Import open issues from a repo
  tillr import github octocat/hello-world

  # Import with filters
  tillr import github octocat/hello-world --labels bug,enhancement --state all

  # Preview what would be imported
  tillr import github octocat/hello-world --dry-run

  # Import issues from a specific milestone
  tillr import github octocat/hello-world --milestone v1.0`,
	Args: cobra.ExactArgs(1),
	RunE: runGitHubImport,
}

func init() {
	importCmd.AddCommand(githubImportCmd)

	githubImportCmd.Flags().String("state", "open", "Issue state filter: open, closed, or all")
	githubImportCmd.Flags().StringSlice("labels", nil, "Filter by GitHub labels")
	githubImportCmd.Flags().String("milestone", "", "Filter by GitHub milestone name")
	githubImportCmd.Flags().Bool("dry-run", false, "Preview import without making changes")
	githubImportCmd.Flags().Int("limit", 50, "Maximum number of issues to import")
}

func runGitHubImport(cmd *cobra.Command, args []string) error {
	repo := args[0]
	if !strings.Contains(repo, "/") {
		return fmt.Errorf("repo must be in owner/repo format, got %q", repo)
	}

	// Check gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) not found. Install it from: https://cli.github.com/")
	}

	state, _ := cmd.Flags().GetString("state")
	labels, _ := cmd.Flags().GetStringSlice("labels")
	milestone, _ := cmd.Flags().GetString("milestone")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	limit, _ := cmd.Flags().GetInt("limit")

	// Validate state flag
	if state != "open" && state != "closed" && state != "all" {
		return fmt.Errorf("invalid state %q: must be open, closed, or all", state)
	}

	// Fetch issues from GitHub
	issues, err := fetchGitHubIssues(repo, state, labels, milestone, limit)
	if err != nil {
		return fmt.Errorf("fetching GitHub issues: %w", err)
	}

	if len(issues) == 0 {
		if jsonOutput {
			return printJSON(ImportSummary{Repo: repo, DryRun: dryRun})
		}
		fmt.Println("No issues found matching the criteria.")
		return nil
	}

	// Open database
	database, _, err := openDB()
	if err != nil {
		return err
	}
	defer database.Close() //nolint:errcheck

	p, err := db.GetProject(database)
	if err != nil {
		return err
	}

	// Build milestone lookup map
	milestoneMap, err := buildMilestoneMap(database, p.ID)
	if err != nil {
		return fmt.Errorf("loading milestones: %w", err)
	}

	summary := ImportSummary{
		Repo:   repo,
		DryRun: dryRun,
	}

	for _, issue := range issues {
		result := importSingleIssue(database, p.ID, repo, issue, milestoneMap, dryRun)
		summary.Results = append(summary.Results, result)
		if result.Skipped {
			summary.Skipped++
		} else {
			summary.Imported++
		}
	}
	summary.Total = len(issues)

	if jsonOutput {
		return printJSON(summary)
	}

	// Human-readable output
	if dryRun {
		fmt.Println("DRY RUN — no changes made")
		fmt.Println()
	}
	fmt.Printf("Import from %s: %d imported, %d skipped (of %d total)\n\n", repo, summary.Imported, summary.Skipped, summary.Total)
	for _, r := range summary.Results {
		if r.Skipped {
			fmt.Printf("  ⊘ #%d %s — skipped: %s\n", r.IssueNumber, r.Title, r.SkipReason)
		} else {
			tags := ""
			if len(r.Tags) > 0 {
				tags = " [" + strings.Join(r.Tags, ", ") + "]"
			}
			ms := ""
			if r.MilestoneID != "" {
				ms = " → " + r.MilestoneID
			}
			fmt.Printf("  ✓ #%d %s → %s (%s)%s%s\n", r.IssueNumber, r.Title, r.FeatureID, r.Status, tags, ms)
		}
	}
	return nil
}

// fetchGitHubIssues runs gh issue list and parses the JSON output.
func fetchGitHubIssues(repo, state string, labels []string, milestone string, limit int) ([]GitHubIssue, error) {
	ghArgs := []string{
		"issue", "list",
		"--repo", repo,
		"--json", "number,title,body,state,labels,milestone,assignees",
		"--limit", fmt.Sprintf("%d", limit),
		"--state", state,
	}

	if len(labels) > 0 {
		for _, l := range labels {
			ghArgs = append(ghArgs, "--label", l)
		}
	}
	if milestone != "" {
		ghArgs = append(ghArgs, "--milestone", milestone)
	}

	out, err := exec.Command("gh", ghArgs...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh cli error: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	var issues []GitHubIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing gh output: %w", err)
	}
	return issues, nil
}

// parseGitHubIssues parses JSON bytes into GitHubIssue structs.
func parseGitHubIssues(data []byte) ([]GitHubIssue, error) {
	var issues []GitHubIssue
	if err := json.Unmarshal(data, &issues); err != nil {
		return nil, fmt.Errorf("parsing issues JSON: %w", err)
	}
	return issues, nil
}

// mapGitHubStatus converts a GitHub issue state to a tillr feature status.
func mapGitHubStatus(ghState string) string {
	switch strings.ToUpper(ghState) {
	case "CLOSED":
		return "done"
	default:
		return "draft"
	}
}

// mapLabelsToTags converts GitHub labels to tillr tags.
func mapLabelsToTags(labels []GitHubLabel) []string {
	tags := make([]string, 0, len(labels))
	for _, l := range labels {
		tag := strings.ToLower(l.Name)
		tag = strings.ReplaceAll(tag, " ", "-")
		tags = append(tags, tag)
	}
	return tags
}

// buildMilestoneMap creates a map from milestone name (lowered) to milestone ID.
func buildMilestoneMap(database *sql.DB, projectID string) (map[string]string, error) {
	milestones, err := db.ListMilestones(database, projectID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(milestones))
	for _, ms := range milestones {
		m[strings.ToLower(ms.Name)] = ms.ID
	}
	return m, nil
}

// matchMilestone finds a tillr milestone ID matching a GitHub milestone title.
func matchMilestone(ghMilestone *GitHubMilestone, milestoneMap map[string]string) string {
	if ghMilestone == nil || ghMilestone.Title == "" {
		return ""
	}
	if id, ok := milestoneMap[strings.ToLower(ghMilestone.Title)]; ok {
		return id
	}
	return ""
}

// importSingleIssue imports one GitHub issue as a tillr feature.
func importSingleIssue(database *sql.DB, projectID, repo string, issue GitHubIssue, milestoneMap map[string]string, dryRun bool) ImportResult {
	status := mapGitHubStatus(issue.State)
	tags := mapLabelsToTags(issue.Labels)
	milestoneID := matchMilestone(issue.Milestone, milestoneMap)

	featureID := fmt.Sprintf("gh-%d-%s", issue.Number, slug(issue.Title))
	// Truncate long IDs
	if len(featureID) > 60 {
		featureID = featureID[:60]
	}

	result := ImportResult{
		IssueNumber: issue.Number,
		Title:       issue.Title,
		FeatureID:   featureID,
		Status:      status,
		Tags:        tags,
		MilestoneID: milestoneID,
	}

	if dryRun {
		return result
	}

	// Create the feature
	desc := issue.Body
	if desc == "" {
		desc = fmt.Sprintf("Imported from GitHub issue %s#%d", repo, issue.Number)
	}

	f, err := engine.AddFeature(database, projectID, issue.Title, desc, "", milestoneID, 0, nil, "")
	if err != nil {
		// Likely duplicate — skip it
		result.Skipped = true
		result.SkipReason = fmt.Sprintf("could not create feature: %v", err)
		return result
	}
	result.FeatureID = f.ID

	// Set status if not draft
	if status != "draft" {
		if err := db.SetFeatureStatus(database, f.ID, status); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not set status for %s: %v\n", f.ID, err)
		}
	}

	// Add tags (GitHub labels + source tag)
	for _, tag := range tags {
		_ = db.AddFeatureTag(database, f.ID, tag)
	}
	_ = db.AddFeatureTag(database, f.ID, "github-import")

	// Record import event
	eventData := fmt.Sprintf(`{"source":"github","repo":%q,"issue_number":%d,"issue_state":%q}`,
		repo, issue.Number, issue.State)
	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: f.ID,
		EventType: "feature.imported",
		Data:      eventData,
	})

	return result
}

// slug generates a URL-friendly ID from a name (mirrors engine.slug).
func slug(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, s)
	return s
}
