package cli

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
)

func TestParseGitHubIssues(t *testing.T) {
	raw := `[
		{
			"number": 42,
			"title": "Fix login bug",
			"body": "Users cannot log in with SSO",
			"state": "OPEN",
			"labels": [{"name": "bug"}, {"name": "high priority"}],
			"milestone": {"title": "v1.0"},
			"assignees": [{"login": "octocat"}]
		},
		{
			"number": 99,
			"title": "Add dark mode",
			"body": "",
			"state": "CLOSED",
			"labels": [],
			"milestone": null,
			"assignees": []
		}
	]`

	issues, err := parseGitHubIssues([]byte(raw))
	if err != nil {
		t.Fatalf("parseGitHubIssues error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	// First issue
	if issues[0].Number != 42 {
		t.Errorf("issue[0].Number = %d, want 42", issues[0].Number)
	}
	if issues[0].Title != "Fix login bug" {
		t.Errorf("issue[0].Title = %q, want %q", issues[0].Title, "Fix login bug")
	}
	if issues[0].Body != "Users cannot log in with SSO" {
		t.Errorf("issue[0].Body = %q", issues[0].Body)
	}
	if len(issues[0].Labels) != 2 {
		t.Fatalf("issue[0] has %d labels, want 2", len(issues[0].Labels))
	}
	if issues[0].Labels[0].Name != "bug" {
		t.Errorf("issue[0].Labels[0].Name = %q, want %q", issues[0].Labels[0].Name, "bug")
	}
	if issues[0].Milestone == nil || issues[0].Milestone.Title != "v1.0" {
		t.Errorf("issue[0].Milestone = %+v, want title v1.0", issues[0].Milestone)
	}

	// Second issue (no milestone, no labels)
	if issues[1].Number != 99 {
		t.Errorf("issue[1].Number = %d, want 99", issues[1].Number)
	}
	if issues[1].Milestone != nil {
		t.Errorf("issue[1].Milestone = %+v, want nil", issues[1].Milestone)
	}
}

func TestMapGitHubStatus(t *testing.T) {
	tests := []struct {
		ghState string
		want    string
	}{
		{"OPEN", "draft"},
		{"open", "draft"},
		{"CLOSED", "done"},
		{"closed", "done"},
		{"", "draft"},
		{"UNKNOWN", "draft"},
	}

	for _, tc := range tests {
		got := mapGitHubStatus(tc.ghState)
		if got != tc.want {
			t.Errorf("mapGitHubStatus(%q) = %q, want %q", tc.ghState, got, tc.want)
		}
	}
}

func TestMapLabelsToTags(t *testing.T) {
	labels := []GitHubLabel{
		{Name: "bug"},
		{Name: "High Priority"},
		{Name: "good first issue"},
		{Name: "Feature Request"},
	}

	tags := mapLabelsToTags(labels)

	want := []string{"bug", "high-priority", "good-first-issue", "feature-request"}
	if len(tags) != len(want) {
		t.Fatalf("got %d tags, want %d", len(tags), len(want))
	}
	for i, tag := range tags {
		if tag != want[i] {
			t.Errorf("tags[%d] = %q, want %q", i, tag, want[i])
		}
	}
}

func TestDryRunNoDBWrites(t *testing.T) {
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

	issue := GitHubIssue{
		Number:    1,
		Title:     "Test issue",
		Body:      "Body text",
		State:     "OPEN",
		Labels:    []GitHubLabel{{Name: "bug"}},
		Milestone: &GitHubMilestone{Title: "v1.0"},
	}

	result := importSingleIssue(database, "test-proj", "owner/repo", issue, milestoneMap, true)

	if result.Skipped {
		t.Errorf("dry-run result should not be skipped")
	}
	if result.Status != "draft" {
		t.Errorf("status = %q, want %q", result.Status, "draft")
	}
	if result.MilestoneID != "v10" {
		t.Errorf("milestone_id = %q, want %q", result.MilestoneID, "v10")
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

func TestImportSingleIssue(t *testing.T) {
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

	if err := db.CreateProject(database, &models.Project{ID: "test-proj", Name: "Test Project"}); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	milestoneMap := map[string]string{}

	issue := GitHubIssue{
		Number: 7,
		Title:  "Add search",
		Body:   "Implement full-text search",
		State:  "CLOSED",
		Labels: []GitHubLabel{{Name: "enhancement"}, {Name: "v2"}},
	}

	result := importSingleIssue(database, "test-proj", "owner/repo", issue, milestoneMap, false)

	if result.Skipped {
		t.Fatalf("import was skipped: %s", result.SkipReason)
	}
	if result.Status != "done" {
		t.Errorf("status = %q, want %q", result.Status, "done")
	}

	// Verify feature was created
	features, err := db.ListFeatures(database, "test-proj", "", "")
	if err != nil {
		t.Fatalf("listing features: %v", err)
	}
	if len(features) != 1 {
		t.Fatalf("got %d features, want 1", len(features))
	}
	if features[0].Name != "Add search" {
		t.Errorf("feature name = %q, want %q", features[0].Name, "Add search")
	}

	// Verify tags were applied
	f, err := db.GetFeature(database, features[0].ID)
	if err != nil {
		t.Fatalf("getting feature: %v", err)
	}
	// Should have "enhancement", "v2", and "github-import" tags
	tagSet := make(map[string]bool)
	for _, tag := range f.Tags {
		tagSet[tag] = true
	}
	for _, expected := range []string{"enhancement", "v2", "github-import"} {
		if !tagSet[expected] {
			t.Errorf("missing expected tag %q, got tags: %v", expected, f.Tags)
		}
	}

	// Verify event was recorded
	events, err := db.ListEvents(database, "test-proj", "", "feature.imported", "", 100)
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(events) == 0 {
		t.Error("no feature.imported event recorded")
	}
}
