package item

import (
	"encoding/json"
	"io/fs"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// AddPath appends one operator-approved prefix without changing the item's place,
// disposition, or run history. The full item.update payload also lets drain
// recognize that the fence which held a run has changed.
func AddPath(tx *store.Tx, id, path, by, why string, at time.Time) (record.Envelope, error) {
	by, why = strings.TrimSpace(by), strings.TrimSpace(why)
	if by == "" || why == "" {
		return record.Envelope{}, Usagef("add-path needs a non-empty --by and --why")
	}
	if err := checkAllowedPaths(id, []string{path}); err != nil {
		return record.Envelope{}, err
	}
	prefix := strings.TrimSuffix(path, "/")
	// Staging writes one prefix per line and trims the lines when it reads them.
	if !fs.ValidPath(prefix) || prefix == "." || path != strings.TrimSpace(path) || strings.ContainsAny(path, "\x00\r\n") {
		return record.Envelope{}, Rejectionf("invalid-fence-path", id, "%q is not a plain repo-relative path prefix", path)
	}
	p, storedAt, err := readStoredItem(tx.Reader(), id)
	if err != nil {
		return record.Envelope{}, err
	}
	d, err := itemDefinition(tx.Reader())
	if err != nil {
		return record.Envelope{}, err
	}
	if p.State == d.Closed || d.IsFinished(p.State) || frozenStates[p.State] {
		return record.Envelope{}, terminalRejection(id, p.State)
	}
	for _, existing := range p.AllowedPaths {
		if strings.TrimSuffix(existing, "/") == prefix {
			return envelopeOf(RecordKind, "item-"+id, storedAt, p)
		}
	}
	p.AllowedPaths = append(p.AllowedPaths, path)
	p.UpdatedAt = at.UTC().Format(time.RFC3339)
	encoded, err := encodeAllowedPaths(p.AllowedPaths)
	if err != nil {
		return record.Envelope{}, err
	}
	body, err := json.Marshal(struct {
		itemPayload
		PathAdded string `json:"path_added"`
		By        string `json:"by"`
		Why       string `json:"why"`
	}{p, path, by, why})
	if err != nil {
		return record.Envelope{}, Internalf("encoding path addition: %v", err)
	}
	seq, err := tx.Append(event.Event{Kind: UpdateKind, ObjectType: "item", ObjectID: &id, Payload: body, At: at})
	if err != nil {
		return record.Envelope{}, Internalf("appending path addition: %v", err)
	}
	if _, err := tx.Exec(`UPDATE items SET allowed_paths = ?, updated_at = ?, event_seq = ? WHERE item_id = ?`, encoded, at.UnixMilli(), seq, id); err != nil {
		return record.Envelope{}, Internalf("updating item %q allowed paths: %v", id, err)
	}
	p.EventSeq = seq
	return envelopeOf(RecordKind, "item-"+id, at, p)
}
