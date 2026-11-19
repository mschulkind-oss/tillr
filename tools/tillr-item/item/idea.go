package item

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const MaxIdeaWords = 256 * 1024

// IdeaHash validates the owner's words without normalizing their bytes.
func IdeaHash(words []byte) (string, error) {
	if len(words) > MaxIdeaWords {
		return "", Rejectionf("words-too-large", "", "words exceed 256 KiB")
	}
	if !utf8.Valid(words) {
		return "", Rejectionf("words-not-text", "", "words must be valid UTF-8")
	}
	if len(bytes.TrimSpace(words)) == 0 {
		return "", Rejectionf("words-empty", "", "words must not be empty or only whitespace")
	}
	return fmt.Sprintf("%x", sha256.Sum256(words)), nil
}

// PreserveIdea uses exclusive creation, never truncating another saved idea. Syncing
// both the file and directory makes the words durable before any item is written.
func PreserveIdea(dir, id string, words []byte) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".txt")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return path, readErr
		}
		if !bytes.Equal(raw, words) {
			return path, fmt.Errorf("saved words path %s holds different bytes", path)
		}
		// A previous attempt may have failed during its flush; flush the retry too.
		f, err = os.OpenFile(path, os.O_WRONLY, 0600)
		if err != nil {
			return path, err
		}
	} else if err != nil {
		return path, err
	} else {
		if _, err = f.Write(words); err != nil {
			f.Close()
			return path, err
		}
	}
	err = f.Sync()
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return path, err
	}
	d, err := os.Open(dir)
	if err != nil {
		return path, err
	}
	err = d.Sync()
	closeErr = d.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return path, err
	}
	// intake may have been created by this save; its parent's new directory
	// entry must be durable too before the store can point at the words.
	parent, err := os.Open(filepath.Dir(dir))
	if err != nil {
		return path, err
	}
	err = parent.Sync()
	closeErr = parent.Close()
	if err == nil {
		err = closeErr
	}
	return path, err
}

// PreserveUnassignedIdea preserves words when a transaction cannot produce an id.
// Files left by earlier failed saves also participate in collision avoidance.
func PreserveUnassignedIdea(dir, hash string, words []byte) (string, error) {
	for n := 10; n <= len(hash); n++ {
		id := "item-idea-" + hash[:n]
		raw, err := os.ReadFile(filepath.Join(dir, id+".txt"))
		if errors.Is(err, os.ErrNotExist) || err == nil && bytes.Equal(raw, words) {
			return PreserveIdea(dir, id, words)
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no unique words path for hash %s", hash)
}

// Idea creates the item and its intake record in the caller's single transaction.
// A hash replay is read-only: it does not move the first item to the retry's place.
func Idea(ctx context.Context, tx *store.Tx, words []byte, hash, intakeDir string, config settings.Intake, destination Destination, at time.Time, by, via string) (record.Envelope, error) {
	var id string
	err := tx.Reader().QueryRow(`SELECT item_id FROM idea_intake WHERE words_hash=?`, hash).Scan(&id)
	if err == nil {
		if config.Dir == "" || len(config.Paths) == 0 {
			file, err := PreserveIdea(intakeDir, id, words)
			if err != nil {
				return record.Envelope{}, err
			}
			return record.Envelope{}, Rejectionf("intake-not-configured", id, "set intake.dir and intake.paths; words saved at %s", file)
		}
		return queuePosition(tx.Reader(), "item.idea", id, at)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return record.Envelope{}, err
	}
	unique := false
	for n := 10; n <= len(hash); n++ {
		id = "item-idea-" + hash[:n]
		var count int
		if err := tx.Reader().QueryRow(`SELECT count(*) FROM items WHERE item_id=?`, id).Scan(&count); err != nil {
			return record.Envelope{}, err
		}
		if count != 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(intakeDir, id+".txt"))
		if errors.Is(err, os.ErrNotExist) || err == nil && bytes.Equal(raw, words) {
			unique = true
			break
		}
		if err != nil {
			return record.Envelope{}, err
		}
		if n == len(hash) {
			return record.Envelope{}, fmt.Errorf("no unique idea id for hash %s", hash)
		}
	}
	if !unique {
		return record.Envelope{}, fmt.Errorf("no unique idea id for hash %s", hash)
	}
	file, err := PreserveIdea(intakeDir, id, words)
	if err != nil {
		return record.Envelope{}, err
	}
	fail := func(err error) (record.Envelope, error) {
		return record.Envelope{}, Rejectionf("intake-store-failed", id, "words saved at %s; item was not saved: %v", file, err)
	}
	if config.Dir == "" || len(config.Paths) == 0 {
		return record.Envelope{}, Rejectionf("intake-not-configured", id, "set intake.dir and intake.paths; words saved at %s", file)
	}
	tracked := filepath.ToSlash(filepath.Join(config.Dir, id+".txt"))
	payload := itemPayload{ItemID: id, Title: ideaTitle(string(words)), State: "in-queue", Seat: "design", AllowedPaths: append([]string{tracked}, config.Paths...), Criteria: []criterion{
		{CriterionID: "c1", Text: "The words are in the tree at the idea's words path, byte for byte as saved."},
		{CriterionID: "c2", Text: "Every piece of work the words ask for is an item block that carries from this idea, written to the prose guide, with paths and criteria; or the design document says it is deliberately not done, and why."},
		{CriterionID: "c3", Text: "Anything only the owner can decide is an open question in a design document, with options and a leaning. Nothing the owner must decide is guessed."},
	}, BlockedBy: []string{}}
	env, err := envelopeOf(RecordKind, id, at, payload)
	if err != nil {
		return fail(err)
	}
	if _, err = Apply(ctx, tx, env); err != nil {
		return fail(err)
	}
	if _, err = tx.Exec(`INSERT INTO idea_intake(item_id,words_hash,words_size,words_path,intake_path) VALUES(?,?,?,?,?)`, id, hash, len(words), tracked, file); err != nil {
		return fail(err)
	}
	placed, err := Place(ctx, tx, id, destination, at, by, via)
	if err != nil {
		return record.Envelope{}, err
	}
	placed.Kind = "item.idea"
	return placed, nil
}

func ideaTitle(words string) string {
	line := strings.TrimSuffix(strings.SplitN(words, "\n", 2)[0], "\r")
	runes := []rune(line)
	if len(runes) <= 80 {
		return line
	}
	end := 80
	for i := 79; i > 0; i-- {
		if unicode.IsSpace(runes[i]) {
			end = i
			break
		}
	}
	return string(runes[:end])
}

// queuePosition answers an existing item's place in the queue, writing nothing: a refiled
// idea or request answers where its first filing put it.
func queuePosition(r *store.Reader, kind, id string, at time.Time) (record.Envelope, error) {
	rows, err := r.Query(`SELECT item_id,event_seq FROM items WHERE state NOT IN ('not-started','landed','closed','resolved-elsewhere') ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`)
	if err != nil {
		return record.Envelope{}, err
	}
	count, place := 0, 0
	var seq int64
	for rows.Next() {
		var next string
		var s int64
		if err := rows.Scan(&next, &s); err != nil {
			rows.Close()
			return record.Envelope{}, err
		}
		count++
		if next == id {
			place = count
			seq = s
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return record.Envelope{}, err
	}
	if place == 0 {
		if err := r.QueryRow(`SELECT event_seq FROM items WHERE item_id=?`, id).Scan(&seq); err != nil {
			return record.Envelope{}, err
		}
	}
	head, err := r.EventHeadSeq()
	if err != nil {
		return record.Envelope{}, err
	}
	return envelopeOf(kind, id, at, map[string]any{"item_id": id, "place": place, "count": count, "event_seq": seq, "event_head_seq": head})
}

// Keep intake metadata separate from the ordinary item definition, which sync and
// placement may rewrite. The unique hash makes concurrent retries one item.
func init() {
	if err := store.RegisterDDL("idea-intake", `CREATE TABLE IF NOT EXISTS idea_intake (
 item_id TEXT PRIMARY KEY REFERENCES items(item_id), words_hash TEXT NOT NULL UNIQUE,
 words_size INTEGER NOT NULL, words_path TEXT NOT NULL, intake_path TEXT NOT NULL
 );`); err != nil {
		panic(err)
	}
}
