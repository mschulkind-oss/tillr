// Architecture and invariants: docs/reference/observatory-actions.md
package drain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

type ideaIntake struct {
	path, hash, intake string
	size               int
}

func readIdeaIntake(r *store.Reader, id string) (*ideaIntake, error) {
	var exists int
	if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='idea_intake'`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	v := &ideaIntake{}
	err := r.QueryRow(`SELECT words_path,words_hash,words_size,intake_path FROM idea_intake WHERE item_id=?`, id).Scan(&v.path, &v.hash, &v.size, &v.intake)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !filepath.IsLocal(v.path) || filepath.ToSlash(filepath.Clean(v.path)) != v.path {
		return nil, fmt.Errorf("invalid idea words path for %s: %q", id, v.path)
	}
	return v, nil
}

type ideaIntakeResult struct {
	intake *ideaIntake
	err    error
}

func readAllIdeaIntakes(r *store.Reader) (map[string]ideaIntakeResult, error) {
	var exists int
	if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='idea_intake'`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	rows, err := r.Query(`SELECT item_id,words_path,words_hash,words_size,intake_path FROM idea_intake`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ideaIntakeResult{}
	for rows.Next() {
		var id string
		v := &ideaIntake{}
		if err := rows.Scan(&id, &v.path, &v.hash, &v.size, &v.intake); err != nil {
			return nil, err
		}
		if !filepath.IsLocal(v.path) || filepath.ToSlash(filepath.Clean(v.path)) != v.path {
			out[id] = ideaIntakeResult{err: fmt.Errorf("invalid idea words path for %s: %q", id, v.path)}
			continue
		}
		out[id] = ideaIntakeResult{intake: v}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func ideaIntakeFor(ctx context.Context, path, id string) (*ideaIntake, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var v *ideaIntake
	err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; v, err = readIdeaIntake(r, id); return err })
	return v, err
}

func (s *LaneStager) ideaTask(ctx context.Context, id string) (string, []byte, error) {
	v, err := ideaIntakeFor(ctx, s.Store, id)
	if err != nil || v == nil {
		return "", nil, err
	}
	raw, err := os.ReadFile(v.intake)
	if err != nil {
		return "", nil, Externalf("reading saved idea %s: %v", id, err)
	}
	if len(raw) != v.size || fmt.Sprintf("%x", sha256.Sum256(raw)) != v.hash {
		return "", nil, Externalf("saved idea %s differs from intake hash", id)
	}
	guide, err := os.ReadFile(filepath.Join(s.Root, ".agents/skills/tillr-prose-style/SKILL.md"))
	if err != nil {
		return "", nil, Externalf("reading prose guide for %s: %v", id, err)
	}
	return fmt.Sprintf(`Design this saved idea; do not implement the work it asks for.

Words path: %s

1. Copy the words into the tree at the words path, unchanged.
2. Decide what the words ask for, and write it as item blocks. Each block has a title, a paragraph that leads with why, criteria, and from="%s".
3. Write a design document, in the house form, when the work needs decisions recorded. Route it from a roadmap.
4. File every decision that is the owner's as an open question in that document. Do not wait and do not guess: a vague idea becomes a short document that holds only its questions.

When the owner must answer, put one design-seat item block in the document's closing section to settle the document and cut it into build items. It must carry from="%s", so it stands in this idea's place and waits on that document's questions. Do not rename or retitle this idea's own item: the idea lands once, and its outcomes are new items.

Prose guide (whole text):

%s

The owner's words follow at the very end of this brief in a byte-counted section. Every byte after that section's heading, through the end of the file, is the saved text. Copy those bytes exactly, including line endings and any literal template braces.`, v.path, id, id, guide), raw, nil
}

// Validate pinned blobs, never loose worktree files. Existing source-marked items
// (including a moved block) are not work produced by this turn.
func (l *Loop) validateIdea(ctx context.Context, c Candidate) (string, error) {
	v, err := ideaIntakeFor(ctx, l.cfg.Store, c.ItemID)
	if err != nil || v == nil {
		return "", err
	}
	listing, err := l.git(ctx, c.Worktree, "ls-tree", "-z", c.HeadSHA, "--", v.path)
	if err != nil {
		return "", err
	}
	if listing == "" {
		return RefusalWordsMissing + ": " + v.path, nil
	}
	if !strings.HasPrefix(listing, "100644 blob ") && !strings.HasPrefix(listing, "100755 blob ") {
		return RefusalWordsAltered + ": words path is not a regular file: " + v.path, nil
	}
	raw, err := l.git(ctx, c.Worktree, "show", c.HeadSHA+":"+v.path)
	if err != nil {
		return "", err
	}
	if len(raw) != v.size || fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) != v.hash {
		return RefusalWordsAltered + ": " + v.path, nil
	}
	before, err := l.ideaBlocksAt(ctx, c, c.BaseSHA, "")
	if err != nil {
		return "", err
	}
	// Private requests may already exist without a declaration in the base.
	// They too are old identities, not new items produced by this turn.
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id FROM items`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			before[id] = true
		}
		return rows.Err()
	}); err != nil {
		return "", err
	}
	after, err := l.ideaBlocksAt(ctx, c, c.HeadSHA, c.ItemID)
	if err != nil {
		return "", err
	}
	for id := range after {
		if id != c.ItemID && !before[id] {
			return "", nil
		}
	}
	return RefusalIdeaNoOutcome + ": no new item block carries from=" + c.ItemID, nil
}

func (l *Loop) ideaBlocksAt(ctx context.Context, c Candidate, sha, from string) (map[string]bool, error) {
	names, err := l.git(ctx, c.Worktree, "ls-tree", "-r", "--name-only", "-z", sha, "--", "docs/")
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, name := range strings.Split(names, "\x00") {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		raw, err := l.git(ctx, c.Worktree, "show", sha+":"+name)
		if err != nil {
			return nil, err
		}
		found := item.BlocksFrom(name, from, []byte(raw))
		if from == "" {
			found = item.DefinitionBlockIDs(name, []byte(raw))
		}
		for _, id := range found {
			ids[id] = true
		}
	}
	return ids, nil
}
