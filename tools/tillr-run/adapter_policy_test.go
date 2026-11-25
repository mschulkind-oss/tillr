package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyAdapterSourceAndExplicitPaths(t *testing.T) {
	source, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(t.TempDir(), "policy.json")
	for _, tc := range []struct{ adapter, want string }{
		{"run-pi", filepath.Join(source, "backends", "pi", "run-pi")},
		{"./backends/pi/run-pi", "./backends/pi/run-pi"},
		{"/custom/run-pi", "/custom/run-pi"},
		{"other-adapter", "other-adapter"},
	} {
		t.Run(tc.adapter, func(t *testing.T) {
			if tc.adapter == "run-pi" && filepath.Base(filepath.Dir(source)) != "tools" {
				t.Skip("source checkout assertion; installed layout is exercised separately")
			}
			raw, err := json.Marshal(map[string]any{"backends": map[string]any{"worker": map[string]string{"adapter": tc.adapter, "model": "fixture"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(policy, raw, 0644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "request", "--policy", policy, "--backend", "worker", "--item", "item-a", "--lane", "lane-1", "--run", "run-a")
			cmd.Dir = source
			cmd.Env = append(os.Environ(), "TILLR_INSTALL_ROOT=")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v: %s", tc.adapter, err, out)
			}
			var record struct {
				Payload struct {
					Command []string `json:"command"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(out, &record); err != nil {
				t.Fatal(err)
			}
			if len(record.Payload.Command) == 0 || record.Payload.Command[0] != tc.want {
				t.Errorf("%s: command %v, want %s", tc.adapter, record.Payload.Command, tc.want)
			}
		})
	}
}

func TestPolicyAdapterSourceBinaryFromConsumer(t *testing.T) {
	dir := t.TempDir()
	module := filepath.Join(dir, "source", "tools", "tillr-run")
	adapter := filepath.Join(module, "backends", "pi", "run-pi")
	if err := os.MkdirAll(filepath.Dir(adapter), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module github.com/mschulkind-oss/tillr-run\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(module, "tillr-run")
	if err := os.WriteFile(exe, binary, 0755); err != nil {
		t.Fatal(err)
	}
	consumer := filepath.Join(dir, "consumer")
	if err := os.MkdirAll(consumer, 0755); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policy, []byte(`{"backends":{"worker":{"adapter":"run-pi","model":"fixture"}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "request", "--policy", policy, "--backend", "worker", "--item", "item-a", "--lane", "lane-1", "--run", "run-a")
	cmd.Dir = consumer
	cmd.Env = append(os.Environ(), "TILLR_INSTALL_ROOT=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("request: %v: %s", err, out)
	}
	var record struct {
		Payload struct {
			Command []string `json:"command"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatal(err)
	}
	if len(record.Payload.Command) == 0 || record.Payload.Command[0] != adapter {
		t.Fatalf("command = %v, want %s", record.Payload.Command, adapter)
	}
}

func TestPolicyAdapterNamesUseInstalledExecutables(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "install")
	consumer := filepath.Join(dir, "consumer")
	decoy := filepath.Join(dir, "decoy")
	for _, d := range []string{consumer, decoy} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pi", "claude", "agy", "muse"} {
		installed := filepath.Join(root, "tillr-run", "backends", name, "run-"+name)
		if err := os.MkdirAll(filepath.Dir(installed), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(installed, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(decoy, "run-"+name), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
			t.Fatal(err)
		}
		policy := filepath.Join(dir, "policy.json")
		if err := os.WriteFile(policy, []byte(`{"backends":{"worker":{"adapter":"run-`+name+`","model":"fixture"}}}`), 0644); err != nil {
			t.Fatal(err)
		}
		invoke := func() ([]byte, error) {
			cmd := exec.Command(exe, "request", "--policy", policy, "--backend", "worker", "--item", "item-a", "--lane", "lane-1", "--run", "run-a", "--now", "2026-11-25T00:00:00Z")
			cmd.Dir = consumer
			cmd.Env = append(os.Environ(), "TILLR_INSTALL_ROOT="+root, "PATH="+decoy+":"+os.Getenv("PATH"))
			return cmd.CombinedOutput()
		}
		out, err := invoke()
		if err != nil {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
		var record struct {
			Payload struct {
				Command []string `json:"command"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(out, &record); err != nil {
			t.Fatal(err)
		}
		if len(record.Payload.Command) == 0 || record.Payload.Command[0] != installed {
			t.Errorf("%s command = %v, want %s", name, record.Payload.Command, installed)
		}
		if err := os.Remove(installed); err != nil {
			t.Fatal(err)
		}
		out, err = invoke()
		if err == nil || !strings.Contains(string(out), installed) {
			t.Errorf("missing %s: err=%v output=%s", installed, err, out)
		}
	}
}
