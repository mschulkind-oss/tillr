package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowCheckCommand(t *testing.T) {
	dir := scratch(t, "workflow-check")
	path := filepath.Join(dir, "large@v1.json")
	stages := []map[string]any{}
	edges := []map[string]string{}
	// 150 nodes in nine levels, with the widest outgoing fan-out equal to 19.
	for i := 0; i < 150; i++ {
		stages = append(stages, map[string]any{"id": fmt.Sprint(i), "kind": "agent", "seat": "scout", "access": "read_only"})
		if i > 0 {
			parent := 0
			if i >= 20 {
				parent = i - 19
			}
			edges = append(edges, map[string]string{"from_node": fmt.Sprint(parent), "to_node": fmt.Sprint(i)})
		}
	}
	raw, _ := json.Marshal(map[string]any{"name": "large", "version": 1, "stages": stages, "edges": edges})
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, "workflow", "check", path)
	if code != 0 || !strings.Contains(string(out), "nodes=150 depth=9 fan-out=19") {
		t.Fatalf("code=%d %s", code, out)
	}
	code, out = invoke(t, "workflow", "check", "--help")
	if code != 0 || !strings.Contains(string(out), "workflow-expansion-cycle") || !strings.Contains(string(out), "workflow-exit") {
		t.Fatalf("help code=%d %s", code, out)
	}
	if err := os.WriteFile(path, []byte(`{"name":"wrong","version":1,"stages":[],"edges":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	code, out = invoke(t, "workflow", "check", dir)
	if code != 1 || !strings.Contains(string(out), path) || !strings.Contains(string(out), "stage=") || !strings.Contains(string(out), "workflow-identity") {
		t.Fatalf("code=%d %s", code, out)
	}
}

func TestWorkflowCheckFindsCommandRegistryOutsideGitRepo(t *testing.T) {
	parent := scratch(t, "workflow-check-nongit")
	t.Setenv("GIT_CEILING_DIRECTORIES", parent)
	root := filepath.Join(parent, "project")
	wfDir := filepath.Join(root, "config", "tillr", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "tillr", "commands.json"), []byte(`{"probe":{"argv":["true"],"flags":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(wfDir, "probe@v1.json")
	wfBody := `{"name":"probe","version":1,"stages":[{"id":"run","kind":"command","command_ref":"probe","access":"read_only"}],"edges":[]}`
	if err := os.WriteFile(wfPath, []byte(wfBody), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, "workflow", "check", wfPath)
	if code != 0 || !strings.Contains(string(out), "nodes=1 depth=1 fan-out=0") {
		t.Fatalf("workflow check outside git repo: code=%d %s", code, out)
	}
}
