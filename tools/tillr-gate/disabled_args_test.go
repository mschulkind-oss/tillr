package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
)

// runCapturing runs the tillr-gate dispatcher and returns its exit code and stdout.
func runCapturing(t *testing.T, args ...string) (int, string) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := run(args)
	os.Stdout = old
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return code, string(out)
}

const disabledArgsList = `{"version":1,"disabled":[
 {"selector":"go-test:tools/m:example.com/m/a:TestShared","source":"hand"},
 {"selector":"go-test:tools/m:example.com/m/a:TestParent/bad.one","source":"hand"},
 {"selector":"go-test:tools/m:example.com/m/b:TestOther","source":"hand"},
 {"selector":"go-test:tools/other:example.com/o:TestNope","source":"hand"},
 {"selector":"some-check","source":"hand"}]}`

func writeDisabledArgsRepo(t *testing.T, list string) (root string) {
	t.Helper()
	root = t.TempDir()
	files := map[string]string{
		".tillr.toml":         "[gate]\ndisabled_tests = \"gate/disabled.json\"\n",
		"gate/disabled.json":  list,
		"tools/m/go.mod":      "module example.com/m\n\ngo 1.23\n",
		"tools/m/a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestShared(t *testing.T) { t.Fatal(\"listed\") }\nfunc TestKept(t *testing.T) {}\nfunc TestParent(t *testing.T) {\n\tt.Run(\"bad.one\", func(t *testing.T) { t.Fatal(\"listed\") })\n\tt.Run(\"good\", func(t *testing.T) {})\n}\n",
		"tools/m/b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestShared(t *testing.T) {}\nfunc TestOther(t *testing.T) { t.Fatal(\"listed\") }\n",
		"tools/m/c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestShared(t *testing.T) {}\n",
	}
	for path, body := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDisabledArgsPackagesListsOnlyPackagesWithListedTests(t *testing.T) {
	root := writeDisabledArgsRepo(t, disabledArgsList)
	code, out := runCapturing(t, "disabled-args", "--root", root, "--module", "tools/m", "--packages")
	if code != 0 || out != "example.com/m/a\nexample.com/m/b\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	code, out = runCapturing(t, "disabled-args", "--root", root, "--module", "tools/m",
		"--prove-disabled", "go-test:tools/m:example.com/m/b:TestOther", "--packages")
	if code != 0 || out != "example.com/m/a\n" {
		t.Fatalf("proved: code %d out %q", code, out)
	}
	code, out = runCapturing(t, "disabled-args", "--root", root, "--module", "tools/none", "--packages")
	if code != 0 || out != "" {
		t.Fatalf("empty module: code %d out %q", code, out)
	}
}

func TestDisabledArgsPackagePrintsSkipArgumentsThatSkipOnlyThatPackage(t *testing.T) {
	root := writeDisabledArgsRepo(t, disabledArgsList)
	code, out := runCapturing(t, "disabled-args", "--root", root, "--module", "tools/m", "--package", "example.com/m/a")
	want := "-skip\n^TestParent$/^bad\\.one$|^TestShared$\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q, want %q", code, out, want)
	}
	if _, out := runCapturing(t, "disabled-args", "--root", root, "--module", "tools/m", "--package", "example.com/m/c"); out != "" {
		t.Fatalf("an unlisted package prints nothing, got %q", out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	run := func(pkg string, extra ...string) (string, error) {
		cmd := exec.Command("go", append([]string{"test", "-v"}, append(extra, pkg)...)...)
		cmd.Dir = filepath.Join(root, "tools", "m")
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	skipped, err := run("./a", lines...)
	if err != nil || !strings.Contains(skipped, "--- PASS: TestKept") || !strings.Contains(skipped, "--- PASS: TestParent/good") ||
		strings.Contains(skipped, "TestShared") || strings.Contains(skipped, "bad.one") {
		t.Fatalf("listed tests must be skipped in a: err %v\n%s", err, skipped)
	}
	sibling, err := run("./c") // the face printed nothing for c, so the recipe adds no -skip
	if err != nil || !strings.Contains(sibling, "--- PASS: TestShared") {
		t.Fatalf("a same-named test in a sibling package must still run: err %v\n%s", err, sibling)
	}
}

func TestDisabledArgsUsageAndMalformedList(t *testing.T) {
	root := writeDisabledArgsRepo(t, disabledArgsList)
	for name, args := range map[string][]string{
		"no module":  {"disabled-args", "--root", root, "--packages"},
		"no mode":    {"disabled-args", "--root", root, "--module", "tools/m"},
		"both modes": {"disabled-args", "--root", root, "--module", "tools/m", "--packages", "--package", "example.com/m/a"},
	} {
		if code, _ := runCapturing(t, args...); code != record.ExitUsage {
			t.Fatalf("%s: exit %d, want %d", name, code, record.ExitUsage)
		}
	}
	bad := writeDisabledArgsRepo(t, `{"version":1,"disabled":[{"selector":"nonsense!","source":"hand"}]}`)
	if code, _ := runCapturing(t, "disabled-args", "--root", bad, "--module", "tools/m", "--packages"); code == 0 {
		t.Fatal("a malformed list must fail")
	}
}
