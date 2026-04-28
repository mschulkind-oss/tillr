package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupProject creates a tmp project dir with a .claude/agents/<name>.md
// file and returns the project root.
func setupProject(t *testing.T, personas ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range personas {
		path := filepath.Join(dir, name+".md")
		if err := os.WriteFile(path, []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func TestList_DiscoversAgentMDFiles(t *testing.T) {
	root := setupProject(t, "implementer", "researcher", "reviewer")

	personas, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(personas) != 3 {
		t.Fatalf("want 3 personas, got %d", len(personas))
	}
	want := map[string]bool{"implementer": false, "researcher": false, "reviewer": false}
	for _, p := range personas {
		want[p.Name] = true
		if p.UpdatedAt != "" || p.ContextWords != 0 {
			t.Errorf("persona %q should have empty context, got %+v", p.Name, p)
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("missing persona %q", k)
		}
	}
}

func TestList_NoAgentsDir(t *testing.T) {
	root := t.TempDir()
	personas, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(personas) != 0 {
		t.Fatalf("want 0 personas, got %d", len(personas))
	}
}

func TestAppendAndRead(t *testing.T) {
	root := setupProject(t, "implementer")

	if err := Append(root, "implementer", "First entry", "Body of first entry"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := Append(root, "implementer", "Second entry", "Body of second entry"); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	body, err := ContextRead(root, "implementer")
	if err != nil {
		t.Fatalf("ContextRead: %v", err)
	}
	if !strings.Contains(body, "## ") || !strings.Contains(body, "First entry") {
		t.Errorf("body missing first entry header: %q", body)
	}
	if !strings.Contains(body, "Second entry") {
		t.Errorf("body missing second entry: %q", body)
	}
	if !strings.Contains(body, "Body of first entry") {
		t.Errorf("body missing first body: %q", body)
	}

	p, err := Get(root, "implementer")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.ContextWords == 0 {
		t.Error("ContextWords should be >0 after appends")
	}
	if p.UpdatedAt == "" {
		t.Error("UpdatedAt should be populated after append")
	}
}

func TestAppend_RejectsUnknownPersona(t *testing.T) {
	root := setupProject(t, "implementer")
	err := Append(root, "ghost", "summary", "body")
	if err == nil {
		t.Fatal("Append on unknown persona should error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention persona name: %v", err)
	}
}

func TestCompact_KeepsRecentBlocks(t *testing.T) {
	root := setupProject(t, "implementer")
	for i := 0; i < 5; i++ {
		if err := Append(root, "implementer", "summary", strings.Repeat("word ", 100)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	result, err := Compact(root, "implementer", 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.BlocksMoved != 3 {
		t.Errorf("want 3 blocks moved, got %d", result.BlocksMoved)
	}
	if result.BlocksKept != 2 {
		t.Errorf("want 2 blocks kept, got %d", result.BlocksKept)
	}
	if result.WordsAfter >= result.WordsBefore {
		t.Errorf("want WordsAfter < WordsBefore, got %d >= %d", result.WordsAfter, result.WordsBefore)
	}
	if _, err := os.Stat(filepath.Join(root, result.BackupPath)); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
}

func TestCompact_NoOpWhenSmall(t *testing.T) {
	root := setupProject(t, "implementer")
	if err := Append(root, "implementer", "only", "tiny"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	result, err := Compact(root, "implementer", 20)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.BlocksMoved != 0 {
		t.Errorf("want no blocks moved, got %d", result.BlocksMoved)
	}
}

func TestRetros(t *testing.T) {
	root := setupProject(t)

	retros, err := ListRetros(root)
	if err != nil {
		t.Fatalf("ListRetros (empty): %v", err)
	}
	if len(retros) != 0 {
		t.Fatalf("want 0 retros, got %d", len(retros))
	}

	rel, err := WriteRetro(root, "2026-04-28T12-00-00Z", "# A retro\n")
	if err != nil {
		t.Fatalf("WriteRetro: %v", err)
	}
	if !strings.Contains(rel, "swarf/retros") {
		t.Errorf("retro path not under swarf/retros: %q", rel)
	}

	retros, err = ListRetros(root)
	if err != nil {
		t.Fatalf("ListRetros: %v", err)
	}
	if len(retros) != 1 {
		t.Fatalf("want 1 retro, got %d", len(retros))
	}

	body, err := ReadRetro(root, "2026-04-28T12-00-00Z")
	if err != nil {
		t.Fatalf("ReadRetro: %v", err)
	}
	if !strings.Contains(body, "# A retro") {
		t.Errorf("body wrong: %q", body)
	}
}

func TestReadRetro_RejectsTraversal(t *testing.T) {
	root := setupProject(t)
	if _, err := ReadRetro(root, "../etc/passwd"); err == nil {
		t.Fatal("ReadRetro should reject traversal")
	}
	if _, err := ReadRetro(root, "subdir/file"); err == nil {
		t.Fatal("ReadRetro should reject path with /")
	}
}

func TestConductor(t *testing.T) {
	root := setupProject(t)

	body, err := ConductorRead(root)
	if err != nil {
		t.Fatalf("ConductorRead empty: %v", err)
	}
	if body != "" {
		t.Errorf("want empty body, got %q", body)
	}

	if err := ConductorAppend(root, "init", "First conductor entry"); err != nil {
		t.Fatalf("ConductorAppend: %v", err)
	}

	body, err = ConductorRead(root)
	if err != nil {
		t.Fatalf("ConductorRead: %v", err)
	}
	if !strings.Contains(body, "First conductor entry") {
		t.Errorf("body missing entry: %q", body)
	}
}
