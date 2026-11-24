package fixer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

const (
	// Utility names the tillr utility recorded on drain events.
	Utility = "drain"
	// TrunkRef is the trunk branch name.
	TrunkRef = "main"
	// TrunkBranch is an alias for TrunkRef.
	TrunkBranch = TrunkRef
	// TrunkFixerPrefix is the item ID prefix for generated trunk fixers.
	TrunkFixerPrefix = "fixer-main-"
	// StateResolvedElsewhere is the item state for fixers resolved by a trunk proof.
	StateResolvedElsewhere = "resolved-elsewhere"
	// FixerAttachedKind is the event kind for attaching a new red tip to an unresolved repair.
	// Registered in drain/events.go.
	FixerAttachedKind = "drain.fixer-attached"

	dispatchStartedKind = "drain.started"
	examinationDoneKind = "drain.examined"
)

// ClassifyCheck is the check-result classifier used by fixer.
// Drain tests override it via site_classify_test.go's withClassifier.
var ClassifyCheck = checkresult.Classify

// CodeError is a structured error carrying a record.Exit* code and message.
// It intentionally defines no ExitCode() method (classification scan rule).
type CodeError struct {
	Code int
	Msg  string
}

func (e *CodeError) Error() string { return e.Msg }

func usagef(format string, args ...any) error {
	return &CodeError{Code: record.ExitUsage, Msg: fmt.Sprintf(format, args...)}
}

func externalf(format string, args ...any) error {
	return &CodeError{Code: record.ExitExternal, Msg: fmt.Sprintf(format, args...)}
}

// IsTrunkFixer answers whether an item is a trunk fixer.
func IsTrunkFixer(itemID string) bool {
	return strings.HasPrefix(itemID, TrunkFixerPrefix)
}

// ReadsCoverPath answers whether any declared read path covers path.
func ReadsCoverPath(reads []string, path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
	for _, r := range reads {
		r = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(r)), "./")
		if r == "." || r == "" || path == r || strings.HasPrefix(path, r+"/") {
			return true
		}
	}
	return false
}

// BoundaryGitEnv returns this process's environment without GIT_DIR or GIT_WORK_TREE.
func BoundaryGitEnv() []string {
	kept := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// RawGit runs git in dir and answers its stdout.
func RawGit(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = BoundaryGitEnv()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", externalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// PublishHeavyJSON atomically writes JSON value to dir/name.
func PublishHeavyJSON(dir, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".heavy-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(raw, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, name))
}

// HeavyProgress mirrors heavy-progress.json for reading tip progress proofs.
type HeavyProgress struct {
	TipSHA   string                      `json:"tip_sha"`
	Checks   map[string]heavy.HeavyCheck `json:"checks"`
	PassedAt map[string]time.Time        `json:"passed_at,omitempty"`
}
