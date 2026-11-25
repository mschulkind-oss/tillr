package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanningPolicyAdapterUsesInstalledExecutable(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "install")
	installed := filepath.Join(root, "tillr-run", "backends", "pi", "run-pi")
	if err := os.MkdirAll(filepath.Dir(installed), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_INSTALL_ROOT", root)
	_, l, _ := restartGraphFixture(t)
	policy := *l.cfg.Policy
	b := policy.Backends["fixture"]
	b.Adapter = "run-pi"
	b.Model = "fixture"
	policy.Backends["fixture"] = b
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(ref, raw, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPolicy(ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Backends["fixture"].Adapter; got != installed {
		t.Fatalf("loaded adapter = %s, want %s", got, installed)
	}
	reloaded, err := decodePolicy(ref, raw)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Backends["fixture"].Adapter != installed {
		t.Fatalf("reloaded adapter = %s", reloaded.Backends["fixture"].Adapter)
	}
	loaded.Root = dir
	l.cfg.Policy = loaded
	decision := mustPlan(t, l)[0].decision
	if decision.RunRequest == nil || decision.RunRequest.Command[0] != installed {
		t.Fatalf("decision request = %+v, want adapter %s", decision.RunRequest, installed)
	}
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(ref); err == nil || !strings.Contains(err.Error(), installed) {
		t.Fatalf("missing installed adapter: %v", err)
	}
	for _, adapter := range []string{"./backends/run-pi", "/explicit/run-pi", "custom"} {
		b.Adapter = adapter
		policy.Backends["fixture"] = b
		raw, err := json.Marshal(policy)
		if err != nil {
			t.Fatal(err)
		}
		explicit, err := decodePolicy(ref, raw)
		if err != nil {
			t.Fatal(err)
		}
		explicit.Root = dir
		want := adapter
		if strings.Contains(adapter, "/") && !filepath.IsAbs(adapter) {
			want = filepath.Join(dir, adapter)
		}
		if got := explicit.request("item-a", "lane-1", "run-a", "fixture").Command[0]; got != want {
			t.Errorf("explicit %s: got %s, want %s", adapter, got, want)
		}
	}
}

func TestPolicyAdapterCompanionsUseInstalledDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "install")
	installed := filepath.Join(root, "tillr-run", "backends", "muse", "run-muse")
	if err := os.MkdirAll(filepath.Dir(installed), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(installed), "ending-capability"), []byte("call-then-message\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_INSTALL_ROOT", root)
	_, l, _ := restartGraphFixture(t)
	p := *l.cfg.Policy
	b := p.Backends["fixture"]
	b.Adapter = "run-muse"
	b.Model = "fixture"
	p.Backends["fixture"] = b
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := decodePolicy("fixture", raw)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.Policy = loaded
	if err := l.recordEndingCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := l.endingCapabilities[installed]; got != "call-then-message" {
		t.Fatalf("capabilities = %v", l.endingCapabilities)
	}
}
