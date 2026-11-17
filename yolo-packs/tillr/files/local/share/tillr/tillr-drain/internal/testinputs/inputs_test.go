package testinputs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "sibling-inputs-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	module := filepath.Join(dir, "tillr-drain")
	if err := os.Mkdir(module, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range Siblings() {
		pkg := filepath.Join(dir, name, "nested")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "source.go"), []byte("package nested\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return module
}

func TestConcurrentAliasCreationAndReuse(t *testing.T) {
	module := fixture(t)
	start := make(chan struct{})
	errors := make(chan error, 20)
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			<-start
			errors <- Track(module)
		})
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := Track(module); err != nil {
		t.Fatal(err)
	}
}

func TestUnexpectedAliasIsRejectedWithoutReplacement(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			module := fixture(t)
			alias := filepath.Join(module, ".sibling-inputs")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(alias, []byte("owner content"), 0o644)
			case "directory":
				err = os.Mkdir(alias, 0o755)
			case "symlink":
				err = os.Symlink("wrong-target", alias)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(alias)
			if err != nil {
				t.Fatal(err)
			}
			if err := Track(module); err == nil || !strings.Contains(err.Error(), "must be a symlink to ..") {
				t.Fatalf("unexpected alias accepted: %v", err)
			}
			after, err := os.Lstat(alias)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("unexpected alias replaced: %v", err)
			}
		})
	}
}

func TestMissingSiblingDirectoryIsAnError(t *testing.T) {
	module := fixture(t)
	name := Siblings()[0]
	if err := os.RemoveAll(filepath.Join(module, "..", name)); err != nil {
		t.Fatal(err)
	}
	if err := Track(module); err == nil || !strings.Contains(err.Error(), name) {
		t.Fatalf("missing sibling source accepted: %v", err)
	}
}
