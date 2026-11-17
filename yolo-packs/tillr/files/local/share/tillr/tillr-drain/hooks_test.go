package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func definitionProgram(t *testing.T, names []string, missing string) (*definition.Hooks, string) {
	t.Helper()
	log := filepath.Join(scratch(t, "hooks"), "calls.jsonl")
	// This independent fixture intentionally knows only the flags in the public contract.
	flags := []string{"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root", "--observed-trunk", "--base-ref", "--message-from", "--store", "--verdict", "--all", "--since", "--only"}
	answers := map[string][]string{}
	for _, name := range names {
		if name == missing {
			continue
		}
		for _, flag := range flags {
			if flag != missing {
				answers[name] = append(answers[name], flag)
			}
		}
	}
	description, _ := json.Marshal(map[string]any{"hooks": answers})
	path := filepath.Join(scratch(t, "hook-program"), "program with spaces.py")
	quote := func(s string) string { raw, _ := json.Marshal(s); return string(raw) }
	writeFile(t, path, `import json, os, sqlite3, sys
store = os.environ.get('TILLR_TEST_HOOK_STORE')
if store:
    with sqlite3.connect(store, timeout=0.1) as db:
        db.execute('BEGIN IMMEDIATE')
        db.rollback()
args = sys.argv[1:]
with open(`+quote(log)+`, 'a') as stream:
    stream.write(json.dumps(args) + '\n')
if args == ['describe']:
    print(`+quote(string(description))+`)
`)
	return &definition.Hooks{Argv: []string{"python3", path}, Names: names}, log
}

func setStoreHooks(t *testing.T, path string, hooks *definition.Hooks) {
	t.Helper()
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Hooks = hooks
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
}

func TestDefinitionHookProgramRunsAPass(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item-a", "in-queue"))
	hooks, log := definitionProgram(t, []string{"gate", "land", "verdict"}, "")
	setStoreHooks(t, f.store, hooks)
	t.Setenv("TILLR_TEST_HOOK_STORE", f.store)
	steps := f.steps(t, "", "", "")
	steps.ConfigureHooks(hooks, nil)
	if err := steps.CheckPrograms(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	result, err := f.newLoop(t, 1, steps).Run(context.Background())
	if err != nil || result.Landed != 1 {
		t.Fatalf("pass: %+v / %v", result, err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var argv []string
		if err := json.Unmarshal([]byte(line), &argv); err != nil {
			t.Fatal(err)
		}
		names = append(names, argv[0])
		if argv[0] == "describe" {
			continue
		}
		for _, flag := range []string{"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root"} {
			if !containsWord(argv, flag) {
				t.Errorf("%s lacks %s: %v", argv[0], flag, argv)
			}
		}
		if argv[0] == "land" && !containsWord(argv, "--observed-trunk") {
			t.Errorf("land flags: %v", argv)
		}
		if argv[0] == "verdict" && (!containsWord(argv, "--store") || !containsWord(argv, "--verdict")) {
			t.Errorf("verdict flags: %v", argv)
		}
	}
	if !reflect.DeepEqual(names, []string{"describe", "gate", "land", "verdict"}) {
		t.Fatalf("calls = %v", names)
	}
}

func containsWord(words []string, word string) bool {
	for _, s := range words {
		if s == word {
			return true
		}
	}
	return false
}

func TestDefinitionHookCLIStartupAndPrecedence(t *testing.T) {
	for _, missing := range []string{"", "land", "--observed-trunk"} {
		t.Run("missing="+missing, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
			args := stepCheckArgs(t, f)
			hooks, log := definitionProgram(t, []string{"gate", "land", "verdict"}, missing)
			setStoreHooks(t, f.store, hooks)
			t.Setenv("TILLR_TEST_HOOK_STORE", f.store)
			args = append(args, "--check", "--gate", "/bin/true", "--replay=")
			code, out := invoke(t, args...)
			if missing == "" {
				if code != 0 || !strings.Contains(string(out), `configured program "/bin/true"`) {
					t.Fatalf("CLI override: %d %s", code, out)
				}
			} else if code != 2 || !strings.Contains(string(out), missing) || !strings.Contains(string(out), "land") {
				t.Fatalf("startup refusal: %d %s", code, out)
			}
			raw, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != "[\"describe\"]\n" {
				t.Fatalf("startup executed hooks: %s", raw)
			}
			entries, err := os.ReadDir(f.laneRoot)
			if err != nil || len(entries) != 0 {
				t.Fatalf("startup dispatched: %v / %v", entries, err)
			}
		})
	}
}
