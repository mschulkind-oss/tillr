package item_test

import (
	"strings"
	"testing"
)

func TestExpressSyncPersistsAndUpdatesDefinition(t *testing.T) {
	s := syncStore(t)
	express := strings.Replace(goodBlock, `paths="tools/thing/"`, `paths="tools/thing/" express="true"`, 1)
	runSync(t, s, "", doc("docs/a.md", express))
	if got := column(t, s, `SELECT express FROM items`); got != "1" {
		t.Fatal(got)
	}
	if report := runSync(t, s, "", doc("docs/a.md", express)); report.UpdatedCount != 0 {
		t.Fatalf("not idempotent: %+v", report)
	}
	if report := runSync(t, s, "", doc("docs/a.md", goodBlock)); report.UpdatedCount != 1 {
		t.Fatalf("removal lost: %+v", report)
	}
	if got := column(t, s, `SELECT express FROM items`); got != "0" {
		t.Fatal(got)
	}
}
