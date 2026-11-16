package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitingPolicyRefusalIsOneLineAndClears(t *testing.T) {
	// TMPDIR is set to lane-owned durable scratch by the verification command.
	root := t.TempDir()
	initial := []waitingRow{{ItemID: "work", Why: "blocked"}}
	rows, err := policyWaitingRows(root, initial)
	if err != nil || len(rows) != 1 {
		t.Fatalf("no snapshot: %+v / %v", rows, err)
	}
	dir := filepath.Join(root, ".tillr", "loop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "backend-policy.json")
	if err := os.WriteFile(path, []byte(`{"refusal":"the policy maps seat build\nto an unknown backend"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err = policyWaitingRows(root, initial)
	if err != nil || len(rows) != 2 {
		t.Fatalf("refusal rows: %+v / %v", rows, err)
	}
	var out bytes.Buffer
	if err := printWaiting(&out, rows); err != nil {
		t.Fatal(err)
	}
	want := "policy on disk is not the policy in force: the policy maps seat build to an unknown backend"
	if strings.Count(out.String(), want) != 1 {
		t.Fatalf("waiting warning: %s", out.String())
	}
	if err := os.WriteFile(path, []byte(`{"refusal":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err = policyWaitingRows(root, initial)
	if err != nil || len(rows) != 1 {
		t.Fatalf("restored: %+v / %v", rows, err)
	}
	if err := os.WriteFile(path, []byte(`not JSON`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := policyWaitingRows(root, initial); err == nil {
		t.Fatal("corrupt state silently ignored")
	}
}
