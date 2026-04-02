package export_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind/tillr/internal/export"
	"github.com/mschulkind/tillr/internal/models"
)

func TestDiagramMermaidBasic(t *testing.T) {
	features := []models.Feature{
		{ID: "auth", Name: "Auth", Status: "done", MilestoneID: "v1", MilestoneName: "v1.0 MVP"},
		{ID: "api", Name: "API", Status: "implementing", MilestoneID: "v1", MilestoneName: "v1.0 MVP", DependsOn: []string{"auth"}},
		{ID: "frontend", Name: "Frontend", Status: "draft", MilestoneID: "v1", MilestoneName: "v1.0 MVP", DependsOn: []string{"api"}},
	}
	milestones := []models.Milestone{
		{ID: "v1", Name: "v1.0 MVP"},
	}

	var buf bytes.Buffer
	if err := export.DiagramMermaid(features, milestones, &buf); err != nil {
		t.Fatalf("DiagramMermaid: %v", err)
	}
	out := buf.String()

	// Should start with graph TD
	if !strings.HasPrefix(out, "graph TD\n") {
		t.Errorf("expected graph TD header, got: %s", out[:30])
	}

	// Should have subgraph for milestone
	if !strings.Contains(out, `subgraph "v1.0 MVP"`) {
		t.Errorf("expected milestone subgraph, got: %s", out)
	}

	// Should have nodes with status emojis
	if !strings.Contains(out, "Auth ✅") {
		t.Errorf("expected done emoji for auth")
	}
	if !strings.Contains(out, "API 🔨") {
		t.Errorf("expected implementing emoji for api")
	}
	if !strings.Contains(out, "Frontend 📋") {
		t.Errorf("expected draft emoji for frontend")
	}

	// Should have edges
	if !strings.Contains(out, "api --> auth") {
		t.Errorf("expected api --> auth edge, got: %s", out)
	}
	if !strings.Contains(out, "frontend --> api") {
		t.Errorf("expected frontend --> api edge, got: %s", out)
	}

	// Should have style definitions
	if !strings.Contains(out, "style auth fill:#2ecc71") {
		t.Errorf("expected green style for done feature")
	}
	if !strings.Contains(out, "style api fill:#f1c40f") {
		t.Errorf("expected yellow style for implementing feature")
	}
}

func TestDiagramMermaidEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := export.DiagramMermaid(nil, nil, &buf); err != nil {
		t.Fatalf("DiagramMermaid empty: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "graph TD") {
		t.Errorf("expected graph TD for empty diagram")
	}
	if !strings.Contains(out, "empty[No features]") {
		t.Errorf("expected empty placeholder node")
	}
}

func TestDiagramMermaidUngrouped(t *testing.T) {
	features := []models.Feature{
		{ID: "misc", Name: "Misc Task", Status: "planning"},
	}

	var buf bytes.Buffer
	if err := export.DiagramMermaid(features, nil, &buf); err != nil {
		t.Fatalf("DiagramMermaid ungrouped: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "misc[Misc Task") {
		t.Errorf("expected ungrouped feature node, got: %s", out)
	}
	// Should not have subgraph
	if strings.Contains(out, "subgraph") {
		t.Errorf("expected no subgraph for ungrouped features")
	}
}

func TestDiagramMermaidMultipleMilestones(t *testing.T) {
	features := []models.Feature{
		{ID: "f1", Name: "Feature 1", Status: "done", MilestoneID: "v1"},
		{ID: "f2", Name: "Feature 2", Status: "draft", MilestoneID: "v2", DependsOn: []string{"f1"}},
	}
	milestones := []models.Milestone{
		{ID: "v1", Name: "v1.0"},
		{ID: "v2", Name: "v2.0"},
	}

	var buf bytes.Buffer
	if err := export.DiagramMermaid(features, milestones, &buf); err != nil {
		t.Fatalf("DiagramMermaid multi-milestone: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `subgraph "v1.0"`) {
		t.Errorf("expected v1.0 subgraph")
	}
	if !strings.Contains(out, `subgraph "v2.0"`) {
		t.Errorf("expected v2.0 subgraph")
	}
	if !strings.Contains(out, "f2 --> f1") {
		t.Errorf("expected cross-milestone edge")
	}
}

func TestDiagramMermaidSkipsUnknownDeps(t *testing.T) {
	features := []models.Feature{
		{ID: "f1", Name: "Feature 1", Status: "draft", DependsOn: []string{"nonexistent"}},
	}

	var buf bytes.Buffer
	if err := export.DiagramMermaid(features, nil, &buf); err != nil {
		t.Fatalf("DiagramMermaid unknown deps: %v", err)
	}
	out := buf.String()
	// Should NOT have an edge to nonexistent
	if strings.Contains(out, "nonexistent") {
		t.Errorf("expected unknown dep to be skipped, got: %s", out)
	}
}

func TestDiagramMermaidSpecialChars(t *testing.T) {
	features := []models.Feature{
		{ID: "f-1", Name: "Auth [v2]", Status: "done"},
	}

	var buf bytes.Buffer
	if err := export.DiagramMermaid(features, nil, &buf); err != nil {
		t.Fatalf("DiagramMermaid special chars: %v", err)
	}
	out := buf.String()
	// ID should be sanitized (no hyphens)
	if !strings.Contains(out, "f_1[") {
		t.Errorf("expected sanitized node ID, got: %s", out)
	}
	// Brackets in name should be escaped
	if strings.Contains(out, "[v2]") {
		t.Errorf("expected brackets to be escaped in label")
	}
}

func TestDiagramDOTBasic(t *testing.T) {
	features := []models.Feature{
		{ID: "auth", Name: "Auth", Status: "done", MilestoneID: "v1"},
		{ID: "api", Name: "API", Status: "implementing", MilestoneID: "v1", DependsOn: []string{"auth"}},
	}
	milestones := []models.Milestone{
		{ID: "v1", Name: "v1.0 MVP"},
	}

	var buf bytes.Buffer
	if err := export.DiagramDOT(features, milestones, &buf); err != nil {
		t.Fatalf("DiagramDOT: %v", err)
	}
	out := buf.String()

	if !strings.HasPrefix(out, "digraph tillr {\n") {
		t.Errorf("expected digraph header, got: %s", out[:30])
	}
	if !strings.Contains(out, "subgraph cluster_0") {
		t.Errorf("expected DOT cluster subgraph")
	}
	if !strings.Contains(out, `label="v1.0 MVP"`) {
		t.Errorf("expected milestone label in DOT")
	}
	if !strings.Contains(out, "api -> auth") {
		t.Errorf("expected DOT edge, got: %s", out)
	}
	if !strings.Contains(out, `fillcolor="#2ecc71"`) {
		t.Errorf("expected green fill for done feature")
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "}") {
		t.Errorf("expected closing brace")
	}
}

func TestDiagramDOTEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := export.DiagramDOT(nil, nil, &buf); err != nil {
		t.Fatalf("DiagramDOT empty: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "digraph tillr") {
		t.Errorf("expected digraph header for empty diagram")
	}
	if !strings.Contains(out, `label="No features"`) {
		t.Errorf("expected empty placeholder node")
	}
}

func TestDiagramDispatch(t *testing.T) {
	features := []models.Feature{
		{ID: "f1", Name: "Test", Status: "draft"},
	}

	// Mermaid (default)
	var buf bytes.Buffer
	if err := export.Diagram(features, nil, &buf, "mermaid"); err != nil {
		t.Fatalf("Diagram mermaid dispatch: %v", err)
	}
	if !strings.Contains(buf.String(), "graph TD") {
		t.Errorf("expected mermaid output from dispatch")
	}

	// DOT
	buf.Reset()
	if err := export.Diagram(features, nil, &buf, "dot"); err != nil {
		t.Fatalf("Diagram dot dispatch: %v", err)
	}
	if !strings.Contains(buf.String(), "digraph") {
		t.Errorf("expected DOT output from dispatch")
	}

	// graphviz alias
	buf.Reset()
	if err := export.Diagram(features, nil, &buf, "graphviz"); err != nil {
		t.Fatalf("Diagram graphviz dispatch: %v", err)
	}
	if !strings.Contains(buf.String(), "digraph") {
		t.Errorf("expected DOT output from graphviz alias")
	}

	// Default → mermaid
	buf.Reset()
	if err := export.Diagram(features, nil, &buf, ""); err != nil {
		t.Fatalf("Diagram default dispatch: %v", err)
	}
	if !strings.Contains(buf.String(), "graph TD") {
		t.Errorf("expected mermaid output from empty format")
	}
}

func TestDiagramMermaidAllStatuses(t *testing.T) {
	statuses := []struct {
		status string
		emoji  string
		style  string
	}{
		{"done", "✅", "fill:#2ecc71"},
		{"implementing", "🔨", "fill:#f1c40f"},
		{"planning", "📐", "fill:#9b59b6"},
		{"agent-qa", "🧪", "fill:#e67e22"},
		{"human-qa", "🧪", "fill:#e67e22"},
		{"blocked", "🚫", "fill:#e74c3c"},
		{"draft", "📋", "fill:#3498db"},
	}

	for _, tc := range statuses {
		t.Run(tc.status, func(t *testing.T) {
			features := []models.Feature{
				{ID: "f1", Name: "Test", Status: tc.status},
			}
			var buf bytes.Buffer
			if err := export.DiagramMermaid(features, nil, &buf); err != nil {
				t.Fatalf("DiagramMermaid %s: %v", tc.status, err)
			}
			out := buf.String()
			if !strings.Contains(out, tc.emoji) {
				t.Errorf("expected emoji %s for status %s", tc.emoji, tc.status)
			}
			if !strings.Contains(out, tc.style) {
				t.Errorf("expected style %s for status %s", tc.style, tc.status)
			}
		})
	}
}
