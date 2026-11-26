package boundary

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
)

// CandidateSize holds a measurement and the effective thresholds at the refusal,
// not a split signal or a verdict.
type CandidateSize struct {
	Files          int64 `json:"files"`
	AddedLines     int64 `json:"added_lines"`
	FileLimit      int64 `json:"file_limit"`
	AddedLineLimit int64 `json:"added_line_limit"`
}

func MeasureCandidateSize(
	ctx context.Context,
	worktree, baseSHA, headSHA, baseRef string,
	configuredLimits *settings.CandidateSizeLimits,
	git func(ctx context.Context, dir string, args ...string) (string, error),
) (CandidateSize, error) {
	if worktree == "" || baseSHA == "" || headSHA == "" {
		return CandidateSize{}, fmt.Errorf("candidate size needs a recorded worktree, base and head")
	}
	if git == nil {
		git = BoundaryGit
	}
	args := []string{"diff", "--numstat", "-z", "--find-renames", "--no-ext-diff", "--no-textconv",
		"--no-relative", "--ignore-submodules=none", "--end-of-options", baseSHA + "^{commit}", headSHA + "^{commit}", "--"}
	if baseRef == "" {
		// Ordinary lane worktrees belong to this repository. The same path in
		// a fork checkout is not our generated mirror and must be counted.
		args = append(args, ":(top,exclude)yolo-packs/tillr/files/local/share/tillr/")
	}
	out, err := git(ctx, worktree, args...)
	if err != nil {
		return CandidateSize{}, err
	}
	limits := settings.DefaultCandidateSizeLimits()
	if configuredLimits != nil {
		limits = *configuredLimits
	}
	size := CandidateSize{FileLimit: limits.FileLimit, AddedLineLimit: limits.AddedLineLimit}
	for out != "" {
		var entry string
		var ok bool
		entry, out, ok = strings.Cut(out, "\x00")
		added, rest, hasAdded := strings.Cut(entry, "\t")
		_, path, hasPath := strings.Cut(rest, "\t")
		if !ok || !hasAdded || !hasPath {
			return CandidateSize{}, fmt.Errorf("candidate size: incomplete git numstat entry")
		}
		if path == "" {
			// Git emits an extra NUL before the old and new paths of a rename.
			// Neither path contributes another file entry.
			for range 2 {
				path, out, ok = strings.Cut(out, "\x00")
				if !ok || path == "" {
					return CandidateSize{}, fmt.Errorf("candidate size: incomplete git numstat rename")
				}
			}
		}
		size.Files++
		if added != "-" { // binary files count as files, with no reported lines
			n, err := strconv.ParseInt(added, 10, 64)
			if err != nil || n < 0 {
				return CandidateSize{}, fmt.Errorf("candidate size: invalid git numstat addition count %q", added)
			}
			size.AddedLines += n
		}
	}
	return size, nil
}
