package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/buildstamp"
	"github.com/mschulkind-oss/tillr/record"
)

func makeCompleteInstallRoot(t *testing.T, rev string) string {
	t.Helper()
	installRoot := scratch(t, "install-root")
	write := func(rel, content string) {
		p := filepath.Join(installRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tillr-run/backends/brief-template.md", "# brief\n")
	write("workflows/planned-build@v1.json", `{"name":"planned-build","version":1,"nodes":[{"id":"n1","seat":"build","access":"write"}]}`+"\n")
	if rev != "" {
		write("tillr/buildstamp/support-revision", rev+"\n")
	}
	return installRoot
}

func TestLoopSupportFileAndRevisionVerification(t *testing.T) {
	wantRev := buildstamp.Read().SupportRevision

	t.Run("missing-workflow-refused", func(t *testing.T) {
		root, storePath, policyPath := setupConsumerRepo(t)
		tomlPath := filepath.Join(root, ".tillr.toml")
		if err := os.WriteFile(tomlPath, []byte("[trunk]\nbranch = \"main\"\n\n[workflows.default]\nbuild = \"planned-build@v1\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		installRoot := makeCompleteInstallRoot(t, wantRev)
		missingWF := filepath.Join(installRoot, "workflows", "planned-build@v1.json")
		if err := os.Remove(missingWF); err != nil {
			t.Fatal(err)
		}

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != record.ExitUsage {
			t.Fatalf("code = %d, want %d; out:\n%s", code, record.ExitUsage, out)
		}
		if !strings.Contains(out, "planned-build@v1") || !strings.Contains(out, installRoot) {
			t.Fatalf("output did not name workflow and install root:\n%s", out)
		}
	})

	t.Run("mismatched-support-revision-refused-naming-both", func(t *testing.T) {
		root, storePath, policyPath := setupConsumerRepo(t)
		otherRev := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		installRoot := makeCompleteInstallRoot(t, otherRev)

		for _, tc := range []struct {
			name string
			args []string
		}{
			{"loop-check", []string{"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath, "--gate", "true", "--land", "true", "--verdict", "true"}},
			{"loop-start", []string{"loop", "--exit-when-drained", "--root", root, "--store", storePath, "--policy", policyPath, "--gate", "true", "--land", "true", "--verdict", "true"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				code, out := invokeInDir(t, root, installRoot, tc.args...)
				if code != record.ExitUsage {
					t.Fatalf("code = %d, want %d; out:\n%s", code, record.ExitUsage, out)
				}
				for _, want := range []string{wantRev, otherRev, installRoot, "tillr-drain"} {
					if !strings.Contains(out, want) {
						t.Fatalf("output missing %q:\n%s", want, out)
					}
				}
			})
		}
	})

	t.Run("unrecorded-support-revision-refused-naming-missing-file", func(t *testing.T) {
		root, storePath, policyPath := setupConsumerRepo(t)
		installRoot := makeCompleteInstallRoot(t, "")
		revPath := filepath.Join(installRoot, "tillr", "buildstamp", "support-revision")

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != record.ExitUsage {
			t.Fatalf("code = %d, want %d; out:\n%s", code, record.ExitUsage, out)
		}
		for _, want := range []string{wantRev, "unrecorded", revPath, installRoot} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("matching-install-root-passes", func(t *testing.T) {
		root, storePath, policyPath := setupConsumerRepo(t)
		installRoot := makeCompleteInstallRoot(t, wantRev)

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != 0 {
			t.Fatalf("code = %d, want 0; out:\n%s", code, out)
		}
	})
}
