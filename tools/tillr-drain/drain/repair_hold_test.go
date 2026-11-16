package drain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepairHoldReopensForCorrectedTemplateNotUnchangedInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brief-template.md")
	if err := os.WriteFile(path, []byte("broken {{placeholder}}"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := repairTemplateHash(path)
	if err != nil {
		t.Fatal(err)
	}
	inputs := RunInputs{ItemBlock: "definition", AllowedPaths: "fence", BackendPolicy: "policy", TillrBuild: "build"}
	hold := repairHold{Inputs: inputs, Template: path, TemplateHash: hash}
	for i := 0; i < 3; i++ {
		same, err := hold.unchanged(&inputs, nil)
		if err != nil || !same {
			t.Fatalf("unchanged repair reopened: %v / %v", same, err)
		}
	}
	if err := os.WriteFile(path, []byte("correct template"), 0600); err != nil {
		t.Fatal(err)
	}
	same, err := hold.unchanged(&inputs, nil)
	if err != nil || same {
		t.Fatalf("corrected template did not reopen: %v / %v", same, err)
	}
	// A newly configured template must be compared, not the stale retained path.
	hold.TemplateHash, err = repairTemplateHash(path)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(path), "other-template.md")
	if err := os.WriteFile(other, []byte("new configured template"), 0600); err != nil {
		t.Fatal(err)
	}
	same, err = hold.unchanged(&inputs, &other)
	if err != nil || same {
		t.Fatalf("new configured template did not reopen: %v / %v", same, err)
	}
}
