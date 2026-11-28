package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDisabledFixture(t *testing.T, toml, list string) string {
	t.Helper()
	root := t.TempDir()
	if toml != "" {
		if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if list != "" {
		if err := os.MkdirAll(filepath.Join(root, "gate"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "gate", "disabled.json"), []byte(list), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDisabledTestsCheckAcceptsAbsentAndValidLists(t *testing.T) {
	root := writeDisabledFixture(t, "", "")
	if f, err := disabledTestsFinding(root); err != nil || f != "" {
		t.Fatalf("absent default list: finding %q, err %v", f, err)
	}
	root = writeDisabledFixture(t, "[gate]\ndisabled_tests = \"gate/disabled.json\"\n", `{"version":1,"disabled":[{"selector":"go-test:m:p:TestA/b","source":"hand"}]}`)
	if f, err := disabledTestsFinding(root); err != nil || f != "" {
		t.Fatalf("valid list: finding %q, err %v", f, err)
	}
}

func TestDisabledTestsCheckRefusesMalformedList(t *testing.T) {
	root := writeDisabledFixture(t, "[gate]\ndisabled_tests = \"gate/disabled.json\"\n", `{"version":1,"disabled":[{"selector":"go-test:m:p:TestA","source":"hand"},{"selector":"go-test:m:p:TestA","source":"hand"}]}`)
	f, err := disabledTestsFinding(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f, "listed twice") || !strings.Contains(f, "disabled.json") {
		t.Fatalf("finding = %q", f)
	}
}

func TestThisRepositoryDisabledListIsValid(t *testing.T) {
	root, _ := loadTestContext(t)
	if f, err := disabledTestsFinding(root); err != nil || f != "" {
		t.Fatalf("finding %q, err %v", f, err)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "gate", "disabled-tests.json")); err != nil {
		t.Fatalf("the tracked list is missing: %v", err)
	}
}

func TestEveryGoTestCheckDeclaresTheDisabledListAsARead(t *testing.T) {
	root, _ := loadTestContext(t)
	paths, err := filepath.Glob(filepath.Join(root, "tools", "gate", "checks", "go-test-*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no go-test checks found: %v", err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var decl struct {
			Reads []string `json:"reads"`
		}
		if err := json.Unmarshal(raw, &decl); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, want := range []string{".tillr.toml", "tools/gate/disabled-tests.json"} {
			covered := false
			for _, r := range decl.Reads {
				if r == want || (strings.HasSuffix(r, "/") && strings.HasPrefix(want, r)) {
					covered = true
				}
			}
			if !covered {
				t.Errorf("%s does not declare %s as a read, so a remembered pass would survive a change to the disabled list", filepath.Base(path), want)
			}
		}
	}
}
