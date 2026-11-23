package workflow

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowWatchCheckWorkflows(t *testing.T) {
	writeWF := func(t *testing.T, watchJSON string) (string, string) {
		t.Helper()
		root := t.TempDir()
		path := filepath.Join(root, "config", "tillr", "workflows", "sample@v1.json")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		watchField := ""
		if watchJSON != "" {
			watchField = `,"watch":` + watchJSON
		}
		raw := `{"name":"sample","version":1,"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write"},{"id":"verify","kind":"agent","seat":"verify","access":"read_only"}],"edges":[{"from_node":"plan","to_node":"work"},{"from_node":"work","to_node":"verify"}]` + watchField + `}`
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		return root, path
	}

	t.Run("valid omitted", func(t *testing.T) {
		root, path := writeWF(t, "")
		var out bytes.Buffer
		if !CheckWorkflows(root, path, &out) {
			t.Fatalf("unexpected failure: %s", out.String())
		}
	})

	t.Run("valid null", func(t *testing.T) {
		root, path := writeWF(t, `null`)
		var out bytes.Buffer
		if !CheckWorkflows(root, path, &out) {
			t.Fatalf("unexpected failure: %s", out.String())
		}
	})

	t.Run("valid full and default threshold", func(t *testing.T) {
		root, path := writeWF(t, `{"cycle_anchor":{"from":"verify","to":"plan","max_cycles":1},"oscillation_pairs":[{"a":"plan","b":"work"},{"a":"work","b":"verify","threshold":2,"hint":"check scope"}]}`)
		var out bytes.Buffer
		if !CheckWorkflows(root, path, &out) {
			t.Fatalf("unexpected failure: %s", out.String())
		}
		w, err := ReadWorkflow(root, path, nil, map[string]bool{})
		if err != nil {
			t.Fatal(err)
		}
		if w.Watch == nil || w.Watch.CycleAnchor == nil || w.Watch.CycleAnchor.MaxCycles != 1 {
			t.Fatalf("unexpected cycle_anchor: %+v", w.Watch)
		}
		if len(w.Watch.OscillationPairs) != 2 || w.Watch.OscillationPairs[0].Threshold != 4 || w.Watch.OscillationPairs[1].Threshold != 2 || w.Watch.OscillationPairs[1].Hint != "check scope" {
			t.Fatalf("unexpected oscillation_pairs: %+v", w.Watch.OscillationPairs)
		}
	})

	t.Run("valid seat names and design in oscillation_pairs", func(t *testing.T) {
		root, path := writeWF(t, `{"cycle_anchor":{"from":"verify","to":"build","max_cycles":2},"oscillation_pairs":[{"a":"plan","b":"design"}]}`)
		var out bytes.Buffer
		if !CheckWorkflows(root, path, &out) {
			t.Fatalf("unexpected failure: %s", out.String())
		}
	})

	t.Run("nested workflow stages and seats", func(t *testing.T) {
		root := t.TempDir()
		wfDir := filepath.Join(root, "config", "tillr", "workflows")
		if err := os.MkdirAll(wfDir, 0755); err != nil {
			t.Fatal(err)
		}
		innerPath := filepath.Join(wfDir, "inner@v1.json")
		innerRaw := `{"name":"inner","version":1,"stages":[{"id":"inner-work","kind":"agent","seat":"tricky","access":"write"}],"edges":[],"exit":{"requires_nodes":["inner-work"]}}`
		if err := os.WriteFile(innerPath, []byte(innerRaw), 0644); err != nil {
			t.Fatal(err)
		}
		outerPath := filepath.Join(wfDir, "outer@v1.json")
		outerRaw := `{"name":"outer","version":1,"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},{"id":"sub","kind":"workflow","workflow":"inner@v1"},{"id":"verify","kind":"agent","seat":"verify","access":"read_only"}],"edges":[{"from_node":"plan","to_node":"sub"},{"from_node":"sub","to_node":"verify"}],"watch":{"cycle_anchor":{"from":"verify","to":"inner-work","max_cycles":2},"oscillation_pairs":[{"a":"plan","b":"design"},{"a":"verify","b":"tricky"}]}}`
		if err := os.WriteFile(outerPath, []byte(outerRaw), 0644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"init", "-q"},
			{"config", "user.name", "Test"},
			{"config", "user.email", "test@example.invalid"},
			{"add", "."},
			{"commit", "-qm", "fixture"},
		} {
			cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		var out bytes.Buffer
		if !CheckWorkflows(root, wfDir, &out) {
			t.Fatalf("unexpected failure: %s", out.String())
		}
		if _, err := LoadWorkflow(root, "outer@v1", nil); err != nil {
			t.Fatalf("LoadWorkflow failed: %v", err)
		}
	})

	for _, tc := range []struct {
		name      string
		watchJSON string
		want      string
	}{
		{"watch not object", `"bad"`, "workflow-watch"},
		{"watch unknown key", `{"extra":true}`, "workflow-unknown-field"},
		{"watch duplicate key", `{"cycle_anchor":null,"cycle_anchor":null}`, "duplicate key"},
		{"cycle_anchor not object", `{"cycle_anchor":"bad"}`, "workflow-watch"},
		{"cycle_anchor unknown key", `{"cycle_anchor":{"from":"verify","to":"plan","max_cycles":1,"extra":1}}`, "workflow-unknown-field"},
		{"cycle_anchor duplicate key", `{"cycle_anchor":{"from":"verify","from":"work","to":"plan","max_cycles":1}}`, "duplicate key"},
		{"cycle_anchor null from", `{"cycle_anchor":{"from":null,"to":"plan","max_cycles":1}}`, "null is not a string"},
		{"cycle_anchor empty from", `{"cycle_anchor":{"from":"","to":"plan","max_cycles":1}}`, "must be non-empty"},
		{"cycle_anchor same from to", `{"cycle_anchor":{"from":"plan","to":"plan","max_cycles":1}}`, "from and to must differ"},
		{"cycle_anchor missing max_cycles", `{"cycle_anchor":{"from":"verify","to":"plan"}}`, "missing required integer"},
		{"cycle_anchor null max_cycles", `{"cycle_anchor":{"from":"verify","to":"plan","max_cycles":null}}`, "null is not an integer"},
		{"cycle_anchor float max_cycles", `{"cycle_anchor":{"from":"verify","to":"plan","max_cycles":1.5}}`, "expected an integer"},
		{"cycle_anchor zero max_cycles", `{"cycle_anchor":{"from":"verify","to":"plan","max_cycles":0}}`, "must be >= 1"},
		{"cycle_anchor unknown stage", `{"cycle_anchor":{"from":"missing","to":"plan","max_cycles":1}}`, "unknown stage"},
		{"cycle_anchor design not allowed when undeclared", `{"cycle_anchor":{"from":"verify","to":"design","max_cycles":1}}`, "unknown stage"},
		{"oscillation_pairs not list", `{"oscillation_pairs":{}}`, "expected a list"},
		{"oscillation_pairs null entry", `{"oscillation_pairs":[null]}`, "expected an object"},
		{"oscillation_pairs unknown key", `{"oscillation_pairs":[{"a":"plan","b":"work","extra":1}]}`, "workflow-unknown-field"},
		{"oscillation_pairs duplicate key", `{"oscillation_pairs":[{"a":"plan","a":"verify","b":"work"}]}`, "duplicate key"},
		{"oscillation_pairs null a", `{"oscillation_pairs":[{"a":null,"b":"work"}]}`, "null is not a string"},
		{"oscillation_pairs empty b", `{"oscillation_pairs":[{"a":"plan","b":""}]}`, "must be non-empty"},
		{"oscillation_pairs same a b", `{"oscillation_pairs":[{"a":"plan","b":"plan"}]}`, "a and b must differ"},
		{"oscillation_pairs null threshold", `{"oscillation_pairs":[{"a":"plan","b":"work","threshold":null}]}`, "null is not an integer"},
		{"oscillation_pairs threshold below 2", `{"oscillation_pairs":[{"a":"plan","b":"work","threshold":1}]}`, "must be >= 2"},
		{"oscillation_pairs float threshold", `{"oscillation_pairs":[{"a":"plan","b":"work","threshold":2.5}]}`, "expected an integer"},
		{"oscillation_pairs null hint", `{"oscillation_pairs":[{"a":"plan","b":"work","hint":null}]}`, "null is not a string"},
		{"oscillation_pairs int hint", `{"oscillation_pairs":[{"a":"plan","b":"work","hint":123}]}`, "expected a string"},
		{"oscillation_pairs unknown stage", `{"oscillation_pairs":[{"a":"plan","b":"missing"}]}`, "unknown stage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, path := writeWF(t, tc.watchJSON)
			var out bytes.Buffer
			if CheckWorkflows(root, path, &out) || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("want failure containing %q, got ok=%v out=%s", tc.want, false, out.String())
			}
		})
	}
}
