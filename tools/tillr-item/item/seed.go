package item

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// SeedRecordKind is the record the seed face emits: what the seed document asked for and
// what the store did about it. It is registered as neither an event kind nor a
// materializer for the same reason RejectionKind is: a seed is a batch of writes whose
// transitions are each recorded as their own item.add, and a second record of the batch
// would claim a state change the log did not make.
const SeedRecordKind = "item.seed"

// seedDocument is the tillr.seed file's shape: one batch of items, each entry the
// item.record payload apply.go's itemPayload defines. The fields around `items` are
// provenance for a reader and are never written to the store; a seed that cannot say
// where it came from is a seed nobody can audit.
//
// The shape is closed. The decoder refuses a key this struct does not name, at this
// level and inside every entry and criterion, because a mistyped field is otherwise
// dropped in silence: `blockedBy` for `blocked_by` would seed an item with no edges and
// the file would still read as if it carried them. Entries are kept raw here so each one
// is decoded on its own and an unknown key inside it can name the entry it is in.
type seedDocument struct {
	Kind      string            `json:"kind"`
	Version   string            `json:"version"`
	Generated string            `json:"generated"`
	Source    string            `json:"source"`
	Triage    string            `json:"triage"`
	Note      string            `json:"note"`
	Items     []json.RawMessage `json:"items"`
}

// seedItem is one entry of a seed document, and it is exactly itemPayload's input
// fields. It is a separate struct rather than itemPayload itself because itemPayload
// also carries what the store sets — updated_at, event_seq, the design document's
// read-through questions — and a seed that stated one of those would be claiming a value
// this face computes.
//
// No field the entry does not have is invented here: provenance and holds have no column in
// ItemDDL, and the triage records them as gaps rather than this loader inventing a place to
// put them. allowed_paths is the exception the fence needed: it has a column, so an entry
// states it and the seed stores it like every other field the record carries.
type seedItem struct {
	ItemID    string      `json:"item_id"`
	Title     string      `json:"title"`
	BodyRef   string      `json:"body_ref"`
	State     string      `json:"state"`
	Criteria  []criterion `json:"criteria"`
	BlockedBy []string    `json:"blocked_by"`
	DesignDoc string      `json:"design_doc"`
	// AllowedPaths is the entry's fence. It is optional at the seed face — an item may name
	// no paths — and staging is where a dispatch needs one, so a seed without it stays
	// legal and an item stored from it is refused by the stager rather than here.
	AllowedPaths []string `json:"allowed_paths"`
	// Rank is the entry's place in the queue's order (rank.go). It is optional like the
	// fence: a document that states none leaves every item it writes unranked, so a store
	// that has never been ranked keeps working.
	Rank string `json:"rank"`
	// Seat is the role the entry asks for (seat.go), optional like the fence: an entry that
	// states none stores an item that asks for no seat. It is part of an item's definition,
	// not a fact the store learns, so a re-seed compares it like the title.
	Seat string `json:"seat"`
}

// seedReport is item.seed's payload. `items` is the document's entries this run wrote,
// so a dry run answers exactly what a real run would have written; `existing` names the
// entries the store already holds with the same definition, which is how a second run of
// one file reports a no-op instead of writing again.
type seedReport struct {
	From     string        `json:"from"`
	DryRun   bool          `json:"dry_run"`
	Written  []string      `json:"written"`
	Ranked   []string      `json:"ranked"`
	Existing []string      `json:"existing"`
	Items    []itemPayload `json:"items"`
}

// Seed writes a tillr.seed document's items into the store through the one record→row
// mapping (Apply), in the caller's transaction: every entry is written or none is.
//
// Identity is the entry's item_id. An entry the store already holds with the same
// definition is reported as existing and not written; an entry whose definition differs
// is refused with the id and the first field that differs, because a seed that rewrote a
// stored item would move criterion text a verdict has already read — the freeze
// carryCriteria enforces is what makes a silent rewrite a lie rather than an update.
//
// Rank is the one field the seed owns rather than compares: it is the queue's order, no
// verdict reads it, and re-seeding after a re-rank is how an order is corrected. An entry
// whose definition matches but whose rank differs is written through the same Apply, which
// touches the rank and re-writes the definition as the store holds it, and is reported
// under Ranked.
//
// The seed owns an item's definition, its intake state at first insert and its rank; the
// store owns everything learned since. So the two fields a verdict and the drain move —
// the item's own state and its criteria's states — are not part of the comparison, and a
// re-rank never reapplies them: re-running the seed after an item lands is a report, not a
// resurrection, and the file never has to be edited to apologize for what the store learned.
//
// The whole document is validated and compared against the store before any row is
// written, so a bad entry near the end of the file cannot leave the entries before it
// stored; the transaction is the second guarantee, not the only one.
func Seed(ctx context.Context, tx *store.Tx, from string, doc []byte, at time.Time) (record.Envelope, error) {
	ordered, err := ReadSeed(doc)
	if err != nil {
		return record.Envelope{}, err
	}
	plan, err := planSeed(tx.Reader(), ordered)
	if err != nil {
		return record.Envelope{}, err
	}
	written := []string{}
	for _, payload := range plan.write {
		env, err := envelopeOf(RecordKind, "item-"+payload.ItemID, at, payload)
		if err != nil {
			return record.Envelope{}, err
		}
		if _, err := Apply(ctx, tx, env); err != nil {
			return record.Envelope{}, annotate(err, payload.ItemID)
		}
		written = append(written, payload.ItemID)
	}
	ranked := []string{}
	for _, payload := range plan.rerank {
		env, err := envelopeOf(RecordKind, "item-"+payload.ItemID, at, payload)
		if err != nil {
			return record.Envelope{}, err
		}
		if _, err := apply(ctx, tx, env, true); err != nil {
			return record.Envelope{}, annotate(err, payload.ItemID)
		}
		ranked = append(ranked, payload.ItemID)
	}
	return seedEnvelope(from, false, written, ranked, plan.existing, append(plan.write, plan.rerank...), at)
}

// Preview is Seed's dry run: it reads the document and the store, answers the same
// report with the entries a real run would write, and writes nothing. A nil reader is a
// store that does not exist yet, which holds no item and so conflicts with nothing.
func Preview(r *store.Reader, from string, doc []byte, at time.Time) (record.Envelope, error) {
	ordered, err := ReadSeed(doc)
	if err != nil {
		return record.Envelope{}, err
	}
	plan, err := planSeed(r, ordered)
	if err != nil {
		return record.Envelope{}, err
	}
	ranked := make([]string, 0, len(plan.rerank))
	for _, entry := range plan.rerank {
		ranked = append(ranked, entry.ItemID)
	}
	return seedEnvelope(from, true, []string{}, ranked, plan.existing, append(plan.write, plan.rerank...), at)
}

// seedEnvelope builds the item.seed record. It takes the entries the run acted on
// rather than the whole document, so a dry run's payload is the write list and a real
// run's is what it wrote.
func seedEnvelope(from string, dryRun bool, written, ranked, existing []string, items []itemPayload, at time.Time) (record.Envelope, error) {
	if items == nil {
		items = []itemPayload{}
	}
	if ranked == nil {
		ranked = []string{}
	}
	return envelopeOf(SeedRecordKind, "seed-"+filepath.Base(from), at, seedReport{
		From: from, DryRun: dryRun, Written: written, Ranked: ranked, Existing: existing, Items: items,
	})
}

// ReadSeed reads and validates one seed document, and answers its entries in the order
// they may be written: an entry blocked by another entry comes after it, so a file may
// name its blocker wherever the author put it and the store's edge rule (a blocker must
// exist) is still met when the edge is written.
//
// Every refusal names the entry: its position in the file and its item_id, or the
// position alone when the id is what is wrong.
func ReadSeed(doc []byte) ([]itemPayload, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	var file seedDocument
	if err := dec.Decode(&file); err != nil {
		return nil, Usagef("reading the seed document: %v", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, Usagef("reading the seed document: it holds more than one JSON value")
	}
	if file.Kind != "tillr.seed" {
		return nil, Usagef("the seed document is kind %q, want \"tillr.seed\"", file.Kind)
	}
	if len(file.Items) == 0 {
		return nil, Usagef("the seed document holds no items: an empty seed would be a queue of nothing")
	}

	items := make([]itemPayload, len(file.Items))
	seen := make(map[string]bool, len(file.Items))
	for i, raw := range file.Items {
		var entry seedItem
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&entry); err != nil {
			return nil, Usagef("seed entry %d: %v", i+1, err)
		}
		payload, err := readSeedItem(i, entry, seen)
		if err != nil {
			return nil, err
		}
		items[i] = payload
	}
	return orderSeed(items)
}

// readSeedItem validates one entry and answers it as an itemPayload. The checks are the
// ones a stored item must pass anyway, made here so the refusal names the entry rather
// than only the item id.
//
// The criteria check is the seed's own defect to fix, not this loader's: a criterion
// frozen at first store is text a verdict reads and hashes, so an item that arrives with
// none has nothing for a verdict to judge and is refused as a malformed entry. Whether
// the text is verifiable is a reviewer's judgement — this face can only refuse the
// absence.
func readSeedItem(index int, entry seedItem, seen map[string]bool) (itemPayload, error) {
	pos := index + 1
	if entry.ItemID == "" {
		return itemPayload{}, Usagef("seed entry %d has no item_id", pos)
	}
	if seen[entry.ItemID] {
		return itemPayload{}, Usagef("seed entry %d repeats item_id %q: one document names an item once", pos, entry.ItemID)
	}
	seen[entry.ItemID] = true
	if entry.Title == "" {
		return itemPayload{}, Usagef("seed entry %d (%s) has no title", pos, entry.ItemID)
	}
	if err := checkState(entry.ItemID, entry.State); err != nil {
		return itemPayload{}, annotate(err, entry.ItemID)
	}
	if len(entry.Criteria) == 0 {
		return itemPayload{}, Usagef("seed entry %d (%s) carries no criteria: an item without one has nothing a verdict can judge", pos, entry.ItemID)
	}
	for _, c := range entry.Criteria {
		if c.Text == "" {
			return itemPayload{}, Usagef("seed entry %d (%s) has criterion %q with no text: the text is what a verdict reads", pos, entry.ItemID, c.CriterionID)
		}
	}
	if err := checkCriteria(entry.ItemID, entry.Criteria); err != nil {
		return itemPayload{}, annotate(err, entry.ItemID)
	}
	// The fence is validated here as well as in Apply so a wildcard is refused with the
	// entry's position named, which is the entry an author has to edit.
	if err := checkAllowedPaths(entry.ItemID, entry.AllowedPaths); err != nil {
		return itemPayload{}, annotate(err, entry.ItemID)
	}
	// The seat is validated here as well as in Apply so an unknown one is refused with the
	// entry's position named, which is the entry an author has to edit.
	if err := checkSeat(entry.ItemID, entry.Seat); err != nil {
		return itemPayload{}, annotate(err, entry.ItemID)
	}

	// Criteria are written and read in criterion-id order (show.go's readCriteria), so
	// the document is normalized to that order before it is compared with a stored item:
	// a file that lists c2 before c1 would otherwise be reported as a conflict with
	// itself.
	criteria := make([]criterion, len(entry.Criteria))
	copy(criteria, entry.Criteria)
	sort.Slice(criteria, func(i, j int) bool { return criteria[i].CriterionID < criteria[j].CriterionID })

	blockedBy := make([]string, len(entry.BlockedBy))
	copy(blockedBy, entry.BlockedBy)
	sort.Strings(blockedBy)

	allowedPaths := make([]string, len(entry.AllowedPaths))
	copy(allowedPaths, entry.AllowedPaths)

	return itemPayload{
		ItemID: entry.ItemID, Title: entry.Title, BodyRef: entry.BodyRef, State: entry.State,
		Criteria: criteria, BlockedBy: blockedBy, DesignDoc: entry.DesignDoc,
		AllowedPaths: allowedPaths, Rank: entry.Rank, Seat: entry.Seat,
	}, nil
}

// orderSeed answers the entries so that an entry naming another entry of the same
// document as its blocker is written after it. File order is kept wherever it does not
// violate that, so a document is written in the order it reads.
//
// Entries that block each other can be written in no order at all. That is the same
// cycle the store refuses as edge-cycle, refused here by name because the file is what
// is wrong and no row has been written yet.
func orderSeed(items []itemPayload) ([]itemPayload, error) {
	index := make(map[string]int, len(items))
	for i, it := range items {
		index[it.ItemID] = i
	}
	placed := make([]bool, len(items))
	ordered := make([]itemPayload, 0, len(items))
	for len(ordered) < len(items) {
		progressed := false
		for i, it := range items {
			if placed[i] || !blockersPlaced(it, index, placed) {
				continue
			}
			placed[i] = true
			ordered = append(ordered, it)
			progressed = true
			break
		}
		if progressed {
			continue
		}
		stuck := make([]string, 0, len(items)-len(ordered))
		for i, it := range items {
			if !placed[i] {
				stuck = append(stuck, it.ItemID)
			}
		}
		return nil, Rejectionf("edge-cycle", stuck[0],
			"seed entries %s are blocked by each other, so none of them can be written first",
			strings.Join(stuck, ", "))
	}
	return ordered, nil
}

// blockersPlaced reports whether every blocker this entry names that is also an entry of
// the same document has already been written. A blocker that is not in the document is
// the store's to answer: Apply refuses it as edge-target-missing if no such item exists.
func blockersPlaced(it itemPayload, index map[string]int, placed []bool) bool {
	for _, blocker := range it.BlockedBy {
		if j, inDocument := index[blocker]; inDocument && !placed[j] {
			return false
		}
	}
	return true
}

// seedPlan is what one seed run decided before writing: the entries to write, in write
// order, the entries the store holds with an identical definition but a different rank
// (the seed owns the queue's order, so these are re-applied as the stored item carrying
// the seed's rank), and the ids the store already holds exactly.
type seedPlan struct {
	write    []itemPayload
	rerank   []itemPayload
	existing []string
}

// planSeed compares every entry with the store and answers what writing it would do. It
// writes nothing, so a conflict found on the last entry answers before the first row is
// touched.
func planSeed(r *store.Reader, ordered []itemPayload) (seedPlan, error) {
	plan := seedPlan{write: []itemPayload{}, rerank: []itemPayload{}, existing: []string{}}
	for _, entry := range ordered {
		if r == nil {
			plan.write = append(plan.write, entry)
			continue
		}
		held, found, err := storedItem(r, entry.ItemID)
		if err != nil {
			return seedPlan{}, err
		}
		if !found {
			plan.write = append(plan.write, entry)
			continue
		}
		if err := sameDefinition(entry, held); err != nil {
			return seedPlan{}, annotate(err, entry.ItemID)
		}
		if entry.Rank != held.Rank {
			// The rank is the seed's, so the entry's rank wants re-applying; the runtime state
			// is the store's, so the payload re-applied is the stored item with the seed's
			// rank. Applying the entry itself would write the seed's intake state and its
			// empty criterion states back over what a verdict and the drain recorded — a
			// resurrection, not a re-rank.
			rerank := held
			rerank.Rank = entry.Rank
			plan.rerank = append(plan.rerank, rerank)
			continue
		}
		plan.existing = append(plan.existing, entry.ItemID)
	}
	return plan, nil
}

// storedItem answers one stored item as the seed would state it, and false when the
// store holds no such item. It reads the rows rather than the log, like Show, but it
// does not follow the design document: a seed compares what it would write, and a
// missing design document is show's refusal to make, not the seed's.
func storedItem(r *store.Reader, id string) (itemPayload, bool, error) {
	var payload itemPayload
	var allowedPaths string
	err := r.QueryRow(
		`SELECT item_id, title, COALESCE(body_ref, ''), state, COALESCE(design_doc, ''),
		        COALESCE(allowed_paths, ''), COALESCE(rank, ''), COALESCE(seat, ''), COALESCE(prose, '')
		   FROM items WHERE item_id = ?`, id,
	).Scan(&payload.ItemID, &payload.Title, &payload.BodyRef, &payload.State, &payload.DesignDoc,
		&allowedPaths, &payload.Rank, &payload.Seat, &payload.Prose)
	if errors.Is(err, sql.ErrNoRows) {
		return itemPayload{}, false, nil
	}
	if err != nil {
		return itemPayload{}, false, Internalf("reading item %q: %v", id, err)
	}
	if payload.AllowedPaths, err = decodeAllowedPaths(allowedPaths); err != nil {
		return itemPayload{}, false, err
	}
	if payload.JudgeMode, err = readJudgeMode(r, id); err != nil {
		return itemPayload{}, false, err
	}
	if payload.Criteria, err = readCriteria(r, id); err != nil {
		return itemPayload{}, false, err
	}
	if payload.BlockedBy, err = readEdges(r, id); err != nil {
		return itemPayload{}, false, err
	}
	return payload, true, nil
}

// sameDefinition answers whether the store already holds this entry's definition, and
// names the first field that differs when it does not. The comparison is the item's
// definition as a reader sees it — title, body_ref, design_doc, the seat, the fence,
// criteria in id order, and the blocker set — and not updated_at or event_seq, which the
// store sets and a seed does not state.
//
// Two fields are deliberately outside the comparison, because they are not definition:
// the item's state and each criterion's state are what the store learns. The seed states
// the intake state an item is inserted with, and a criterion arrives with the state it was
// authored at; after that a verdict and the drain own both. Comparing them made a seed
// conflict with the item it had just successfully described — measured 2026-11-14, when the
// seed after `item-obs-filter` landed was refused on `criterion c1 state is "done" in the
// store and "" in the seed`, and the file had to be edited to say what the store already
// knew. Rank is outside it for the same reason: the seed is the queue's only rank writer,
// so a differing rank is an update the plan carries (rerank), not a conflict.
func sameDefinition(entry, held itemPayload) error {
	switch {
	case entry.Title != held.Title:
		return conflict(entry.ItemID, "title", held.Title, entry.Title)
	case entry.BodyRef != held.BodyRef:
		return conflict(entry.ItemID, "body_ref", held.BodyRef, entry.BodyRef)
	case entry.DesignDoc != held.DesignDoc:
		return conflict(entry.ItemID, "design_doc", held.DesignDoc, entry.DesignDoc)
	case entry.Seat != held.Seat:
		return conflict(entry.ItemID, "seat", held.Seat, entry.Seat)
	case strings.Join(entry.AllowedPaths, ",") != strings.Join(held.AllowedPaths, ","):
		return conflict(entry.ItemID, "allowed_paths",
			strings.Join(held.AllowedPaths, ","), strings.Join(entry.AllowedPaths, ","))
	case len(entry.Criteria) != len(held.Criteria):
		return Rejectionf("seed-conflict", entry.ItemID,
			"item %q is already in the store with %d criteria and the seed presents %d: a seed reports an existing item, it does not rewrite one",
			entry.ItemID, len(held.Criteria), len(entry.Criteria))
	}
	for i, c := range entry.Criteria {
		h := held.Criteria[i]
		if c.CriterionID != h.CriterionID {
			return conflict(entry.ItemID, "criterion_id", h.CriterionID, c.CriterionID)
		}
		if c.Text != h.Text {
			return conflict(entry.ItemID, "criterion "+c.CriterionID+" text", h.Text, c.Text)
		}
	}
	if len(entry.BlockedBy) != len(held.BlockedBy) {
		return conflict(entry.ItemID, "blocked_by",
			strings.Join(held.BlockedBy, ","), strings.Join(entry.BlockedBy, ","))
	}
	for i, blocker := range entry.BlockedBy {
		if blocker != held.BlockedBy[i] {
			return conflict(entry.ItemID, "blocked_by",
				strings.Join(held.BlockedBy, ","), strings.Join(entry.BlockedBy, ","))
		}
	}
	return nil
}

// conflict is one field's disagreement, named so a reader can see which value is stored
// and which the seed presents. Both blocker lists are sorted before they are compared, so
// the message states sets rather than an order neither table has.
func conflict(id, field, stored, presented string) error {
	return Rejectionf("seed-conflict", id,
		"item %q is already in the store with a different definition: %s is %q in the store and %q in the seed",
		id, field, stored, presented)
}

// annotate prefixes an error with the entry it is about, keeping the exit code and the
// rejection's rule and item. A refusal that does not name its entry leaves a reader to
// match messages against a file.
func annotate(err error, id string) error {
	var e *ExitError
	if errors.As(err, &e) {
		return &ExitError{Code: e.Code, Rule: e.Rule, ItemID: e.ItemID,
			Msg: fmt.Sprintf("seed entry %q: %s", id, e.Msg)}
	}
	return fmt.Errorf("seed entry %q: %w", id, err)
}
