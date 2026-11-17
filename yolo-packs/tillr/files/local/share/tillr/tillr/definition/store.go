package definition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const storeKey = "store.definition"
const SetEvent = "store.definition.set"

func init() {
	if err := event.Register(SetEvent, "store"); err != nil {
		panic(err)
	}
}

// Record carries the exact file content and its SHA-256 (the hash of those bytes).
// Default is true only when no declaration is recorded. Parsed is the validated
// definition for consumers, omitted from the wire record to avoid two vocabularies.
// See README.md, "A store's definition", for the storage and reader contract.
type Record struct {
	Name    string     `json:"definition"`
	Version int        `json:"version"`
	Content string     `json:"content"`
	SHA256  string     `json:"sha256"`
	Default bool       `json:"default"`
	Parsed  Definition `json:"-"`
}

func fromContent(content []byte, isDefault bool) (Record, error) {
	d, err := Read(bytes.NewReader(content))
	if err != nil {
		return Record{}, err
	}
	return Record{Name: d.Name, Version: d.Version, Content: string(content), SHA256: fmt.Sprintf("%x", sha256.Sum256(content)), Default: isDefault, Parsed: d}, nil
}

// ForStore is the shared answer for every utility. It reads one declaration in a
// read transaction, or the embedded Git-landing definition for a legacy store.
// Corrupt declarations fail explicitly; they never fall back to the default.
func ForStore(ctx context.Context, s *store.Store) (Record, error) {
	var result Record
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		result, err = readStore(r)
		return err
	})
	return result, err
}

func readStore(r *store.Reader) (Record, error) {
	var raw string
	err := r.QueryRow(`SELECT value FROM meta WHERE key = ?`, storeKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fromContent(gitLandingJSON, true)
	}
	if err != nil {
		return Record{}, fmt.Errorf("definition: reading store declaration: %w", err)
	}
	var recorded Record
	if err := json.Unmarshal([]byte(raw), &recorded); err != nil {
		return Record{}, fmt.Errorf("definition: decoding store declaration: %w", err)
	}
	actual, err := fromContent([]byte(recorded.Content), false)
	if err != nil {
		return Record{}, fmt.Errorf("definition: invalid store declaration: %w", err)
	}
	if recorded.Name != actual.Name || recorded.Version != actual.Version || recorded.SHA256 != actual.SHA256 || recorded.Default {
		return Record{}, fmt.Errorf("definition: store declaration metadata does not match its content")
	}
	return actual, nil
}

// OccupiedStatesError names every item whose current state a replacement drops.
// A caller can report this as a refused request, rather than a store failure.
type OccupiedStatesError struct{ Items []string }

func (e *OccupiedStatesError) Error() string {
	return "definition: replacement drops states held by items: " + strings.Join(e.Items, ", ")
}

// Set validates content before acquiring SQLite's writer. Under that lock it checks
// every current item state and atomically replaces the declaration and appends its
// event. It does no filesystem, Git, or process work in the write transaction.
func Set(ctx context.Context, s *store.Store, content []byte) (Record, error) {
	next, err := fromContent(content, false)
	if err != nil {
		return Record{}, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return Record{}, err
	}
	at := time.Now().UTC()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		old, err := readStore(tx.Reader())
		if err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'items'`).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			states := make(map[string]bool, len(next.Parsed.States))
			for _, state := range next.Parsed.States {
				states[state] = true
			}
			rows, err := tx.Query(`SELECT item_id, state FROM items ORDER BY item_id`)
			if err != nil {
				return err
			}
			var occupied []string
			for rows.Next() {
				var id, state string
				if err := rows.Scan(&id, &state); err != nil {
					rows.Close()
					return err
				}
				if !states[state] {
					occupied = append(occupied, fmt.Sprintf("%s (%s)", id, state))
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(occupied) != 0 {
				return &OccupiedStatesError{Items: occupied}
			}
		}
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, storeKey, string(raw)); err != nil {
			return err
		}
		payload, err := json.Marshal(struct {
			Old string `json:"old_sha256"`
			New string `json:"new_sha256"`
		}{old.SHA256, next.SHA256})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{Kind: SetEvent, ObjectType: "store", Payload: payload, At: at})
		return err
	})
	if err != nil {
		return Record{}, err
	}
	return next, nil
}
