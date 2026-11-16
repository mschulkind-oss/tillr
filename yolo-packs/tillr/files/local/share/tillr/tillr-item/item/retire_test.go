package item

import (
	"strings"
	"testing"
)

func TestRetireItemBlock(t *testing.T) {
	block := "<!-- tillr: item\n id=\"item-target\" paths=\"tools/\"\n-->\n\n- **c1:** Do something.\n  Continued criterion.\n"
	other := "## Other\n<!-- tillr: item id=\"item-other\" paths=\"tools/\" -->\n- **c1:** Keep this.\n"
	for _, tc := range []struct{ name, before, after string }{
		{"first", "## Target\n" + block + "\n" + other, "## Target\n\n\n" + other},
		{"last", other + "## Target\n" + block, other + "## Target\n\n"},
		{"middle", other + "## Target\n" + block + "\n" + other, other + "## Target\n\n\n" + other},
		{"heading stays", "## Target\n" + block, "## Target\n\n"},
		{"prose stays", "## Target\n" + block + "\nKeep this prose.\n", "## Target\n\n\nKeep this prose.\n"},
		{"absent", other, other},
		{"fenced example", "```md\n" + block + "```\n", "```md\n" + block + "```\n"},
		{"crlf", strings.ReplaceAll("## Target\n"+block, "\n", "\r\n"), "## Target\r\n\r\n"},
		{"no final newline", strings.TrimSuffix("## Target\n"+block, "\n"), "## Target\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := RetireItemBlock(tc.before, "item-target")
			if got != tc.after {
				t.Fatalf("got %q, want %q", got, tc.after)
			}
			if found != (tc.before != tc.after) {
				t.Fatalf("found = %v", found)
			}
			again, found := RetireItemBlock(got, "item-target")
			if again != got || found {
				t.Fatal("retirement is not idempotent")
			}
		})
	}
}

func TestRetireItemBlockRemovesOnlyMatchingDependencies(t *testing.T) {
	for _, quote := range []string{"\"", "'", ""} {
		before := "## Other\n<!-- tillr: item\n id=\"item-other\" paths=\"tools/\"\n blocked_by=" + quote + "item-target,item-target-extra,item-second" + quote + "\n-->\n- **c1:** Keep item-target in prose.\n"
		want := strings.Replace(before, "item-target,item-target-extra,item-second", "item-target-extra,item-second", 1)
		got, found := RetireItemBlock(before, "item-target")
		if got != want || found {
			t.Fatalf("got %q, found %v; want %q", got, found, want)
		}
	}
	before := "## Other\n<!-- tillr: item id=\"item-other\" paths=\"tools/\" blocked_by=\"item-target\" -->\n- **c1:** Keep.\n"
	got, _ := RetireItemBlock(before, "item-target")
	unquoted := strings.Replace(before, `blocked_by="item-target"`, `blocked_by=item-target`, 1)
	got, _ = RetireItemBlock(unquoted, "item-target")
	if got != strings.Replace(before, `blocked_by="item-target"`, `blocked_by=""`, 1) {
		t.Fatal(got)
	}
	if _, findings := parseItemBlocks("docs/fixture.md", []byte(got)); len(findings) != 0 {
		t.Fatalf("empty unquoted dependency became invalid: %v", findings)
	}
}
