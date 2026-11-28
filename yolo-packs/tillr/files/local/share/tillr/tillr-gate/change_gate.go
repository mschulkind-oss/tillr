package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
)

const treeOverrun = "keep"

var gateHandoffs = map[string]bool{
	"TILLR_ORDINARY_GATE_TIMING": true,
	"TILLR_FAST_TREE_TIMING":     true,
	"TILLR_GATE_SPANS_OUT":       true,
	"TILLR_FAST_VERDICT_OUT":     true,
}

var scopeToChangeHelpRe = regexp.MustCompile(`(?m)^[ \t]*--?scope-to-change(?:[ \t]|$)`)

func defaultRunner() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "tillr-gate"
}

func resolveChecksDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return settings.ReadChecksDir(".")
}

func checkEnvironment() []string {
	var env []string
	for _, e := range os.Environ() {
		k, _, ok := strings.Cut(e, "=")
		if ok && gateHandoffs[k] {
			continue
		}
		env = append(env, e)
	}
	return env
}

func supportsChangeScope(runner string) bool {
	cmd := exec.Command(runner, "checks", "--help")
	cmd.Env = checkEnvironment()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	combined := stdout.String() + "\n" + stderr.String()
	return scopeToChangeHelpRe.MatchString(combined)
}

func goBuildCommand(checksDir string) []string {
	goBuildDecl := filepath.Join(checksDir, "go-build.json")
	if info, err := os.Stat(goBuildDecl); err != nil || info.IsDir() {
		return nil
	}
	repoGate := filepath.Join("tools", "gate", "repo-gate")
	if info, err := os.Stat(repoGate); err == nil && !info.IsDir() {
		return []string{repoGate, "go-build"}
	}
	return nil
}

func goBuildNeeded(base, head, checksDir string) bool {
	if goBuildCommand(checksDir) == nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(checksDir, "go-build.json"))
	if err != nil {
		return true
	}
	var decl struct {
		Reads []string `json:"reads"`
	}
	if json.Unmarshal(raw, &decl) != nil || len(decl.Reads) == 0 {
		return true
	}
	reads := make([]string, 0, len(decl.Reads))
	for _, r := range decl.Reads {
		if r == "" {
			return true
		}
		reads = append(reads, filepath.ToSlash(filepath.Clean(r)))
	}
	out, err := exec.Command("git", "diff", "--name-only", "--no-renames", "-z", base, head, "--").Output()
	if err != nil {
		return true
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		for _, r := range reads {
			if r == "." || p == r || strings.HasPrefix(p, r+"/") {
				return true
			}
		}
	}
	return false
}

func warmTrimmedGoCache(checksDir string) {
	goBuildCmd := goBuildCommand(checksDir)
	if len(goBuildCmd) == 0 {
		return
	}
	verOut, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return
	}
	version := strings.TrimSpace(string(verOut))
	if version == "" {
		return
	}
	commonOut, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return
	}
	common := strings.TrimSpace(string(commonOut))
	marker := filepath.Join(common, "tillr-gate", "go-build-warm-"+version)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "change-gate: warming the trimmed Go build cache for %s, once, outside the fast tier's ceilings\n", version)
	cmd := exec.Command(goBuildCmd[0], goBuildCmd[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = checkEnvironment()
	warmErr := cmd.Run()
	code := 0
	if warmErr != nil {
		if ee, ok := warmErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = record.ExitExternal
		}
	}
	ans := checkresult.Classify(checkresult.Observation{
		Check:    "go-build",
		Source:   checkresult.JudgingProgram,
		ExitCode: &code,
	})
	if ans.Outcome == checkresult.Pass || ans.Outcome == checkresult.Fail {
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "change-gate: could not record the warm cache (%v); the next gate warms again\n", err)
			return
		}
		if err := os.WriteFile(marker, []byte(version+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "change-gate: could not record the warm cache (%v); the next gate warms again\n", err)
		}
	}
}

func changeGateFace(args []string) int {
	flags := flag.NewFlagSet("change-gate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	base := flags.String("base", "", "base commit (required)")
	head := flags.String("head", "", "head commit (required)")
	runner := flags.String("runner", defaultRunner(), "gate runner binary")
	checks := flags.String("checks", "", "declaration directory (defaults to [gate] checks_dir in .tillr.toml, or tools/gate/checks)")
	logs := flags.String("logs", "", "logs directory (required)")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if *base == "" || *head == "" || *logs == "" {
		fmt.Fprintln(os.Stderr, "change-gate: could not judge: --base, --head, and --logs are required")
		return record.ExitUsage
	}

	failJudge := func(err error) int {
		fmt.Fprintf(os.Stderr, "change-gate: could not judge: %v\n", err)
		return record.ExitExternal
	}

	checksDir, err := resolveChecksDir(*checks)
	if err != nil {
		return failJudge(err)
	}

	actual, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return failJudge(err)
	}
	named, err := exec.Command("git", "rev-parse", *head+"^{commit}").Output()
	if err != nil {
		return failJudge(err)
	}
	if string(actual) != string(named) {
		return failJudge(errors.New("--head is not checked out"))
	}

	scoped := supportsChangeScope(*runner)
	if !scoped || goBuildNeeded(*base, *head, checksDir) {
		warmTrimmedGoCache(checksDir)
	}

	checksArgs := []string{"checks", "--fast-tree"}
	if scoped {
		checksArgs = append(checksArgs, "--scope-to-change")
	}
	checksArgs = append(checksArgs,
		"--base", *base,
		"--head", *head,
		"--tree-overrun", treeOverrun,
		"--worktree", ".",
		"--checks", checksDir,
		"--logs", *logs,
	)

	verdictPath := os.Getenv("TILLR_FAST_VERDICT_OUT")
	measureLanding := os.Getenv("TILLR_ORDINARY_GATE_TIMING") == "1" && verdictPath != "" && os.Getenv("TILLR_GATE_SPANS_OUT") != ""
	timingVal := ""
	if measureLanding {
		timingVal = "1"
	}
	env := append(checkEnvironment(), "TILLR_FAST_TREE_TIMING="+timingVal)

	cmd := exec.Command(*runner, checksArgs...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return failJudge(runErr)
		}
	}

	if verdictPath != "" {
		if err := os.WriteFile(verdictPath, stdout.Bytes(), 0o644); err != nil {
			return failJudge(err)
		}
	}
	if _, err := os.Stdout.Write(stdout.Bytes()); err != nil {
		return failJudge(err)
	}
	return code
}
