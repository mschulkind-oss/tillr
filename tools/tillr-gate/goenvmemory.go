package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// goBuildIdentity binds inherited build options, not just `go version`. Inputs
// selected by workspaces, external local replacements or file-valued flags cannot
// be proved by the static declaration: execute normally, but remember no verdict.
func goBuildIdentity(check PlanCheck, root string, inputs []treeInput) (string, string, error) {
	cmd := exec.Command("go", "env", "-json")
	cmd.Dir, cmd.Env = root, checkEnv(check)
	raw, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("Go build environment: %w", err)
	}
	var settings map[string]string
	if err := json.Unmarshal(raw, &settings); err != nil {
		return "", "", err
	}
	reason := ""
	if work := settings["GOWORK"]; work != "" && work != "off" {
		reason = "Go workspace inputs"
	}
	for _, flag := range []string{"-overlay", "-modfile", "-toolexec", "-pkgdir", "-asmflags", "-gcflags", "-ldflags", "-gccgoflags", "-compiler", "-C"} {
		if strings.Contains(settings["GOFLAGS"], flag) {
			reason = "Go build flags with undeclared inputs"
		}
	}
	// Go's parser, rather than a second module-file grammar. This is metadata only,
	// never a compile, and catches local inputs outside the declaration's fence.
	for _, input := range inputs {
		if input.Mode == "120000" && coversPath(check.Reads, input.Path) {
			reason = "symlinked Go inputs"
		}
		if !strings.HasSuffix(input.Path, "/go.mod") || !coversPath(check.Reads, input.Path) {
			continue
		}
		dir := filepath.Dir(input.Path)
		work := exec.Command("go", "env", "GOWORK")
		work.Dir, work.Env = filepath.Join(root, dir), checkEnv(check)
		workspace, err := work.Output()
		if err != nil || strings.TrimSpace(string(workspace)) != "" && strings.TrimSpace(string(workspace)) != "off" {
			reason = "Go workspace inputs in " + dir
		}
		cmd := exec.Command("go", "mod", "edit", "-json")
		cmd.Dir, cmd.Env = filepath.Join(root, dir), checkEnv(check)
		raw, err := cmd.Output()
		if err != nil {
			reason = "unreadable Go module metadata"
			continue // the real build retains its ordinary compile verdict
		}
		var metadata struct {
			Replace []struct {
				New struct{ Path, Version string }
			}
		}
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return "", "", err
		}
		for _, replacement := range metadata.Replace {
			if replacement.New.Version != "" {
				continue
			}
			target := replacement.New.Path
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, dir, target)
			}
			relative, err := filepath.Rel(root, target)
			if err != nil || !coversPath(check.Reads, filepath.ToSlash(relative)) {
				reason = "local Go replacement outside declared inputs"
			}
		}
	}
	// Build cache locations do not change compiled semantics. GOGCCFLAGS carries
	// a new temporary path each query; this check turns cgo off in its program.
	for _, name := range []string{"GOMOD", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOENV", "GOROOT", "GOTOOLDIR", "GOGCCFLAGS"} {
		delete(settings, name)
	}
	raw, err = json.Marshal(settings)
	return string(raw), reason, err
}
