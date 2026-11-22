package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
)

type charterData struct {
	source string
	def    definition.Definition
	valid  bool
}

func loadAndLintCharter(absRoot, charterPath string, fc *findingCollector) charterData {
	if charterPath != "" {
		info, err := os.Stat(charterPath)
		if err != nil {
			fc.add(charterPath, CodeWatchLintInput, fmt.Sprintf("charter %q: %v", charterPath, err))
			return charterData{source: charterPath}
		}
		if info.IsDir() {
			fc.add(charterPath, CodeWatchLintInput, fmt.Sprintf("charter %q must be a file, not a directory", charterPath))
			return charterData{source: charterPath}
		}
		return readCharterFile(charterPath, fc)
	}

	defaultPath := filepath.Join(absRoot, "definition.json")
	info, err := os.Stat(defaultPath)
	if err == nil {
		if info.IsDir() {
			fc.add(defaultPath, CodeWatchLintInput, fmt.Sprintf("charter %q must be a file, not a directory", defaultPath))
			return charterData{source: defaultPath}
		}
		return readCharterFile(defaultPath, fc)
	}
	if !os.IsNotExist(err) {
		fc.add(defaultPath, CodeWatchLintInput, fmt.Sprintf("charter %q: %v", defaultPath, err))
		return charterData{source: defaultPath}
	}

	def, err := definition.GitLanding()
	if err != nil {
		fc.add("builtin:"+definition.GitLandingName, CodeWatchLintCharterSchema, err.Error())
		return charterData{source: "builtin:" + definition.GitLandingName}
	}
	return charterData{
		source: "builtin:" + definition.GitLandingName,
		def:    def,
		valid:  true,
	}
}

func readCharterFile(path string, fc *findingCollector) charterData {
	raw, err := os.ReadFile(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("reading charter %q: %v", path, err))
		return charterData{source: path}
	}
	def, err := definition.Read(bytes.NewReader(raw))
	if err != nil {
		fc.add(path, CodeWatchLintCharterSchema, err.Error())
		return charterData{source: path}
	}
	return charterData{
		source: path,
		def:    def,
		valid:  true,
	}
}

func lintCharterObservability(c charterData, rulesDir string, rulesDirExists bool, kinds map[string]bool, fc *findingCollector) {
	if c.def.Observability == nil {
		return
	}
	if pe := c.def.Observability.ProgressEvent; pe != "" && !kinds[pe] {
		fc.add(c.source, CodeWatchLintUnknownSignal, fmt.Sprintf("observability.progress_event: unregistered event kind %q", pe))
	}
	if rp := c.def.Observability.RulePack; rp != "" && rulesDirExists {
		packPath := filepath.Join(rulesDir, filepath.FromSlash(rp))
		info, err := os.Stat(packPath)
		if err != nil || !info.Mode().IsRegular() {
			fc.add(c.source, CodeWatchLintCharterSchema, fmt.Sprintf("observability.rule_pack: %q is not an existing regular file under %s", rp, fc.formatSource(rulesDir)))
		}
	}
}

func hookContractFlags() map[string][]string {
	candidate := []string{"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root"}
	return map[string][]string{
		"gate":             slices.Clone(candidate),
		"land":             append(slices.Clone(candidate), "--observed-trunk", "--base-ref", "--message-from"),
		"verdict":          append(slices.Clone(candidate), "--store", "--verdict"),
		"fixer-gate":       append(slices.Clone(candidate), "--only"),
		"replay":           []string{"--only"},
		"rolling":          []string{"--all", "--since"},
		"complete-item":    []string{"--item", "--run", "--store", "--root"},
		"after-completion": []string{"--item", "--store", "--root"},
		"detail":           []string{"--body-ref", "--root"},
	}
}

func lintCharterHooks(ctx context.Context, absRoot string, c charterData, hookTimeout time.Duration, fc *findingCollector) {
	if c.def.Completes == "hook" {
		if c.def.Hooks == nil || !slices.Contains(c.def.Hooks.Names, "complete-item") {
			fc.add(c.source, CodeWatchLintHookContract, `completes == "hook" requires "complete-item" in hooks.names`)
		}
		if len(c.def.Finished) == 0 {
			fc.add(c.source, CodeWatchLintHookContract, `completes == "hook" requires at least one finished state`)
		} else {
			hasReadyToFinished := false
			for _, tr := range c.def.Transitions {
				if tr.From == c.def.Ready && slices.Contains(c.def.Finished, tr.To) {
					hasReadyToFinished = true
					break
				}
			}
			if !hasReadyToFinished {
				fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf(`completes == "hook" requires a transition from ready %q to a finished state`, c.def.Ready))
			}
		}
	}

	if c.def.Hooks == nil {
		return
	}

	contract := hookContractFlags()
	seenNames := make(map[string]bool, len(c.def.Hooks.Names))
	for _, name := range c.def.Hooks.Names {
		if seenNames[name] {
			fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("duplicate hook name %q in hooks.names", name))
			continue
		}
		seenNames[name] = true
		if _, ok := contract[name]; !ok {
			fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("unknown hook name %q in hooks.names", name))
		}
	}

	if len(c.def.Hooks.Argv) == 0 || strings.TrimSpace(c.def.Hooks.Argv[0]) == "" {
		fc.add(c.source, CodeWatchLintHookContract, "hooks.argv[0] must be a non-empty executable")
		return
	}

	argv0 := c.def.Hooks.Argv[0]
	if strings.ContainsRune(argv0, '/') || strings.ContainsRune(argv0, filepath.Separator) {
		resolved := argv0
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(absRoot, argv0)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hooks.argv[0] %q: %v", argv0, err))
			return
		}
		if !info.Mode().IsRegular() {
			fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hooks.argv[0] %q must be a regular file", argv0))
			return
		}
		if info.Mode().Perm()&0111 == 0 {
			fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hooks.argv[0] %q is not executable", argv0))
			return
		}
	} else {
		if _, err := exec.LookPath(argv0); err != nil {
			fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hooks.argv[0] %q not found in PATH: %v", argv0, err))
			return
		}
	}

	out, err := runHookDescribe(ctx, absRoot, c.def.Hooks.Argv, hookTimeout)
	if err != nil {
		fc.add(c.source, CodeWatchLintHookContract, err.Error())
		return
	}
	if err := checkDuplicateJSONKeys(out); err != nil {
		fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hook describe returned invalid JSON: %v", err))
		return
	}

	var desc struct {
		Hooks map[string][]string `json:"hooks"`
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&desc); err != nil {
		fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hook describe JSON: %v", err))
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		fc.add(c.source, CodeWatchLintHookContract, "hook describe must print exactly one JSON object")
		return
	}
	if desc.Hooks == nil {
		fc.add(c.source, CodeWatchLintHookContract, `hook describe JSON missing "hooks" object`)
		return
	}

	for _, name := range c.def.Hooks.Names {
		reqFlags, known := contract[name]
		if !known {
			continue
		}
		gotFlags, ok := desc.Hooks[name]
		if !ok {
			fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hook describe missing declared hook %q", name))
			continue
		}
		for _, req := range reqFlags {
			if !slices.Contains(gotFlags, req) {
				fc.add(c.source, CodeWatchLintHookContract, fmt.Sprintf("hook %q missing required flag %s in describe output", name, req))
			}
		}
	}
}

func runHookDescribe(ctx context.Context, absRoot string, argv []string, hookTimeout time.Duration) ([]byte, error) {
	timeout := hookTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append(slices.Clone(argv[1:]), "describe")
	cmd := exec.CommandContext(hookCtx, argv[0], args...)
	cmd.Dir = absRoot
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "TILLR_HOOK_CALL=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if hookCtx.Err() != nil {
		return nil, fmt.Errorf("hook describe %v timed out or canceled: %v", argv, hookCtx.Err())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("hook describe %v failed: %v: %s", argv, err, msg)
		}
		return nil, fmt.Errorf("hook describe %v failed: %v", argv, err)
	}
	return out, nil
}
