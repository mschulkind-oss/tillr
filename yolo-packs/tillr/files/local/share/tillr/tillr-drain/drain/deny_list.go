package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr/store"
)

const denyListPath = "config/tillr/deny-list.json"

type denyEntry struct {
	Path    string
	Content string
	Reason  string
}

type itemDeclaration struct{ id, from, text, path string }

// declarationComments follows the document reader: a declaration begins on a line
// outside a code fence and may continue onto later lines. Identity and comment bytes
// (not file or position) are the comparison key.
func declarationComments(path, content string) []itemDeclaration {
	var fence docshare.Fence
	var out []itemDeclaration
	var comment string
	for _, line := range strings.SplitAfter(content, "\n") {
		if comment == "" {
			if fence.Step(line) || !docshare.Start.MatchString(strings.TrimLeft(line, " \t")) {
				continue
			}
			line = line[strings.Index(line, "<!--"):]
		}
		comment += line
		end := strings.Index(comment, "-->")
		if end < 0 {
			continue
		}
		text := comment[:end+3]
		body := strings.TrimSpace(comment[4:end])
		body = strings.TrimSpace(strings.TrimPrefix(body, "tillr:"))
		attrs, _ := docshare.Attributes(strings.TrimPrefix(body, "item"))
		out = append(out, itemDeclaration{attrs["id"], attrs["from"], text, path})
		comment = ""
	}
	return out
}

func (l *Loop) denyListViolation(ctx context.Context, c Candidate) (string, error) {
	// Policy comes from committed trunk, not the worktree being judged or loose files.
	// Repositories that do not track this policy have no repository deny-list.
	trunk, err := l.revParse(ctx, l.cfg.Root, "refs/heads/"+TrunkRef)
	if err != nil {
		return "", err
	}
	body, err := l.blobAt(ctx, Candidate{Worktree: l.cfg.Root}, trunk, denyListPath)
	if err != nil || body == nil {
		return "", err
	}
	var list struct{ Entries []denyEntry }
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("reading %s: %w", denyListPath, err)
	}
	for _, entry := range list.Entries {
		if entry.Reason == "" || !((entry.Path != "" && entry.Content == "") || (entry.Path == "" && entry.Content == "item-declaration")) {
			return "", fmt.Errorf("invalid entry in %s: %+v", denyListPath, entry)
		}
	}
	raw, err := l.git(ctx, c.Worktree, "diff", "--no-renames", "--name-status", "-z", c.BaseSHA, c.HeadSHA, "--")
	if err != nil {
		return "", err
	}
	changes := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	if raw == "" {
		return "", nil
	}
	if len(changes)%2 != 0 {
		return "", fmt.Errorf("unexpected Git changed paths: %q", raw)
	}
	for _, entry := range list.Entries {
		if entry.Path != "" {
			for i := 1; i < len(changes); i += 2 {
				p := changes[i]
				if p == entry.Path || strings.HasPrefix(p, entry.Path+"/") {
					return fmt.Sprintf("%s: %s matches denied entry %q (%s)", ScopeEscape, p, entry.Path, entry.Reason), nil
				}
			}
			continue
		}
		if detail, err := l.declarationViolation(ctx, c, entry, changes); err != nil || detail != "" {
			return detail, err
		}
	}
	return "", nil
}

func (l *Loop) declarationViolation(ctx context.Context, c Candidate, entry denyEntry, changes []string) (string, error) {
	type key struct{ id, text string }
	remaining := make(map[key]int)
	var before, after []itemDeclaration
	for i := 0; i < len(changes); i += 2 {
		status, path := changes[i], changes[i+1]
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			continue
		}
		if status != "A" {
			s, err := l.git(ctx, c.Worktree, "show", c.BaseSHA+":"+path)
			if err != nil {
				return "", err
			}
			before = append(before, declarationComments(path, s)...)
		}
		if status != "D" {
			s, err := l.git(ctx, c.Worktree, "show", c.HeadSHA+":"+path)
			if err != nil {
				return "", err
			}
			after = append(after, declarationComments(path, s)...)
		}
	}
	refusal := func(d itemDeclaration, change string) string {
		return fmt.Sprintf("%s: %s: %s of declaration %q matches denied entry %q (%s)", ScopeEscape, d.path, change, d.id, entry.Content, entry.Reason)
	}
	for _, d := range after {
		remaining[key{d.id, d.text}]++
	}
	for _, d := range before {
		k := key{d.id, d.text}
		if remaining[k] == 0 {
			return refusal(d, "edit or removal"), nil
		}
		remaining[k]--
	}
	var added []itemDeclaration
	for _, d := range after {
		k := key{d.id, d.text}
		if remaining[k] > 0 {
			added = append(added, d)
			remaining[k]--
		}
	}
	if len(added) == 0 {
		return "", nil
	}
	if c.BlockConflict != nil && c.BlockConflict.TriageVersion != "" && len(added) == 1 && added[0].id == c.ItemID {
		return "", nil // the turn's triage record is captured by the drain
	}
	intake, err := ideaIntakeFor(ctx, l.cfg.Store, c.ItemID)
	if err != nil {
		return "", err
	}
	if intake == nil {
		return refusal(added[0], "addition"), nil
	}
	seen := map[string]bool{}
	paths, err := l.git(ctx, c.Worktree, "ls-tree", "-r", "--name-only", "-z", c.BaseSHA)
	if err != nil {
		return "", err
	}
	for _, path := range strings.Split(paths, "\x00") {
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			continue
		}
		s, err := l.git(ctx, c.Worktree, "show", c.BaseSHA+":"+path)
		if err != nil {
			return "", err
		}
		for _, d := range declarationComments(path, s) {
			seen[d.id] = true
		}
	}
	for _, d := range added {
		if d.id == "" || d.from != c.ItemID || seen[d.id] {
			return refusal(d, "addition"), nil
		}
		var exists int
		if err := l.readStore(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM items WHERE item_id=?`, d.id).Scan(&exists)
		}); err != nil {
			return "", err
		}
		if exists != 0 {
			return refusal(d, "addition"), nil
		}
		seen[d.id] = true
	}
	return "", nil
}
