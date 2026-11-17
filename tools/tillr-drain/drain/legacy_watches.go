package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type legacyWatch struct {
	Path        string   `json:"path"`
	Regex       string   `json:"regex"`
	Paths       bool     `json:"paths"`
	ExcludeDirs []string `json:"exclude_dirs"`
	MustExist   bool     `json:"must_exist"`
}

type legacyDeclaration struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"`
	Label      string        `json:"label"`
	SelectedBy []string      `json:"selected_by"`
	Selects    []legacyWatch `json:"selects"`
	Watches    []legacyWatch `json:"watches"`
}

// legacyCheckWatches reads the legacy gate's declaration export, not its output
// writes. The export runs no checks and takes no landing lock. Custom judges without
// that gate supply reads/watches in their spans instead; an existing gate that cannot
// export its declarations is an error, never permission to guess a narrower fence.
func legacyCheckWatches(ctx context.Context, dir string) (map[string][]string, error) {
	path := filepath.Join(dir, "tools", "fork-check.py")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "python3", path, "--registry-json")
	cmd.Dir = dir
	raw, err := cmd.Output()
	if err != nil {
		return nil, Externalf("reading legacy check watches: %v", err)
	}
	var registry struct {
		Declarations []legacyDeclaration `json:"declarations"`
		InProcess    []legacyDeclaration `json:"in_process"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		return nil, Externalf("reading legacy check watches: %v", err)
	}
	if registry.Declarations == nil {
		return nil, Externalf("legacy registry has no declarations")
	}
	// The registry is not a plan instance: dynamic watches have no selected_by
	// paths yet. Expand their selection patterns over the whole tracked tree so
	// untouched inputs are repairable too. Never use regexes as fence permissions.
	files, err := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "-z").Output()
	if err != nil {
		return nil, err
	}
	tracked := strings.Split(string(files), "\x00")
	out := map[string][]string{}
	for _, check := range append(registry.Declarations, registry.InProcess...) {
		out[check.ID] = []string{}
		for _, watch := range check.Watches {
			if watch.Paths {
				out[check.ID] = append(out[check.ID], check.SelectedBy...)
				for _, selection := range check.Selects {
					paths, err := legacyWatchFiles(dir, tracked, selection)
					if err != nil {
						return nil, Externalf("legacy selection for %s: %v", check.ID, err)
					}
					out[check.ID] = append(out[check.ID], paths...)
				}
				continue
			}
			if watch.Path != "" {
				out[check.ID] = append(out[check.ID], watch.Path)
				continue
			}
			paths, err := legacyWatchFiles(dir, tracked, watch)
			if err != nil {
				return nil, Externalf("legacy watch for %s: %v", check.ID, err)
			}
			out[check.ID] = append(out[check.ID], paths...)
		}
		// Some legacy spans use the kind/label-derived ID while the export
		// overrides the ID (the two storybook checks). Keep both identities.
		if check.Kind != "" && check.Label != "" {
			out[check.Kind+"\x00"+check.Label] = out[check.ID]
		}
	}
	return out, nil
}

func legacyWatchFiles(root string, files []string, watch legacyWatch) ([]string, error) {
	var re *regexp.Regexp
	if watch.Regex != "" {
		var err error
		re, err = regexp.Compile("^(?:" + watch.Regex + ")")
		if err != nil {
			return nil, err
		}
	}
	var paths []string
	for _, file := range files {
		if file == "" {
			continue
		}
		matched := file == watch.Path || (strings.HasSuffix(watch.Path, "/") && strings.HasPrefix(file, watch.Path))
		if re != nil {
			matched = re.MatchString(file)
			for _, part := range strings.Split(file, "/") {
				for _, excluded := range watch.ExcludeDirs {
					if part == excluded {
						matched = false
					}
				}
			}
			if matched && watch.MustExist {
				info, err := os.Stat(filepath.Join(root, file))
				matched = err == nil && info.Mode().IsRegular()
			}
		}
		if matched {
			paths = append(paths, file)
		}
	}
	return paths, nil
}
