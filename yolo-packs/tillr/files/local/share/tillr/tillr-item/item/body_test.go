package item_test

import (
	"github.com/mschulkind-oss/tillr-item/item"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionBodySelectsOnlyItsSection(t *testing.T) {
	root := scratch(t)
	doc := "# Document\n## Other\nnot ours\n## The item\nbody\n```md\n## Not a heading\n```\n### Child\nchild body\n## Next\nnot ours either\n"
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := item.DefinitionBody(root, "doc.md#the-item", "item-a")
	if err != nil || !strings.Contains(body, "child body") || strings.Contains(body, "not ours") || !strings.Contains(body, "Not a heading") {
		t.Fatalf("body %q, err %v", body, err)
	}
	whole, err := item.DefinitionBody(root, "doc.md", "item-a")
	if err != nil || whole != doc {
		t.Fatalf("whole file %q %v", whole, err)
	}
	missing, err := item.DefinitionBody(root, "missing.md#anchor", "item-a")
	if err != nil || !strings.Contains(missing, "source missing") {
		t.Fatalf("missing %q %v", missing, err)
	}
	missing, err = item.DefinitionBody(root, "doc.md#absent", "item-a")
	if err != nil || !strings.Contains(missing, "anchor missing") {
		t.Fatalf("anchor %q %v", missing, err)
	}
}
