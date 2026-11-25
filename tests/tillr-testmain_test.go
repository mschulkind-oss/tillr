package cleanup_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// unsafeExit checks the function's own statements, not nested function literals:
// os.Exit skips its caller's defers, including deferred directory removal.
func unsafeExit(fn *ast.FuncDecl) bool {
	var exit bool
	deferredRemove, directRemove := map[string]bool{}, map[string]bool{}
	argument := func(call *ast.CallExpr) string {
		var out bytes.Buffer
		_ = printer.Fprint(&out, token.NewFileSet(), call.Args[0])
		return out.String()
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt:
			ast.Inspect(n.Call, func(child ast.Node) bool {
				if call, ok := child.(*ast.CallExpr); ok && osCall(call, "RemoveAll") {
					deferredRemove[argument(call)] = true
				}
				return true
			})
			return false
		case *ast.CallExpr:
			exit = exit || osCall(n, "Exit")
			if osCall(n, "RemoveAll") {
				directRemove[argument(n)] = true
			}
		}
		return true
	})
	if exit {
		for dir := range deferredRemove {
			if !directRemove[dir] {
				return true
			}
		}
	}
	return false
}

func osCall(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "os" && sel.Sel.Name == name
}

func TestExitGuardControls(t *testing.T) {
	for _, tc := range []struct {
		body string
		bad  bool
	}{
		{`defer os.RemoveAll(dir); os.Exit(m.Run())`, true},
		{`defer func() { os.RemoveAll(dir) }(); os.Exit(1)`, true},
		{`os.Exit(runTests(m))`, false},
		{`defer os.RemoveAll(dir); return`, false},
		{`os.RemoveAll(dir); os.Exit(1)`, false},
		{`defer os.RemoveAll(dir); os.RemoveAll(dir); os.Exit(1)`, false},
		{`defer os.RemoveAll(dir); os.RemoveAll(other); os.Exit(1)`, true},
		{`defer os.RemoveAll(dir); f := func() { os.Exit(1) }; _ = f`, false},
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "control.go", "package main; func TestMain() {"+tc.body+"}", 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := unsafeExit(f.Decls[0].(*ast.FuncDecl)); got != tc.bad {
			t.Errorf("unsafeExit(%s) = %v, want %v", tc.body, got, tc.bad)
		}
	}
}

func TestRepositoryTestMainCleanup(t *testing.T) {
	paths, err := filepath.Glob("../tools/tillr*/*_test.go")
	if err != nil || len(paths) == 0 {
		t.Fatalf("cannot find tillr test sources: %v", err)
	}
	found := 0
	for _, path := range paths {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "TestMain" {
				continue
			}
			found++
			t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
				if unsafeExit(fn) {
					t.Errorf("%s: TestMain calls os.Exit but only a defer removes its directory", path)
				}
				probeCleanup(t, path, fset, file)
			})
		}
	}
	if found != 9 {
		t.Errorf("checked %d TestMain builders, want 9; update the inventory for new modules", found)
	}
}

// Compile the actual setup functions in a small process. The fake go command replaces
// only the child binary build; the real Go compiler builds this probe. suite.Run models
// successful/failed tests and a panic on the setup goroutine. An unrecovered panic on a
// separate testing goroutine terminates the process without unwinding TestMain, and is
// deliberately not claimed to be recoverable here.
func probeCleanup(t *testing.T, path string, fset *token.FileSet, file *ast.File) {
	t.Helper()
	var source bytes.Buffer
	source.WriteString(`package main
import ("os"; "os/exec"; "path/filepath")
var bin, binDir, gateBin, wrapperBin, viewBin, testBinPath, testDefaultTraceDir, testRepoRoot string
type suite struct{}
func (*suite) Run() int {
    switch os.Getenv("PROBE_MODE") {
    case "fail": return 7
    case "panic": panic("cleanup probe")
    }
    return 0
}
func main() { TestMain(&suite{}) }
`)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "TestMain" && fn.Name.Name != "runTests" && fn.Name.Name != "scratchRoot") {
			continue
		}
		var body bytes.Buffer
		if err := printer.Fprint(&body, fset, fn); err != nil {
			t.Fatal(err)
		}
		source.WriteString(strings.ReplaceAll(body.String(), "*testing.M", "*suite"))
		source.WriteString("\n")
	}
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("probe.go", source.String(), 0o600)
	probe := filepath.Join(dir, "probe")
	cmd := exec.Command("go", "build", "-o", probe, filepath.Join(dir, "probe.go"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building probe: %v\n%s", err, out)
	}
	write("go", "#!/bin/sh\nexit \"${BUILD_EXIT:-0}\"\n", 0o700)
	cases := []struct {
		mode, build string
		code        int
	}{
		{"pass", "0", 0}, {"fail", "0", 7}, {"panic", "0", 2}, {"build-fail", "1", 1},
	}
	if filepath.Base(filepath.Dir(path)) == "tillr-drain" {
		// The deliberately killed process helper must reuse its parent's resources.
		cases = append(cases, struct {
			mode, build string
			code        int
		}{"helper", "1", 0})
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			root := t.TempDir()
			cmd := exec.Command(probe)
			helper := "0"
			if tc.mode == "helper" {
				helper = "1"
			}
			cmd.Env = append(os.Environ(), "YOLO_DURABLE_DIR="+root, "TMPDIR="+root,
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PROBE_MODE="+tc.mode, "BUILD_EXIT="+tc.build, "RUN_OUTPUT_HELPER="+helper)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != tc.code {
				t.Errorf("exit %d, want %d: %s", code, tc.code, out)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				t.Errorf("setup left scratch entry %s behind", entry.Name())
			}
		})
	}
}
