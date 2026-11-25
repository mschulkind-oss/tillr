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
			launcher := filepath.Join(repo, "yolo-packs", "tillr", "programs", name, "run")
			invoke := func() string {
				cmd := exec.Command(launcher)
				cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "PATH="+fakebin+":"+os.Getenv("PATH"), "TILLR_INSTALL_ROOT=/wrong")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("launcher: %v: %s", err, out)
				}
				return strings.TrimSpace(string(out))
			}
			if got := invoke(); got != root {
				t.Fatalf("cold root = %q, want %q", got, root)
			}
			if err := os.Remove(filepath.Join(fakebin, "go")); err != nil {
				t.Fatal(err)
			}
			// A failing go ensures the second call uses the warm cache.
			if err := os.WriteFile(filepath.Join(fakebin, "go"), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
				t.Fatal(err)
			}
			if got := invoke(); got != root {
				t.Fatalf("warm root = %q, want %q", got, root)
			}
		})
	}
}
