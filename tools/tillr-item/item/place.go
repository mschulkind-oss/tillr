package item

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Destination is one of the four positions accepted by the place face.
type Destination struct {
	To     string `json:"to,omitempty"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

func (d Destination) Valid() bool {
	n := 0
	for _, v := range []string{d.To, d.Before, d.After} {
		if v != "" {
			n++
		}
	}
	return n == 1 && (d.To == "" || d.To == "first" || d.To == "last")
}

type orderedItem struct{ id, rank, state string }

// Place changes only ordering and its update receipt. A crowded gap is repaired in
// this same transaction, preserving the other items' relative order. Empty keys
// sort last by id until sync supplies them.
func Place(ctx context.Context, tx *store.Tx, id string, d Destination, at time.Time, by, via string) (record.Envelope, error) {
	if id == "" || !d.Valid() {
		return record.Envelope{}, Usagef("place needs an item and exactly one of --to first|last, --before <item>, --after <item>")
	}
	rows, err := tx.Reader().Query(`SELECT item_id,rank,state FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`)
	if err != nil {
		return record.Envelope{}, Internalf("reading item order: %v", err)
	}
	all := []orderedItem{}
	index := map[string]int{}
	for rows.Next() {
		var v orderedItem
		if err := rows.Scan(&v.id, &v.rank, &v.state); err != nil {
			rows.Close()
			return record.Envelope{}, err
		}
		index[v.id] = len(all)
		all = append(all, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return record.Envelope{}, err
	}
	inOrder := func(v orderedItem) bool {
		return InOrder(v.state)
	}
	check := func(id string) error {
		i, ok := index[id]
		if !ok {
			return Rejectionf("unknown-item", id, "no item %q in this store", id)
		}
		if !inOrder(all[i]) {
			return Rejectionf("not-in-order", id, "item %q is terminal or parked", id)
		}
		return nil
	}
	if err := check(id); err != nil {
		return record.Envelope{}, err
	}
	neighbour := d.Before
	if d.After != "" {
		neighbour = d.After
	}
	if neighbour != "" {
		if err := check(neighbour); err != nil {
			return record.Envelope{}, err
		}
	}
	order := []orderedItem{}
	old := 0
	for _, v := range all {
		if inOrder(v) {
			if v.id == id {
				old = len(order)
			}
			order = append(order, v)
		}
	}
	var seq int64
	if err := tx.Reader().QueryRow(`SELECT event_seq FROM items WHERE item_id=?`, id).Scan(&seq); err != nil {
		return record.Envelope{}, err
	}
	result := func(place int) (record.Envelope, error) {
		head, err := tx.Reader().EventHeadSeq()
		if err != nil {
			return record.Envelope{}, err
		}
		return envelopeOf("item.place", "place-"+id, at, map[string]any{"item_id": id, "place": place, "count": len(order), "event_seq": seq, "event_head_seq": head})
	}
	if neighbour == id {
		return result(old + 1)
	}
	target := order[old]
	rest := append(append([]orderedItem{}, order[:old]...), order[old+1:]...)
	pos := 0
	if d.To == "last" {
		pos = len(rest)
	}
	if neighbour != "" {
		for i, v := range rest {
			if v.id == neighbour {
				pos = i
				if d.After != "" {
					pos++
				}
				break
			}
		}
	}
	if pos == old {
		return result(old + 1)
	}
	seq, err = insertOrdered(tx, target, rest, pos, at, by, via, "")
	if err != nil {
		return record.Envelope{}, err
	}
	return result(pos + 1)
}

// insertOrdered shares the sparse key choice and crowded-order respacing between
// manual moves and first-import placement. The caller decides which rows belong
// in its order; sync includes keyed terminal sources, unlike the place face.
func insertOrdered(tx *store.Tx, target orderedItem, rest []orderedItem, pos int, at time.Time, by, via, from string) (int64, error) {
	var seq int64
	if err := tx.Reader().QueryRow(`SELECT event_seq FROM items WHERE item_id=?`, target.id).Scan(&seq); err != nil {
		return 0, Internalf("reading the placement receipt: %v", err)
	}
	key, respaced := insertionKeys(rest, pos)
	write := func(v orderedItem, key string) error {
		payload, _, err := readStoredItem(tx.Reader(), v.id)
		if err != nil {
			return err
		}
		payload.Rank = key
		payload.UpdatedAt = at.UTC().Format(time.RFC3339)
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		fields["placed_by"] = by
		fields["placed_via"] = via
		if v.id == target.id && from != "" {
			// This immutable receipt finds prior siblings even after their blocks
			// are retired. It is not the idea pipeline's future derived_from edge.
			fields["placed_from"] = from
		}
		raw, err = json.Marshal(fields)
		if err != nil {
			return err
		}
		next, err := tx.Append(event.Event{Kind: UpdateKind, ObjectType: "item", ObjectID: &v.id, Payload: raw, At: at})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET rank=?,updated_at=?,event_seq=? WHERE item_id=?`, key, at.UnixMilli(), next, v.id); err != nil {
			return err
		}
		if v.id == target.id {
			seq = next
		}
		return nil
	}
	if respaced == nil {
		if err := write(target, key); err != nil {
			return 0, err
		}
	} else {
		final := append(append(append([]orderedItem{}, rest[:pos]...), target), rest[pos:]...)
		for i, v := range final {
			key := respaced[i]
			if v.rank != key || (v.id == target.id && from != "") {
				if err := write(v, key); err != nil {
					return 0, err
				}
			}
		}
	}
	return seq, nil
}

// insertionKeys is the shared allocation for writing placement and scoped
// previews. A non-nil slice supplies every key after crowded-order respacing.
func insertionKeys(rest []orderedItem, pos int) (string, []string) {
	lo, hi := "", ""
	if pos > 0 {
		lo = rest[pos-1].rank
	}
	if pos < len(rest) {
		hi = rest[pos].rank
	}
	key, ok := betweenKeys(lo, hi)
	if (pos > 0 && lo == "") || (pos < len(rest) && hi == "") || (pos > 0 && pos < len(rest) && lo >= hi) {
		ok = false
	}
	if ok {
		return key, nil
	}
	// Eight base-36 digits leave wide gaps on both ends and between every pair.
	const space int64 = 2821109907456 // 36^8
	keys := make([]string, len(rest)+1)
	stride := space / int64(len(keys)+1)
	for i := range keys {
		n := int64(i+1) * stride
		buf := [8]byte{}
		for j := 7; j >= 0; j-- {
			buf[j] = RankAlphabet[n%36]
			n /= 36
		}
		keys[i] = string(buf[:])
	}
	return "", keys
}

// betweenKeys returns a strict lexical gap, or false when a prefix ending in
// zeros leaves none. A length bound makes repeated moves repair rather than grow
// keys without limit. Empty hi means the list's end, not an unranked neighbour.
func betweenKeys(lo, hi string) (string, bool) {
	if hi == "" {
		key := lo + "m"
		return key, len(key) <= 64
	}
	if lo >= hi {
		return "", false
	}
	candidate := lo + "m"
	if candidate < hi && len(candidate) <= 64 {
		return candidate, true
	}
	common := 0
	for common < len(lo) && common < len(hi) && lo[common] == hi[common] {
		common++
	}
	if common < len(lo) {
		a := strings.IndexByte(RankAlphabet, lo[common])
		b := strings.IndexByte(RankAlphabet, hi[common])
		if b-a > 1 {
			candidate = lo[:common] + string(RankAlphabet[(a+b)/2])
			return candidate, true
		}
		return "", false
	}
	for i := common; i < len(hi); i++ {
		digit := strings.IndexByte(RankAlphabet, hi[i])
		if digit > 0 {
			candidate = hi[:i] + string(RankAlphabet[(digit-1)/2]) + "m"
			return candidate, len(candidate) <= 64
		}
	}
	return "", false
}
