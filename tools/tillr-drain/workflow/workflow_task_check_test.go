package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTaskFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNodeTaskRefusals(t *testing.T) {
	root := t.TempDir()
	writeTaskFixtureFile(t, root, "tools/gate/go_affected.py", "# python calculator\n")
	writeTaskFixtureFile(t, root, "tools/gate/checks/go-affected.json", `{"id":"go-affected","command":["python3","tools/gate/go_affected.py"]}`+"\n")
	writeTaskFixtureFile(t, root, "tests/go-affected.test.py", `
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
mod = load_module("go_affected", ROOT / "tools" / "gate" / "go_affected.py")
`)
	writeTaskFixtureFile(t, root, "tools/gate/checks/go-affected-test.json", `{
  "id": "go-affected-test",
  "command": ["python3", "tests/go-affected.test.py"],
  "reads": ["tests/go-affected.test.py", "tools/gate/go_affected.py"]
}`)
	writeTaskFixtureFile(t, root, "tools/tillr-gate/goaffected/goaffected_test.go", `package goaffected
import (
	"os"
	"path/filepath"
	"testing"
)
func TestCheckDeclarationsCoverSelectionInputs(t *testing.T) {
	_, _ = os.ReadFile(filepath.Join(root, "tools", "gate", "checks", "go-affected.json"))
}
`)
	writeTaskFixtureFile(t, root, "tests/unrelated.test.py", "assert 1 + 1 == 2\n")

	for _, tc := range []struct {
		name       string
		nodeID     string
		access     string
		writeScope []string
		prompt     string
		wantCode   string
		wantSubstr []string
	}{
		{
			name:       "file to change outside write_scope",
			nodeID:     "cutover",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "Delete tools/gate/go_affected.py and update tools/gate/checks/go-affected.json.",
			wantCode:   "workflow-task-outside-scope",
			wantSubstr: []string{"cutover", "tools/gate/go_affected.py"},
		},
		{
			name:       "passive voice file to change outside write_scope",
			nodeID:     "cleanup",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "tools/gate/go_affected.py must be deleted.",
			wantCode:   "workflow-task-outside-scope",
			wantSubstr: []string{"cleanup", "tools/gate/go_affected.py"},
		},
		{
			name:       "python test loading deleted file outside write_scope",
			nodeID:     "delete-py",
			access:     "write",
			writeScope: []string{"tools/gate/go_affected.py"},
			prompt:     "Delete tools/gate/go_affected.py and ensure python3 tests/go-affected.test.py passes.",
			wantCode:   "workflow-task-outside-scope",
			wantSubstr: []string{"delete-py", "tests/go-affected.test.py"},
		},
		{
			name:       "go test pinning changed check declaration outside write_scope via go test -C",
			nodeID:     "update-check",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "Update the check declaration tools/gate/checks/go-affected.json and pass go test -C tools/tillr-gate ./goaffected.",
			wantCode:   "workflow-task-outside-scope",
			wantSubstr: []string{"update-check", "tools/tillr-gate/goaffected/goaffected_test.go"},
		},
		{
			name:       "go test pinning changed check declaration outside write_scope via cd && go test",
			nodeID:     "update-check-cd",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "Update tools/gate/checks/go-affected.json and verify cd tools/tillr-gate && go test ./goaffected -run TestCheckDeclarationsCoverSelectionInputs.",
			wantCode:   "workflow-task-outside-scope",
			wantSubstr: []string{"update-check-cd", "tools/tillr-gate/goaffected/goaffected_test.go"},
		},
		{
			name:       "gate check json reading changed file outside write_scope",
			nodeID:     "gate-check-ref",
			access:     "write",
			writeScope: []string{"tools/gate/go_affected.py"},
			prompt:     "Delete tools/gate/go_affected.py and pass go-affected-test.",
			wantCode:   "workflow-task-outside-scope",
			wantSubstr: []string{"gate-check-ref", "tools/gate/checks/go-affected-test.json"},
		},
		{
			name:       "task forbids committing with parenthetical",
			nodeID:     "impl",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "Update tools/gate/checks/go-affected.json. Leave all changes in the worktree (do not commit yet).",
			wantCode:   "workflow-task-forbids-commit",
			wantSubstr: []string{"impl"},
		},
		{
			name:       "task tells worker to leave uncommitted",
			nodeID:     "tests",
			access:     "write",
			writeScope: []string{"tools/item-a/"},
			prompt:     "Write failing test, leave uncommitted.",
			wantCode:   "workflow-task-forbids-commit",
			wantSubstr: []string{"tests"},
		},
		{
			name:       "task says without committing",
			nodeID:     "draft",
			access:     "write",
			writeScope: []string{"tools/item-a/"},
			prompt:     "Edit tools/item-a/foo.go without committing.",
			wantCode:   "workflow-task-forbids-commit",
			wantSubstr: []string{"draft"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNodeTask(root, tc.nodeID, tc.access, tc.writeScope, tc.prompt)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantCode)
			}
			if !strings.Contains(err.Error(), tc.wantCode) {
				t.Fatalf("error %q lacks code %q", err.Error(), tc.wantCode)
			}
			for _, sub := range tc.wantSubstr {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q lacks %q", err.Error(), sub)
				}
			}
		})
	}
}

func TestValidateNodeTaskPasses(t *testing.T) {
	root := t.TempDir()
	writeTaskFixtureFile(t, root, "tools/gate/go_affected.py", "# python calculator\n")
	writeTaskFixtureFile(t, root, "tools/gate/checks/go-affected.json", `{"id":"go-affected"}`+"\n")
	writeTaskFixtureFile(t, root, "tests/go-affected.test.py", `
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
mod = load_module("go_affected", ROOT / "tools" / "gate" / "go_affected.py")
`)
	writeTaskFixtureFile(t, root, "tests/unrelated.test.py", "assert 1 + 1 == 2\n")
	writeTaskFixtureFile(t, root, "docs/design/ref.md", "# ref\n")

	for _, tc := range []struct {
		name       string
		access     string
		writeScope []string
		prompt     string
	}{
		{
			name:       "files and check inside write_scope",
			access:     "write",
			writeScope: []string{"tools/gate/go_affected.py", "tests/go-affected.test.py"},
			prompt:     "Delete tools/gate/go_affected.py and update tests/go-affected.test.py, then pass python3 tests/go-affected.test.py and commit.",
		},
		{
			name:       "read-only reference outside write_scope",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "Read docs/design/ref.md, then update tools/gate/checks/go-affected.json using the format in docs/design/ref.md.",
		},
		{
			name:       "explicitly excluded file outside write_scope",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "Do not edit tools/gate/go_affected.py; only update tools/gate/checks/go-affected.json.",
		},
		{
			name:       "unrelated check outside write_scope passes",
			access:     "write",
			writeScope: []string{"tools/gate/checks/go-affected.json"},
			prompt:     "Update tools/gate/checks/go-affected.json and pass python3 tests/unrelated.test.py.",
		},
		{
			name:       "negated leave uncommitted and specific do-not-commit object",
			access:     "write",
			writeScope: []string{"tools/item-a/"},
			prompt:     "Update tools/item-a/main.go. Do not leave uncommitted changes and do not commit unrelated files.",
		},
		{
			name:       "expanded tillr module companion path allowed",
			access:     "write",
			writeScope: []string{"tools/tillr-drain/workflow/"},
			prompt:     "Update tools/tillr-drain/workflow/workflow.go and tools/tillr-drain/README.md.",
		},
		{
			name:       "read_only node may say do not commit",
			access:     "read_only",
			writeScope: nil,
			prompt:     "Inspect tools/gate/go_affected.py and do not commit anything.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateNodeTask(root, "node-1", tc.access, tc.writeScope, tc.prompt); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
