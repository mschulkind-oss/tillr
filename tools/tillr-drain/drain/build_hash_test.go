package drain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The green-build deployment fixture uses only build.go. Keep the build stamp
// self-contained so that fixture measures the delivered stamp, not a substitute.
func TestBuildStampCompilesWithoutTheDrainLoop(t *testing.T) {
	dir := t.TempDir()
	source, err := os.ReadFile("build.go")
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"build.go": source,
		"go.mod":   []byte("module fixture\n\ngo 1.24\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("standalone build stamp: %v\n%s", err, out)
	}
}

func TestBuildHashPreservesTheDispatchFactEncoding(t *testing.T) {
	b := BuildStamp{Revision: "revision", Modified: true, Digest: "digest", Executable: "/old/path"}
	want, err := inputHash([]any{b.Revision, b.Modified, b.Digest})
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Hash()
	if err != nil || got != want {
		t.Fatalf("Hash = %q, %v; want %q", got, err, want)
	}
	b.Executable = "/new/path"
	moved, err := b.Hash()
	if err != nil || moved != got {
		t.Fatalf("relocation changed build identity: %q, %v", moved, err)
	}
}
