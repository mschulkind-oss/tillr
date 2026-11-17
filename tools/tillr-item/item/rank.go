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

// SetRank updates only an existing item's rank and transition bookkeeping. It never
// rewrites the definition or runtime facts, even on a finished or closed item.
// An unchanged rank appends no event.
func SetRank(ctx context.Context, tx *store.Tx, itemID, rank string, at time.Time) (record.Envelope, error) {
	if itemID == "" {
		return record.Envelope{}, Usagef("an item rank write needs an item_id")
	}
	payload, storedAt, err := readStoredItem(tx.Reader(), itemID)
	if err != nil {
		return record.Envelope{}, err
	}
	if err := checkRank(itemID, rank); err != nil {
		return record.Envelope{}, err
	}
	if payload.Rank == rank {
		return envelopeOf(RecordKind, "item-"+itemID, storedAt, payload)
	}
	if _, err := tx.Exec(`UPDATE items SET rank = ?, updated_at = ? WHERE item_id = ?`, rank, at.UnixMilli(), itemID); err != nil {
		return record.Envelope{}, Internalf("ranking item %q: %v", itemID, err)
	}
	payload.Rank = rank
	payload.UpdatedAt = at.UTC().Format(time.RFC3339)
	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the rank update: %v", err)
	}
	seq, err := tx.Append(event.Event{Kind: UpdateKind, ObjectType: "item", ObjectID: &payload.ItemID, Payload: body, At: at})
	if err != nil {
		return record.Envelope{}, Internalf("appending the rank update: %v", err)
	}
	if _, err := tx.Exec(`UPDATE items SET event_seq = ? WHERE item_id = ?`, seq, itemID); err != nil {
		return record.Envelope{}, Internalf("recording the event sequence: %v", err)
	}
	payload.EventSeq = seq
	return envelopeOf(RecordKind, "item-"+itemID, at, payload)
}

// RankAlphabet is the character set a rank is spelled in, listed in the order string
// comparison puts its characters: the sparse lexical rank alphabet the capacity queue
// already carries (`QUEUE_RANK_DIGITS` in pi-subagents' `src/runs/background/queue-rank.ts`,
// itself the pile's `PILE_RANK_DIGITS` in `work-control.ts`). A rank is an opaque token over
// these 36 characters compared as a plain string — there is deliberately no numeric
// priority beside it, because two orderings of one queue is how a queue ends up with an
// order nobody can explain.
//
// The alphabet is what makes a rank *sparse*: a token may be any length, so a gap between
// two ranks is what makes inserting between them cheap — `a`, then `am` between `a` and `b`
// — and no rank is renumbered to make room. Ranks are stored and compared as the strings
// they are; nothing here generates one, because the value is the caller's decision and
// this utility only refuses one it cannot order.
const RankAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// checkRank refuses a rank spelled outside the alphabet. The refusal is what keeps the
// store's order the alphabet's order: an uppercase letter sorts before every lowercase
// one, so a rank carrying one would take a place in the queue that no rank in the alphabet
// describes. An empty rank is not a rank at all — it is the unranked item, which sorts
// after every ranked one — so it is legal.
func checkRank(itemID, rank string) error {
	for offset, character := range rank {
		if strings.ContainsRune(RankAlphabet, character) {
			continue
		}
		return Rejectionf("rank-alphabet", itemID,
			"rank %q carries %q at offset %d: a rank is a string over %q, the alphabet the queue's ranks are compared in",
			rank, string(character), offset, RankAlphabet)
	}
	return nil
}
