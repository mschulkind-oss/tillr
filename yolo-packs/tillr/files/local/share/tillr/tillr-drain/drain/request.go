package drain

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// A filed request (`tillr item request`) is an item whose whole statement is the words the
// store holds for it: it has no document block, and its work must not give it one. Staging
// hands every node and the judge those words verbatim, and the candidate check below
// refuses a change that adds, edits or removes an item block.

// RequestBlockRefusal prefixes the refusal detail of a request candidate that touched an
// item block. The refusal is a fence refusal, so it returns to the same session.
const RequestBlockRefusal = "request-item-block"

// requestWordsHeading is the byte-counted suffix the adapter's preflight excludes from its
// template checks (backends/preflight.sh), the same form a saved idea's words use.
func requestWordsSuffix(words []byte) string {
	return fmt.Sprintf("\n## Saved request words (verbatim; %d bytes)\n%s", len(words), words)
}

// requestWordsFor answers a filed request's words, or nil for any other item. It opens the
// store read-only: staging writes no store row.
func requestWordsFor(ctx context.Context, path, id string) ([]byte, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var words []byte
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		words, err = item.RequestWords(r, id)
		return err
	})
	return words, err
}

// requestTask is a request's statement of the work in its brief.
func requestTask(recorded stagedItem) string {
	return recorded.Title + `

This item was filed from the command line as a request: it has no document block, and the owner's words are its whole statement. They follow at the very end of this brief in a byte-counted section; every byte after that section's heading, through the end of the file, is the saved text. Do what they ask — change what the work needs, and nothing else — on this node's part of the workflow.

Do not add, edit or remove a ` + "`<!-- tillr: item … -->`" + ` comment in any document, and do not depend on one: this item finishes without a block, and the loop refuses a candidate that touches one. Where the words leave a decision only the owner can make, do not guess: end blocked, naming the owner, with the question and its options.`
}

// validateRequest refuses a request candidate whose change adds, removes or edits an item
// block in any Markdown file it changes. It reads pinned blobs, never the lane's loose files.
func (l *Loop) validateRequest(ctx context.Context, c Candidate) (string, error) {
	words, err := requestWordsFor(ctx, l.cfg.Store, c.ItemID)
	if err != nil || words == nil {
		return "", err
	}
	changed, err := l.git(ctx, c.Worktree, "diff", "--no-renames", "--name-only", "-z", c.BaseSHA+".."+c.HeadSHA)
	if err != nil {
		return "", err
	}
	for _, name := range strings.Split(changed, "\x00") {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		before, err := l.blobAt(ctx, c, c.BaseSHA, name)
		if err != nil {
			return "", err
		}
		after, err := l.blobAt(ctx, c, c.HeadSHA, name)
		if err != nil {
			return "", err
		}
		was, is := item.DefinitionBlockIDs(name, before), item.DefinitionBlockIDs(name, after)
		if !slices.Equal(was, is) {
			return fmt.Sprintf("%s: %s: the change moves item blocks from %v to %v; a request finishes with no block and changes none", RequestBlockRefusal, name, was, is), nil
		}
		for _, id := range was {
			ref := name + "#" + id
			if item.DefinitionBodyContent(ref, id, before) != item.DefinitionBodyContent(ref, id, after) {
				return fmt.Sprintf("%s: %s: the change edits item block %s; a request finishes with no block and changes none", RequestBlockRefusal, name, id), nil
			}
		}
	}
	return "", nil
}

// blobAt answers a file's bytes at a commit, or nil when the commit has no such file.
func (l *Loop) blobAt(ctx context.Context, c Candidate, sha, name string) ([]byte, error) {
	listing, err := l.git(ctx, c.Worktree, "ls-tree", "-z", sha, "--", name)
	if err != nil || listing == "" {
		return nil, err
	}
	raw, err := l.git(ctx, c.Worktree, "show", sha+":"+name)
	return []byte(raw), err
}
