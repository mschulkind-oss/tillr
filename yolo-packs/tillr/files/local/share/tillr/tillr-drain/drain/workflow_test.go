package drain

import (
	"encoding/json"
	"strings"
	"testing"
)

const workflowFixture = `{"name":"scout-then-build","version":1,"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"scout","to_node":"work"}]}`

func TestWorkflowLoaderRules(t *testing.T) {
	for _, tc := range []struct {
		rule string
		edit func(map[string]any)
	}{
		{"workflow-unknown-field", func(d map[string]any) { d["timer"] = 1 }},
		{"workflow-unknown-field", func(d map[string]any) { d["stages"].([]any)[0].(map[string]any)["wall"] = 1 }},
		{"workflow-unknown-field", func(d map[string]any) { d["edges"].([]any)[0].(map[string]any)["condition"] = "true" }},
		{"workflow-edges", func(d map[string]any) { delete(d, "edges") }},
		{"workflow-edges", func(d map[string]any) { d["edges"] = nil }},
		{"workflow-empty-stages", func(d map[string]any) { d["stages"] = []any{} }},
		{"workflow-duplicate-id", func(d map[string]any) { s := d["stages"].([]any); s[1].(map[string]any)["id"] = "scout" }},
		{"workflow-unknown-node", func(d map[string]any) { d["edges"].([]any)[0].(map[string]any)["to_node"] = "absent" }},
		{"workflow-cycle", func(d map[string]any) {
			d["edges"] = append(d["edges"].([]any), map[string]any{"from_node": "work", "to_node": "scout"})
		}},
		{"workflow-unknown-seat", func(d map[string]any) { d["stages"].([]any)[0].(map[string]any)["seat"] = "Absent" }},
		{"workflow-kind", func(d map[string]any) { d["stages"].([]any)[0].(map[string]any)["kind"] = "command" }},
		{"workflow-access", func(d map[string]any) { d["stages"].([]any)[0].(map[string]any)["access"] = "anything" }},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			var d map[string]any
			json.Unmarshal([]byte(workflowFixture), &d)
			tc.edit(d)
			raw, _ := json.Marshal(d)
			_, err := decodeWorkflow(raw, "scout-then-build@v1")
			if err == nil || !strings.Contains(err.Error(), tc.rule) {
				t.Fatalf("got %v", err)
			}
		})
	}
	w, err := decodeWorkflow([]byte(workflowFixture), "scout-then-build@v1")
	if err != nil || len(w.Stages) != 2 || w.Stages[0].ID != "scout" {
		t.Fatalf("%+v %v", w, err)
	}
}
