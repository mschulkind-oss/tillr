package item

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// ListKind is the read-only record containing item summaries, not item definitions.
const ListKind = "item.list"

// ItemSummary is one stored item's summary fields for list output.
type ItemSummary struct {
	ItemID    string   `json:"item_id"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	BlockedBy []string `json:"blocked_by"`
	After     []string `json:"after,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

type itemListPayload struct {
	Items []ItemSummary `json:"items"`
}

// ListSummaries reads every item, or the union of the requested states, ordered by id.
// It reads only stored summaries: missing body or design documents do not prevent
// a consumer from inspecting its queue. States are matched exactly, without a
// fixed vocabulary, so a consumer can also query states from its own definition.
func ListSummaries(r *store.Reader, states []string) ([]ItemSummary, error) {
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
		return nil, Internalf("listing items: %v", err)
	}
	defer rows.Close()
	items := []ItemSummary{}
	for rows.Next() {
		var summary ItemSummary
		var updatedAt int64
		if err := rows.Scan(&summary.ItemID, &summary.Title, &summary.State, &updatedAt); err != nil {
			return nil, Internalf("reading an item summary: %v", err)
		}
		summary.UpdatedAt = time.UnixMilli(updatedAt).UTC().Format(time.RFC3339)
		items = append(items, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, Internalf("listing items: %v", err)
	}
	// Release the item rows before reading edges on the same transaction.
	if err := rows.Close(); err != nil {
		return nil, Internalf("closing item summaries: %v", err)
	}
	for i := range items {
		items[i].BlockedBy, items[i].After, err = readEdges(r, items[i].ItemID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// List returns the item.list record for every item, or the union of the requested states.
func List(r *store.Reader, states []string) (record.Envelope, error) {
	items, err := ListSummaries(r, states)
	if err != nil {
		return record.Envelope{}, err
	}
	return envelopeOf(ListKind, "items", time.Now().UTC(), itemListPayload{Items: items})
}

// FormatListTable formats item summaries as a readable aligned table.
func FormatListTable(items []ItemSummary) string {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ITEM\tSTATE\tBLOCKED BY\tUPDATED\tTITLE")
	for _, it := range items {
		blocked := "-"
		if len(it.BlockedBy) > 0 {
			blocked = strings.Join(it.BlockedBy, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.ItemID, it.State, blocked, it.UpdatedAt, it.Title)
	}
	_ = tw.Flush()
	return buf.String()
}
