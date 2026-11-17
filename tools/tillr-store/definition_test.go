package main_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type definitionRecord struct {
	Name    string `json:"definition"`
	Version int    `json:"version"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
	Default bool   `json:"default"`
}

func definitionFile(t *testing.T, name string, states []string) (string, []byte) {
	t.Helper()
	d := definition.Definition{Name: name, Version: 1, States: states, Ready: states[0], Blocked: states[0], Closed: states[0], Completes: "landing", Retention: "keep"}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join(t.TempDir(), "definition.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func readDefinitionFace(t *testing.T, path string) definitionRecord {
	t.Helper()
	code, out := run(t, "definition", "--store", path)
	if code != 0 {
		t.Fatalf("definition exited %d: %s", code, out)
	}
	var env struct {
		Kind    string           `json:"kind"`
		Payload definitionRecord `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "store.definition" {
		t.Fatalf("kind = %q", env.Kind)
	}
	return env.Payload
}

func TestDefinitionInitAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	input, raw := definitionFile(t, "checks", []string{"ready", "closed"})
	if code, out := run(t, "init", "--store", path, "--definition", input); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	got := readDefinitionFace(t, path)
	if got.Name != "checks" || got.Version != 1 || got.Content != string(raw) || got.Default || got.SHA256 != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatalf("definition = %+v", got)
	}
	next, nextRaw := definitionFile(t, "review", []string{"ready", "closed", "finished"})
	if code, out := run(t, "definition", "--store", path, "--set", next); code != 0 {
		t.Fatalf("set exited %d: %s", code, out)
	}
	updated := readDefinitionFace(t, path)
	if updated.Content != string(nextRaw) || updated.SHA256 == got.SHA256 || updated.Default {
		t.Fatalf("updated = %+v", updated)
	}
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	shared, err := definition.ForStore(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(shared)
	if err != nil {
		t.Fatal(err)
	}
	var sharedRecord definitionRecord
	if err := json.Unmarshal(encoded, &sharedRecord); err != nil {
		t.Fatal(err)
	}
	if sharedRecord != updated {
		t.Fatalf("shared reader = %+v, CLI = %+v", sharedRecord, updated)
	}
	parsed, err := definition.Read(strings.NewReader(updated.Content))
	if err != nil || !reflect.DeepEqual(shared.Parsed, parsed) {
		t.Fatalf("shared parsed definition = %+v, %v", shared.Parsed, err)
	}
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	for i, e := range events {
		if e.Kind != "store.definition.set" {
			t.Fatalf("event = %+v", e)
		}
		var p struct {
			Old string `json:"old_sha256"`
			New string `json:"new_sha256"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Old == "" || p.New == "" {
			t.Fatalf("event hashes = %+v", p)
		}
		if i == 1 && (p.Old != got.SHA256 || p.New != updated.SHA256) {
			t.Fatalf("replacement hashes = %+v", p)
		}
	}
}

func TestDefinitionInvalidUTF8RefusedBeforeWriting(t *testing.T) {
	input, raw := definitionFile(t, "checks", []string{"ready"})
	bad := bytes.Replace(raw, []byte("checks"), []byte{'c', 'h', 'e', 'c', 'k', 's', 0xff}, 1)
	if err := os.WriteFile(input, bad, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "absent", "store.db")
	if code, out := run(t, "init", "--store", path, "--definition", input); code != 2 {
		t.Fatalf("invalid UTF-8 init exited %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("invalid UTF-8 init created directory: %v", err)
	}
	if code, out := run(t, "init", "--store", path); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	before := readDefinitionFace(t, path)
	if code, out := run(t, "definition", "--store", path, "--set", input); code != 2 {
		t.Fatalf("invalid UTF-8 set exited %d: %s", code, out)
	}
	if got := readDefinitionFace(t, path); got != before {
		t.Fatal("invalid UTF-8 set changed definition")
	}
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil || len(events) != 0 {
		t.Fatalf("invalid UTF-8 events: %+v, %v", events, err)
	}
}

func TestDefinitionInvalidSetLeavesStoreUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if code, out := run(t, "init", "--store", path); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	before := readDefinitionFace(t, path)
	input := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(input, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "definition", "--store", path, "--set", input); code != 2 {
		t.Fatalf("invalid set exited %d: %s", code, out)
	}
	if got := readDefinitionFace(t, path); got != before {
		t.Fatal("invalid set changed definition")
	}
}

func TestDefinitionDefaultReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if code, out := run(t, "init", "--store", path); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	before := readDefinitionFace(t, path)
	input, raw := definitionFile(t, "checks", []string{"ready"})
	if code, out := run(t, "definition", "--store", path, "--set", input); code != 0 {
		t.Fatalf("set exited %d: %s", code, out)
	}
	after := readDefinitionFace(t, path)
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	var p struct {
		Old string `json:"old_sha256"`
		New string `json:"new_sha256"`
	}
	if err := json.Unmarshal(events[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Old != before.SHA256 || p.New != after.SHA256 || after.Content != string(raw) || after.Default {
		t.Fatalf("replacement = %+v, %+v", after, p)
	}
}

func TestDefinitionInvalidInitCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(input, []byte(`{"definition":"invalid","version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "absent", "store.db")
	if code, out := run(t, "init", "--store", path, "--definition", input); code != 2 {
		t.Fatalf("invalid init exited %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("invalid init created directory: %v", err)
	}
}

func TestDefinitionLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	// This helper builds the pre-change store creator without the definition package.
	createWithOwners(t, dir, path, false)
	before := schemaColumns(t, path)
	got := readDefinitionFace(t, path)
	embedded, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := definition.Read(strings.NewReader(got.Content))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Default || got.Name != definition.GitLandingName || !reflect.DeepEqual(parsed, embedded) {
		t.Fatalf("legacy definition = %+v", got)
	}
	if !reflect.DeepEqual(before, schemaColumns(t, path)) {
		t.Fatal("definition read migrated legacy store")
	}
	drainEmptyStore(t, dir, path)
	if after := readDefinitionFace(t, path); after != got {
		t.Fatalf("drain changed definition: %+v", after)
	}
}

func TestDefinitionSetRefusesOccupiedStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if code, out := run(t, "init", "--store", path); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (item_id,title,state,updated_at) VALUES ('alpha','A','in-queue',0),('beta','B','blocked',0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := readDefinitionFace(t, path)
	input, _ := definitionFile(t, "checks", []string{"ready"})
	cmd := exec.Command(bin, "definition", "--store", path, "--set", input)
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("set: %v: %s", err, out)
	}
	for _, value := range []string{"alpha", "beta", "in-queue", "blocked"} {
		if !strings.Contains(string(out), value) {
			t.Fatalf("refusal missing %q: %s", value, out)
		}
	}
	if got := readDefinitionFace(t, path); got != before {
		t.Fatal("refusal changed definition")
	}
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil || len(events) != 0 {
		t.Fatalf("refusal events: %+v, %v", events, err)
	}
}
