package boundary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
)

type gateFormatCheckDecl struct {
	Command []string `json:"command"`
}

// UnformattedRepoFiles runs the repository's declared format check (`<checks_dir>/format.json`,
// e.g. `just format --check`) in `worktree` and returns the unformatted file paths when the
// formatter would rewrite any file. Repositories or fixtures that declare no format check return nil.
func UnformattedRepoFiles(ctx context.Context, worktree string) ([]string, error) {
	checksDir, err := settings.ReadChecksDir(worktree)
	if err != nil {
		return nil, drainmodel.Externalf("%v", err)
	}
	return UnformattedRepoFilesInChecksDir(ctx, worktree, checksDir)
}

func UnformattedRepoFilesInChecksDir(ctx context.Context, worktree, checksDir string) ([]string, error) {
	formatRel := filepath.ToSlash(filepath.Join(filepath.FromSlash(checksDir), "format.json"))
	raw, err := os.ReadFile(filepath.Join(worktree, formatRel))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, drainmodel.Externalf("read %s in %s: %v", formatRel, worktree, err)
	}
	var decl gateFormatCheckDecl
	if err := json.Unmarshal(raw, &decl); err != nil {
		return nil, drainmodel.Externalf("parse %s in %s: %v", formatRel, worktree, err)
	}
	if len(decl.Command) == 0 || strings.TrimSpace(decl.Command[0]) == "" {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, decl.Command[0], decl.Command[1:]...)
	cmd.Dir = worktree
	cmd.Env = BoundaryGitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return nil, drainmodel.Externalf("run format check %v in %s: %v", decl.Command, worktree, err)
	}
	files := parseUnformattedFiles(stdout.String())
	if len(files) == 0 {
		files = parseUnformattedFiles(stderr.String())
	}
	if len(files) == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = exitErr.Error()
		}
		return []string{msg}, nil
	}
	return files, nil
}

func parseUnformattedFiles(output string) []string {
	seen := make(map[string]struct{})
	var files []string
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "error: recipe ") {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		files = append(files, line)
	}
	sort.Strings(files)
	return files
}

func EligibleFormatContinuation(rec run.Record) bool {
	if rec.State != run.StateExited {
		return false
	}
	if rec.EndCause == run.EndNormalExit {
		return true
	}
	return rec.EndCause == "" && rec.ExitCode != nil && *rec.ExitCode == 0
}

func UnformattedContinuationPrompt(files []string) string {
	return fmt.Sprintf(
		"The repository format check failed on committed files in this worktree: %s. "+
			"Run `just format` (and `just pack-sync` if any `tools/tillr*` source changed so the pack mirror matches), "+
			"amend the commit so the branch has one formatted commit on a clean worktree, and end with RESULT: done.",
		strings.Join(files, ", "),
	)
}

func UnformattedRefusalDetail(files []string) string {
	return fmt.Sprintf("unformatted files (%s); run `just format` before finishing", strings.Join(files, ", "))
}
