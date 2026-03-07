package export_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind/lifecycle/internal/export"
	"github.com/mschulkind/lifecycle/internal/models"
)

func TestReportMarkdown(t *testing.T) {
	data := export.ReportData{
		ProjectName: "TestProject",
		Milestones: []models.Milestone{
			{Name: "v1.0", TotalFeatures: 4, DoneFeatures: 2},
		},
		Features: []models.Feature{
			{ID: "f1", Name: "Auth", Status: "done", Priority: 10},
			{ID: "f2", Name: "Search", Status: "implementing", Priority: 5},
			{ID: "f3", Name: "Dashboard", Status: "draft", Priority: 3},
			{ID: "f4", Name: "API", Status: "done", Priority: 8},
		},
		Roadmap: []models.RoadmapItem{
			{Title: "Performance", Priority: "high", Status: "in-progress", Category: "infra"},
		},
	}

	var buf bytes.Buffer
	if err := export.Report(data, &buf, "md"); err != nil {
		t.Fatalf("ReportMarkdown: %v", err)
	}
	out := buf.String()

	checks := []string{
		"# TestProject",
		"**Total Features:** 4",
		"**Completed:** 2 (50%)",
		"v1.0",
		"Auth",
		"Search",
		"Project Roadmap",
		"Performance",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("expected %q in markdown report output", c)
		}
	}
}

func TestReportHTML(t *testing.T) {
	data := export.ReportData{
		ProjectName: "HTMLProject",
		Milestones:  []models.Milestone{},
		Features: []models.Feature{
			{ID: "f1", Name: "Widget", Status: "done", Priority: 5},
		},
		Roadmap: []models.RoadmapItem{},
	}

	var buf bytes.Buffer
	if err := export.Report(data, &buf, "html"); err != nil {
		t.Fatalf("ReportHTML: %v", err)
	}
	out := buf.String()

	checks := []string{
		"<!DOCTYPE html>",
		"HTMLProject",
		"@media print",
		"Widget",
		"badge-done",
		"summary-grid",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("expected %q in HTML report output", c)
		}
	}
}

func TestReportJSON(t *testing.T) {
	data := export.ReportData{
		ProjectName: "JSONProject",
		Features:    []models.Feature{{ID: "f1", Name: "Test"}},
	}

	var buf bytes.Buffer
	if err := export.ReportJSON(data, &buf); err != nil {
		t.Fatalf("ReportJSON: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `"project_name": "JSONProject"`) {
		t.Errorf("expected project_name in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"generated"`) {
		t.Errorf("expected generated timestamp in JSON output")
	}
}

func TestReportEmptyProject(t *testing.T) {
	data := export.ReportData{
		ProjectName: "Empty",
	}

	var buf bytes.Buffer
	if err := export.Report(data, &buf, "md"); err != nil {
		t.Fatalf("ReportMarkdown empty: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "**Total Features:** 0") {
		t.Errorf("expected zero features in empty project report")
	}
}
