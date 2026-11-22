package drain

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
)

// candidateSize holds a measurement and the effective thresholds at the refusal,
// not a split signal or a verdict.
type candidateSize struct {
	Files          int64 `json:"files"`
	AddedLines     int64 `json:"added_lines"`
	FileLimit      int64 `json:"file_limit"`
	AddedLineLimit int64 `json:"added_line_limit"`
}

func (l *Loop) measureCandidateSize(ctx context.Context, c Candidate) (candidateSize, error) {
	if c.Worktree == "" || c.BaseSHA == "" || c.HeadSHA == "" {
		return candidateSize{}, fmt.Errorf("candidate size needs a recorded worktree, base and head")
	}
	args := []string{"diff", "--numstat", "-z", "--find-renames", "--no-ext-diff", "--no-textconv",
		"--no-relative", "--ignore-submodules=none", "--end-of-options", c.BaseSHA + "^{commit}", c.HeadSHA + "^{commit}", "--"}
	if c.BaseRef == "" {
		// Ordinary lane worktrees belong to this repository. The same path in
		// a fork checkout is not our generated mirror and must be counted.
		args = append(args, ":(top,exclude)yolo-packs/tillr/files/local/share/tillr/")
	}
	out, err := l.git(ctx, c.Worktree, args...)
	if err != nil {
		return candidateSize{}, err
	}
	limits := settings.DefaultCandidateSizeLimits()
	if l.cfg.CandidateSizeLimits != nil {
		limits = *l.cfg.CandidateSizeLimits
	}
	size := candidateSize{FileLimit: limits.FileLimit, AddedLineLimit: limits.AddedLineLimit}
	for out != "" {
		var entry string
		var ok bool
		entry, out, ok = strings.Cut(out, "\x00")
		added, rest, hasAdded := strings.Cut(entry, "\t")
		_, path, hasPath := strings.Cut(rest, "\t")
		if !ok || !hasAdded || !hasPath {
			return candidateSize{}, fmt.Errorf("candidate size: incomplete git numstat entry")
		}
		if path == "" {
			// Git emits an extra NUL before the old and new paths of a rename.
			// Neither path contributes another file entry.
			for range 2 {
				path, out, ok = strings.Cut(out, "\x00")
				if !ok || path == "" {
					return candidateSize{}, fmt.Errorf("candidate size: incomplete git numstat rename")
				}
			}
		}
		size.Files++
		if added != "-" { // binary files count as files, with no reported lines
			n, err := strconv.ParseInt(added, 10, 64)
			if err != nil || n < 0 {
				return candidateSize{}, fmt.Errorf("candidate size: invalid git numstat addition count %q", added)
			}
			size.AddedLines += n
		}
	}
	return size, nil
}
