package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestShippedRulesConsumerRepoWithoutConfigDir(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tillrDir := filepath.Join(root, ".tillr")
	if err := os.MkdirAll(tillrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(tillrDir, "state.db")
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	var out, stderr bytes.Buffer
	if code := run(ctx, []string{"rule", "list", "--store", storePath}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("rule list without --rules: code=%d stderr=%q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 44 {
		t.Fatalf("expected 44 shipped rules, got %d:\n%s", len(lines), out.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, " source=shipped ") {
			t.Fatalf("expected source=shipped on %q", line)
		}
	}
}

func TestShippedRulesConsumerOverrideAddAndLintAttribution(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, dir := range []string{".tillr", "config/tillr/watch", "config/tillr/workflows"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	storePath := filepath.Join(root, ".tillr", "state.db")
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Write a minimal workflow and policy for lint.
	if err := os.WriteFile(filepath.Join(root, "config/tillr/workflows/pb@v1.json"), []byte(`{
		"name": "pb",
		"version": 1,
		"stages": [
			{"id": "plan", "kind": "agent", "seat": "plan", "access": "read_only"},
			{"id": "work", "kind": "agent", "seat": "build", "access": "write"},
			{"id": "verify", "kind": "agent", "seat": "verify", "access": "read_only"}
		],
		"edges": [
			{"from_node": "plan", "to_node": "work"},
			{"from_node": "work", "to_node": "verify"}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config/tillr/backend-policy.json"), []byte(`{
		"default_backend": "pi-build",
		"seats": {
			"plan": "pi-build",
			"build": "pi-build",
			"verify": "pi-build"
		},
		"backends": {
			"pi-build": {
				"adapter": "pi",
				"model": "test"
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Add a custom rule, override one shipped rule, and replace another shipped rule wholesale.
	if err := os.WriteFile(filepath.Join(root, "config/tillr/watch/local.toml"), []byte(`
[[override]]
id = "drain-lane-kept"
enabled = false

[[rule]]
id = "drain-store-busy"
scope = "core"
severity = "high"

[rule.reads]
events = ["drain.store-busy"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.op == "write"'
window = "2h"

[rule.guidance]
summary = "custom store busy"
check = ["check busy"]
do = ["do busy"]

[rule.delivery]
receiver = "foreground"

[[rule]]
id = "repo-custom"
scope = "workflow:nonexistent"
severity = "warning"

[rule.reads]
events = ["drain.loop-ended"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'event.reason == "x"'
window = "1h"

[rule.guidance]
summary = "repo custom"
check = ["check custom"]
do = ["do custom"]

[rule.delivery]
receiver = "foreground"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// rule list infers <root> from <root>/.tillr/state.db.
	var out, stderr bytes.Buffer
	if code := run(ctx, []string{"rule", "list", "--store", storePath}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("rule list: code=%d stderr=%q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 45 {
		t.Fatalf("expected 45 rules (44 shipped + 1 added), got %d", len(lines))
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "rule drain-lane-kept "):
			if !strings.Contains(line, " source=file enabled=false ") {
				t.Fatalf("expected overridden drain-lane-kept source=file enabled=false, got %q", line)
			}
		case strings.HasPrefix(line, "rule drain-store-busy "):
			if !strings.Contains(line, " source=file enabled=true ") {
				t.Fatalf("expected replaced drain-store-busy source=file, got %q", line)
			}
		case strings.HasPrefix(line, "rule repo-custom "):
			if !strings.Contains(line, " source=file ") {
				t.Fatalf("expected repo-custom source=file, got %q", line)
			}
		default:
			if !strings.Contains(line, " source=shipped ") {
				t.Fatalf("expected shipped rule source=shipped, got %q", line)
			}
		}
	}

	// lint attributes repo findings to config/tillr/watch/local.toml.
	out.Reset()
	stderr.Reset()
	code := run(ctx, []string{"lint", root}, &out, &stderr)
	if code != 1 || !strings.Contains(out.String(), "config/tillr/watch/local.toml: watch-lint-unknown-stage:") {
		t.Fatalf("expected lint finding attributed to config/tillr/watch/local.toml, code=%d out=%q err=%q", code, out.String(), stderr.String())
	}

	// Fixing repo-custom's scope makes lint pass and report shipped vs repo sources.
	if err := os.WriteFile(filepath.Join(root, "config/tillr/watch/local.toml"), []byte(`
[[override]]
id = "drain-lane-kept"
enabled = false

[[rule]]
id = "drain-store-busy"
scope = "core"
severity = "high"

[rule.reads]
events = ["drain.store-busy"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.op == "write"'
window = "2h"

[rule.guidance]
summary = "custom store busy"
check = ["check busy"]
do = ["do busy"]

[rule.delivery]
receiver = "foreground"

[[rule]]
id = "repo-custom"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-ended"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'event.reason == "x"'
window = "1h"

[rule.guidance]
summary = "repo custom"
check = ["check custom"]
do = ["do custom"]

[rule.delivery]
receiver = "foreground"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := run(ctx, []string{"lint", root}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("clean lint failed: code=%d out=%q err=%q", code, out.String(), stderr.String())
	}
	for _, want := range []string{
		"rule drain-loop-fault source=shipped:rules/drain-loop-fault.toml",
		"rule drain-lane-kept source=config/tillr/watch/local.toml",
		"rule drain-store-busy source=config/tillr/watch/local.toml",
		"rule repo-custom source=config/tillr/watch/local.toml",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("lint output missing %q:\n%s", want, out.String())
		}
	}
}

func TestShippedRulesCoexistenceAndRecordedVerdictEvalWithoutRoot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tillrDir := filepath.Join(root, ".tillr")
	watchDir := filepath.Join(root, "config/tillr/watch")
	if err := os.MkdirAll(tillrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Copy one shipped rule into config/tillr/watch/ to simulate pre-cleanup consumer repo.
	data, err := os.ReadFile(filepath.Join("rules", "drain-loop-fault.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "drain-loop-fault.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(tillrDir, "state.db")
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defBytes, err := os.ReadFile("../tillr/examples/recorded-verdict/definition.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(ctx, s, json.RawMessage(defBytes)); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Coexistence: eval --once without --root succeeds on recorded-verdict, and drain-loop-fault has source=file.
	var out, stderr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", storePath, "--rules", watchDir}, &out, &stderr); code != 0 || !strings.Contains(stderr.String(), "trunk-proof-lag not evaluated: the store's charter is recorded-verdict") {
		t.Fatalf("eval --once coexistence: code=%d stderr=%q", code, stderr.String())
	}
	if code := run(ctx, []string{"rule", "list", "--store", storePath, "--rules", watchDir}, &out, &stderr); code != 0 {
		t.Fatalf("rule list coexistence: code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(out.String(), "rule drain-loop-fault source=file ") {
		t.Fatalf("expected drain-loop-fault source=file, got %q", out.String())
	}
	if !strings.Contains(out.String(), "rule trunk-proof-lag source=shipped ") {
		t.Fatalf("expected trunk-proof-lag source=shipped, got %q", out.String())
	}
}
