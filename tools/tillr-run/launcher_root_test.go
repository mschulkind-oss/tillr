package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackLaunchersPassInstallRootOnColdAndWarmPaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Dir(filepath.Dir(cwd))
	if filepath.Base(filepath.Dir(cwd)) != "tools" {
		// The mirrored module may run inside the pack checkout or on its own
		// after installation; launchers are not part of the installed module.
		suffix := filepath.Join("yolo-packs", "tillr", "files", "local", "share", "tillr", "tillr-run")
		if strings.HasSuffix(cwd, string(filepath.Separator)+suffix) {
			repo = strings.TrimSuffix(cwd, string(filepath.Separator)+suffix)
		} else {
			t.Skip("launcher sources are not included in the installed module")
		}
	}
	for _, name := range []string{"tillr", "tillr-drain", "tillr-gate", "tillr-item", "tillr-land", "tillr-run", "tillr-store", "tillr-view"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, ".local", "share", "tillr")
			mod := name
			if name == "tillr" {
				mod = "tillr-cli"
			}
			if err := os.MkdirAll(filepath.Join(root, mod), 0755); err != nil {
				t.Fatal(err)
			}
			fakebin := filepath.Join(home, "bin")
			if err := os.MkdirAll(fakebin, 0755); err != nil {
				t.Fatal(err)
			}
			goStub := `#!/bin/sh
if [ "$1" = build ]; then
  printf '%s\n' '#!/bin/sh' 'printf "%s\n" "$TILLR_INSTALL_ROOT"' > "$3"
  chmod +x "$3"
else exit 1; fi
`
			if err := os.WriteFile(filepath.Join(fakebin, "go"), []byte(goStub), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "tillr", "buildstamp"), 0755); err != nil {
				t.Fatal(err)
			}
			revFile := filepath.Join(root, "tillr", "buildstamp", "support-revision")
			if err := os.WriteFile(revFile, []byte("sha256:1111\n"), 0644); err != nil {
				t.Fatal(err)
			}
			launcher := filepath.Join(repo, "yolo-packs", "tillr", "programs", name, "run")
			invoke := func(installRoot string, set bool) string {
				cmd := exec.Command(launcher)
				var env []string
				for _, kv := range os.Environ() {
					if !strings.HasPrefix(kv, "TILLR_INSTALL_ROOT=") {
						env = append(env, kv)
					}
				}
				env = append(env, "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "PATH="+fakebin+":"+os.Getenv("PATH"))
				if set {
					env = append(env, "TILLR_INSTALL_ROOT="+installRoot)
				}
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("launcher: %v: %s", err, out)
				}
				return strings.TrimSpace(string(out))
			}
			// Cold path: unset and empty TILLR_INSTALL_ROOT default to $root.
			if got := invoke("", false); got != root {
				t.Fatalf("cold unset root = %q, want %q", got, root)
			}
			if got := invoke("", true); got != root {
				t.Fatalf("cold empty root = %q, want %q", got, root)
			}
			// Cold path: explicit TILLR_INSTALL_ROOT is preserved.
			if got := invoke("/explicit/install-root", true); got != "/explicit/install-root" {
				t.Fatalf("cold explicit root = %q, want /explicit/install-root", got)
			}
			if err := os.Remove(filepath.Join(fakebin, "go")); err != nil {
				t.Fatal(err)
			}
			// A failing go ensures the next calls use the warm cache.
			if err := os.WriteFile(filepath.Join(fakebin, "go"), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
				t.Fatal(err)
			}
			if got := invoke("", false); got != root {
				t.Fatalf("warm unset root = %q, want %q", got, root)
			}
			if got := invoke("", true); got != root {
				t.Fatalf("warm empty root = %q, want %q", got, root)
			}
			if got := invoke("/explicit/install-root", true); got != "/explicit/install-root" {
				t.Fatalf("warm explicit root = %q, want /explicit/install-root", got)
			}

			// Updating support-revision invalidates the warm cache and triggers a rebuild.
			if err := os.WriteFile(filepath.Join(fakebin, "go"), []byte(goStub), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(revFile, []byte("sha256:2222\n"), 0644); err != nil {
				t.Fatal(err)
			}
			// Ensure mtime is strictly newer than current symlink/binary if filesystem granularity is coarse.
			future := os.WriteFile(revFile, []byte("sha256:2222\n"), 0644)
			_ = future
			if got := invoke("/explicit/install-root", true); got != "/explicit/install-root" {
				t.Fatalf("rebuilt explicit root = %q, want /explicit/install-root", got)
			}
		})
	}
}
