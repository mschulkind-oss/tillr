package gitidentity

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

var identPattern = regexp.MustCompile(`^(.* <[^>]*>) [0-9]+ [+-][0-9]{4}$`)

// MainPreCommit runs the pre-commit identity guard for the current working directory.
func MainPreCommit(args []string, stdout, stderr io.Writer) int {
	return PreCommit("", nil, stderr)
}

// PreCommit checks that commits on main use the configured non-local Git identity.
// dir and env may be empty/nil to use the current process working directory and environment.
func PreCommit(dir string, env []string, stderr io.Writer) int {
	if err := checkPreCommit(dir, env, stderr); err != nil {
		if errors.Is(err, errIdentityMismatch) {
			return 1
		}
		fmt.Fprintf(stderr, "pre-commit: %s; no commit made\n", err)
		return 1
	}
	return 0
}

var errIdentityMismatch = errors.New("identity mismatch")

func checkPreCommit(dir string, env []string, stderr io.Writer) error {
	stdout, errStr, code, err := runGit(dir, env, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		if code == 1 {
			return nil // detached HEAD
		}
		if errStr != "" {
			return errors.New(errStr)
		}
		return err
	}
	if strings.TrimSpace(stdout) != "refs/heads/main" {
		return nil
	}

	var identity [2]string
	for i, key := range []string{"user.name", "user.email"} {
		out, errStr, code, err := runGit(dir, env, "config", "--show-scope", "--get-all", key)
		if err != nil || code != 0 {
			if errStr != "" {
				return errors.New(errStr)
			}
			return fmt.Errorf("git config --show-scope --get-all %s failed", key)
		}
		var value string
		for _, line := range splitLines(out) {
			scope, entry, ok := strings.Cut(line, "\t")
			if !ok {
				return fmt.Errorf("unexpected git config output line: %q", line)
			}
			if scope == "local" || scope == "worktree" {
				return fmt.Errorf("%s is set in %s config; unset it and use the configured global identity", key, scope)
			}
			value = strings.TrimSpace(entry)
		}
		if value == "" {
			return fmt.Errorf("%s is missing; configure the owner's global Git identity", key)
		}
		identity[i] = value
	}

	expected := fmt.Sprintf("%s <%s>", identity[0], identity[1])
	for _, kind := range []string{"AUTHOR", "COMMITTER"} {
		out, errStr, code, err := runGit(dir, env, "var", "GIT_"+kind+"_IDENT")
		if err != nil || code != 0 {
			if errStr != "" {
				return errors.New(errStr)
			}
			return fmt.Errorf("git var GIT_%s_IDENT failed", kind)
		}
		raw := strings.TrimSpace(out)
		m := identPattern.FindStringSubmatch(raw)
		if m == nil || m[1] != expected {
			fmt.Fprintf(stderr, "pre-commit: foreign identity on main: %s %s; expected %s. Remove the identity override or re-author the commit with the configured identity.\n",
				strings.ToLower(kind), raw, expected)
			return errIdentityMismatch
		}
	}

	return nil
}

func runGit(dir string, env []string, args ...string) (stdout string, stderr string, exitCode int, err error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = strings.TrimSpace(errBuf.String())
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return stdout, stderr, exitErr.ExitCode(), runErr
		}
		return stdout, stderr, -1, runErr
	}
	return stdout, stderr, 0, nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}
