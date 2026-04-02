package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/engine"
)

func setupTestDB(t *testing.T) *Server {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { database.Close() }) //nolint:errcheck

	p, err := engine.InitProject(database, "test-project")
	if err != nil {
		t.Fatalf("creating test project: %v", err)
	}

	return NewServer(database, p.ID, nil, nil)
}

func TestInitialize(t *testing.T) {
	s := setupTestDB(t)

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocol version 2024-11-05, got %v", result["protocolVersion"])
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("expected serverInfo map, got %T", result["serverInfo"])
	}
	if info["name"] != "tillr" {
		t.Errorf("expected server name tillr, got %v", info["name"])
	}
}

func TestToolsList(t *testing.T) {
	s := setupTestDB(t)

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	tools, ok := result["tools"].([]Tool)
	if !ok {
		t.Fatalf("expected []Tool, got %T", result["tools"])
	}

	expectedTools := map[string]bool{
		"tillr_next":     false,
		"tillr_done":     false,
		"tillr_fail":     false,
		"tillr_status":   false,
		"tillr_features": false,
		"tillr_feedback": false,
	}

	for _, tool := range tools {
		if _, exists := expectedTools[tool.Name]; exists {
			expectedTools[tool.Name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("expected tool %q not found in tools/list", name)
		}
	}
}

func TestToolCallStatus(t *testing.T) {
	s := setupTestDB(t)

	params, _ := json.Marshal(map[string]any{
		"name":      "tillr_status",
		"arguments": map[string]any{},
	})

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	contents, ok := result["content"].([]Content)
	if !ok {
		t.Fatalf("expected []Content, got %T", result["content"])
	}
	if len(contents) == 0 {
		t.Fatal("expected at least one content block")
	}
	if contents[0].Type != "text" {
		t.Errorf("expected content type text, got %s", contents[0].Type)
	}

	// Parse the text as StatusOverview JSON
	var overview map[string]any
	if err := json.Unmarshal([]byte(contents[0].Text), &overview); err != nil {
		t.Fatalf("expected valid JSON in content text: %v", err)
	}
	proj, ok := overview["project"].(map[string]any)
	if !ok {
		t.Fatalf("expected project in status overview")
	}
	if proj["name"] != "test-project" {
		t.Errorf("expected project name test-project, got %v", proj["name"])
	}
}

func TestToolCallFeatures(t *testing.T) {
	s := setupTestDB(t)

	// Add a feature via engine
	_, err := engine.AddFeature(s.db, s.projectID, "Test Feature", "A test", "1. Do X", "", 5, nil, "")
	if err != nil {
		t.Fatalf("adding test feature: %v", err)
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "tillr_features",
		"arguments": map[string]any{},
	})

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}
	result := resp.Result.(map[string]any)
	contents := result["content"].([]Content)

	var features []map[string]any
	if err := json.Unmarshal([]byte(contents[0].Text), &features); err != nil {
		t.Fatalf("expected valid JSON array: %v", err)
	}
	if len(features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(features))
	}
	if features[0]["name"] != "Test Feature" {
		t.Errorf("expected feature name 'Test Feature', got %v", features[0]["name"])
	}
}

func TestToolCallFeaturesWithStatusFilter(t *testing.T) {
	s := setupTestDB(t)

	_, err := engine.AddFeature(s.db, s.projectID, "Draft Feature", "", "", "", 5, nil, "")
	if err != nil {
		t.Fatalf("adding feature: %v", err)
	}

	// Filter for a status that doesn't match — should return empty
	params, _ := json.Marshal(map[string]any{
		"name":      "tillr_features",
		"arguments": map[string]any{"status": "done"},
	})

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}
	result := resp.Result.(map[string]any)
	contents := result["content"].([]Content)

	// Should be null/empty JSON array
	if contents[0].Text != "null" && contents[0].Text != "[]" {
		var features []map[string]any
		if err := json.Unmarshal([]byte(contents[0].Text), &features); err != nil {
			t.Fatalf("expected valid JSON: %v", err)
		}
		if len(features) != 0 {
			t.Errorf("expected 0 features with status done, got %d", len(features))
		}
	}
}

func TestToolCallFeedback(t *testing.T) {
	s := setupTestDB(t)

	params, _ := json.Marshal(map[string]any{
		"name": "tillr_feedback",
		"arguments": map[string]any{
			"title":       "Add dark mode",
			"description": "Would be nice to have dark mode support",
			"type":        "feature",
		},
	})

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      6,
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] != nil {
		contents := result["content"].([]Content)
		t.Fatalf("tool returned error: %s", contents[0].Text)
	}
	contents := result["content"].([]Content)

	var idea map[string]any
	if err := json.Unmarshal([]byte(contents[0].Text), &idea); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if idea["title"] != "Add dark mode" {
		t.Errorf("expected title 'Add dark mode', got %v", idea["title"])
	}
	if idea["idea_type"] != "feature" {
		t.Errorf("expected idea_type 'feature', got %v", idea["idea_type"])
	}
}

func TestToolCallFeedbackMissingTitle(t *testing.T) {
	s := setupTestDB(t)

	params, _ := json.Marshal(map[string]any{
		"name":      "tillr_feedback",
		"arguments": map[string]any{},
	})

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      7,
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error.Message)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] != true {
		t.Error("expected isError=true for missing title")
	}
}

func TestUnknownMethod(t *testing.T) {
	s := setupTestDB(t)

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      8,
		Method:  "unknown/method",
	})

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}

func TestUnknownTool(t *testing.T) {
	s := setupTestDB(t)

	params, _ := json.Marshal(map[string]any{
		"name":      "nonexistent_tool",
		"arguments": map[string]any{},
	})

	resp := s.HandleRequest(Request{
		JSONRPC: "2.0",
		ID:      9,
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error.Message)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] != true {
		t.Error("expected isError=true for unknown tool")
	}
}
