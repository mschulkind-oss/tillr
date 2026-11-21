package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
)

// Hook protocol: tools/tillr/definition/README.md, Hook program contract.
// These internal command keys preserve a definition's argv exactly; CLI programs
// continue to use their historical whitespace-separated command form.
const hookCommandPrefix = "definition-hook:"

func hookFlags(name string) []string {
	candidate := []string{"--item", "--lane", "--run", "--worktree", "--base", "--head", "--root"}
	switch name {
	case "gate":
		return candidate
	case "land":
		return append(candidate, "--observed-trunk", "--base-ref", "--message-from")
	case "verdict":
		return append(candidate, "--store", "--verdict")
	case "fixer-gate":
		return append(candidate, "--only")
	case "replay":
		return []string{"--only"}
	case "rolling":
		return []string{"--all", "--since"}
	case "complete-item":
		return []string{"--item", "--run", "--store", "--root"}
	case "after-completion":
		return []string{"--item", "--store", "--root"}
	case "detail":
		return []string{"--body-ref", "--root"}
	default:
		return nil
	}
}

// ConfigureHooks selects definition defaults only for step flags not explicitly
// supplied (including an explicit empty flag). A nil declaration changes nothing.
func (s *ProcessSteps) ConfigureHooks(hooks *definition.Hooks, explicit map[string]bool) {
	s.hooks = hooks
	s.hookCommands = map[string][]string{}
	if hooks == nil {
		return
	}
	for _, step := range []struct {
		name    string
		program *string
		suffix  string
	}{
		{"gate", &s.GateProgram, ""}, {"land", &s.LandProgram, ""}, {"verdict", &s.VerdictProgram, ""},
		{"replay", &s.ReplayProgram, " --only {only}"}, {"rolling", &s.RollingProgram, " {since}"}, {"fixer-gate", &s.FixerGateProgram, ""},
	} {
		if explicit[step.name] {
			continue
		}
		*step.program = ""
		if !slices.Contains(hooks.Names, step.name) {
			continue
		}
		key := hookCommandPrefix + step.name
		s.hookCommands[key] = append(append([]string{}, hooks.Argv...), step.name)
		*step.program = key + step.suffix
	}
}

func (s *ProcessSteps) commandArgv(program string) []string {
	words := strings.Fields(program)
	if len(words) == 0 {
		return nil
	}
	if argv, ok := s.hookCommands[words[0]]; ok {
		return append(append([]string{}, argv...), words[1:]...)
	}
	return words
}

func (s *ProcessSteps) isHookCommand(program string) bool {
	words := strings.Fields(program)
	return len(words) > 0 && s.hookCommands[words[0]] != nil
}

// checkHooks runs outside all store transactions, as part of CheckPrograms, not
// as a candidate judgment. Describe is the only call made to the hook program.
func (s *ProcessSteps) checkHooks(ctx context.Context) error {
	if s.hooks == nil {
		return nil
	}
	argv := append(append([]string{}, s.hooks.Argv...), "describe")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = s.Root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return Usagef("configuration fault: hook describe %v failed: %v: %s", s.hooks.Argv, err, strings.TrimSpace(stderr.String()))
	}
	var description struct {
		Hooks map[string][]string `json:"hooks"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&description); err != nil {
		return Usagef("configuration fault: hook describe: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Usagef("configuration fault: hook describe must print exactly one JSON object")
	}
	for _, name := range s.hooks.Names {
		accepted, ok := description.Hooks[name]
		if !ok {
			return Usagef("configuration fault: hook %q is listed by the definition but not answered by describe", name)
		}
		for _, flag := range hookFlags(name) {
			if !slices.Contains(accepted, flag) {
				return Usagef("configuration fault: hook %q does not accept %s", name, flag)
			}
		}
	}
	if slices.Contains(s.hooks.Names, "complete-item") {
		if s.ItemBin == "" {
			return Usagef("configuration fault: complete-item requires an item binary with --require-verdicts")
		}
		cmd := exec.CommandContext(ctx, s.ItemBin, "state", "--help")
		cmd.Dir = s.Root
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return Usagef("configuration fault: item state --help: %v: %s", err, strings.TrimSpace(string(out)))
		}
		for _, flag := range []string{"--store", "--item", "--state", "--now", "--reason", "--by", "--resolves-via", "--require-verdicts"} {
			// Go's flag help uses a single dash; both spellings parse identically.
			if !strings.Contains(string(out), strings.TrimPrefix(flag, "-")) {
				return Usagef("configuration fault: item state does not accept %s", flag)
			}
		}
	}
	return nil
}

func (s *ProcessSteps) reportHookSource(report io.Writer, name, program string) {
	fmt.Fprintf(report, "loop: step %s: definition hook %v\n", name, s.commandArgv(program))
}
