package docshare_test

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr-item/docshare"
)

func TestDeclarationComparisonSharesDirectiveGrammar(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		declared   bool
	}{
		{"plain", `<!-- tillr: item id="item-a" paths="docs/" -->`, true},
		{"single quotes", `<!-- tillr: item id='item-a' paths='docs/' -->`, true},
		{"multiline", "<!-- tillr: item\n id=\"item-a\"\n paths=\"docs/\" -->", true},
		{"unknown attribute", `<!-- tillr: item unknown="value" id="item-a" -->`, true},
		{"partial malformed", `<!-- tillr: item id="item-a" not-an-attribute -->`, true},
		{"malformed before id", `<!-- tillr: item not-an-attribute id="item-a" -->`, false},
		{"duplicate first wins", `<!-- tillr: item id="item-other" id="item-a" -->`, false},
		{"unclosed", `<!-- tillr: item id="item-a"`, false},
		{"inline", `Text <!-- tillr: item id="item-a" -->`, false},
		{"fenced", "```md\n<!-- tillr: item id=\"item-a\" -->\n```", false},
		{"longer fence", "~~~~\n~~~\n<!-- tillr: item id=\"item-a\" -->\n~~~~", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fed, missing := docshare.Sharing([]docshare.Document{{Path: "docs/request.md", Content: []byte(tc.body)}}, map[string]string{"item-a": "in-queue"})
			if fed != tc.declared || (len(missing) == 0) != tc.declared {
				t.Fatalf("fed=%v missing=%v", fed, missing)
			}
		})
	}
}

func TestSharingIncludesTerminalDeclarationsButExcludesTerminalResidue(t *testing.T) {
	docs := []docshare.Document{{Path: "docs/request.md", Content: []byte(`<!-- tillr: item id="item-done" -->`)}}
	states := map[string]string{"item-done": "closed", "item-missing": "in-queue", "item-landed": "landed", "item-abandoned": "abandoned", "item-deleted": "deleted", "item-resolved": "resolved-elsewhere", "item-comments-doc-1": "in-queue"}
	fed, missing := docshare.Sharing(docs, states)
	if !fed || !reflect.DeepEqual(missing, []string{"item-missing"}) {
		t.Fatalf("fed=%v missing=%v", fed, missing)
	}
	fed, missing = docshare.Sharing(nil, states)
	if fed || !reflect.DeepEqual(missing, []string{"item-missing"}) {
		t.Fatalf("document-free fed=%v missing=%v", fed, missing)
	}
}
