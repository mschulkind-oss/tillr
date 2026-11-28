package goaffected

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-gate/internal/skip"
	"github.com/mschulkind-oss/tillr/disabledtests"
	"github.com/mschulkind-oss/tillr/settings"
)

// DisabledTests is the set of go-test selectors a lane gate excludes, keyed by the module
// and import path of the package they belong to. A nil *DisabledTests disables nothing.
type DisabledTests struct {
	byPackage map[string][]string
}

func disabledKey(module, importPath string) string { return module + "\x00" + importPath }

// For returns the sorted, unique selectors (a top-level test, optionally followed by
// /-separated subtests) listed for one package.
func (d *DisabledTests) For(module, importPath string) []string {
	if d == nil {
		return nil
	}
	return d.byPackage[disabledKey(module, importPath)]
}

// Split separates a package's selectors into listed top-level tests and listed subtests.
func (d *DisabledTests) Split(module, importPath string) (topLevel, subtests []string) {
	for _, sel := range d.For(module, importPath) {
		if strings.Contains(sel, "/") {
			subtests = append(subtests, sel)
		} else {
			topLevel = append(topLevel, sel)
		}
	}
	return topLevel, subtests
}

// LoadDisabledTests builds the active disabled set of a lane gate: the union of the gated
// worktree's list and the trunk tip's list, less the selectors in prove. A broken or absent
// worktree list fails the run; the trunk tip contributes nothing when root is not a git
// repository, the trunk ref or the list is absent there, or its blobs do not parse (that
// is logged to warn, so a broken trunk blob never deadlocks the lane that fixes it).
func LoadDisabledTests(root string, prove []string, warn io.Writer) (*DisabledTests, error) {
	cfg, err := settings.Read(root)
	if err != nil {
		return nil, err
	}
	list, err := disabledtests.Load(filepath.Join(root, filepath.FromSlash(cfg.DisabledTestsPath())))
	if err != nil {
		return nil, err
	}
	selectors := make(map[string]struct{})
	for _, e := range list.Disabled {
		selectors[e.Selector] = struct{}{}
	}
	if tip, terr := trunkTipList(root, cfg); terr != nil {
		if warn != nil {
			fmt.Fprintf(warn, "go-affected: the trunk tip's disabled-test list contributes nothing: %v\n", terr)
		}
	} else {
		for _, e := range tip.Disabled {
			selectors[e.Selector] = struct{}{}
		}
	}
	for _, sel := range prove {
		delete(selectors, sel)
	}
	d := &DisabledTests{byPackage: map[string][]string{}}
	for sel := range selectors {
		g, ok := disabledtests.ParseGoSelector(sel)
		if !ok {
			continue
		}
		key := disabledKey(g.Module, g.Package)
		d.byPackage[key] = append(d.byPackage[key], g.Test)
	}
	for key := range d.byPackage {
		sort.Strings(d.byPackage[key])
	}
	return d, nil
}

// trunkTipList reads the disabled-test list committed at the trunk ref. An error means the
// trunk tip contributes nothing; the empty list with a nil error means it lists nothing.
func trunkTipList(root string, cfg settings.Settings) (disabledtests.List, error) {
	git := func(args ...string) ([]byte, error) {
		// exec.Command resolves "git" on this process's PATH, not on cmd.Env, so name the
		// real git the bypassed PATH finds rather than a guard shim ahead of it.
		env := BuildEnv()
		gitBin := "git"
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "PATH="); ok {
				if real, _ := resolveRealGit(v); real != "" {
					gitBin = real
				}
			}
		}
		cmd := exec.Command(gitBin, append([]string{"-C", root}, args...)...)
		cmd.Env = env
		return cmd.Output()
	}
	out, err := git("rev-parse", "--verify", "--quiet", cfg.TrunkRef()+"^{commit}")
	if err != nil {
		return disabledtests.List{}, nil
	}
	tip := strings.TrimSpace(string(out))
	path := cfg.DisabledTestsPath()
	if toml, terr := git("show", tip+":.tillr.toml"); terr == nil {
		dir, derr := os.MkdirTemp("", "go-affected-trunk-")
		if derr != nil {
			return disabledtests.List{}, derr
		}
		defer os.RemoveAll(dir)
		if werr := os.WriteFile(filepath.Join(dir, ".tillr.toml"), toml, 0o644); werr != nil {
			return disabledtests.List{}, werr
		}
		tipCfg, rerr := settings.Read(dir)
		if rerr != nil {
			return disabledtests.List{}, fmt.Errorf("%s:.tillr.toml: %w", tip, rerr)
		}
		path = tipCfg.DisabledTestsPath()
	}
	blob, err := git("show", tip+":"+path)
	if err != nil {
		return disabledtests.List{}, nil
	}
	list, err := disabledtests.Parse(blob)
	if err != nil {
		return disabledtests.List{}, fmt.Errorf("%s:%s: %w", tip, path, err)
	}
	return list, nil
}

// subtestSkip returns the -skip pattern for subtests only, via the shards helper.
func subtestSkip(subtests []string) string { return skip.Subtests(subtests) }

// fullSkip returns the -skip pattern for top-level tests and subtests.
func fullSkip(sels []string) string { return skip.Full(sels) }
