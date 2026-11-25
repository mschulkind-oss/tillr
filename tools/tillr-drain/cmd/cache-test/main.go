package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

func main() {
	defer store.FlushSQLiteTraceAtExit()
	runPattern := flag.String("run", "", "targeted Go test pattern for development only")
	flag.Parse()

	if err := run(*runPattern); err != nil {
		fmt.Fprintln(os.Stderr, err)
		store.FlushSQLiteTraceAtExit()
		os.Exit(1)
	}
}

func run(runPattern string) error {
	source, err := resolveRepoRoot()
	if err != nil {
		return err
	}
	scratch := os.Getenv("YOLO_DURABLE_DIR")
	if scratch == "" {
		return errors.New("YOLO_DURABLE_DIR must be set")
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return err
	}

	temp, err := os.MkdirTemp(scratch, "sibling-cache-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)

	repo := filepath.Join(temp, "repo")
	cloneCmd := exec.Command("git", "clone", "--quiet", "--local", source, repo)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, out)
	}

	if err := overlayCandidate(source, repo); err != nil {
		return err
	}

	module := filepath.Join(repo, "tools", "tillr-drain")
	env := withEnv(os.Environ(), "YOLO_DURABLE_DIR", filepath.Join(temp, "scratch"))

	// -timeout is a hang guard, never a budget: a loaded machine runs the drain
	// suite for ten minutes or more, and Go's default 10m would kill it with no failing test.
	goArgs := []string{"test", "./...", "-timeout", "60m"}
	if runPattern != "" {
		goArgs = append(goArgs, "-run", runPattern)
	}

	check := func(label string, success, cached bool, rootCached *bool) error {
		cmd := exec.Command("go", goArgs...)
		cmd.Dir = module
		cmd.Env = env
		outBytes, err := cmd.CombinedOutput()
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return fmt.Errorf("%s: failed to run go test: %w", label, err)
			}
			exitCode = exitErr.ExitCode()
		}

		stdout := string(outBytes)
		logPath := filepath.Join(scratch, "cache-"+label+".log")
		if err := os.WriteFile(logPath, outBytes, 0o644); err != nil {
			return fmt.Errorf("%s: write log: %w", label, err)
		}

		lines := strings.Split(stdout, "\n")
		drainLines := matchingPackageLines(lines, "github.com/mschulkind-oss/tillr-drain/drain")
		if len(drainLines) == 0 {
			return fmt.Errorf("%s: missing drain package result:\n%s", label, stdout)
		}
		lastDrain := drainLines[len(drainLines)-1]
		if strings.Contains(lastDrain, "[no tests to run]") {
			return fmt.Errorf("%s: drain had no tests to run:\n%s", label, stdout)
		}
		if (exitCode == 0) != success {
			return fmt.Errorf("%s: exit %d, want success=%v:\n%s", label, exitCode, success, stdout)
		}
		if strings.Contains(lastDrain, "(cached)") != cached {
			return fmt.Errorf("%s: drain cached=%v, want %v (%s):\n%s", label, strings.Contains(lastDrain, "(cached)"), cached, lastDrain, stdout)
		}

		if runPattern == "" {
			rootLines := matchingPackageLines(lines, "github.com/mschulkind-oss/tillr-drain")
			if len(rootLines) == 0 {
				return fmt.Errorf("%s: missing root package result:\n%s", label, stdout)
			}
			lastRoot := rootLines[len(rootLines)-1]
			if strings.Contains(lastRoot, "[no tests to run]") {
				return fmt.Errorf("%s: root had no tests to run:\n%s", label, stdout)
			}
			expectedRootCached := cached
			if rootCached != nil {
				expectedRootCached = *rootCached
			}
			if strings.Contains(lastRoot, "(cached)") != expectedRootCached {
				return fmt.Errorf("%s: root cached=%v, want %v (%s):\n%s", label, strings.Contains(lastRoot, "(cached)"), expectedRootCached, lastRoot, stdout)
			}
			fmt.Printf("%s: %s\n", label, lastRoot)
		}
		fmt.Printf("%s: exit %d; %s\n", label, exitCode, lastDrain)
		return nil
	}

	if err := check("warm", true, false, nil); err != nil {
		return err
	}
	if err := check("unchanged", true, true, nil); err != nil {
		return err
	}

	// Derive the exercised names from the actual sibling calls, not a second input list.
	pipelineBytes, err := os.ReadFile(filepath.Join(module, "drain", "pipeline_test.go"))
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`sibling\(t, "([a-z]+)"\)`)
	matches := re.FindAllSubmatch(pipelineBytes, -1)
	seen := make(map[string]struct{}, len(matches))
	siblings := make([]string, 0, len(matches))
	for _, m := range matches {
		name := string(m[1])
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			siblings = append(siblings, name)
		}
	}
	sort.Strings(siblings)
	if len(siblings) != 5 {
		return fmt.Errorf("expected 5 siblings, got %v", siblings)
	}

	trueVal := true
	for _, sibling := range siblings {
		mainPath := filepath.Join(repo, "tools", "tillr-"+sibling, "main.go")
		origBytes, err := os.ReadFile(mainPath)
		if err != nil {
			return err
		}
		original := string(origBytes)
		if !strings.Contains(original, "func main() {") {
			return fmt.Errorf("%s does not contain 'func main() {'", mainPath)
		}
		probed := strings.Replace(original, "func main() {",
			`func main() { println("broken sibling cache probe"); os.Exit(9);`, 1)
		if err := os.WriteFile(mainPath, []byte(probed), 0o644); err != nil {
			return err
		}
		checkErr := check(sibling, false, false, &trueVal)
		restoreErr := os.WriteFile(mainPath, origBytes, 0o644)
		if checkErr != nil {
			return checkErr
		}
		if restoreErr != nil {
			return restoreErr
		}
	}

	if err := check("restored", true, false, &trueVal); err != nil {
		return err
	}
	if err := check("restored-unchanged", true, true, &trueVal); err != nil {
		return err
	}
	fmt.Println("PASS: all five broken siblings invalidate the drain result; root stays cached and unchanged results cache")
	return nil
}

func resolveRepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return filepath.Clean(trimmed), nil
		}
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot resolve repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..")), nil
}

func overlayCandidate(source, repo string) error {
	deletedOut, err := exec.Command("git", "-C", source, "diff", "--no-renames", "--name-only", "--diff-filter=D", "-z", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("git diff deleted files: %w", err)
	}
	for _, raw := range bytes.Split(deletedOut, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		dst := filepath.Join(repo, filepath.FromSlash(string(raw)))
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	filesOut, err := exec.Command("git", "-C", source, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	for _, raw := range bytes.Split(filesOut, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		rel := filepath.FromSlash(string(raw))
		src := filepath.Join(source, rel)
		dst := filepath.Join(repo, rel)
		info, err := os.Stat(src)
		if err == nil && info.Mode().IsRegular() {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := copyFile(src, dst, info.Mode().Perm()); err != nil {
				return err
			}
		} else {
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func withEnv(base []string, key, val string) []string {
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+val)
}

func matchingPackageLines(lines []string, pkg string) []string {
	var matched []string
	for _, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if !strings.HasPrefix(line, "ok ") && !strings.HasPrefix(line, "ok\t") && !strings.HasPrefix(line, "FAIL\t") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == pkg {
			matched = append(matched, line)
		}
	}
	return matched
}
