package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/engine"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

// JiraExport represents the top-level Jira JSON export structure.
type JiraExport struct {
	Projects []JiraProject `json:"projects"`
}

// JiraProject represents a project in the Jira export.
type JiraProject struct {
	Key    string      `json:"key"`
	Issues []JiraIssue `json:"issues"`
}

// JiraIssue represents an issue in the Jira export.
type JiraIssue struct {
	Key    string     `json:"key"`
	Fields JiraFields `json:"fields"`
}

// JiraFields contains the fields of a Jira issue.
type JiraFields struct {
	Summary     string        `json:"summary"`
	Description string        `json:"description"`
	Status      JiraStatus    `json:"status"`
	Priority    JiraPriority  `json:"priority"`
	Labels      []string      `json:"labels"`
	FixVersions []JiraVersion `json:"fixVersions"`
}

// JiraStatus represents the status of a Jira issue.
type JiraStatus struct {
	Name string `json:"name"`
}

// JiraPriority represents the priority of a Jira issue.
type JiraPriority struct {
	Name string `json:"name"`
}

// JiraVersion represents a fix version in Jira.
type JiraVersion struct {
	Name string `json:"name"`
}

// JiraImportResult represents the result of importing a single Jira issue.
type JiraImportResult struct {
	IssueKey    string   `json:"issue_key"`
	Title       string   `json:"title"`
	FeatureID   string   `json:"feature_id"`
	Status      string   `json:"status"`
	Tags        []string `json:"tags,omitempty"`
	MilestoneID string   `json:"milestone_id,omitempty"`
	Skipped     bool     `json:"skipped,omitempty"`
	SkipReason  string   `json:"skip_reason,omitempty"`
}

// JiraImportSummary represents the overall Jira import summary.
type JiraImportSummary struct {
	Source   string             `json:"source"`
	File     string             `json:"file"`
	Project  string             `json:"project,omitempty"`
	Total    int                `json:"total"`
	Imported int                `json:"imported"`
	Skipped  int                `json:"skipped"`
	DryRun   bool               `json:"dry_run"`
	Results  []JiraImportResult `json:"results"`
}

var jiraImportCmd = &cobra.Command{
	Use:   "jira <file.json>",
	Short: "Import Jira issues as tillr features",
	Long: `Import issues from a Jira JSON export file as tillr features.

Each imported issue becomes a feature with:
  - Title from issue summary
  - Description from issue description
  - Status mapped from Jira status (To Do/Open/Backlog → draft,
    In Progress/In Development → implementing, In Review/In QA → human-qa,
    Done/Closed/Resolved → done)
  - Jira labels mapped to tillr tags
  - Jira fixVersions matched to tillr milestones (if they exist)

An event is recorded for each imported issue.`,
	Example: `  # Import all issues from a Jira export
  tillr import jira export.json

  # Import only issues from project PROJ
  tillr import jira export.json --project PROJ

  # Preview what would be imported
  tillr import jira export.json --dry-run

  # Limit the number of imported issues
  tillr import jira export.json --limit 10`,
	Args: cobra.ExactArgs(1),
	RunE: runJiraImport,
}

func init() {
	importCmd.AddCommand(jiraImportCmd)

	jiraImportCmd.Flags().String("project", "", "Filter by Jira project key")
	jiraImportCmd.Flags().Bool("dry-run", false, "Preview import without making changes")
	jiraImportCmd.Flags().Int("limit", 0, "Maximum number of issues to import (0 = no limit)")
}

func runJiraImport(cmd *cobra.Command, args []string) error {
	filePath := args[0]
	projectKey, _ := cmd.Flags().GetString("project")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	limit, _ := cmd.Flags().GetInt("limit")

	// Parse the Jira export file
	issues, err := parseJiraExportFile(filePath, projectKey)
	if err != nil {
		return fmt.Errorf("parsing Jira export: %w", err)
	}

	// Apply limit
	if limit > 0 && len(issues) > limit {
		issues = issues[:limit]
	}

	if len(issues) == 0 {
		if jsonOutput {
			return printJSON(JiraImportSummary{
				Source:  "jira",
				File:    filePath,
				Project: projectKey,
				DryRun:  dryRun,
			})
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

	summary := JiraImportSummary{
		Source:  "jira",
		File:    filePath,
		Project: projectKey,
		DryRun:  dryRun,
	}

	for _, issue := range issues {
		result := importSingleJiraIssue(database, p.ID, issue, milestoneMap, dryRun)
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
	fmt.Printf("Import from %s: %d imported, %d skipped (of %d total)\n\n", filePath, summary.Imported, summary.Skipped, summary.Total)
	for _, r := range summary.Results {
		if r.Skipped {
			fmt.Printf("  ⊘ %s %s — skipped: %s\n", r.IssueKey, r.Title, r.SkipReason)
		} else {
			tags := ""
			if len(r.Tags) > 0 {
				tags = " [" + strings.Join(r.Tags, ", ") + "]"
			}
			ms := ""
			if r.MilestoneID != "" {
				ms = " → " + r.MilestoneID
			}
			fmt.Printf("  ✓ %s %s → %s (%s)%s%s\n", r.IssueKey, r.Title, r.FeatureID, r.Status, tags, ms)
		}
	}
	return nil
}

// parseJiraExportFile reads and parses a Jira JSON export file.
func parseJiraExportFile(filePath, projectKey string) ([]JiraIssue, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", filePath, err)
	}
	return parseJiraExport(data, projectKey)
}

// parseJiraExport parses Jira JSON export bytes and returns matching issues.
func parseJiraExport(data []byte, projectKey string) ([]JiraIssue, error) {
	var export JiraExport
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("parsing Jira JSON: %w", err)
	}

	var issues []JiraIssue
	for _, proj := range export.Projects {
		if projectKey != "" && !strings.EqualFold(proj.Key, projectKey) {
			continue
		}
		issues = append(issues, proj.Issues...)
	}
	return issues, nil
}

// mapJiraStatus converts a Jira status name to a tillr feature status.
func mapJiraStatus(jiraStatus string) string {
	switch strings.ToLower(strings.TrimSpace(jiraStatus)) {
	case "to do", "open", "backlog":
		return "draft"
	case "in progress", "in development":
		return "implementing"
	case "in review", "in qa":
		return "human-qa"
	case "done", "closed", "resolved":
		return "done"
	default:
		return "draft"
	}
}

// mapJiraLabelsToTags converts Jira labels to tillr tags.
func mapJiraLabelsToTags(labels []string) []string {
	tags := make([]string, 0, len(labels))
	for _, l := range labels {
		tag := strings.ToLower(l)
		tag = strings.ReplaceAll(tag, " ", "-")
		tags = append(tags, tag)
	}
	return tags
}

// matchJiraVersion finds a tillr milestone ID matching a Jira fixVersion.
func matchJiraVersion(versions []JiraVersion, milestoneMap map[string]string) string {
	for _, v := range versions {
		if id, ok := milestoneMap[strings.ToLower(v.Name)]; ok {
			return id
		}
	}
	return ""
}

// importSingleJiraIssue imports one Jira issue as a tillr feature.
func importSingleJiraIssue(database *sql.DB, projectID string, issue JiraIssue, milestoneMap map[string]string, dryRun bool) JiraImportResult {
	status := mapJiraStatus(issue.Fields.Status.Name)
	tags := mapJiraLabelsToTags(issue.Fields.Labels)
	milestoneID := matchJiraVersion(issue.Fields.FixVersions, milestoneMap)

	featureID := fmt.Sprintf("jira-%s", strings.ToLower(issue.Key))
	if len(featureID) > 60 {
		featureID = featureID[:60]
	}

	result := JiraImportResult{
		IssueKey:    issue.Key,
		Title:       issue.Fields.Summary,
		FeatureID:   featureID,
		Status:      status,
		Tags:        tags,
		MilestoneID: milestoneID,
	}

	if dryRun {
		return result
	}

	// Create the feature
	desc := issue.Fields.Description
	if desc == "" {
		desc = fmt.Sprintf("Imported from Jira issue %s", issue.Key)
	}

	f, err := engine.AddFeature(database, projectID, issue.Fields.Summary, desc, "", milestoneID, 0, nil, "")
	if err != nil {
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

	// Add tags (Jira labels + source tag)
	for _, tag := range tags {
		_ = db.AddFeatureTag(database, f.ID, tag)
	}
	_ = db.AddFeatureTag(database, f.ID, "jira-import")

	// Record import event
	eventData := fmt.Sprintf(`{"source":"jira","issue_key":%q,"jira_status":%q}`,
		issue.Key, issue.Fields.Status.Name)
	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: f.ID,
		EventType: "feature.imported",
		Data:      eventData,
	})

	return result
}
