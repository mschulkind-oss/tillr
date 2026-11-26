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

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

type treeCheckRunnerFunc func(runner string, args []string) (int, string, string, error)

func runTreeCheck(args []string, runRunner treeCheckRunnerFunc) int {
	flags := flag.NewFlagSet("tree-check", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	only := flags.String("only", "", "fast tree check id (required)")
	base := flags.String("base", "HEAD", "base commit")
	head := flags.String("head", "HEAD", "head commit")
	checks := flags.String("checks", "", "declaration directory (defaults to [gate] checks_dir in .tillr.toml, or tools/gate/checks)")
	logs := flags.String("logs", "", "logs directory (optional)")
	runner := flags.String("runner", defaultRunner(), "gate runner binary")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if *only == "" {
		fmt.Fprintf(os.Stderr, "fast-tree: %s: could not judge: --only is required\n", *only)
		return record.ExitUsage
	}

	failJudge := func(err error) int {
		fmt.Fprintf(os.Stderr, "fast-tree: %s: could not judge: %v\n", *only, err)
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

	entries, err := os.ReadDir(checksDir)
	if err != nil {
		return failJudge(err)
	}
	var matches []json.RawMessage
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(checksDir, e.Name()))
		if err != nil {
			return failJudge(err)
		}
		var meta struct {
			ID    string `json:"id"`
			Scope string `json:"scope"`
			Tier  string `json:"tier"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return failJudge(err)
		}
		if meta.ID == *only && meta.Scope == "tree" && meta.Tier == "fast" {
			matches = append(matches, raw)
		}
	}
	if len(matches) != 1 {
		return failJudge(fmt.Errorf("expected one fast tree declaration for %s, found %d", *only, len(matches)))
	}

	scratch := os.Getenv("YOLO_DURABLE_DIR")
	if scratch == "" {
		commonOut, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
		if err != nil {
			return failJudge(err)
		}
		scratch = filepath.Join(strings.TrimSpace(string(commonOut)), "fast-tree")
	}
	scratchAbs, err := filepath.Abs(scratch)
	if err != nil {
		return failJudge(err)
	}
	if err := os.MkdirAll(scratchAbs, 0o755); err != nil {
		return failJudge(err)
	}
	dir, err := os.MkdirTemp(scratchAbs, "fast-tree-")
	if err != nil {
		return failJudge(err)
	}
	defer os.RemoveAll(dir)

	tmpChecks := filepath.Join(dir, "checks")
	if err := os.MkdirAll(tmpChecks, 0o755); err != nil {
		return failJudge(err)
	}
	if err := os.WriteFile(filepath.Join(tmpChecks, "check.json"), matches[0], 0o644); err != nil {
		return failJudge(err)
	}

	logsDir := *logs
	if logsDir == "" {
		logsDir = filepath.Join(dir, "logs")
	}

	runnerArgs := []string{
		"checks", "--fast-tree",
		"--base", *base,
		"--head", *head,
		"--tree-overrun", "keep",
		"--worktree", ".",
		"--checks", tmpChecks,
		"--logs", logsDir,
	}

	var code int
	var stdout, stderr string
	if runRunner != nil {
		code, stdout, stderr, err = runRunner(*runner, runnerArgs)
	} else {
		cmd := exec.Command(*runner, runnerArgs...)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		if runErr != nil {
			if ee, ok := runErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				err = runErr
			}
		}
		stdout = outBuf.String()
		stderr = errBuf.String()
	}
	if err != nil {
		return failJudge(err)
	}
	fmt.Print(stdout)
	fmt.Fprint(os.Stderr, stderr)

	var envelope struct {
		Payload struct {
			Checks []struct {
				CheckID string `json:"check_id"`
				Result  string `json:"result"`
				LogRef  string `json:"log_ref"`
			} `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		return failJudge(err)
	}
	if len(envelope.Payload.Checks) != 1 || envelope.Payload.Checks[0].CheckID != *only {
		return failJudge(errors.New("runner did not judge the requested check"))
	}
	result := envelope.Payload.Checks[0].Result
	if result == "" {
		return failJudge(errors.New("runner did not report a result word"))
	}

	exitAns := checkresult.Classify(checkresult.Observation{
		Check:    *only,
		Commit:   *head,
		Source:   checkresult.JudgingProgram,
		ExitCode: &code,
	})
	wordAns := checkresult.Classify(checkresult.Observation{
		Check:  *only,
		Commit: *head,
		Source: checkresult.ResultWord,
		Word:   result,
	})

	allowedWireWords := map[string]bool{
		string(checkresult.Pass):  true,
		string(checkresult.Fail):  true,
		checkresult.DetailStalled: true,
	}
	type verdictKey struct {
		outcome checkresult.Outcome
		detail  string
	}
	verdictMap := map[verdictKey]int{
		{checkresult.Pass, checkresult.DetailPassed}:     record.ExitOK,
		{checkresult.Fail, checkresult.DetailFailed}:     1,
		{checkresult.Unknown, checkresult.DetailStalled}: 6,
	}
	exitKey := verdictKey{exitAns.Outcome, exitAns.Detail}
	wordKey := verdictKey{wordAns.Outcome, wordAns.Detail}
	verdict, ok := verdictMap[exitKey]
	if !allowedWireWords[result] || exitKey != wordKey || !ok {
		return failJudge(fmt.Errorf("runner exit %d does not match %s as a verdict", code, result))
	}

	fmt.Printf("fast-tree: %s: %s\n", *only, result)
	if logRef := envelope.Payload.Checks[0].LogRef; logRef != "" {
		if data, err := os.ReadFile(logRef); err == nil {
			fmt.Println(string(data))
		}
	}
	return verdict
}

func treeCheckFace(args []string) int {
	return runTreeCheck(args, nil)
}
