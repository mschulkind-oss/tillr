package disabledtests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/disabledtests"
)

const drainSel = "go-test:tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestWhy/resumed_segment"

func TestParseAcceptsTheDesignExample(t *testing.T) {
	list, err := disabledtests.Parse([]byte(`{"version":1,"disabled":[
	  {"selector":"` + drainSel + `","source":"trunk-proof","failed_on":"abc","first_line":"idle_test.go:88: x"},
	  {"selector":"go-test-tillr-drain","source":"hand"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Disabled) != 2 {
		t.Fatalf("got %d entries", len(list.Disabled))
	}
	got := list.GoSelectors("tools/tillr-drain")
	if len(got) != 1 || got[0].Package != "github.com/mschulkind-oss/tillr-drain/drain" || got[0].Top() != "TestWhy" || !got[0].IsSubtest() {
		t.Fatalf("GoSelectors = %+v", got)
	}
	if len(list.GoSelectors("tools/other")) != 0 {
		t.Fatal("another module's selectors must not match")
	}
	if c := list.Checks(); len(c) != 1 || c[0] != "go-test-tillr-drain" {
		t.Fatalf("Checks = %v", c)
	}
}

func TestParseRefusesMalformedLists(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"not json":         {`nope`, "not a disabled-test list"},
		"unknown field":    {`{"version":1,"disabled":[],"extra":1}`, "unknown field"},
		"wrong version":    {`{"version":2,"disabled":[]}`, "version must be 1"},
		"missing version":  {`{"disabled":[]}`, "version must be 1"},
		"trailing data":    {`{"version":1,"disabled":[]} {}`, "trailing data"},
		"bad selector":     {`{"version":1,"disabled":[{"selector":"TestX","source":"hand"}]}`, "selector"},
		"lowercase test":   {`{"version":1,"disabled":[{"selector":"go-test:m:p:testX","source":"hand"}]}`, "selector"},
		"empty subtest":    {`{"version":1,"disabled":[{"selector":"go-test:m:p:TestX/","source":"hand"}]}`, "selector"},
		"missing test":     {`{"version":1,"disabled":[{"selector":"go-test:m:p","source":"hand"}]}`, "selector"},
		"unknown source":   {`{"version":1,"disabled":[{"selector":"a-check","source":"robot"}]}`, "source"},
		"duplicate":        {`{"version":1,"disabled":[{"selector":"a-check","source":"hand"},{"selector":"a-check","source":"hand"}]}`, "listed twice"},
		"unknown entry kv": {`{"version":1,"disabled":[{"selector":"a-check","source":"hand","item":"x"}]}`, "unknown field"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := disabledtests.Parse([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadMissingFileIsEmptyAndBadFileNamesPath(t *testing.T) {
	dir := t.TempDir()
	list, err := disabledtests.Load(filepath.Join(dir, "none.json"))
	if err != nil || len(list.Disabled) != 0 {
		t.Fatalf("missing file: %+v, %v", list, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"version":1,"disabled":[{"selector":"X","source":"hand"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := disabledtests.Load(bad); err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("Load error = %v, want it to name the file", err)
	}
}
