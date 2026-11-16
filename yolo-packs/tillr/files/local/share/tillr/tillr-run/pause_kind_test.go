package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryAdapterDeclaresPauseKindAndREADMEAgrees(t *testing.T) {
	entries, err := os.ReadDir("backends")
	if err != nil {
		t.Fatal(err)
	}
	adapters := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join("backends", entry.Name())
		programs, err := filepath.Glob(filepath.Join(dir, "run-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(programs) == 0 {
			continue
		}
		adapters++
		raw, err := os.ReadFile(filepath.Join(dir, "pause-kind"))
		if err != nil {
			t.Fatal(err)
		}
		kind := strings.TrimSuffix(string(raw), "\n")
		switch kind {
		case "stops-at-a-gap", "stops-where-it-stands", "cannot-resume":
		default:
			t.Fatalf("%s: invalid pause kind %q", dir, raw)
		}
		body, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "Pause kind: `"+kind+"`") {
			t.Fatalf("%s README disagrees with declaration", dir)
		}
	}
	if adapters == 0 {
		t.Fatal("no adapter directories checked")
	}
}
