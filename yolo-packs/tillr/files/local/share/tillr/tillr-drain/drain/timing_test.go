package drain

import (
	"os"
	"path/filepath"
	"testing"
)

// scratchUnderDurable answers a directory under $YOLO_DURABLE_DIR, never /tmp: the owner
// refuses /tmp for evidence, and a suite that writes there loses its scratch when the box is
// cleaned.
func scratchUnderDurable(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, "drain-timing-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
