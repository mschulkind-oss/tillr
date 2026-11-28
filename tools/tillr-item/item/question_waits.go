// Architecture and invariants: docs/design/tillr-per-step-question-holds.md Section 2.3
package item

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// QuestionWaitKind is the event one `wait-question` write appends.
const QuestionWaitKind = "item.question-wait"

// QuestionWaitRecordKind is the record the wait-question face answers.
const QuestionWaitRecordKind = "item.question-wait"

// WaitAllKey is the ref key of the drop override `--none` writes: it removes every
// recorded wait of the item, which is how an item opts out of the whole-document hold.
const WaitAllKey = "*"

// The two tables store only the dependency edge item -> question reference, never
// whether the question is open (D-QM1). item_question_overrides also carries the three
// prose-reference columns, so an unmarked prose question added by hand can be stored.
func init() {
	if err := event.Register(QuestionWaitKind, Utility); err != nil {
		panic(err)
	}
	if err := store.RegisterDDL("item-question-waits", `CREATE TABLE IF NOT EXISTS item_question_waits (
		item_id TEXT NOT NULL,
		ref_key TEXT NOT NULL,
		doc_path TEXT NOT NULL,
		question_id TEXT NOT NULL DEFAULT '',
		heading_slug TEXT NOT NULL DEFAULT '',
		quoted_question TEXT NOT NULL DEFAULT '',
		section_sha256 TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT 'plan',
		PRIMARY KEY (item_id, ref_key)
	)`); err != nil {
		panic(err)
	}
	if err := store.RegisterDDL("item-question-overrides", `CREATE TABLE IF NOT EXISTS item_question_overrides (
		item_id TEXT NOT NULL,
		ref_key TEXT NOT NULL,
		op TEXT NOT NULL CHECK (op IN ('add', 'drop')),
		doc_path TEXT NOT NULL DEFAULT '',
		question_id TEXT NOT NULL DEFAULT '',
		heading_slug TEXT NOT NULL DEFAULT '',
		quoted_question TEXT NOT NULL DEFAULT '',
		section_sha256 TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		actor TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (item_id, ref_key)
	)`); err != nil {
		panic(err)
	}
}

// QuestionRef is one question an item waits on. QuestionID is empty for an unmarked prose
// question, which is named by HeadingSlug and guarded by SectionSHA256.
type QuestionRef struct {
	Key            string
	DocPath        string
	QuestionID     string
	HeadingSlug    string
	QuotedQuestion string
	SectionSHA256  string
	Reason         string
}

// Canonical is the `<doc>#<id>` spelling shown to operators and kept in Decision.QuestionRefs.
func (q QuestionRef) Canonical() string {
	if q.QuestionID != "" {
		return q.DocPath + "#" + q.QuestionID
	}
	return q.DocPath + "#" + q.HeadingSlug
}

// QuestionWaits is one item's recorded question-wait metadata. An item with no row in
// either table is absent from the reader's map: it is a legacy item and keeps the
// whole-document hold. An item that is present with no Refs waits on nothing.
type QuestionWaits struct {
	Refs []QuestionRef
}

// ReadQuestionWaits returns the effective wait list of every item that has any recorded
// metadata: item_question_waits rows plus `add` overrides minus `drop` overrides, where
// the `*` drop removes every waits row. A store without the tables answers empty.
func ReadQuestionWaits(r *store.Reader) (map[string]QuestionWaits, error) {
	out := map[string]QuestionWaits{}
	var have int
	if err := r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('item_question_waits','item_question_overrides')`).Scan(&have); err != nil {
		return nil, err
	}
	if have < 2 {
		return out, nil
	}
	drops := map[string]map[string]bool{}
	var adds = map[string][]QuestionRef{}
	rows, err := r.Query(`SELECT item_id, ref_key, doc_path, question_id, heading_slug, quoted_question, section_sha256, reason FROM item_question_waits ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	waits := map[string][]QuestionRef{}
	for rows.Next() {
		var id string
		var q QuestionRef
		if err := rows.Scan(&id, &q.Key, &q.DocPath, &q.QuestionID, &q.HeadingSlug, &q.QuotedQuestion, &q.SectionSHA256, &q.Reason); err != nil {
			rows.Close()
			return nil, err
		}
		waits[id] = append(waits[id], q)
		out[id] = QuestionWaits{}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	orows, err := r.Query(`SELECT item_id, ref_key, op, doc_path, question_id, heading_slug, quoted_question, section_sha256, reason FROM item_question_overrides ORDER BY created_at, ref_key`)
	if err != nil {
		return nil, err
	}
	for orows.Next() {
		var id, op string
		var q QuestionRef
		if err := orows.Scan(&id, &q.Key, &op, &q.DocPath, &q.QuestionID, &q.HeadingSlug, &q.QuotedQuestion, &q.SectionSHA256, &q.Reason); err != nil {
			orows.Close()
			return nil, err
		}
		out[id] = QuestionWaits{}
		if op == "drop" {
			if drops[id] == nil {
				drops[id] = map[string]bool{}
			}
			drops[id][q.Key] = true
		} else {
			adds[id] = append(adds[id], q)
		}
	}
	if err := orows.Close(); err != nil {
		return nil, err
	}
	for id := range out {
		var refs []QuestionRef
		if !drops[id][WaitAllKey] {
			for _, q := range waits[id] {
				if !drops[id][q.Key] {
					refs = append(refs, q)
				}
			}
		}
		seen := map[string]bool{}
		for _, q := range refs {
			seen[q.Key] = true
		}
		for _, q := range adds[id] {
			if !seen[q.Key] {
				refs = append(refs, q)
			}
		}
		out[id] = QuestionWaits{Refs: refs}
	}
	return out, nil
}

var (
	sectionHeading = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t#]*$`)
	sectionFence   = regexp.MustCompile("^[ \t]{0,3}(```|~~~)")
)

// SectionSHA256 hashes the section a heading names: the heading line through the line
// before the next heading of the same or a higher level, headings inside code fences
// ignored. The heading is matched by its anchor slug. found is false when no heading
// carries that slug.
func SectionSHA256(text, slug string) (hash string, found bool) {
	var section []string
	level := 0
	inFence := false
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if sectionFence.MatchString(line) {
			inFence = !inFence
		}
		if !inFence {
			if m := sectionHeading.FindStringSubmatch(line); m != nil {
				switch {
				case !found && headingSlug(m[2]) == slug:
					found, level = true, len(m[1])
				case found && len(m[1]) <= level:
					return sectionHash(section), true
				}
			}
		}
		if found {
			section = append(section, line)
		}
	}
	if !found {
		return "", false
	}
	return sectionHash(section), true
}

func sectionHash(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// QuestionWaitChange is one wait-question write.
type QuestionWaitChange struct {
	Op     string // add, drop or none
	Ref    string // <doc>#<id-or-slug>; empty for none
	Quote  string // the prose question, for an unmarked add
	Reason string
	Actor  string
	Root   string // the repository root, to hash a prose section
}

var oqShaped = regexp.MustCompile(`^OQ-[A-Za-z0-9_.-]+$`)

// WriteQuestionWait records one owner or operator correction of an item's question waits.
func WriteQuestionWait(ctx context.Context, tx *store.Tx, itemID string, c QuestionWaitChange, at time.Time) (record.Envelope, error) {
	if strings.TrimSpace(c.Reason) == "" {
		return record.Envelope{}, Usagef("wait-question needs a --reason")
	}
	exists, err := itemExists(ctx, tx.Reader(), itemID)
	if err != nil {
		return record.Envelope{}, err
	}
	if !exists {
		return record.Envelope{}, Rejectionf("item-missing", itemID, "item %q does not exist", itemID)
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	var q QuestionRef
	op := c.Op
	switch c.Op {
	case "none":
		op = "drop"
		q.Key = WaitAllKey
		if _, err := tx.Exec(`DELETE FROM item_question_overrides WHERE item_id = ? AND op = 'add'`, itemID); err != nil {
			return record.Envelope{}, Internalf("clearing add overrides: %v", err)
		}
	case "add", "drop":
		doc, id, ok := strings.Cut(c.Ref, "#")
		if !ok || doc == "" || id == "" {
			return record.Envelope{}, Usagef("wait-question %s needs <doc>#<question-id-or-heading-slug>, got %q", c.Op, c.Ref)
		}
		q = QuestionRef{Key: c.Ref, DocPath: doc}
		switch {
		case c.Op == "add" && c.Quote != "":
			text, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(doc)))
			if err != nil {
				return record.Envelope{}, Rejectionf("doc-missing", itemID, "cannot read %q: %v", doc, err)
			}
			hash, found := SectionSHA256(string(text), id)
			if !found {
				return record.Envelope{}, Rejectionf("heading-missing", itemID, "%q has no heading with the anchor %q", doc, id)
			}
			q.HeadingSlug, q.QuotedQuestion, q.SectionSHA256 = id, c.Quote, hash
		case c.Op == "add" && !oqShaped.MatchString(id):
			return record.Envelope{}, Usagef("wait-question --add %q is not an OQ-* question; name a prose question's heading with --quote", c.Ref)
		case oqShaped.MatchString(id):
			q.QuestionID = id
		default:
			q.HeadingSlug = id
		}
	default:
		return record.Envelope{}, Usagef("wait-question needs one of --add, --drop or --none")
	}
	if _, err := tx.Exec(`INSERT INTO item_question_overrides(item_id, ref_key, op, doc_path, question_id, heading_slug, quoted_question, section_sha256, reason, actor, created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(item_id, ref_key) DO UPDATE SET op=excluded.op, doc_path=excluded.doc_path,
 question_id=excluded.question_id, heading_slug=excluded.heading_slug, quoted_question=excluded.quoted_question,
 section_sha256=excluded.section_sha256, reason=excluded.reason, actor=excluded.actor, created_at=excluded.created_at`,
		itemID, q.Key, op, q.DocPath, q.QuestionID, q.HeadingSlug, q.QuotedQuestion, q.SectionSHA256, c.Reason, c.Actor, stamp); err != nil {
		return record.Envelope{}, Internalf("writing the override: %v", err)
	}
	payload := map[string]any{"item_id": itemID, "op": c.Op, "ref": q.Key, "reason": c.Reason, "actor": c.Actor}
	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the wait-question event: %v", err)
	}
	if _, err := tx.Append(event.Event{Kind: QuestionWaitKind, ObjectType: "item", ObjectID: &itemID, Payload: body, At: at}); err != nil {
		return record.Envelope{}, Internalf("appending the wait-question event: %v", err)
	}
	return envelopeOf(QuestionWaitRecordKind, fmt.Sprintf("question-wait-%s", itemID), at, payload)
}
