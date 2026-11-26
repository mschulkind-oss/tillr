package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const definitionMetaKey = "store.definition"

// Completes returns the store definition's "completes" mode ("landing" by default
// when no declaration is recorded, or the declared mode such as "hook") without
// requiring callers to import package definition.
func (s *Store) Completes(ctx context.Context) (string, error) {
	var completes string
	err := s.ReadTx(ctx, func(r *Reader) error {
		var raw string
		err := r.QueryRow(`SELECT value FROM meta WHERE key = ?`, definitionMetaKey).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			completes = "landing"
			return nil
		}
		if err != nil {
			return fmt.Errorf("store: reading definition completes: %w", err)
		}
		var rec struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return fmt.Errorf("store: decoding definition record: %w", err)
		}
		var def struct {
			Completes string `json:"completes"`
		}
		if err := json.Unmarshal([]byte(rec.Content), &def); err != nil {
			return fmt.Errorf("store: decoding definition content: %w", err)
		}
		if def.Completes == "" {
			completes = "landing"
			return nil
		}
		completes = def.Completes
		return nil
	})
	if err != nil {
		return "", err
	}
	return completes, nil
}
