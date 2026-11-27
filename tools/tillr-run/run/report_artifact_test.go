package run_test

import (
	"os"
	"path/filepath"
	"strings"
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
	lane := filepath.Dir(report)
	if err := os.WriteFile(filepath.Join(lane, "report.md"), []byte("RESULT: done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(report)
	if err != nil || string(raw) != body {
		t.Fatalf("report overwritten: %s / %v", raw, err)
	}
}

func TestExtractedReportLivesOutsideScratchAndSurvivesScratchRemoval(t *testing.T) {
	body := "# run report\ntokens: unknown\ncost usd: unknown\n---\nAll checks passed\nRESULT: done\n"
	backends, adapter := backendWithExtractor(t, body, "0")
	rec, _ := execFixture(t, backends, adapter, "fixture")
	var reportRef, logRef, scratchRef string
	for _, ref := range rec.ArtifactRefs {
		switch ref.Kind {
		case "report":
			reportRef = ref.Ref
		case "log":
			logRef = ref.Ref
		case "scratch":
			scratchRef = ref.Ref
		}
	}
	if reportRef == "" || logRef == "" || scratchRef == "" {
		t.Fatalf("missing artifact refs: report=%q log=%q scratch=%q", reportRef, logRef, scratchRef)
	}
	wantReport := filepath.Join(filepath.Dir(logRef), rec.RunID+".report.md")
	if reportRef != wantReport {
		t.Fatalf("report ref = %q, want %q (beside log %q)", reportRef, wantReport, logRef)
	}
	if strings.HasPrefix(reportRef, scratchRef+string(filepath.Separator)) || reportRef == scratchRef {
		t.Fatalf("report ref %q is inside scratch %q", reportRef, scratchRef)
	}
	if err := os.RemoveAll(scratchRef); err != nil {
		t.Fatalf("removing scratch dir %q: %v", scratchRef, err)
	}
	raw, err := os.ReadFile(reportRef)
	if err != nil || string(raw) != body {
		t.Fatalf("report unreadable after scratch removal: %q / %v", string(raw), err)
	}
}
