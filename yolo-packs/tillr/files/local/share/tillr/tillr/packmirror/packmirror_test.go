package packmirror

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewrite(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		in   string
		want string
	}{
		{
			name: "escaping link from root",
			dir:  ".",
			in:   "[doc](../../docs/design/x.md)",
			want: "[doc](" + Repository + "docs/design/x.md)",
		},
		{
			name: "escaping link with anchor from subdir",
			dir:  "sub/dir",
			in:   "[doc](../../../docs/a.md#sec-1)",
			want: "[doc](" + Repository + "docs/a.md#sec-1)",
		},
		{
			name: "sibling inside module is untouched",
			dir:  "sub",
			in:   "[sibling](../brief-template.md)",
			want: "[sibling](../brief-template.md)",
		},
		{
			name: "absolute, scheme, and fragment untouched",
			dir:  ".",
			in:   "[web](https://example.com) [abs](/etc/hosts) [frag](#local)",
			want: "[web](https://example.com) [abs](/etc/hosts) [frag](#local)",
		},
		{
			name: "empty anchor omitted",
			dir:  ".",
			in:   "[doc](../docs/x.md#)",
			want: "[doc](" + Repository + "docs/x.md)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Rewrite(tc.in, tc.dir)
			if got != tc.want {
				t.Fatalf("Rewrite(%q, %q) = %q, want %q", tc.in, tc.dir, got, tc.want)
			}
		})
	}
}

func TestRewriteReadmesAndMainLinkMode(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("[x](../../docs/a.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "README.md"), []byte("[y](../README.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	code := Main([]string{dir}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("Main exit = %d, stderr = %s", code, errBuf.String())
	}
	wantOut := "pack-mirror-links: rewrote 1 README(s) under " + dir + "\n"
	if out.String() != wantOut {
		t.Fatalf("stdout = %q, want %q", out.String(), wantOut)
	}
	gotRoot, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRoot) != "[x]("+Repository+"docs/a.md)\n" {
		t.Fatalf("rewritten root README = %q", string(gotRoot))
	}
	gotSub, err := os.ReadFile(filepath.Join(sub, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotSub) != "[y](../README.md)\n" {
		t.Fatalf("sub README should be unchanged, got %q", string(gotSub))
	}
}

func TestCheckDestinationRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "yolo-packs")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, filepath.FromSlash(MirrorRoot), "tillr")
	err := CheckDestination(root, dest)
	if err == nil || !strings.Contains(err.Error(), "refusing symlinked destination ancestor:") {
		t.Fatalf("expected symlink refusal error, got %v", err)
	}
}

func TestSyncAndExecBitPreservation(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")

	modDir := filepath.Join(repo, "tools", "tillr")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "README.md"), []byte("[d](../../docs/a.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	loopDir := filepath.Join(repo, "tools", LoopModule)
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopDir, "README.md"), []byte("source readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	destLoop := filepath.Join(repo, filepath.FromSlash(MirrorRoot), LoopModule)
	if err := os.MkdirAll(destLoop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destLoop, "README.md"), []byte("pack-docs readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tools/tillr/README.md", "tools/tillr/run.sh", "tools/tillr-loop/main.go", "tools/tillr-loop/README.md")

	var out, errBuf bytes.Buffer
	code := Main([]string{"--sync", repo, "tillr", LoopModule}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("Main(--sync) = %d, stderr = %s", code, errBuf.String())
	}

	destMod := filepath.Join(repo, filepath.FromSlash(MirrorRoot), "tillr")
	info, err := os.Stat(filepath.Join(destMod, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected exec bit preserved on run.sh, mode = %v", info.Mode())
	}
	if !strings.Contains(out.String(), "pack-sync: mirrored tools/tillr -> ") {
		t.Fatalf("unexpected stdout: %s", out.String())
	}
	if !strings.Contains(out.String(), "pack-sync: mirrored tools/tillr-loop -> ") {
		t.Fatalf("unexpected stdout for loop adapter: %s", out.String())
	}
	if got, err := os.ReadFile(filepath.Join(destLoop, "main.go")); err != nil || string(got) != "package main\n" {
		t.Fatalf("mirrored tillr-loop/main.go = %q, err = %v", string(got), err)
	}
	if got, err := os.ReadFile(filepath.Join(destLoop, "README.md")); err != nil || string(got) != "pack-docs readme\n" {
		t.Fatalf("tillr-loop/README.md should be preserved for pack-docs, got %q, err = %v", string(got), err)
	}
}

func TestMainExitCodes(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Main(nil, &out, &errBuf); code != 2 {
		t.Fatalf("Main(nil) = %d, want 2", code)
	}
	out.Reset()
	errBuf.Reset()
	if code := Main([]string{"--sync"}, &out, &errBuf); code != 2 {
		t.Fatalf("Main(--sync) = %d, want 2", code)
	}
	out.Reset()
	errBuf.Reset()
	if code := Main([]string{"--sync", t.TempDir(), "bogus"}, &out, &errBuf); code != 2 {
		t.Fatalf("Main(unknown module) = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "pack-sync: unknown module: bogus") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}
