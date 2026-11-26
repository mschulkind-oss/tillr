// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drainmodel

import (
	"fmt"

	"github.com/mschulkind-oss/tillr/store"
)

// generationDDL stores one ordinal per pass that applied work. Allocation is in
// the first applied transition's transaction (including a lease reap), so empty
// and stale passes consume none.
const GenerationDDL = `
CREATE TABLE IF NOT EXISTS drain_generations (
 generation INTEGER PRIMARY KEY
);
`

func AllocateGeneration(tx *store.Tx) (string, error) {
	var n int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(generation), 0) + 1 FROM drain_generations`).Scan(&n); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO drain_generations (generation) VALUES (?)`, n); err != nil {
		return "", err
	}
	return fmt.Sprintf("gen-%d", n), nil
}
