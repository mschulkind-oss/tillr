package export_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind/tillr/internal/export"
	"github.com/mschulkind/tillr/internal/models"
)

func TestFeaturesJSON(t *testing.T) {
	features := []models.Feature{
		{ID: "f1", Name: "Auth", Status: "done", Priority: 10},
		{ID: "f2", Name: "Search", Status: "draft", Priority: 5},
	}
	var buf bytes.Buffer
	if err := export.FeaturesJSON(features, &buf); err != nil {
		t.Fatalf("FeaturesJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"id": "f1"`) {
		t.Errorf("expected feature f1 in output, got: %s", out)
	}
	if !strings.Contains(out, `"name": "Search"`) {
		t.Errorf("expected feature Search in output")
	}
}

func TestFeaturesCSV(t *testing.T) {
	features := []models.Feature{
		{ID: "f1", Name: "Auth Module", Status: "done", Priority: 10, MilestoneName: "v1", Description: "JWT auth", CreatedAt: "2024-01-01"},
	}
	var buf bytes.Buffer
	if err := export.FeaturesCSV(features, &buf); err != nil {
		t.Fatalf("FeaturesCSV: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (header + 1 row), got %d", len(lines))
	}
	if !strings.Contains(lines[0], "id,name,status") {
		t.Errorf("expected CSV header, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "Auth Module") {
		t.Errorf("expected feature name in CSV row, got: %s", lines[1])
	}
}

func TestFeaturesMarkdown(t *testing.T) {
	features := []models.Feature{
		{ID: "f1", Name: "Auth", Status: "done", Priority: 10},
		{ID: "f2", Name: "Search", Status: "draft", Priority: 5},
	}
	var buf bytes.Buffer
	if err := export.FeaturesMarkdown(features, &buf); err != nil {
		t.Fatalf("FeaturesMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Features") {
		t.Errorf("expected '## Features' header")
	}
	if !strings.Contains(out, "~~Auth~~") {
		t.Errorf("expected done feature to be struck through")
	}
	if !strings.Contains(out, "✅") {
		t.Errorf("expected ✅ for done feature")
	}
	if !strings.Contains(out, "[ ] Search") {
		t.Errorf("expected draft feature with empty checkbox")
	}
}

func TestRoadmapMarkdown(t *testing.T) {
	items := []models.RoadmapItem{
		{ID: "r1", Title: "WebSocket Updates", Priority: "high", Status: "done"},
		{ID: "r2", Title: "MCP Integration", Priority: "high", Status: "proposed"},
		{ID: "r3", Title: "Custom Templates", Priority: "medium", Status: "proposed"},
	}
	var buf bytes.Buffer
	if err := export.RoadmapMarkdown(items, &buf); err != nil {
		t.Fatalf("RoadmapMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Project Roadmap") {
		t.Errorf("expected roadmap header")
	}
	if !strings.Contains(out, "~~WebSocket Updates~~ ✅") {
		t.Errorf("expected done item to be struck through with ✅")
	}
	if !strings.Contains(out, "[ ] MCP Integration") {
		t.Errorf("expected proposed item with empty checkbox")
	}
}

func TestRoadmapCSV(t *testing.T) {
	items := []models.RoadmapItem{
		{ID: "r1", Title: "WebSocket", Priority: "high", Status: "done", Category: "infra", CreatedAt: "2024-01-01"},
	}
	var buf bytes.Buffer
	if err := export.RoadmapCSV(items, &buf); err != nil {
		t.Fatalf("RoadmapCSV: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "WebSocket") {
		t.Errorf("expected item in CSV")
	}
}

func TestDecisionsMarkdown(t *testing.T) {
	decisions := []models.Decision{
		{ID: "d1", Title: "Use SQLite", Status: "accepted", Context: "Need embedded DB", Decision: "SQLite via modernc", Consequences: "No server needed"},
	}
	var buf bytes.Buffer
	if err := export.DecisionsMarkdown(decisions, &buf); err != nil {
		t.Fatalf("DecisionsMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Architecture Decisions") {
		t.Errorf("expected decisions header")
	}
	if !strings.Contains(out, "Use SQLite [ACCEPTED]") {
		t.Errorf("expected decision with status badge")
	}
}

func TestDecisionsADR(t *testing.T) {
	decisions := []models.Decision{
		{ID: "d1", Title: "Use SQLite", Status: "accepted", Context: "Need embedded DB", Decision: "SQLite via modernc", Consequences: "No server needed"},
		{ID: "d2", Title: "Use REST", Status: "superseded", Context: "API design", Decision: "REST over GraphQL", Consequences: "Simple clients", SupersededBy: "d3"},
	}
	var buf bytes.Buffer
	if err := export.DecisionsADR(decisions, &buf); err != nil {
		t.Fatalf("DecisionsADR: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# ADR-001: Use SQLite") {
		t.Errorf("expected ADR-001 header, got: %s", out)
	}
	if !strings.Contains(out, "## Status") {
		t.Errorf("expected Status section")
	}
	if !strings.Contains(out, "Accepted") {
		t.Errorf("expected Accepted status")
	}
	if !strings.Contains(out, "## Context") {
		t.Errorf("expected Context section")
	}
	if !strings.Contains(out, "Need embedded DB") {
		t.Errorf("expected context text")
	}
	if !strings.Contains(out, "## Decision") {
		t.Errorf("expected Decision section")
	}
	if !strings.Contains(out, "## Consequences") {
		t.Errorf("expected Consequences section")
	}
	if !strings.Contains(out, "# ADR-002: Use REST") {
		t.Errorf("expected ADR-002 header")
	}
	if !strings.Contains(out, "Superseded by [d3]") {
		t.Errorf("expected superseded_by link, got: %s", out)
	}
	if !strings.Contains(out, "---") {
		t.Errorf("expected separator between ADRs")
	}
}

func TestDecisionsADREmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := export.DecisionsADR(nil, &buf); err != nil {
		t.Fatalf("DecisionsADR empty: %v", err)
	}
	if !strings.Contains(buf.String(), "*No decisions recorded.*") {
		t.Errorf("expected empty message for ADR export")
	}
}

func TestAllJSON(t *testing.T) {
	features := []models.Feature{{ID: "f1", Name: "Auth"}}
	roadmap := []models.RoadmapItem{{ID: "r1", Title: "WebSocket"}}
	decisions := []models.Decision{{ID: "d1", Title: "Use SQLite"}}

	var buf bytes.Buffer
	if err := export.AllJSON("TestProject", features, roadmap, decisions, &buf); err != nil {
		t.Fatalf("AllJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"project": "TestProject"`) {
		t.Errorf("expected project name in output")
	}
	if !strings.Contains(out, `"features"`) {
		t.Errorf("expected features key")
	}
	if !strings.Contains(out, `"roadmap"`) {
		t.Errorf("expected roadmap key")
	}
	if !strings.Contains(out, `"decisions"`) {
		t.Errorf("expected decisions key")
	}
}

func TestAllMarkdown(t *testing.T) {
	features := []models.Feature{{ID: "f1", Name: "Auth", Status: "done"}}
	roadmap := []models.RoadmapItem{{ID: "r1", Title: "WebSocket", Priority: "high", Status: "done"}}
	decisions := []models.Decision{{ID: "d1", Title: "Use SQLite", Status: "accepted"}}

	var buf bytes.Buffer
	if err := export.All("TestProject", features, roadmap, decisions, &buf, "md"); err != nil {
		t.Fatalf("All markdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "TestProject — Full Project Export") {
		t.Errorf("expected project title in markdown export")
	}
	if !strings.Contains(out, "## Features") {
		t.Errorf("expected features section")
	}
	if !strings.Contains(out, "## Project Roadmap") {
		t.Errorf("expected roadmap section")
	}
	if !strings.Contains(out, "## Architecture Decisions") {
		t.Errorf("expected decisions section")
	}
}

func TestEmptyExports(t *testing.T) {
	var buf bytes.Buffer

	if err := export.FeaturesMarkdown(nil, &buf); err != nil {
		t.Fatalf("empty features markdown: %v", err)
	}
	if !strings.Contains(buf.String(), "*No features.*") {
		t.Errorf("expected empty message for features")
	}

	buf.Reset()
	if err := export.RoadmapMarkdown(nil, &buf); err != nil {
		t.Fatalf("empty roadmap markdown: %v", err)
	}
	if !strings.Contains(buf.String(), "*No roadmap items.*") {
		t.Errorf("expected empty message for roadmap")
	}

	buf.Reset()
	if err := export.DecisionsMarkdown(nil, &buf); err != nil {
		t.Fatalf("empty decisions markdown: %v", err)
	}
	if !strings.Contains(buf.String(), "*No decisions recorded.*") {
		t.Errorf("expected empty message for decisions")
	}
}

func TestExportDispatch(t *testing.T) {
	features := []models.Feature{{ID: "f1", Name: "Test", Status: "draft", CreatedAt: "2024-01-01"}}

	// JSON dispatch
	var buf bytes.Buffer
	if err := export.Features(features, &buf, "json"); err != nil {
		t.Fatalf("Features json dispatch: %v", err)
	}
	if !strings.Contains(buf.String(), `"id": "f1"`) {
		t.Errorf("expected JSON output from dispatch")
	}

	// Markdown dispatch
	buf.Reset()
	if err := export.Features(features, &buf, "md"); err != nil {
		t.Fatalf("Features md dispatch: %v", err)
	}
	if !strings.Contains(buf.String(), "## Features") {
		t.Errorf("expected markdown output from dispatch")
	}

	// CSV dispatch
	buf.Reset()
	if err := export.Features(features, &buf, "csv"); err != nil {
		t.Fatalf("Features csv dispatch: %v", err)
	}
	if !strings.Contains(buf.String(), "id,name,status") {
		t.Errorf("expected CSV output from dispatch")
	}
}
