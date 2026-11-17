package drain

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const stepArgvCheckVersion = "tillr-step-argv-check-v1"

func (s *ProcessSteps) programArgv(program string, c Candidate, extra ...string) []string {
	argv := append([]string{}, strings.Fields(program)...)
	argv = append(argv, candidateArgs(c, s.Root)...)
	return append(argv, extra...)
}

func landArgs(c Candidate) []string {
	var args []string
	if c.ObservedTrunk != "" {
		args = append(args, "--observed-trunk", c.ObservedTrunk)
	}
	if c.BaseRef != "" {
		args = append(args, "--base-ref", c.BaseRef)
	}
	if c.WorkerHeadSHA != "" {
		args = append(args, "--message-from", c.WorkerHeadSHA)
	}
	return args
}

func (s *ProcessSteps) verdictArgs(verdict string) []string {
	return []string{"--store", s.Store, "--verdict", verdict}
}

// CheckPrograms negotiates parse-only support before handing any synthetic candidate to
// a program. Unsupported programs receive help only, never candidate flags that could
// accidentally judge or land. No step spans or judgment records are written here.
func (s *ProcessSteps) CheckPrograms(ctx context.Context, report io.Writer) error {
	sha := strings.Repeat("1", 40)
	c := Candidate{ItemID: "startup-check", LaneID: "startup-check", RunID: "startup-check",
		Worktree: s.Root, BaseSHA: sha, HeadSHA: sha, ObservedTrunk: sha,
		BaseRef: "refs/heads/startup-check", WorkerHeadSHA: sha}
	type programCheck struct {
		name, program string
		candidate     Candidate
		extra         []string
	}
	checks := []programCheck{
		{"gate", s.GateProgram, c, nil},
		{"land", s.LandProgram, c, landArgs(c)},
		{"verdict", s.VerdictProgram, c, s.verdictArgs("pass")},
		{"verdict", s.VerdictProgram, c, s.verdictArgs("fail")},
	}
	if s.FixerGateProgram != "" {
		c.ItemID = TrunkFixerID(sha)
		checks = append(checks, programCheck{"fixer gate", s.fixerGate(c), c, nil})
		c.FixerBaseSHA = sha
		checks = append(checks, programCheck{"fixer gate", s.fixerGate(c), c, nil})
	}
	// Support is cached by configured command, not executable: an adapter's faces can
	// differ. Two fixer range expansions share one negotiated contract.
	supported := map[string]bool{}
	for _, check := range checks {
		key := check.program
		if check.name == "fixer gate" {
			key = s.FixerGateProgram
		}
		ok, known := supported[key]
		if !known {
			fields := strings.Fields(check.program)
			if len(fields) == 0 {
				return Usagef("configuration fault: no %s program", check.name)
			}
			cmd := exec.CommandContext(ctx, fields[0], append(fields[1:], "--help")...)
			cmd.Dir = s.Root
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			ok = err == nil && strings.Contains(string(out), stepArgvCheckVersion)
			supported[key] = ok
			if !ok && report != nil {
				if err != nil {
					fmt.Fprintf(report, "loop: step program %q unchecked: --help failed: %v: %s\n", key, err, strings.TrimSpace(string(out)))
				} else {
					fmt.Fprintf(report, "loop: step program %q unchecked: --help does not advertise %s\n", key, stepArgvCheckVersion)
				}
			}
		}
		if !ok {
			continue
		}
		argv := s.programArgv(check.program, check.candidate, check.extra...)
		argv = append(argv, "--check-argv")
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = s.Root
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return Usagef("configuration fault: %s program %q refused startup arguments %v: %v: %s", check.name, check.program, argv[1:], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
