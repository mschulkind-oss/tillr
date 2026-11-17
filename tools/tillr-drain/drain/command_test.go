package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

const commandRegistryFixture = `{"check":{"argv":["python3","tools/check.py"],"flags":{"--text":"string","--count":"number","--path":"path"}}}`

func TestCommandWorkflowValidation(t *testing.T) {
	root := scratchUnderDurable(t)
	if err := os.MkdirAll(filepath.Join(root, "config/tillr/workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ flags, ref, want string }{
		{`{"--text":"$(touch escaped); spaces","--count":2,"--path":"tools/check.py"}`, "check", ""},
		{`{}`, "absent", "command-reference"},
		{`{"--unknown":"x"}`, "check", "command-flag"},
		{`{"--text":true}`, "check", "command-value"},
		{`{"--text":{}}`, "check", "command-value"},
		{`{"--text":[]}`, "check", "command-value"},
		{`{"--text":null}`, "check", "command-value"},
		{`{"--count":"2"}`, "check", "command-value"},
		{`{"--path":"../outside"}`, "check", "command-path"},
		{`{"--path":"/outside"}`, "check", "command-path"},
	} {
		t.Run(tc.ref+tc.flags, func(t *testing.T) {
			writeForkFile(t, filepath.Join(root, "config/tillr/commands.json"), commandRegistryFixture)
			raw := `{"name":"command","version":1,"stages":[{"id":"check","kind":"command","command_ref":"` + tc.ref + `","flags":` + tc.flags + `,"access":"read_only"}],"edges":[]}`
			writeForkFile(t, filepath.Join(root, "config/tillr/workflows/command@v1.json"), raw)
			w, err := loadWorkflow(root, "command@v1", &Policy{})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				want := "python3|tools/check.py|--count=2|--path=tools/check.py|--text=$(touch escaped); spaces"
				if got := strings.Join(w.Stages[0].Command, "|"); got != want {
					t.Fatalf("argv %q", got)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
}

func TestCommandWorkflowRefusalsHoldBeforeStaging(t *testing.T) {
	for _, tc := range []struct{ name, ref, flags, registry, want string }{
		{"missing-reference", "absent", `{}`, commandRegistryFixture, "command-reference"},
		{"unknown-flag", "check", `{"--other":"x"}`, commandRegistryFixture, "command-flag"},
		{"boolean-value", "check", `{"--text":true}`, commandRegistryFixture, "command-value"},
		{"object-value", "check", `{"--text":{}}`, commandRegistryFixture, "command-value"},
		{"outside-path", "check", `{"--path":"../escape"}`, commandRegistryFixture, "command-path"},
		{"symlink-path", "check", `{"--path":"escape/new.txt"}`, commandRegistryFixture, "command-path"},
		{"symlink-parent-path", "check", `{"--path":"escape/../outside"}`, commandRegistryFixture, "command-path"},
		{"missing-registry", "check", `{}`, "", "command-registry"},
		{"malformed-registry", "check", `{}`, "{", "command-registry"},
		{"trailing-registry", "check", `{}`, commandRegistryFixture + ` {}`, "command-registry"},
		{"shell-program", "check", `{}`, `{"check":{"argv":["sh","-c","true"],"flags":{}}}`, "command-registry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, steps := workflowLoopFixture(t, "")
			if tc.registry != "" {
				writeForkFile(t, filepath.Join(f.root, "config/tillr/commands.json"), tc.registry)
			}
			if strings.HasPrefix(tc.name, "symlink-") {
				if err := os.Symlink(scratchUnderDurable(t), filepath.Join(f.root, "escape")); err != nil {
					t.Fatal(err)
				}
			}
			raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"work","kind":"agent","seat":"build","access":"write"},{"id":"check","kind":"command","command_ref":"` + tc.ref + `","flags":` + tc.flags + `,"access":"read_only"}],"edges":[{"from_node":"work","to_node":"check"}]}`
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
			found := false
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID == "item-a" {
					found = true
					if p.decision.Action != ActionHold || !strings.HasPrefix(p.decision.Reason, "workflow-invalid: ") || !strings.Contains(p.decision.Reason, tc.want) || p.decision.RunRequest != nil {
						t.Fatalf("finding %+v", p.decision)
					}
				}
			}
			if !found {
				t.Fatal("missing item")
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var n int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs`).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Errorf("staged %d nodes", n)
				}
				return nil
			})
			if len(steps.runs) != 0 {
				t.Fatalf("started runs %+v", steps.runs)
			}
		})
	}
}

func TestCommandRegistryOptionalForAgentWorkflow(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	if _, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/commands.json"), "broken registry")
	if _, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy); err != nil {
		t.Fatal(err)
	}
}
