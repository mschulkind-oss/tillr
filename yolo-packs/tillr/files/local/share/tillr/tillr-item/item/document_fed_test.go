package item_test

import (
	"testing"
)

func TestDocumentFedComparisonIncludesTerminalDeclarations(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/a.md", goodBlock))
	exec(t, s, `UPDATE items SET state='closed'`)
	report := runSync(t, s, "", doc("docs/a.md", goodBlock))
	if !report.DocumentFed || report.UnsharedOpen != 0 {
		t.Fatalf("terminal declaration: %+v", report)
	}
	report = runSync(t, s, "", doc("docs/a.md", "# Nothing declared\n"))
	if report.DocumentFed || report.UnsharedOpen != 0 {
		t.Fatalf("terminal without declaration: %+v", report)
	}
	exec(t, s, `UPDATE items SET state='in-queue'`)
	report = runSync(t, s, "", doc("docs/a.md", "# Nothing declared\n"))
	if report.DocumentFed || report.UnsharedOpen != 1 {
		t.Fatalf("document-free open store: %+v", report)
	}
}
