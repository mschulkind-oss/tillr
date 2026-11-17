package item

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// ListKind is the read-only record containing item summaries, not item definitions.
const ListKind = "item.list"

type itemSummary struct {
	ItemID    string   `json:"item_id"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	BlockedBy []string `json:"blocked_by"`
	UpdatedAt string   `json:"updated_at"`
}

type itemListPayload struct {
	Items []itemSummary `json:"items"`
}

// List reads every item, or the union of the requested states, ordered by id.
// It reads only stored summaries: missing body or design documents do not prevent
// a consumer from inspecting its queue. States are matched exactly, without a
// fixed vocabulary, so a consumer can also query states from its own definition.
func List(r *store.Reader, states []string) (record.Envelope, error) {
	query := `SELECT item_id, title, state, updated_at FROM items`
	args := make([]any, len(states))
	if len(states) > 0 {
		query += ` WHERE state IN (` + strings.TrimSuffix(strings.Repeat("?,", len(states)), ",") + `)`
		for i, state := range states {
			args[i] = state
		}
	}
	query += ` ORDER BY item_id`
	rows, err := r.Query(query, args...)
	if err != nil {
		return record.Envelope{}, Internalf("listing items: %v", err)
	}
	defer rows.Close()
	items := []itemSummary{}
	for rows.Next() {
		var summary itemSummary
		var updatedAt int64
		if err := rows.Scan(&summary.ItemID, &summary.Title, &summary.State, &updatedAt); err != nil {
			return record.Envelope{}, Internalf("reading an item summary: %v", err)
		}
		summary.UpdatedAt = time.UnixMilli(updatedAt).UTC().Format(time.RFC3339)
		items = append(items, summary)
	}
	if err := rows.Err(); err != nil {
		return record.Envelope{}, Internalf("listing items: %v", err)
	}
	// Release the item rows before reading edges on the same transaction.
	if err := rows.Close(); err != nil {
		return record.Envelope{}, Internalf("closing item summaries: %v", err)
	}
	for i := range items {
		items[i].BlockedBy, err = readEdges(r, items[i].ItemID)
		if err != nil {
			return record.Envelope{}, err
		}
	}
	return envelopeOf(ListKind, "items", time.Now().UTC(), itemListPayload{Items: items})
}
