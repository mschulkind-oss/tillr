package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
)

func (s *LaneStager) proseRewriteTask(ctx context.Context, id string) (string, error) {
	original := item.ProseRewriteOriginal(id)
	recorded, err := s.readItem(ctx, id)
	if err != nil {
		return "", err
	}
	file, _, _ := strings.Cut(recorded.BodyRef, "#")
	guide, err := os.ReadFile(filepath.Join(s.Root, ".agents/skills/tillr-prose-style/SKILL.md"))
	if err != nil {
		return "", Externalf("reading prose guide for %s: %v", id, err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Root, file))
	if err != nil {
		return "", Externalf("reading prose rewrite source for %s: %v", id, err)
	}
	report, err := item.Sync(ctx, nil, nil, item.SyncInput{Root: s.Root, Documents: []item.SyncDocument{{Path: file, Content: raw}}, Partial: true})
	if err != nil {
		return "", err
	}
	findings := item.ProseRewriteFindings(report)[original]
	text := fmt.Sprintf("Rewrite only the title and section prose for %s in %s. Do not implement the original work. Keep id, paths, blocked_by and seat unchanged. Keep every criterion's meaning and number; add or remove no criterion.\n\n## Prose guide (whole text)\n\n%s\n\nFindings:\n", original, file, guide)
	for _, n := range findings {
		text += fmt.Sprintf("- %s: %s\n", n.Rule, n.Message)
	}
	if len(findings) == 0 {
		text += "The block now passes the prose check; preserve its contract.\n"
	}
	return text, nil
}

func (l *Loop) validateProseRewrite(ctx context.Context, c Candidate, ref string) (string, error) {
	original := item.ProseRewriteOriginal(c.ItemID)
	file, _, _ := strings.Cut(ref, "#")
	if file == "" {
		return "body_ref: rewrite has no source document", nil
	}
	before, err := l.git(ctx, c.Worktree, "show", c.BaseSHA+":"+file)
	if err != nil {
		return "", err
	}
	// A deleted document is a named contract refusal, not a Git invocation fault.
	names, err := l.git(ctx, c.Worktree, "ls-tree", "--name-only", c.HeadSHA, "--", file)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(names) == "" {
		return "id: original document removed", nil
	}
	after, err := l.git(ctx, c.Worktree, "show", c.HeadSHA+":"+file)
	if err != nil {
		return "", err
	}
	return item.ValidateProseRewrite(file, original, []byte(before), []byte(after)), nil
}
