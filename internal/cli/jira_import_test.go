package cli

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
)

func TestParseJiraExport(t *testing.T) {
	raw := `{
		"projects": [
			{
				"key": "PROJ",
				"issues": [
					{
						"key": "PROJ-1",
						"fields": {
							"summary": "Fix login bug",
							"description": "Users cannot log in with SSO",
							"status": {"name": "Done"},
							"priority": {"name": "High"},
							"labels": ["bug", "security"],
							"fixVersions": [{"name": "v1.0"}]
						}
					},
					{
						"key": "PROJ-2",
						"fields": {
							"summary": "Add dark mode",
							"description": "",
							"status": {"name": "To Do"},
							"priority": {"name": "Low"},
							"labels": [],
							"fixVersions": []
						}
					}
				]
			},
			{
				"key": "OTHER",
				"issues": [
					{
						"key": "OTHER-1",
						"fields": {
							"summary": "Other project issue",
							"description": "Should be filtered out",
							"status": {"name": "Open"},
							"priority": {"name": "Medium"},
							"labels": [],
							"fixVersions": []
						}
					}
				]
			}
		]
	}`

	// Parse all projects
	issues, err := parseJiraExport([]byte(raw), "")
	if err != nil {
		t.Fatalf("parseJiraExport error: %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("got %d issues, want 3", len(issues))
	}

	// First issue
	if issues[0].Key != "PROJ-1" {
		t.Errorf("issue[0].Key = %q, want %q", issues[0].Key, "PROJ-1")
	}
	if issues[0].Fields.Summary != "Fix login bug" {
		t.Errorf("issue[0].Fields.Summary = %q, want %q", issues[0].Fields.Summary, "Fix login bug")
	}
	if issues[0].Fields.Description != "Users cannot log in with SSO" {
		t.Errorf("issue[0].Fields.Description = %q", issues[0].Fields.Description)
	}
	if issues[0].Fields.Status.Name != "Done" {
		t.Errorf("issue[0].Fields.Status.Name = %q, want %q", issues[0].Fields.Status.Name, "Done")
	}
	if len(issues[0].Fields.Labels) != 2 {
		t.Fatalf("issue[0] has %d labels, want 2", len(issues[0].Fields.Labels))
	}
	if issues[0].Fields.Labels[0] != "bug" {
		t.Errorf("issue[0].Fields.Labels[0] = %q, want %q", issues[0].Fields.Labels[0], "bug")
	}
	if len(issues[0].Fields.FixVersions) != 1 || issues[0].Fields.FixVersions[0].Name != "v1.0" {
		t.Errorf("issue[0].Fields.FixVersions = %+v, want [{Name: v1.0}]", issues[0].Fields.FixVersions)
	}

	// Second issue (no labels, no fixVersions)
	if issues[1].Key != "PROJ-2" {
		t.Errorf("issue[1].Key = %q, want %q", issues[1].Key, "PROJ-2")
	}
	if issues[1].Fields.Description != "" {
		t.Errorf("issue[1].Fields.Description = %q, want empty", issues[1].Fields.Description)
	}

	// Filter by project key
	filtered, err := parseJiraExport([]byte(raw), "PROJ")
	if err != nil {
		t.Fatalf("parseJiraExport with filter error: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered got %d issues, want 2", len(filtered))
	}
	for _, issue := range filtered {
		if !hasPrefix(issue.Key, "PROJ-") {
			t.Errorf("filtered issue key %q does not start with PROJ-", issue.Key)
		}
	}

	// Case-insensitive project key filtering
	filteredLower, err := parseJiraExport([]byte(raw), "proj")
	if err != nil {
		t.Fatalf("parseJiraExport case-insensitive error: %v", err)
	}
	if len(filteredLower) != 2 {
		t.Fatalf("case-insensitive filter got %d issues, want 2", len(filteredLower))
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func TestMapJiraStatus(t *testing.T) {
	tests := []struct {
		jiraStatus string
		want       string
	}{
		{"To Do", "draft"},
		{"Open", "draft"},
		{"Backlog", "draft"},
		{"In Progress", "implementing"},
		{"In Development", "implementing"},
		{"In Review", "human-qa"},
		{"In QA", "human-qa"},
		{"Done", "done"},
		{"Closed", "done"},
		{"Resolved", "done"},
		{"", "draft"},
		{"Unknown Status", "draft"},
		// Case-insensitive
		{"to do", "draft"},
		{"IN PROGRESS", "implementing"},
		{"done", "done"},
		{"  Done  ", "done"},
	}

	for _, tc := range tests {
		got := mapJiraStatus(tc.jiraStatus)
		if got != tc.want {
			t.Errorf("mapJiraStatus(%q) = %q, want %q", tc.jiraStatus, got, tc.want)
		}
	}
}

func TestJiraDryRunNoDBWrites(t *testing.T) {
	// Temporarily disable webhook dispatch to avoid cross-test DB contention.
	saved := db.WebhookDispatchFunc
	db.WebhookDispatchFunc = nil
	defer func() { db.WebhookDispatchFunc = saved }()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer database.Close() //nolint:errcheck

	// Create a project
	if err := db.CreateProject(database, &models.Project{ID: "test-proj", Name: "Test Project"}); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	milestoneMap := map[string]string{"v1.0": "v10"}

	issue := JiraIssue{
		Key: "PROJ-42",
		Fields: JiraFields{
			Summary:     "Test issue",
			Description: "Body text",
			Status:      JiraStatus{Name: "In Progress"},
			Priority:    JiraPriority{Name: "High"},
			Labels:      []string{"bug"},
			FixVersions: []JiraVersion{{Name: "v1.0"}},
		},
	}

	result := importSingleJiraIssue(database, "test-proj", issue, milestoneMap, true)

	if result.Skipped {
		t.Errorf("dry-run result should not be skipped")
	}
	if result.Status != "implementing" {
		t.Errorf("status = %q, want %q", result.Status, "implementing")
	}
	if result.MilestoneID != "v10" {
		t.Errorf("milestone_id = %q, want %q", result.MilestoneID, "v10")
	}
	if result.IssueKey != "PROJ-42" {
		t.Errorf("issue_key = %q, want %q", result.IssueKey, "PROJ-42")
	}

	// Verify no features were created in the database
	features, err := db.ListFeatures(database, "test-proj", "", "")
	if err != nil {
		t.Fatalf("listing features: %v", err)
	}
	if len(features) != 0 {
		t.Errorf("dry-run created %d features, want 0", len(features))
	}

	// Verify no events were created
	events, err := db.ListEvents(database, "test-proj", "", "", "", 100)
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("dry-run created %d events, want 0", len(events))
	}
}
