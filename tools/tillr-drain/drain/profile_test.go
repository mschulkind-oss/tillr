package drain_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestResolveConfigDirOrderAndRefusal(t *testing.T) {
	home := scratch(t, "home")
	xdg := scratch(t, "xdg")
	custom := scratch(t, "tillr-cfg")

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TILLR_CONFIG_DIR", "")

	got, err := drain.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir(HOME): %v", err)
	}
	if want := filepath.Join(home, ".config", "tillr"); got != want {
		t.Fatalf("ResolveConfigDir(HOME) = %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "relative/xdg")
	got, err = drain.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir(relative XDG): %v", err)
	}
	if want := filepath.Join(home, ".config", "tillr"); got != want {
		t.Fatalf("ResolveConfigDir(relative XDG) = %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err = drain.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir(XDG): %v", err)
	}
	if want := filepath.Join(xdg, "tillr"); got != want {
		t.Fatalf("ResolveConfigDir(XDG) = %q, want %q", got, want)
	}

	t.Setenv("TILLR_CONFIG_DIR", custom)
	got, err = drain.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir(TILLR_CONFIG_DIR): %v", err)
	}
	if got != custom {
		t.Fatalf("ResolveConfigDir(TILLR_CONFIG_DIR) = %q, want %q", got, custom)
	}

	t.Setenv("TILLR_CONFIG_DIR", "relative/tillr")
	_, err = drain.ResolveConfigDir()
	var exitErr *drain.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != record.ExitUsage {
		t.Fatalf("ResolveConfigDir(relative TILLR_CONFIG_DIR) = %v, want ExitUsage (2)", err)
	}
}

func TestResolveProfilePathValidationAndMissingList(t *testing.T) {
	cfgDir := scratch(t, "cfg")
	t.Setenv("TILLR_CONFIG_DIR", cfgDir)

	for _, bad := range []string{"", "-lead", "_lead", "Upper", "with.dot", "a/b", "../x"} {
		_, err := drain.ResolveProfilePath(bad)
		var exitErr *drain.ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != record.ExitUsage {
			t.Errorf("ResolveProfilePath(%q) = %v, want ExitUsage (2)", bad, err)
		}
	}

	// Empty directory: refuses and states no profiles exist.
	_, err := drain.ResolveProfilePath("missing")
	var exitErr *drain.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != record.ExitUsage {
		t.Fatalf("ResolveProfilePath(missing in empty) = %v, want ExitUsage (2)", err)
	}
	for _, want := range []string{"missing", cfgDir, "no profiles exist"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}

	// Populate valid profiles, a symlinked profile, and ignored files.
	writeFile(t, filepath.Join(cfgDir, "backend-policy.zeta.json"), "{}")
	writeFile(t, filepath.Join(cfgDir, "backend-policy.alpha.json"), "{}")
	target := filepath.Join(scratch(t, "linked-target"), "real.json")
	writeFile(t, target, "{}")
	if err := os.Symlink(target, filepath.Join(cfgDir, "backend-policy.linked.json")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	writeFile(t, filepath.Join(cfgDir, "backend-policy.json"), "{}")
	writeFile(t, filepath.Join(cfgDir, "backend-policy.bad.name.json"), "{}")
	if err := os.MkdirAll(filepath.Join(cfgDir, "backend-policy.dir.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := drain.ResolveProfilePath("linked")
	if err != nil {
		t.Fatalf("ResolveProfilePath(linked): %v", err)
	}
	if want := filepath.Join(cfgDir, "backend-policy.linked.json"); got != want {
		t.Fatalf("ResolveProfilePath(linked) = %q, want %q", got, want)
	}

	_, err = drain.ResolveProfilePath("nope")
	if !errors.As(err, &exitErr) || exitErr.Code != record.ExitUsage {
		t.Fatalf("ResolveProfilePath(nope) = %v, want ExitUsage (2)", err)
	}
	wantList := "alpha, linked, zeta"
	if !strings.Contains(err.Error(), wantList) {
		t.Fatalf("missing-profile error %q does not list %q", err.Error(), wantList)
	}
	for _, ignored := range []string{"bad.name", "dir"} {
		if strings.Contains(err.Error(), ignored) {
			t.Errorf("missing-profile error %q should not list ignored %q", err.Error(), ignored)
		}
	}
}

func TestLoopProfileSelectionAndPrecedence(t *testing.T) {
	setupRepo := func(t *testing.T, tomlBody string) (root, storePath string) {
		t.Helper()
		root = scratch(t, "repo")
		storePath = filepath.Join(root, ".tillr", "state.db")
		if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(context.Background(), storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		if tomlBody != "" {
			writeFile(t, filepath.Join(root, ".tillr.toml"), tomlBody)
		}
		return root, storePath
	}
	policyJSON := func(t *testing.T, backendName string) string {
		t.Helper()
		return `{"default_backend":"` + backendName + `","preference":["` + backendName + `"],"backends":{"` + backendName + `":{"adapter":"/bin/true","model":"fixture"}}}`
	}

	t.Run("flag selects profile from config dir", func(t *testing.T) {
		cfgDir := scratch(t, "cfg")
		t.Setenv("TILLR_CONFIG_DIR", cfgDir)
		writeFile(t, filepath.Join(cfgDir, "backend-policy.alpha.json"), policyJSON(t, "alpha-b"))
		root, storePath := setupRepo(t, "")

		code, out := invoke(t, "loop", "--check", "--root", root, "--store", storePath,
			"--profile", "alpha",
			"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")
		if code != 0 {
			t.Fatalf("loop --check --profile alpha exited %d: %s", code, out)
		}
	})

	t.Run("toml selects profile when flag unset", func(t *testing.T) {
		cfgDir := scratch(t, "cfg")
		t.Setenv("TILLR_CONFIG_DIR", cfgDir)
		writeFile(t, filepath.Join(cfgDir, "backend-policy.from-toml.json"), policyJSON(t, "toml-b"))
		root, storePath := setupRepo(t, "[drain]\nprofile = 'from-toml'\n")

		code, out := invoke(t, "loop", "--check", "--root", root, "--store", storePath,
			"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")
		if code != 0 {
			t.Fatalf("loop --check with [drain] profile exited %d: %s", code, out)
		}
	})

	t.Run("profile flag overrides toml", func(t *testing.T) {
		cfgDir := scratch(t, "cfg")
		t.Setenv("TILLR_CONFIG_DIR", cfgDir)
		// Deliberately make the toml profile invalid JSON so only --profile succeeds.
		writeFile(t, filepath.Join(cfgDir, "backend-policy.from-toml.json"), "{invalid")
		writeFile(t, filepath.Join(cfgDir, "backend-policy.from-flag.json"), policyJSON(t, "flag-b"))
		root, storePath := setupRepo(t, "[drain]\nprofile = 'from-toml'\n")

		code, out := invoke(t, "loop", "--check", "--root", root, "--store", storePath,
			"--profile", "from-flag",
			"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")
		if code != 0 {
			t.Fatalf("loop --check --profile overriding toml exited %d: %s", code, out)
		}
	})

	t.Run("policy flag wins over profile flag and toml without reading config dir", func(t *testing.T) {
		t.Setenv("TILLR_CONFIG_DIR", "relative/not-consulted")
		root, storePath := setupRepo(t, "[drain]\nprofile = 'missing-toml'\n")
		explicitPolicy := filepath.Join(scratch(t, "explicit"), "policy.json")
		writeFile(t, explicitPolicy, policyJSON(t, "explicit-b"))

		code, out := invoke(t, "loop", "--check", "--root", root, "--store", storePath,
			"--policy", explicitPolicy, "--profile", "missing-flag",
			"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")
		if code != 0 {
			t.Fatalf("loop --check --policy winning exited %d: %s", code, out)
		}
	})

	t.Run("missing profile refuses with exit 2 and lists existing names", func(t *testing.T) {
		cfgDir := scratch(t, "cfg")
		t.Setenv("TILLR_CONFIG_DIR", cfgDir)
		writeFile(t, filepath.Join(cfgDir, "backend-policy.beta.json"), policyJSON(t, "b"))
		writeFile(t, filepath.Join(cfgDir, "backend-policy.alpha.json"), policyJSON(t, "a"))
		root, storePath := setupRepo(t, "")

		code, out := invoke(t, "loop", "--check", "--root", root, "--store", storePath,
			"--profile", "gamma",
			"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")
		if code != 2 {
			t.Fatalf("loop --check missing profile exited %d, want 2: %s", code, out)
		}
		for _, want := range []string{"gamma", cfgDir, "alpha, beta"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("output %q missing %q", out, want)
			}
		}
	})
}

func TestParseProfileListAndResolveProfilePaths(t *testing.T) {
	got, err := drain.ParseProfileList(" alpha , beta-2 , gamma_3 ")
	if err != nil {
		t.Fatalf("ParseProfileList valid: %v", err)
	}
	want := []string{"alpha", "beta-2", "gamma_3"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("ParseProfileList = %v, want %v", got, want)
	}

	for _, bad := range []string{"", "   ", "alpha,", ",beta", "alpha,,beta", "alpha,Bad", "alpha,beta,alpha"} {
		if _, err := drain.ParseProfileList(bad); err == nil {
			t.Errorf("ParseProfileList(%q) succeeded, want error", bad)
		} else if drain.ExitCode(err) != 2 {
			t.Errorf("ParseProfileList(%q) exit code = %d, want 2 (%v)", bad, drain.ExitCode(err), err)
		}
	}

	cfgDir := scratch(t, "cfg-list")
	t.Setenv("TILLR_CONFIG_DIR", cfgDir)
	writeFile(t, filepath.Join(cfgDir, "backend-policy.alpha.json"), "{}\n")
	writeFile(t, filepath.Join(cfgDir, "backend-policy.beta.json"), "{}\n")

	paths, err := drain.ResolveProfilePaths([]string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("ResolveProfilePaths valid: %v", err)
	}
	if len(paths) != 2 || paths[0] != filepath.Join(cfgDir, "backend-policy.alpha.json") || paths[1] != filepath.Join(cfgDir, "backend-policy.beta.json") {
		t.Fatalf("ResolveProfilePaths = %v", paths)
	}

	_, err = drain.ResolveProfilePaths([]string{"alpha", "missing"})
	if err == nil {
		t.Fatal("ResolveProfilePaths with missing second entry succeeded, want error")
	}
	if drain.ExitCode(err) != 2 {
		t.Fatalf("ResolveProfilePaths exit code = %d, want 2 (%v)", drain.ExitCode(err), err)
	}
	for _, sub := range []string{"missing", "index 1 of 2", cfgDir, "alpha, beta"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q missing %q", err.Error(), sub)
		}
	}
}
