package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNonLandingLoopStartsWithoutGit(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "outside-repository"
		if nested {
			name = "repository-subdirectory"
		}
		t.Run(name, func(t *testing.T) {
			root := scratch(t, "findings-root")
			if nested {
				git(t, root, "init", "-b", "main")
				root = filepath.Join(root, "findings")
				if err := os.MkdirAll(root, 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
			}
			path := fixture(t)
			d := plannerFixtureDefinition(t)
			d.Hooks, _ = definitionProgram(t, d.Hooks.Names, "")
			setPlannerDefinition(t, path, d)
			// Git and document sync remain forbidden; item state help is now
			// required to negotiate the transactional verdict guard at startup.
			count := filepath.Join(scratch(t, "forbidden"), "calls")
			forbidden := stub(t, "git", "echo invoked >> '"+count+"'\nexit 99")
			itemBin := stub(t, "item", `if [ "$*" = 'state --help' ]; then
 echo '--store --item --state --now --reason --by --resolves-via --require-verdicts'
 exit 0
fi
`+"echo invoked >> '"+count+"'\nexit 99")
			t.Setenv("PATH", filepath.Dir(forbidden)+":"+os.Getenv("PATH"))
			code, out := invoke(t, "loop", "--root", root, "--store", path,
				"--policy", policy(t, "true"), "--item-bin", itemBin,
				"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true",
				"--rolling", forbidden, "--exit-when-drained")
			if code != 0 {
				t.Fatalf("startup: %d %s", code, out)
			}
			for _, want := range []string{"definition: findings", "Git", "item sync", "trunk history", "rolling", "roll", "push"} {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q: %s", want, out)
				}
			}
			if raw, err := os.ReadFile(count); !os.IsNotExist(err) {
				t.Fatalf("forbidden calls: %s, %v", raw, err)
			}
		})
	}
}

func TestNonLandingLoopRefusesLandingSettings(t *testing.T) {
	for _, setting := range []string{"roll.command = ''", "roll.paths = []", "roll.pause_timeout_seconds = 120", "push.after_landing = false", "push.remote = ''", "push.ref = ''"} {
		t.Run(strings.Split(setting, " =")[0], func(t *testing.T) {
			root := scratch(t, "findings-settings")
			path := fixture(t)
			d := plannerFixtureDefinition(t)
			d.Hooks, _ = definitionProgram(t, d.Hooks.Names, "")
			setPlannerDefinition(t, path, d)
			writeFile(t, filepath.Join(root, ".tillr.toml"), setting+"\n")
			code, out := invoke(t, "loop", "--root", root, "--store", path, "--policy", policy(t, "true"),
				"--item-bin", sibling(t, "item"), "--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true", "--exit-when-drained")
			key := strings.Split(setting, " =")[0]
			if code != 2 || !strings.Contains(string(out), key) || !strings.Contains(string(out), "findings") {
				t.Fatalf("want named settings refusal: %d %s", code, out)
			}
		})
	}
}
