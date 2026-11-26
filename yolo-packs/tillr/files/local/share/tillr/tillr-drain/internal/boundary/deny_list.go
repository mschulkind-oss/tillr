package boundary

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr/store"
)

const DenyListPath = "config/tillr/deny-list.json"

type denyEntry struct {
	Path    string
	Content string
	Reason  string
}

type itemDeclaration struct{ id, from, text, path string }

// DenyListRequest carries the inputs needed to check a candidate against the
// repository's committed deny-list without requiring a drain.Loop.
type DenyListRequest struct {
	Env              drainmodel.Env
	TrunkRef         string
	ItemID           string
	Worktree         string
	BaseSHA          string
	HeadSHA          string
	TriageCaptured   bool
	Git              func(ctx context.Context, dir string, args ...string) (string, error)
	RevParse         func(ctx context.Context, dir, rev string) (string, error)
	BlobAt           func(ctx context.Context, worktree, rev, path string) ([]byte, error)
	IdeaIntakeExists func(ctx context.Context, storePath, itemID string) (bool, error)
}

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

func DenyListViolation(ctx context.Context, req DenyListRequest) (string, error) {
	git := req.Git
	if git == nil {
		git = BoundaryGit
	}
	trunkRef := req.TrunkRef
	if trunkRef == "" {
		trunkRef = drainmodel.TrunkRef
	}
	// Policy comes from committed trunk, not the worktree being judged or loose files.
	// Repositories that do not track this policy have no repository deny-list.
	var trunk string
	var err error
	if req.RevParse != nil {
		trunk, err = req.RevParse(ctx, req.Env.Root, "refs/heads/"+trunkRef)
	} else {
		var out string
		out, err = git(ctx, req.Env.Root, "rev-parse", "refs/heads/"+trunkRef)
		trunk = strings.TrimSpace(out)
	}
	if err != nil {
		return "", err
	}
	var body []byte
	if req.BlobAt != nil {
		body, err = req.BlobAt(ctx, req.Env.Root, trunk, DenyListPath)
	} else {
		var out string
		out, err = git(ctx, req.Env.Root, "show", trunk+":"+DenyListPath)
		if err != nil {
			// Repositories that do not track this policy have no repository deny-list.
			return "", nil
		}
		body = []byte(out)
	}
	if err != nil || body == nil {
		return "", err
	}
	var list struct{ Entries []denyEntry }
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("reading %s: %w", DenyListPath, err)
	}
	for _, entry := range list.Entries {
		if entry.Reason == "" || !((entry.Path != "" && entry.Content == "") || (entry.Path == "" && entry.Content == "item-declaration")) {
			return "", fmt.Errorf("invalid entry in %s: %+v", DenyListPath, entry)
		}
	}
	raw, err := git(ctx, req.Worktree, "diff", "--no-renames", "--name-status", "-z", req.BaseSHA, req.HeadSHA, "--")
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
					return fmt.Sprintf("%s: %s matches denied entry %q (%s)", drainmodel.ReasonBoundaryScopeEscape, p, entry.Path, entry.Reason), nil
				}
			}
			continue
		}
		if detail, err := declarationViolation(ctx, req, git, entry, changes); err != nil || detail != "" {
			return detail, err
		}
	}
	return "", nil
}

func declarationViolation(
	ctx context.Context,
	req DenyListRequest,
	git func(ctx context.Context, dir string, args ...string) (string, error),
	entry denyEntry,
	changes []string,
) (string, error) {
	type key struct{ id, text string }
	remaining := make(map[key]int)
	var before, after []itemDeclaration
	for i := 0; i < len(changes); i += 2 {
		status, path := changes[i], changes[i+1]
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			continue
		}
		if status != "A" {
			s, err := git(ctx, req.Worktree, "show", req.BaseSHA+":"+path)
			if err != nil {
				return "", err
			}
			before = append(before, declarationComments(path, s)...)
		}
		if status != "D" {
			s, err := git(ctx, req.Worktree, "show", req.HeadSHA+":"+path)
			if err != nil {
				return "", err
			}
			after = append(after, declarationComments(path, s)...)
		}
	}
	refusal := func(d itemDeclaration, change string) string {
		return fmt.Sprintf("%s: %s: %s of declaration %q matches denied entry %q (%s)", drainmodel.ReasonBoundaryScopeEscape, d.path, change, d.id, entry.Content, entry.Reason)
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
	if req.TriageCaptured && len(added) == 1 && added[0].id == req.ItemID {
		return "", nil // the turn's triage record is captured by the drain
	}
	var hasIntake bool
	if req.IdeaIntakeExists != nil {
		var err error
		hasIntake, err = req.IdeaIntakeExists(ctx, req.Env.StorePath, req.ItemID)
		if err != nil {
			return "", err
		}
	} else {
		var err error
		hasIntake, err = defaultIdeaIntakeExists(ctx, req.Env.StorePath, req.ItemID)
		if err != nil {
			return "", err
		}
	}
	if !hasIntake {
		return refusal(added[0], "addition"), nil
	}
	seen := map[string]bool{}
	paths, err := git(ctx, req.Worktree, "ls-tree", "-r", "--name-only", "-z", req.BaseSHA)
	if err != nil {
		return "", err
	}
	for _, path := range strings.Split(paths, "\x00") {
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			continue
		}
		s, err := git(ctx, req.Worktree, "show", req.BaseSHA+":"+path)
		if err != nil {
			return "", err
		}
		for _, d := range declarationComments(path, s) {
			seen[d.id] = true
		}
	}
	s, err := store.OpenRead(ctx, req.Env.StorePath)
	if err != nil {
		return "", err
	}
	defer s.Close()
	for _, d := range added {
		if d.id == "" || d.from != req.ItemID || seen[d.id] {
			return refusal(d, "addition"), nil
		}
		var exists int
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
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

func defaultIdeaIntakeExists(ctx context.Context, storePath, itemID string) (bool, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var count int
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM idea_intake WHERE item_id=?`, itemID).Scan(&count)
	}); err != nil {
		return false, err
	}
	return count > 0, nil
}
