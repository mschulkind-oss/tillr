package item

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExpressItemDirective(t *testing.T) {
	for _, value := range []string{"true", "false", "yes"} {
		blocks, findings := parseItemBlocks("docs/work.md", []byte("# Work\n<!-- tillr: item id=\"item-work\" paths=\"tools/\" express=\""+value+"\" -->\n- **c1:** Done.\n"))
		if value == "yes" {
			if len(findings) == 0 {
				t.Fatal("invalid express accepted")
			}
			continue
		}
		if len(findings) != 0 {
			t.Fatalf("%s: %+v", value, findings)
		}
		raw, err := json.Marshal(blocks[0].payload)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"express":`+value) {
			t.Fatalf("express lost: %s", raw)
		}
	}
}
