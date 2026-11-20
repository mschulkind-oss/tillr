// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package item

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr/store"
)

// ReadRoadmapDocuments opens only the named roadmaps and their direct tracked
// Markdown links. It validates every selection before a caller opens a writer.
// Paths are repository-relative; explicit roadmaps need not be named roadmap.md.
func ReadRoadmapDocuments(root string, roadmaps []string) ([]SyncDocument, []string, error) {
	if len(roadmaps) == 0 {
		docs, err := ReadSyncDocuments(root)
		return docs, nil, err
	}
	paths, err := docshare.TrackedPaths(root)
	if err != nil {
		var subdir docshare.SubdirectoryError
		if errors.As(err, &subdir) {
			return nil, nil, Usagef("%s", err)
		}
		return nil, nil, Externalf("%s", err)
	}
	tracked := map[string]bool{}
	for _, p := range paths {
		tracked[p] = true
	}
	selected := []string{}
	seen := map[string]bool{}
	for _, given := range roadmaps {
		p := filepath.ToSlash(filepath.Clean(given))
		if filepath.IsAbs(given) || !strings.HasPrefix(p, "docs/") || !tracked[p] {
			return nil, nil, Usagef("--roadmap %q must name a tracked Markdown file under docs/", given)
		}
		if err := checkSyncFile(root, p); err != nil {
			return nil, nil, Usagef("--roadmap %q must name an existing regular tracked Markdown file under docs/: %v", given, err)
		}
		if !seen[p] {
			selected = append(selected, p)
			seen[p] = true
		}
	}
	docs := []SyncDocument{}
	read := map[string]bool{}
	open := func(p string, required bool) error {
		if read[p] {
			return nil
		}
		if err := checkSyncFile(root, p); err != nil {
			if errors.Is(err, os.ErrNotExist) && !required {
				return nil
			}
			return Usagef("scoped document %q must be a regular tracked file without symlink traversal: %v", p, err)
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return Externalf("reading %s: %v", p, err)
		}
		read[p] = true
		docs = append(docs, SyncDocument{Path: p, Content: content})
		return nil
	}
	for _, p := range selected {
		if err := open(p, true); err != nil {
			return nil, nil, err
		}
	}
	// Copy the selected slice before appending links: never follow a linked document.
	for _, doc := range append([]SyncDocument(nil), docs...) {
		var readErr error
		roadmapLines(doc, func(_ int, text string, _ bool) {
			if readErr != nil {
				return
			}
			for _, m := range linkTarget.FindAllStringSubmatch(text, -1) {
				p := roadmapLink(doc.Path, m[1])
				if tracked[p] {
					if err := open(p, false); err != nil {
						readErr = err
						return
					}
				}
			}
		})
		if readErr != nil {
			return nil, nil, readErr
		}
	}
	return docs, selected, nil
}

// A tracked symlink is not the Markdown it points to; neither an explicit
// roadmap nor a direct link may import blocks from an untracked target.
func checkSyncFile(root, file string) error {
	parts := strings.Split(file, "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return Usagef("%s is a symlink", file)
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return Usagef("%s is not a regular file", file)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return Usagef("%s has a non-directory parent", file)
		}
	}
	return nil
}

func unreadRankRefusal(id string) error {
	return Usagef("narrowed sync cannot change unread item %q's rank; placement needs a sync of every document", id)
}

func allowedDocuments(in SyncInput) map[string]bool {
	if !in.Partial {
		return nil
	}
	allowed := map[string]bool{}
	for _, d := range in.Documents {
		allowed[d.Path] = true
	}
	return allowed
}

// Source and saved-order placement may need to rewrite crowded rank keys. A
// narrowed transaction must refuse that operation rather than rewrite an unread
// item. Returning an error rolls back the caller's entire sync transaction.
func protectUnreadRanks(r *store.Reader, allowed map[string]bool) (func(*store.Reader) error, error) {
	noop := func(*store.Reader) error { return nil }
	if allowed == nil {
		return noop, nil
	}
	ranks := map[string]string{}
	rows, err := r.Query(`SELECT item_id,rank FROM items`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, rank string
		if err := rows.Scan(&id, &rank); err != nil {
			rows.Close()
			return nil, err
		}
		if !allowed[id] {
			ranks[id] = rank
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return func(after *store.Reader) error {
		for id, before := range ranks {
			var rank string
			if err := after.QueryRow(`SELECT rank FROM items WHERE item_id=?`, id).Scan(&rank); err != nil {
				return err
			}
			if rank != before {
				return unreadRankRefusal(id)
			}
		}
		return nil
	}, nil
}

// Partial reports deliberately encode null, not zero, for observations that
// require every document. Keep integer fields for whole-sync Go consumers.
func (report SyncReport) MarshalJSON() ([]byte, error) {
	type alias SyncReport
	if !report.Partial {
		return json.Marshal(alias(report))
	}
	return json.Marshal(struct {
		alias
		UnsharedOpen   *int `json:"unshared_open"`
		StaleDocBlocks *int `json:"stale_doc_blocks"`
	}{alias: alias(report)})
}
