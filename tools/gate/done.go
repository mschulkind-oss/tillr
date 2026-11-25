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
	"strings"
)

func doneGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "git " + strings.Join(args, " ") + " failed"
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func doneDefaultBase() (string, string, error) {
	for _, candidate := range []string{"refs/tillr/green", "@{upstream}"} {
		shaCmd := exec.Command("git", "rev-parse", "--verify", "--quiet", candidate+"^{commit}")
		shaOut, err := shaCmd.Output()
		if err == nil {
			sha := strings.TrimSpace(string(shaOut))
			if exec.Command("git", "merge-base", "--is-ancestor", sha, "HEAD").Run() == nil {
				return sha, candidate, nil
			}
		}
	}
	parentOut, err := exec.Command("git", "rev-parse", "--verify", "--quiet", "HEAD^").Output()
	if err == nil {
		return strings.TrimSpace(string(parentOut)), "HEAD's first parent", nil
	}
	sha, err := doneGit("rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	return sha, "HEAD (a root commit)", nil
}

func doneBuildRunner(common string) (string, error) {
	target := filepath.Join(common, "tillr-gate", "bin", "tillr-gate")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "build", "-o", target, ".")
	cmd.Dir = filepath.Join("tools", "tillr-gate")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New("tillr-gate did not build")
	}
	return target, nil
}

func doneJudge(runner, title string, flags []string, base, head, logs string) int {
	fmt.Printf("done: %s\n", title)
	args := append([]string{"checks"}, flags...)
	args = append(args,
		"--base", base,
		"--head", head,
		"--worktree", ".",
		"--checks", filepath.Join("tools", "gate", "checks"),
		"--logs", logs,
	)
	absRunner, err := filepath.Abs(runner)
	if err != nil {
		absRunner = runner
	}
	runnerDir := filepath.Dir(absRunner)
	var env []string
	hasPath := false
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PATH=") {
			env = append(env, "PATH="+runnerDir+string(os.PathListSeparator)+e[len("PATH="):])
			hasPath = true
		} else {
			env = append(env, e)
		}
	}
	if !hasPath {
		env = append(env, "PATH="+runnerDir)
	}

	cmd := exec.Command(runner, args...)
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
			code = 4
		}
	}

	var envelope struct {
		Payload struct {
			Checks []struct {
				CheckID string `json:"check_id"`
				Result  string `json:"result"`
				LogRef  string `json:"log_ref"`
				LogPath string `json:"log_path"`
			} `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		fmt.Printf("done: %s: could not judge: the gate printed no verdict (exit %d)\n", title, code)
		return 4
	}
	failed := 0
	for _, check := range envelope.Payload.Checks {
		result := check.Result
		passed := result == "pass" || strings.HasPrefix(result, "skipped") || strings.HasPrefix(result, "remembered")
		note := ""
		if !passed {
			failed++
			logLoc := check.LogRef
			if logLoc == "" {
				logLoc = check.LogPath
			}
			if logLoc == "" {
				logLoc = "none"
			}
			note = "  log: " + logLoc
		}
		label := "ok  "
		if !passed {
			label = "FAIL"
		}
		fmt.Printf("  %s %s: %s%s\n", label, check.CheckID, result, note)
	}
	if code != 0 && code != 1 {
		fmt.Printf("done: %s: could not judge (exit %d)\n", title, code)
		return 4
	}
	if failed > 0 || code != 0 {
		return 1
	}
	return 0
}

func cmdDone(args []string) int {
	fs := flag.NewFlagSet("done", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	since := fs.String("since", "", "judge the change checks over BASE..HEAD")
	fast := fs.Bool("fast", false, "run the first pass only")
	only := fs.String("only", "", "run exactly this heavy check")
	runnerFlag := fs.String("runner", "", "use this tillr-gate instead of building one")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	failJudge := func(err error) int {
		fmt.Fprintf(os.Stderr, "done: could not judge: %v\n", err)
		return 4
	}

	top, err := doneGit("rev-parse", "--show-toplevel")
	if err != nil {
		return failJudge(err)
	}
	if err := os.Chdir(top); err != nil {
		return failJudge(err)
	}
	st, err := doneGit("status", "--porcelain")
	if err != nil {
		return failJudge(err)
	}
	if st != "" {
		return failJudge(errors.New("the working tree has uncommitted changes; the checks judge HEAD, so commit first"))
	}
	head, err := doneGit("rev-parse", "HEAD")
	if err != nil {
		return failJudge(err)
	}

	var base, source string
	if *since != "" {
		base, err = doneGit("rev-parse", "--verify", *since+"^{commit}")
		if err != nil {
			return failJudge(err)
		}
		source = "--since"
	} else {
		base, source, err = doneDefaultBase()
		if err != nil {
			return failJudge(err)
		}
	}

	common, err := doneGit("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return failJudge(err)
	}
	logs := filepath.Join(common, "tillr-gate", "done-logs", head)
	runner := *runnerFlag
	if runner == "" {
		runner, err = doneBuildRunner(common)
		if err != nil {
			return failJudge(err)
		}
	}

	worst := 0
	if *only == "" {
		title := fmt.Sprintf("change and fast checks over %s..%s (base: %s)", base[:min(12, len(base))], head[:min(12, len(head))], source)
		worst = doneJudge(runner, title, []string{"--fast-tree"}, base, head, filepath.Join(logs, "fast"))
	}
	if !*fast && worst != 4 {
		flags := []string{"--heavy-tree"}
		if *only != "" {
			flags = append(flags, "--only", *only)
		}
		title := fmt.Sprintf("heavy checks on %s", head[:min(12, len(head))])
		r := doneJudge(runner, title, flags, head, head, filepath.Join(logs, "heavy"))
		if r > worst {
			worst = r
		}
	}
	verdictLabel := "COULD NOT JUDGE"
	if worst == 0 {
		verdictLabel = "PASSED"
	} else if worst == 1 {
		verdictLabel = "FAILED"
	}
	fmt.Printf("done: %s\n", verdictLabel)
	return worst
}
