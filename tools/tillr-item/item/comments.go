// Architecture and invariants: docs/reference/tillr-designer-sessions.md
package item

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr/store"
)

// ReviewComment is Vantage's review wire record. Reactions retain all fields so
// staging carries the whole thread rather than projecting away reviewer evidence.
type ReviewComment struct {
	ID        string  `json:"id"`
	Comment   string  `json:"comment"`
	CreatedAt float64 `json:"created_at"`
	EditedAt  float64 `json:"edited_at,omitempty"`
	Resolved  bool    `json:"resolved,omitempty"`
	// BoxOpen is derived from Vantage's comment_sessions, not a persisted comment field.
	BoxOpen bool `json:"-"`
	Anchor  *struct {
		SourceLine int `json:"source_line"`
	} `json:"anchor,omitempty"`
	CapturedBlock   string            `json:"captured_block,omitempty"`
	SelectedText    string            `json:"selected_text,omitempty"`
	QuestionID      string            `json:"question_id,omitempty"`
	RootTextVersion string            `json:"root_text_version,omitempty"`
	Reactions       []json.RawMessage `json:"reactions"`
	Heading         string            `json:"heading"`
}

type CommentBatch struct {
	ItemID   string          `json:"item_id"`
	Document string          `json:"document"`
	Comments []ReviewComment `json:"comments"`
}

func init() {
	if err := store.RegisterDDL("item-comments", `CREATE TABLE IF NOT EXISTS comment_batches (
 item_id TEXT PRIMARY KEY REFERENCES items(item_id), document TEXT NOT NULL, batch TEXT NOT NULL
 );
 CREATE TABLE IF NOT EXISTS comment_taken (
 document TEXT NOT NULL, comment_id TEXT NOT NULL, turns INTEGER NOT NULL, changed REAL NOT NULL,
 PRIMARY KEY(document,comment_id,turns,changed));
 CREATE TABLE IF NOT EXISTS comment_intake (
 id INTEGER PRIMARY KEY CHECK(id=1), first_intake TEXT NOT NULL, last_answer TEXT NOT NULL DEFAULT '', unavailable INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS comment_intake_documents (
 document TEXT PRIMARY KEY, older INTEGER NOT NULL DEFAULT 0);
 `); err != nil {
		panic(err)
	}
}

func IsCommentItem(id string) bool { return docshare.IsCommentItem(id) }

type reviewReaction struct {
	Actor        string  `json:"actor"`
	Kind         string  `json:"kind"`
	Timestamp    float64 `json:"timestamp"`
	EditedAt     float64 `json:"edited_at"`
	AnswersRound *int    `json:"answers_round"`
}

// ReadReview uses the public review interface, never Vantage's private files.
func ReadReview(ctx context.Context, base, repo, doc string) ([]ReviewComment, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, Usagef("--vantage needs an HTTP server URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/r/" + repo + "/review"
	q := u.Query()
	q.Set("path", doc)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, Internalf("reading Vantage review: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, Internalf("Vantage review returned HTTP %d", res.StatusCode)
	}
	var review *struct {
		Comments []ReviewComment `json:"comments"`
		Sessions []struct {
			CommentID string `json:"comment_id"`
			State     string `json:"state"`
		} `json:"comment_sessions"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&review); err != nil {
		return nil, Internalf("reading Vantage review JSON: %v", err)
	}
	if review == nil {
		return nil, nil
	}
	for i := range review.Comments {
		c := &review.Comments[i]
		for _, session := range review.Sessions {
			if session.CommentID == c.ID && session.State == "open" {
				c.BoxOpen = true
			}
		}
		for _, raw := range c.Reactions {
			var parsed reviewReaction
			if err := json.Unmarshal(raw, &parsed); err != nil {
				return nil, Internalf("reading Vantage reaction for %s: %v", c.ID, err)
			}
		}
	}
	return review.Comments, nil
}

// PendingReview mirrors the review protocol's reviewer/agent turn ordering. An
// edited root or reviewer reply after the last answer is pending too.
func PendingReview(c ReviewComment) bool {
	if c.Resolved || strings.TrimSpace(c.Comment) == "" {
		return false
	}
	changed := c.CreatedAt
	if c.EditedAt > changed {
		changed = c.EditedAt
	}
	answered := float64(-1)
	lastReviewerRound := 0
	for i, raw := range c.Reactions {
		var r reviewReaction
		if json.Unmarshal(raw, &r) != nil {
			return false
		}
		if r.Kind == "noted" {
			continue
		}
		if r.Actor == "reviewer" {
			lastReviewerRound = i + 1
			if r.Timestamp > changed {
				changed = r.Timestamp
			}
			if r.EditedAt > changed {
				changed = r.EditedAt
			}
		}
		if r.Actor == "agent" && (r.AnswersRound == nil || *r.AnswersRound >= lastReviewerRound) && r.Timestamp > answered {
			answered = r.Timestamp
		}
	}
	return changed > answered
}

// CommentDocumentName keeps the common flat docs/design name short. Byte escapes
// preserve punctuation and case; _p (not a byte escape) introduces a full-path
// suffix for every other location or extension, so two paths cannot share a desk.
func CommentDocumentName(doc string) string {
	stem := strings.TrimSuffix(filepath.Base(doc), filepath.Ext(doc))
	var name strings.Builder
	for _, b := range []byte(stem) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' {
			name.WriteByte(b)
		} else {
			fmt.Fprintf(&name, "_%02x", b)
		}
	}
	if doc != "docs/design/"+stem+".md" {
		name.WriteString("_p" + hex.EncodeToString([]byte(doc)))
	}
	return name.String()
}

func ReadCommentBatch(r *store.Reader, id string) (*CommentBatch, error) {
	// A read-open of a pre-feature store must remain readable before its next migration.
	var exists bool
	if err := r.QueryRow("SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='comment_batches')").Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	var raw string
	err := r.QueryRow("SELECT batch FROM comment_batches WHERE item_id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var batch CommentBatch
	if err = json.Unmarshal([]byte(raw), &batch); err != nil {
		return nil, err
	}
	return &batch, nil
}

// TakeComments writes a loop-owned batch and its ordinary item in the same transaction.
func TakeComments(ctx context.Context, tx *store.Tx, root, doc string, comments []ReviewComment, at time.Time) (string, error) {
	if !filepath.IsLocal(doc) || filepath.ToSlash(filepath.Clean(doc)) != doc {
		return "", Usagef("--doc needs a clean repository-relative document path")
	}
	raw, err := os.ReadFile(filepath.Join(root, doc))
	if err != nil {
		return "", Usagef("reading document %s: %v", doc, err)
	}
	pending := []ReviewComment{}
	// Older stores already hold batches from hand intake. Seed their identities
	// in the same transaction rather than treating a new ledger as empty history.
	rows, err := tx.Reader().Query("SELECT batch FROM comment_batches WHERE document=?", doc)
	if err != nil {
		return "", err
	}
	prior := []ReviewComment{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return "", err
		}
		var batch CommentBatch
		if err = json.Unmarshal([]byte(raw), &batch); err != nil {
			rows.Close()
			return "", err
		}
		prior = append(prior, batch.Comments...)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for _, c := range prior {
		if _, err = tx.Exec("INSERT INTO comment_taken(document,comment_id,turns,changed) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", doc, c.ID, len(c.Reactions), ReviewChanged(c)); err != nil {
			return "", err
		}
	}
	seen := map[string]bool{}
	lines := strings.Split(string(raw), "\n")
	title := CommentDocumentName(doc)
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			break
		}
	}
	for _, c := range comments {
		if !PendingReview(c) || c.BoxOpen {
			continue
		}
		var taken bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM comment_taken WHERE document=? AND comment_id=? AND turns=? AND changed=?)", doc, c.ID, len(c.Reactions), ReviewChanged(c)).Scan(&taken); err != nil {
			return "", err
		}
		if taken {
			continue
		}
		if c.ID == "" || seen[c.ID] {
			return "", Usagef("Vantage review contains an empty or duplicate comment id")
		}
		seen[c.ID] = true
		if c.Anchor != nil {
			for i, line := range lines {
				if i+1 > c.Anchor.SourceLine {
					break
				}
				if strings.HasPrefix(line, "#") {
					c.Heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
				}
			}
		}
		pending = append(pending, c)
	}
	if len(pending) == 0 {
		return "", nil
	}
	var open string
	err = tx.Reader().QueryRow(`SELECT b.item_id FROM comment_batches b JOIN items i USING(item_id) WHERE b.document=? AND i.state NOT IN ('landed','closed','resolved-elsewhere')`, doc).Scan(&open)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	name := CommentDocumentName(doc)
	if name == "" {
		return "", Usagef("document has no usable name")
	}
	var count int
	if err = tx.Reader().QueryRow("SELECT count(*) FROM comment_batches WHERE document=?", doc).Scan(&count); err != nil {
		return "", err
	}
	id := fmt.Sprintf("item-comments-%s-%d", name, count+1)
	// Keep ordinary ranks unchanged; append a character below the first ordinary rank.
	rank := "0m"
	var first string
	err = tx.Reader().QueryRow("SELECT rank FROM items WHERE rank<>'' AND item_id NOT LIKE 'fixer-main-%' ORDER BY rank LIMIT 1").Scan(&first)
	if err == nil {
		rank = ""
		for i := 0; i < len(first); i++ {
			if first[i] > '0' {
				rank = first[:i] + "0m"
				break
			}
		}
		if rank == "" {
			return "", Usagef("no rank available above %q; move the leading item before taking comments", first)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	fence := []string{doc}
	plan := strings.TrimSuffix(doc, filepath.Ext(doc)) + "-plan" + filepath.Ext(doc)
	if info, e := os.Stat(filepath.Join(root, plan)); e == nil && info.Mode().IsRegular() {
		fence = append(fence, plan)
	}
	err = filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(p) != "roadmap.md" {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(filepath.Dir(p), filepath.Join(root, doc))
		if e != nil {
			return e
		}
		if strings.Contains(string(b), "]("+filepath.ToSlash(rel)) {
			f, e := filepath.Rel(root, p)
			if e != nil {
				return e
			}
			fence = append(fence, filepath.ToSlash(f))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	payload := itemPayload{ItemID: id, Title: fmt.Sprintf("Answer %d review comments on %s", len(pending), title), State: "in-queue", DesignDoc: doc, AllowedPaths: fence, Rank: rank, Seat: SeatDesign, Criteria: []criterion{
		{CriterionID: "c1", Text: "The reply file holds exactly one reply for each comment in the batch, and no other.", State: "not-started"},
		{CriterionID: "c2", Text: "The document passes uvx vantage-check.", State: "not-started"},
		{CriterionID: "c3", Text: "The document reads as if written with the comment's point known from the start: no sentence refers to a reviewer, a comment, or an earlier wording.", State: "not-started"},
	}}
	env, err := envelopeOf(RecordKind, id, at, payload)
	if err != nil {
		return "", err
	}
	if _, err = Apply(ctx, tx, env); err != nil {
		return "", err
	}
	batch, err := json.Marshal(CommentBatch{ItemID: id, Document: doc, Comments: pending})
	if err != nil {
		return "", err
	}
	_, err = tx.Exec("INSERT INTO comment_batches(item_id,document,batch) VALUES(?,?,?)", id, doc, string(batch))
	if err != nil {
		return "", err
	}
	for _, c := range pending {
		if _, err = tx.Exec("INSERT INTO comment_taken(document,comment_id,turns,changed) VALUES(?,?,?,?)", doc, c.ID, len(c.Reactions), ReviewChanged(c)); err != nil {
			return "", err
		}
	}
	return id, nil
}
