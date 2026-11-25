package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func runBenchCmd(args []string, dir string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %w\n%s%s", args, err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

func medianInt64(vals []int64) float64 {
	s := append([]int64(nil), vals...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return float64(s[n/2-1]+s[n/2]) / 2.0
}

func cmdBenchmarkFastChecks(args []string) int {
	fs := flag.NewFlagSet("benchmark-fast-checks", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baselineRev := fs.String("baseline", "", "baseline commit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *baselineRev == "" {
		fmt.Fprintln(os.Stderr, "benchmark-fast-checks: --baseline is required")
		return 2
	}
	durable := os.Getenv("YOLO_DURABLE_DIR")
	if durable == "" {
		fmt.Fprintln(os.Stderr, "benchmark-fast-checks: YOLO_DURABLE_DIR is required")
		return 4
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
		return 4
	}

	dest := filepath.Join(durable, "fast-check-audit-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	fixture := filepath.Join(dest, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
		return 4
	}

	entries, err := treeEntries(root, "HEAD")
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
		return 4
	}
	roadmap := "docs/tillr/roadmap.md"
	rawRoadmap, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(roadmap)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
		return 4
	}
	text := string(rawRoadmap)
	named, _ := namedInputs(text, roadmap, entries)

	selected := map[string]bool{roadmap: true, ".vantage.toml": true}
	for p, e := range entries {
		if e.kind != "blob" {
			continue
		}
		if strings.HasPrefix(p, "tools/gate/") {
			selected[p] = true
			continue
		}
		for _, m := range goToolDirs {
			if strings.HasPrefix(p, m+"/") {
				selected[p] = true
				break
			}
		}
	}
	for p := range named {
		if entries[p].kind == "blob" {
			selected[p] = true
		}
	}

	for p := range selected {
		src := filepath.Join(root, filepath.FromSlash(p))
		info, err := os.Stat(src)
		if err != nil || info.IsDir() {
			continue
		}
		dst := filepath.Join(fixture, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
			return 4
		}
		data, err := os.ReadFile(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
			return 4
		}
		if err := os.WriteFile(dst, data, info.Mode()); err != nil {
			fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
			return 4
		}
	}
	for p := range named {
		if entries[p].kind == "tree" {
			dir := filepath.Join(fixture, filepath.FromSlash(p))
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, ".audit-directory"), []byte("directory link target\n"), 0o644)
		}
	}

	if _, err := runBenchCmd([]string{"git", "init", "-q"}, fixture); err != nil {
		fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
		return 4
	}
	commitFixture := func() string {
		_, _ = runBenchCmd([]string{"git", "add", "."}, fixture)
		_, _ = runBenchCmd([]string{"git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"}, fixture)
		out, _ := runBenchCmd([]string{"git", "rev-parse", "HEAD"}, fixture)
		return strings.TrimSpace(out)
	}
	base := commitFixture()

	goPath := filepath.Join(fixture, "tools", "tillr-gate", "main.go")
	goBytes, err := os.ReadFile(goPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
		return 4
	}
	goText := string(goBytes)
	repoGateBin := filepath.Join(fixture, "tools", "gate", "repo-gate")

	type stageResult struct {
		RunsMS   []int64 `json:"runs_ms"`
		MedianMS float64 `json:"median_ms"`
	}
	results := make(map[string]stageResult)
	for _, stage := range []string{"before", "after"} {
		if stage == "after" {
			_ = os.WriteFile(filepath.Join(fixture, filepath.FromSlash(roadmap)), []byte(text), 0o644)
			_ = os.WriteFile(goPath, []byte(goText), 0o644)
			commitFixture()
			_, _ = runBenchCmd([]string{repoGateBin, "documents", base, "HEAD"}, fixture)
			_, _ = runBenchCmd([]string{repoGateBin, "go-build"}, fixture)
		}
		for _, spec := range []struct {
			label string
			argv  []string
		}{
			{"documents", []string{repoGateBin, "documents", base, "HEAD"}},
			{"go-build", []string{repoGateBin, "go-build"}},
		} {
			var values []int64
			for n := 0; n < 5; n++ {
				if spec.label == "documents" {
					_ = os.WriteFile(filepath.Join(fixture, filepath.FromSlash(roadmap)), []byte(fmt.Sprintf("%s\nBudget audit %s %d.\n", text, stage, n)), 0o644)
				} else {
					_ = os.WriteFile(filepath.Join(fixture, filepath.FromSlash(roadmap)), []byte(text), 0o644)
					offset := 0
					if stage == "after" {
						offset = 10
					}
					_ = os.WriteFile(goPath, []byte(fmt.Sprintf("%s\nvar gateBudgetAudit = %d\n", goText, n+offset)), 0o644)
				}
				commitFixture()
				started := time.Now()
				out, err := runBenchCmd(spec.argv, fixture)
				if err != nil {
					fmt.Fprintf(os.Stderr, "benchmark-fast-checks: %v\n", err)
					return 4
				}
				elapsed := time.Since(started).Milliseconds()
				values = append(values, elapsed)
				_ = os.WriteFile(filepath.Join(dest, fmt.Sprintf("%s-%s-%d.log", stage, spec.label, n)), []byte(out), 0o644)
				fmt.Println(stage, spec.label, n, elapsed, "ms")
			}
			med := medianInt64(values)
			results[stage+"-"+spec.label] = stageResult{RunsMS: values, MedianMS: med}
			fmt.Println(stage, spec.label, "median", med, "ms")
		}
	}
	resJSON, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(filepath.Join(dest, "results.json"), append(resJSON, '\n'), 0o644)
	fmt.Println(string(resJSON))
	fmt.Println("Retained audit:", dest)
	return 0
}
