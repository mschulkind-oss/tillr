// Architecture and invariants: docs/reference/tillr-designer-sessions.md
package drain

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const commentRepliesFile = "replies.jsonl"

type lastDesigner struct {
	Document             string `json:"document"`
	SessionID            string `json:"session_id"`
	Backend              string `json:"backend"`
	Model                string `json:"model"`
	Desk                 string `json:"desk"`
	RunID                string `json:"run_id"`
	LastRequest          string `json:"last_request"`
	CacheLifetimeSeconds *int   `json:"cache_lifetime_seconds"`
	ContextTokens        *int   `json:"context_tokens"`
	BlobID               string `json:"blob_id"`
	Compacted            bool   `json:"compacted"`
	Transcript           string `json:"transcript"`
}

type commentReply struct {
	Path            string `json:"path,omitempty"`
	ID              string `json:"id"`
	Round           int    `json:"round"`
	Summary         string `json:"summary"`
	Nonce           string `json:"nonce,omitempty"`
	RootTextVersion string `json:"root_text_version,omitempty"`
}

func init() {
	if err := store.RegisterDDL("drain-comments", `CREATE TABLE IF NOT EXISTS last_designers (
 document TEXT PRIMARY KEY, record TEXT NOT NULL
 );
 CREATE TABLE IF NOT EXISTS comment_runs (
 run_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, session_id TEXT NOT NULL,
 reason TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT '', delivered INTEGER NOT NULL DEFAULT 0
 );
 CREATE TABLE IF NOT EXISTS comment_deliveries (
 run_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, document TEXT NOT NULL,
 blob_id TEXT NOT NULL, body TEXT NOT NULL, written INTEGER NOT NULL DEFAULT 0
 );
 CREATE TABLE IF NOT EXISTS comment_arrivals (
 item_id TEXT NOT NULL, comment_id TEXT NOT NULL, session_id TEXT NOT NULL,
 reason TEXT NOT NULL, cache_lifetime_seconds INTEGER, seconds_since_request REAL,
 PRIMARY KEY(item_id,comment_id)
 );
 CREATE TABLE IF NOT EXISTS comment_reply_states (
 run_id TEXT NOT NULL, comment_id TEXT NOT NULL, state TEXT NOT NULL,
 PRIMARY KEY(run_id,comment_id)
 );
 CREATE TABLE IF NOT EXISTS comment_confirmation (
 id INTEGER PRIMARY KEY CHECK(id=1), configured INTEGER NOT NULL
 );`); err != nil {
		panic(err)
	}
}

func readDesigner(r *store.Reader, doc string) (*lastDesigner, error) {
	var exists bool
	if err := r.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='last_designers')").Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	var raw string
	err := r.QueryRow("SELECT record FROM last_designers WHERE document=?", doc).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d lastDesigner
	if err = json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func itemIsComment(id string) bool  { return item.IsCommentItem(id) }
func commentDesk(doc string) string { return "desk-" + item.CommentDocumentName(doc) }

// deskDocument requires a literal Markdown file in the fence, not a directory
// that could contain many designs. Hand-taken comment batches keep their desk
// even when automatic intake is disabled.
func deskDocument(row itemRow, comments *settings.Comments) string {
	if item.IsCommentItem(row.ItemID) {
		return row.DesignDoc
	}
	if row.Seat != "design" || comments == nil {
		return ""
	}
	docs := map[string]bool{}
	for _, allowed := range row.Allowed {
		doc := filepath.ToSlash(filepath.Clean(allowed))
		if filepath.IsAbs(doc) || doc == ".." || strings.HasPrefix(doc, "../") || strings.ContainsAny(doc, "*?[") || strings.HasSuffix(allowed, "/") || filepath.Ext(doc) != ".md" {
			continue
		}
		for _, configured := range comments.Paths {
			prefix := filepath.ToSlash(filepath.Clean(configured))
			if doc == prefix || strings.HasPrefix(doc, prefix+"/") {
				docs[doc] = true
			}
		}
	}
	if len(docs) == 1 {
		for doc := range docs {
			return doc
		}
	}
	return ""
}

func readDeskOwners(r *store.Reader, held map[string]bool) (map[string]string, error) {
	owners := map[string]string{}
	for lane, busy := range held {
		if !busy || !strings.HasPrefix(lane, "desk-") {
			continue
		}
		var raw []byte
		err := r.QueryRow("SELECT payload FROM events WHERE kind=? AND object_type='lane' AND object_id=? ORDER BY seq DESC LIMIT 1", run.AllocateKind, lane).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var allocation struct {
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal(raw, &allocation); err != nil {
			return nil, err
		}
		owners[lane] = allocation.ItemID
	}
	return owners, nil
}

func documentDesk(lane string) bool {
	return strings.HasPrefix(lane, "desk-") && lane != "desk-trunk-fixer"
}

func countDocumentDesks(held, assigned map[string]bool) int64 {
	busy := map[string]bool{}
	for lane, held := range held {
		if held && documentDesk(lane) {
			busy[lane] = true
		}
	}
	for lane := range assigned {
		if documentDesk(lane) {
			busy[lane] = true
		}
	}
	return int64(len(busy))
}

func (l *Loop) commentBatch(ctx context.Context, id string) (*item.CommentBatch, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var batch *item.CommentBatch
	err = s.ReadTx(ctx, func(r *store.Reader) error { var e error; batch, e = item.ReadCommentBatch(r, id); return e })
	return batch, err
}

func commentThreadTask(batch *item.CommentBatch, moved, transcript, durable string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Answer the review comments on %s.\n\n", batch.Document)
	for _, c := range batch.Comments {
		passage := c.CapturedBlock
		if passage == "" {
			passage = c.SelectedText
		}
		fmt.Fprintf(&b, "Comment id: %s\nTurn count (Vantage round): %d\nHeading: %s\nPassage:\n%s\nQuestion id: %s\nThread:\nReviewer: %s\n", c.ID, len(c.Reactions), c.Heading, passage, c.QuestionID, c.Comment)
		for _, r := range c.Reactions {
			fmt.Fprintf(&b, "%s\n", r)
		}
		b.WriteString("\n")
	}
	b.WriteString("Revise the document so a first reader never has the question.\nA comment that rules on an open question is recorded by the answering protocol.\nA comment that only asks gets a reply and settles nothing.\nNever copy the thread into the document.\n\n")
	fmt.Fprintf(&b, "What moved: %s\n", moved)
	if transcript != "" {
		fmt.Fprintf(&b, "Previous session transcript (optional reading): %s\n", transcript)
	}
	fmt.Fprintf(&b, "Write one commit for a revision, and exactly one JSON line per comment to %s. Each line carries id, round (the turn count above), and summary: one or two sentences answering the comment and naming the section changed. If the document already answers it, write the replies without a commit and end RESULT: done.\n", filepath.Join(durable, commentRepliesFile))
	return b.String()
}

func (s *LaneStager) commentTask(ctx context.Context, spec StageSpec, durable string) (string, error) {
	st, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return "", err
	}
	defer st.Close()
	var batch *item.CommentBatch
	var d *lastDesigner
	err = st.ReadTx(ctx, func(r *store.Reader) error {
		var e error
		batch, e = item.ReadCommentBatch(r, spec.ItemID)
		if e != nil || batch == nil {
			return e
		}
		d, e = readDesigner(r, batch.Document)
		return e
	})
	if err != nil || batch == nil {
		return "", err
	}
	moved, transcript := "new session; read the document", ""
	if d != nil {
		transcript = d.Transcript
		if spec.SessionID == d.SessionID && d.BlobID == "" {
			moved = "no recorded document blob; read the document"
		} else if spec.SessionID == d.SessionID {
			old, e := s.git(ctx, s.Root, "cat-file", "blob", d.BlobID)
			if e != nil {
				return "", e
			}
			current, e := s.git(ctx, s.Root, "show", spec.BaseSHA+":"+batch.Document)
			if e != nil {
				return "", e
			}
			if old == current {
				moved = "unchanged"
			} else {
				diff, e := s.git(ctx, s.Root, "diff", d.BlobID, spec.BaseSHA+":"+batch.Document)
				if e != nil {
					return "", e
				}
				if strings.Count(diff, "\n") < 400 {
					moved = "\n" + diff
				} else {
					moved = "the document was rewritten; read it again"
				}
			}
			if d.Compacted {
				moved += "\nYour earlier conversation was summarized."
			}
		}
	}
	return commentThreadTask(batch, moved, transcript, durable), nil
}

func (l *Loop) stageComment(ctx context.Context, d *Decision, spec *StageSpec) error {
	if !item.IsCommentItem(d.ItemID) && d.DeskDocument == "" {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec("INSERT INTO lanes(lane_id,state) VALUES(?,?) ON CONFLICT(lane_id) DO NOTHING", d.LaneID, run.LaneStateIdle); err != nil {
			return err
		}
		session, err := sessionIDFor(d.DesignerSessionID)
		if err != nil {
			return err
		}
		spec.SessionID = session
		d.DesignerSessionID = session
		if !item.IsCommentItem(d.ItemID) {
			return nil
		}
		if _, err = tx.Exec("INSERT INTO comment_runs(run_id,item_id,session_id,reason) VALUES(?,?,?,?) ON CONFLICT(run_id) DO NOTHING", d.RunRequest.RunID, d.ItemID, session, d.DesignerReason); err != nil {
			return err
		}
		return recordCommentArrivals(tx, *d)
	})
}

// recordDesigner reads only settled messages, never partial usage snapshots. Absent
// provider measurements stay NULL; neither the wall clock nor settings invent them.
func (l *Loop) recordDesigner(ctx context.Context, decision Decision, rec run.Record) error {
	doc := decision.DeskDocument
	if item.IsCommentItem(rec.ItemID) {
		batch, err := l.commentBatch(ctx, rec.ItemID)
		if err != nil {
			return err
		}
		if batch == nil {
			return Internalf("comment item %s has no batch", rec.ItemID)
		}
		doc = batch.Document
	}
	if doc == "" {
		return nil
	}
	session, err := ReadSessionID(l.laneDirPath(rec.LaneID))
	if err != nil {
		return err
	}
	d := lastDesigner{Document: doc, SessionID: session, Backend: rec.Backend, Model: rec.Route, Desk: rec.LaneID, RunID: rec.RunID, Compacted: decision.DesignerReason == "resumed-compacted"}
	if d.Model == "" && decision.RunRequest != nil {
		d.Model = decision.RunRequest.Route()
	}
	for _, ref := range rec.ArtifactRefs {
		if ref.Kind != "log" {
			continue
		}
		d.Transcript = ref.Ref
		f, e := os.Open(ref.Ref)
		if e != nil {
			return e
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 64<<10), 16<<20)
		for scan.Scan() {
			var event struct {
				Type       string `json:"type"`
				ID         string `json:"id"`
				Transcript string `json:"transcript"`
				Message    struct {
					Role      string `json:"role"`
					Timestamp int64  `json:"timestamp"`
					Model     string `json:"model"`
					Usage     struct {
						Input    int  `json:"input"`
						Output   int  `json:"output"`
						Read     int  `json:"cacheRead"`
						Write    int  `json:"cacheWrite"`
						Write1h  *int `json:"cacheWrite1h"`
						Lifetime *int `json:"cacheLifetimeSeconds"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(scan.Bytes(), &event) != nil {
				continue
			}
			if event.Type == "session" {
				if event.ID != "" {
					d.SessionID = event.ID
				}
				if event.Transcript != "" {
					d.Transcript = event.Transcript
				}
			}
			if event.Type == "auto_compaction_end" {
				d.Compacted = true
			}
			if event.Type != "turn_end" || event.Message.Role != "assistant" {
				continue
			}
			m := event.Message
			if m.Timestamp > 0 {
				d.LastRequest = time.UnixMilli(m.Timestamp).UTC().Format(time.RFC3339Nano)
			}
			// Compare policy routes to policy routes at dispatch: streams may name only
			// the physical model id, not the provider-qualified route that selected it.
			if d.Model == "" && m.Model != "" {
				d.Model = m.Model
			}
			tokens := m.Usage.Input + m.Usage.Read + m.Usage.Write + m.Usage.Output
			d.ContextTokens = &tokens
			d.CacheLifetimeSeconds = m.Usage.Lifetime
			// Anthropic's reported one-hour cache-creation split identifies the retention
			// of the most recent cache write; a cache-read alone does not report its lifetime.
			if d.CacheLifetimeSeconds == nil && m.Usage.Write > 0 && m.Usage.Write1h != nil {
				lifetime := 300
				if *m.Usage.Write1h > 0 {
					lifetime = 3600
				}
				d.CacheLifetimeSeconds = &lifetime
			}
		}
		e = scan.Err()
		f.Close()
		if e != nil {
			return e
		}
	}
	// A failed writing run can end before it creates its document. It still
	// becomes the last designer; an absent blob remains unknown.
	blob, err := l.git(ctx, l.laneTarget(rec.LaneID), "ls-tree", "--format=%(objectname)", "HEAD", "--", ":(literal)"+doc)
	if err != nil {
		return err
	}
	d.BlobID = strings.TrimSpace(blob)
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		previous, err := readDesigner(tx.Reader(), doc)
		if err != nil {
			return err
		}
		if previous != nil && previous.SessionID == d.SessionID {
			d.Compacted = d.Compacted || previous.Compacted
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO last_designers(document,record) VALUES(?,?) ON CONFLICT(document) DO UPDATE SET record=excluded.record", doc, string(raw))
		return err
	})
}

func (l *Loop) readCommentReplies(ctx context.Context, c Candidate) (*item.CommentBatch, []commentReply, error) {
	batch, err := l.commentBatch(ctx, c.ItemID)
	if err != nil || batch == nil {
		return batch, nil, err
	}
	file := filepath.Join(run.DurableDir(l.laneDirPath(c.LaneID), c.RunID), commentRepliesFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		return batch, nil, fmt.Errorf("criterion 1: reading reply file: %w", err)
	}
	expected := map[string]item.ReviewComment{}
	for _, v := range batch.Comments {
		expected[v.ID] = v
	}
	replies := []commentReply{}
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 64<<10), 8<<20)
	for scan.Scan() {
		if strings.TrimSpace(scan.Text()) == "" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(scan.Bytes(), &fields); err != nil {
			return batch, nil, fmt.Errorf("criterion 1: malformed reply: %w", err)
		}
		if round, ok := fields["round"]; !ok || bytes.Equal(bytes.TrimSpace(round), []byte("null")) {
			return batch, nil, fmt.Errorf("criterion 1: reply omits its turn count")
		}
		var r commentReply
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			return batch, nil, fmt.Errorf("criterion 1: malformed reply: %w", err)
		}
		comment, ok := expected[r.ID]
		if !ok || r.Round != len(comment.Reactions) || strings.TrimSpace(r.Summary) == "" {
			return batch, nil, fmt.Errorf("criterion 1: missing, duplicate, foreign or wrong-turn reply %q", r.ID)
		}
		delete(expected, r.ID)
		r.Path = batch.Document
		r.RootTextVersion = comment.RootTextVersion
		hash := sha256.Sum256([]byte(c.ItemID + "\x00" + r.ID))
		r.Nonce = "tillr-" + hex.EncodeToString(hash[:])
		replies = append(replies, r)
	}
	if err := scan.Err(); err != nil {
		return batch, nil, err
	}
	if len(expected) > 0 {
		return batch, nil, fmt.Errorf("criterion 1: reply file misses %d comments", len(expected))
	}
	return batch, replies, nil
}

type commentDelivery struct{ RunID, ItemID, Document, BlobID, Body string }

func (l *Loop) deliverCommentReplies(ctx context.Context, c Candidate) error {
	if !item.IsCommentItem(c.ItemID) {
		return nil
	}
	batch, replies, err := l.readCommentReplies(ctx, c)
	if err != nil || batch == nil {
		return err
	}
	delivery := commentDelivery{RunID: c.RunID, ItemID: c.ItemID, Document: batch.Document}
	var revision, outcome string
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		// A replay uses immutable stored bytes, not a reply file changed after settlement.
		err := r.QueryRow("SELECT blob_id,body FROM comment_deliveries WHERE run_id=?", c.RunID).Scan(&delivery.BlobID, &delivery.Body)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = r.QueryRow("SELECT outcome FROM comment_runs WHERE run_id=?", c.RunID).Scan(&outcome); err != nil {
			return err
		}
		if outcome == "answered" {
			revision = c.HeadSHA
			return nil
		}
		var exists bool
		if err = r.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')").Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return Externalf("reply delivery needs a recorded landing for %s", c.RunID)
		}
		err = r.QueryRow("SELECT trunk_after FROM landings WHERE item_id=? AND run_id=? AND result='landed' ORDER BY event_seq DESC LIMIT 1", c.ItemID, c.RunID).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			return Externalf("reply delivery needs a recorded landing for %s", c.RunID)
		}
		if err == nil {
			// A rebase of the trunk may have replaced the landed commit: read the document
			// at the commit that carries it now.
			revision, err = heavy.RewriteResolve(r, revision)
		}
		return err
	})
	s.Close()
	if err != nil {
		return err
	}
	if delivery.BlobID == "" {
		delivery.BlobID, err = l.revParse(ctx, l.cfg.Root, revision+":"+batch.Document)
		if err != nil {
			return err
		}
		var body bytes.Buffer
		encoder := json.NewEncoder(&body)
		for _, r := range replies {
			if err = encoder.Encode(r); err != nil {
				return err
			}
		}
		delivery.Body = body.String()
		s, err = store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec("INSERT INTO comment_deliveries(run_id,item_id,document,blob_id,body) VALUES(?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING", delivery.RunID, delivery.ItemID, delivery.Document, delivery.BlobID, delivery.Body)
			return err
		})
		s.Close()
		if err != nil {
			return err
		}
	}
	return l.writeCommentDelivery(ctx, delivery)
}

func (l *Loop) writeCommentDelivery(ctx context.Context, d commentDelivery) error {
	landed, err := l.git(ctx, l.cfg.Root, "cat-file", "blob", d.BlobID)
	if err != nil {
		return err
	}
	served, err := os.ReadFile(filepath.Join(l.cfg.Root, d.Document))
	if os.IsNotExist(err) || err == nil && !bytes.Equal(served, []byte(landed)) {
		l.reportf("loop: reply delivery waits on edited served document %s\n", d.Document)
		return nil
	}
	if err != nil {
		return err
	}
	dir := filepath.Join(l.cfg.Root, ".vantage", "inbox")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tillr-*.writing")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.WriteString(d.Body); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpPath, filepath.Join(dir, d.ItemID+".jsonl")); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec("UPDATE comment_deliveries SET written=1 WHERE run_id=?", d.RunID); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE comment_runs SET delivered=1 WHERE run_id=?", d.RunID)
		return err
	})
}

func (l *Loop) flushCommentDeliveries(ctx context.Context) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var pending []commentDelivery
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var exists bool
		if err := r.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='comment_deliveries')").Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return nil
		}
		rows, err := r.Query("SELECT run_id,item_id,document,blob_id,body FROM comment_deliveries WHERE written=0 ORDER BY run_id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d commentDelivery
			if err := rows.Scan(&d.RunID, &d.ItemID, &d.Document, &d.BlobID, &d.Body); err != nil {
				return err
			}
			pending = append(pending, d)
		}
		return rows.Err()
	})
	s.Close()
	if err != nil {
		return err
	}
	for _, d := range pending {
		if err = l.writeCommentDelivery(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) rejectCommentReplies(ctx context.Context, c Candidate, result *LoopResult) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var text string
		if err := tx.Reader().QueryRow("SELECT text FROM item_criteria WHERE item_id=? AND criterion_id='c1'", c.ItemID).Scan(&text); err != nil {
			return err
		}
		body, err := json.Marshal(map[string]any{"item_id": c.ItemID, "criterion_id": "c1", "criterion_hash": item.CriterionHash(text), "verdict": item.VerdictNotDone, "claim_class": "verification-only", "reason": c.RefusalDetail, "evidence": []string{filepath.Join(run.DurableDir(l.laneDirPath(c.LaneID), c.RunID), commentRepliesFile)}, "evaluated_head": c.HeadSHA, "actor_kind": item.ActorMechanical, "actor": "drain/comment-replies"})
		if err != nil {
			return err
		}
		_, err = item.RecordVerdict(ctx, tx, record.Envelope{RecordVersion: record.SupportedVersion, Kind: item.VerdictRecordKind, ID: c.RunID + "-replies", Producer: record.Producer{System: record.System, Utility: Utility, Version: Version}, CreatedAt: l.cfg.Now(), Payload: body})
		return err
	})
	if err != nil {
		return err
	}
	return l.refuse(ctx, c, result, RefusalGate)
}

func (l *Loop) answerCommentWithoutCommit(ctx context.Context, c Candidate, result *LoopResult) error {
	status, err := l.git(ctx, c.Worktree, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		c.RefusalDetail = "answer without commit left uncommitted changes"
		return l.refuse(ctx, c, result, RefusalFence)
	}
	if _, _, err := l.readCommentReplies(ctx, c); err != nil {
		c.RefusalDetail = err.Error()
		return l.rejectCommentReplies(ctx, c, result)
	}
	if err := l.cfg.Steps.Verdict(ctx, c, VerdictPassed); err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE comment_runs SET outcome='answered' WHERE run_id=?", c.RunID)
		return err
	})
	if err != nil {
		return err
	}
	if err := l.deliverCommentReplies(ctx, c); err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, c.ItemID, "answered review comments without a revision", "drain", l.cfg.Now())
		return err
	})
}
