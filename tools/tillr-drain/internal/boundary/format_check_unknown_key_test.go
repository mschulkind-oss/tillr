package boundary

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A candidate whose .tillr.toml carries a key this build does not know must still reach
// the settle format check: only [gate] checks_dir is read from it.
func TestUnformattedRepoFilesIgnoresUnknownCandidateSettings(t *testing.T) {
	worktree := t.TempDir()
	toml := "[gate]\nchecks_dir = \"custom/checks\"\ndisabled_tests = [\"pkg:TestX\"]\n\n[future]\nkey = 1\n"
	if err := os.WriteFile(filepath.Join(worktree, ".tillr.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(worktree, "custom", "checks", "format.json"), `{"command":["sh","-c","echo a.go; exit 1"]}`)
	got, err := UnformattedRepoFiles(context.Background(), worktree)
	if err != nil {
		t.Fatalf("unknown candidate keys must not refuse the settle format check: %v", err)
	}
	if want := []string{"a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"../x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UnformattedRepoFiles(context.Background(), worktree); err == nil {
		t.Fatal("an invalid checks_dir must still refuse")
	}
}
