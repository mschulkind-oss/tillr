package run_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractedReportIsPinnedToItsRunNotTheReusableLane(t *testing.T) {
	body := "# run report\ntokens: unknown\ncost usd: unknown\n---\nConflict: c1 contradicts the API\nRESULT: blocked\n"
	backends, adapter := backendWithExtractor(t, body, "0")
	rec, _ := execFixture(t, backends, adapter, "fixture")
	report := ""
	for _, ref := range rec.ArtifactRefs {
		if ref.Kind == "report" {
			report = ref.Ref
		}
	}
	if report == "" {
		t.Fatal("run record carries no extracted report artifact")
	}
	raw, err := os.ReadFile(report)
	if err != nil || string(raw) != body {
		t.Fatalf("report %s / %v", raw, err)
	}
	// A later run owns the lane's conventional report.md; this run keeps its evidence.
	lane := filepath.Dir(filepath.Dir(filepath.Dir(report)))
	if err := os.WriteFile(filepath.Join(lane, "report.md"), []byte("RESULT: done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(report)
	if err != nil || string(raw) != body {
		t.Fatalf("report overwritten: %s / %v", raw, err)
	}
}
