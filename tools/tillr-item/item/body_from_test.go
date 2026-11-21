package item_test

import (
	"github.com/mschulkind-oss/tillr-item/item"
	"reflect"
	"strings"
	"testing"
)

func TestBlocksFromUsesDeclarationParser(t *testing.T) {
	raw := fromBlock("item-new", "item-source")
	for _, body := range []string{raw, strings.Replace(raw, `paths="tools/thing/"`, `paths="../outside"`, 1)} {
		if got := item.BlocksFrom("docs/new.md", "item-source", []byte(body)); !reflect.DeepEqual(got, []string{"item-new"}) {
			t.Fatalf("valid outcome (legacy paths are ignored): %v", got)
		}
	}
	for _, body := range []string{raw, "```md\n" + raw + "```\n", strings.Replace(raw, `id="item-new"`, `id="INVALID"`, 1)} {
		source := "item-source"
		if body == raw {
			source = "item-other"
		}
		if got := item.BlocksFrom("docs/new.md", source, []byte(body)); len(got) != 0 {
			t.Fatalf("not a valid outcome: %v", got)
		}
	}
}
