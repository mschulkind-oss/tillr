package item

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// frozenStates are the legacy values that stay readable and are never written again.
// `in-progress` joined them because no transition writes it now; a legacy row stays
// readable, and it moves out through SetState like any other state.
var frozenStates = map[string]bool{"abandoned": true, "deleted": true, "in-progress": true}

// itemPayload is the item.record payload: design §5.3's field list, plus the two
// fields the store sets and the input never carries.
type itemPayload struct {
	ItemID          string      `json:"item_id"`
	Title           string      `json:"title"`
	BodyRef         string      `json:"body_ref"`
	State           string      `json:"state"`
	WaitingOn       *WaitingOn  `json:"waiting_on,omitempty"`
	UnderlyingState string      `json:"underlying_state,omitempty"`
	CloseReason     string      `json:"close_reason,omitempty"`
	ClosedAt        string      `json:"closed_at,omitempty"`
	ClosedBy        string      `json:"closed_by,omitempty"`
	ClosedFrom      string      `json:"closed_from,omitempty"`
	LifecycleReason string      `json:"lifecycle_reason,omitempty"`
	LifecycleBy     string      `json:"lifecycle_by,omitempty"`
	ResolvesVia     string      `json:"resolves_via,omitempty"`
	Finding         *Finding    `json:"finding,omitempty"`
	Criteria        []criterion `json:"criteria"`
	BlockedBy       []string    `json:"blocked_by"`
	DesignDoc       string      `json:"design_doc,omitempty"`
	// AllowedPaths is the item's fence: the repo-relative path prefixes a run's change
	// may include (design §3's Fence section). An item that names none has no fence, which
	// is legal to store — an item outlives any one run — and refused by staging, where a
	// brief needs one. A wildcard is refused here: `*` or `/` would make the fence admit
	// every path, which is the same as admitting none while reading as a guarantee.
	AllowedPaths []string `json:"allowed_paths"`
	// Rank is the item's place in the queue's order: a sparse lexical string over
	// RankAlphabet (rank.go), compared as a plain string. Empty is unranked — an item the
	// queue sorts after every ranked one — and an edit replaces the value like every other
	// field, so a record that states no rank stores the item unranked.
	Rank string `json:"rank"`
	// Place is show's 1-based queue position, accepted on apply for round-trip
	// compatibility and ignored on write.
	Place int `json:"place,omitempty"`
	// Seat is the role of the work the item asks for — one of the seats in seat.go — and
	// never a model id. The backend policy maps the seat to a backend, and that backend
	// names its model, so changing which model fills a seat is a policy edit and touches no
	// item. Empty means the item asks for no seat; the planner then uses the tick's default
	// backend.
	Seat string `json:"seat,omitempty"`
	// Prose is the version of the prose guide recorded by the document author.
	Prose            string `json:"prose,omitempty"`
	Workflow         string `json:"workflow"`
	SelectedWorkflow string `json:"selected_workflow,omitempty"`
	WorkflowSource   string `json:"workflow_source,omitempty"`
	// WorkflowArgs are the arguments the item's own workflow is bound with, for a workflow
	// whose top-level args a containing stage would otherwise supply (doc-triage@v1's
	// documents). Only the store carries them: a document block has no attribute for them,
	// so an item that has them is declared by its record (StoreDeclared).
	WorkflowArgs map[string]any `json:"workflow_args,omitempty"`
	JudgeMode    string         `json:"judge_mode"`
	Express      bool           `json:"express"`
	UpdatedAt    string         `json:"updated_at,omitempty"`
	EventSeq     int64          `json:"event_seq,omitempty"`
	// Questions and QuestionsSource are show's read-through of the design document
	// the item points at. They are never stored: the document is the only copy.
	Questions       []Question `json:"questions,omitempty"`
	QuestionsSource string     `json:"questions_source,omitempty"`
	// QuestionsStatus is known only after a successful index read. Unknown carries
	// QuestionsReason and must not be interpreted as a design with no questions.
	QuestionsStatus string `json:"questions_status,omitempty"`
	QuestionsReason string `json:"questions_reason,omitempty"`
}

// criterion is one acceptance criterion of an item.
type criterion struct {
	CriterionID string `json:"criterion_id"`
	Text        string `json:"text"`
	State       string `json:"state"`
	ClaimClass  string `json:"claim_class"`
}

// Apply is item's one record→row mapping: it validates the record, writes the rows
// it describes, appends the transition's event in the same transaction, and answers
// the record as stored. The write face and document sync both call it, so a
// fixture cannot construct a state the write face would reject (lanes §1, OQ-L2).
func Apply(ctx context.Context, tx *store.Tx, env record.Envelope) (record.Envelope, error) {
	return ApplyWithReceipt(ctx, tx, env, MoveReceipt{})
}

// ApplyWithReceipt records an authorized finished-state move with its reason and actor.
func ApplyWithReceipt(ctx context.Context, tx *store.Tx, env record.Envelope, receipt MoveReceipt) (record.Envelope, error) {
	var payload itemPayload
	// Read the whole value to retain the item id even when an unknown field sorts
	// before item_id. Only unknown fields must refuse a replay: ordinary typed
	// decoding errors keep their existing behavior after a replay miss.
	payloadErr := json.Unmarshal(env.Payload, &payload)
	if field, unknown := unknownPayloadField(env.Payload, reflect.TypeOf(payload)); unknown {
		return record.Envelope{}, Rejectionf("unknown-field", payload.ItemID,
			"item payload carries unknown field %q: this field is not stored", field)
	}

	// A replayed request answers the original result and writes nothing: the key is
	// the caller's statement that this is the same request, and a second write would
	// make the log say the transition happened twice.
	if env.IdempotencyKey != "" {
		stored, found, err := replay(ctx, tx, env.IdempotencyKey)
		if err != nil {
			return record.Envelope{}, err
		}
		if found {
			return stored, nil
		}
	}

	if payloadErr != nil {
		return record.Envelope{}, Usagef("reading the item payload: %v", payloadErr)
	}
	payload.Place = 0
	if payload.ItemID == "" {
		return record.Envelope{}, Usagef("an item.record needs an item_id")
	}
	if err := checkCriteria(payload.ItemID, payload.Criteria); err != nil {
		return record.Envelope{}, err
	}
	criteria, err := carryCriteria(tx.Reader(), payload.ItemID, payload.Criteria)
	if err != nil {
		return record.Envelope{}, err
	}
	payload.Criteria = criteria

	d, err := itemDefinition(tx.Reader())
	if err != nil {
		return record.Envelope{}, err
	}
	existing, err := itemExists(ctx, tx.Reader(), payload.ItemID)
	if err != nil {
		return record.Envelope{}, err
	}
	from := ""
	payload.CloseReason, payload.ClosedAt, payload.ClosedBy, payload.ClosedFrom = "", "", "", ""
	payload.LifecycleReason, payload.LifecycleBy, payload.ResolvesVia = "", "", ""
	payload.Finding = nil
	if existing {
		stored, storedAt, err := readStoredItem(tx.Reader(), payload.ItemID)
		if err != nil {
			return record.Envelope{}, err
		}
		if !hasPayloadField(env.Payload, "rank") {
			payload.Rank = stored.Rank
		}
		from = stored.State
		if err := checkSplitMove(d, payload.ItemID, stored.WaitingOn, payload.State); err != nil {
			return record.Envelope{}, err
		}
		payload.CloseReason, payload.ClosedAt, payload.ClosedBy, payload.ClosedFrom = stored.CloseReason, stored.ClosedAt, stored.ClosedBy, stored.ClosedFrom
		payload.LifecycleReason, payload.LifecycleBy, payload.ResolvesVia = stored.LifecycleReason, stored.LifecycleBy, stored.ResolvesVia
		payload.Finding = stored.Finding
		// carryCriteria runs first so its existing refusals keep their names,
		// including when a record also tries to reopen or rename the item.
		if d.IsFinished(from) && from != d.Closed && from == payload.State {
			comparison := payload
			if !sameTerminalDefinition(stored, comparison) {
				return record.Envelope{}, terminalRejection(payload.ItemID, from)
			}
			if payload.Rank == stored.Rank {
				return envelopeOf(RecordKind, "item-"+payload.ItemID, storedAt, stored)
			}
		}
	}
	if err := checkAllowedPaths(payload.ItemID, payload.AllowedPaths); err != nil {
		return record.Envelope{}, err
	}
	if err := checkRank(payload.ItemID, payload.Rank); err != nil {
		return record.Envelope{}, err
	}
	if payload.JudgeMode != "" && payload.JudgeMode != "agent" {
		return record.Envelope{}, Rejectionf("invalid-judge", payload.ItemID, "judge mode must be agent or absent")
	}
	if err := checkSeat(payload.ItemID, payload.Seat); err != nil {
		return record.Envelope{}, err
	}
	workflowArgs, err := encodeWorkflowArgs(payload)
	if err != nil {
		return record.Envelope{}, err
	}
	if d.Name == definition.GitLandingName && d.IsFinished(from) && from != d.Closed && from != payload.State {
		// A record carries no receipt, so it never moves a finished item: the one listed
		// move out of landed (a reverted landing's return) goes through the state face.
		return record.Envelope{}, terminalRejection(payload.ItemID, from)
	}
	if err := checkState(d, payload.ItemID, payload.State); err != nil {
		return record.Envelope{}, err
	}
	if d.Name != definition.GitLandingName {
		if err := checkMove(d, payload.ItemID, from, payload.State, receipt); err != nil {
			return record.Envelope{}, err
		}
	}
	if err := checkClosedTransition(d, payload.ItemID, from, payload.State, true); err != nil {
		return record.Envelope{}, err
	}
	if err := checkMove(d, payload.ItemID, from, payload.State, receipt); err != nil {
		return record.Envelope{}, err
	}
	if d.Name == definition.GitLandingName && existing && from != payload.State && payload.State == "landed" {
		if err := checkLandedCriteria(payload.ItemID, payload.Criteria); err != nil {
			return record.Envelope{}, err
		}
	}
	if err := checkTerminalEvidence(tx.Reader(), d, payload.ItemID, from, payload.State, receipt); err != nil {
		return record.Envelope{}, err
	}
	if existing && from != payload.State && payload.State == "resolved-elsewhere" {
		if err := refuseClaimedLanding(tx.Reader(), payload.ItemID, payload.State); err != nil {
			return record.Envelope{}, err
		}
	}
	if existing && from != payload.State {
		if err := checkStateReceipt(tx.Reader(), d, payload.ItemID, from, payload.State, receipt); err != nil {
			return record.Envelope{}, err
		}
		if d.IsFinished(payload.State) {
			payload.LifecycleReason, payload.LifecycleBy, payload.ResolvesVia = receipt.Reason, receipt.By, receipt.ResolvesVia
		}
	}
	allowedPaths, err := encodeAllowedPaths(payload.AllowedPaths)
	if err != nil {
		return record.Envelope{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths, rank, seat, prose, workflow, workflow_args, judge_mode, express)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(item_id) DO UPDATE SET
		   title = excluded.title, body_ref = excluded.body_ref, state = excluded.state,
		   design_doc = excluded.design_doc, updated_at = excluded.updated_at,
		   allowed_paths = excluded.allowed_paths, rank = excluded.rank, seat = excluded.seat, prose = excluded.prose, workflow = excluded.workflow, workflow_args = excluded.workflow_args, judge_mode = excluded.judge_mode, express = excluded.express,
		   waiting_on = CASE WHEN excluded.state IN (?, ?) THEN items.waiting_on ELSE '' END,
		   underlying_state = CASE WHEN excluded.state IN (?, ?) THEN items.underlying_state ELSE '' END,
		   lifecycle_reason = ?, lifecycle_by = ?, resolves_via = ?`,
		payload.ItemID, payload.Title, payload.BodyRef, payload.State, payload.DesignDoc, env.CreatedAt.UnixMilli(),
		allowedPaths, payload.Rank, payload.Seat, payload.Prose, payload.Workflow, workflowArgs, payload.JudgeMode, payload.Express,
		d.Blocked, d.Closed, d.Blocked, d.Closed, payload.LifecycleReason, payload.LifecycleBy, payload.ResolvesVia,
	); err != nil {
		return record.Envelope{}, Internalf("writing the item: %v", err)
	}
	if err := replaceCriteria(ctx, tx, payload); err != nil {
		return record.Envelope{}, err
	}
	if err := replaceEdges(ctx, tx, payload); err != nil {
		return record.Envelope{}, err
	}

	if payload.WaitingOn, payload.UnderlyingState, err = ReadWaitingOn(tx.Reader(), payload.ItemID); err != nil {
		return record.Envelope{}, err
	}
	payload.UpdatedAt = env.CreatedAt.UTC().Format(time.RFC3339)
	var eventPayload any = payload
	if from != payload.State && d.IsFinished(from) {
		eventPayload = lifecycleEvent(payload, "state", receipt.Reason, receipt.By)
	}
	body, err := json.Marshal(eventPayload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the stored record: %v", err)
	}
	kind := AddKind
	if existing {
		kind = UpdateKind
	}
	seq, err := tx.Append(event.Event{
		Kind:       kind,
		ObjectType: "item",
		ObjectID:   &payload.ItemID,
		Payload:    body,
		At:         env.CreatedAt,
	})
	if err != nil {
		return record.Envelope{}, Internalf("appending the %s event: %v", kind, err)
	}
	// The row records the sequence of the event that last wrote it, so show answers the
	// echo's event_seq from the row rather than by reading the log.
	payload.EventSeq = seq
	if _, err := tx.Exec(`UPDATE items SET event_seq = ? WHERE item_id = ?`, seq, payload.ItemID); err != nil {
		return record.Envelope{}, Internalf("recording the event sequence: %v", err)
	}
	stored, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the stored record: %v", err)
	}
	env.Payload = stored
	if env.IdempotencyKey != "" {
		// The ledger holds the whole echo, not just its payload: a replay answers the
		// record the first request produced, envelope included.
		recorded, err := json.Marshal(env)
		if err != nil {
			return record.Envelope{}, Internalf("encoding the replayed record: %v", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO item_requests (idempotency_key, record) VALUES (?, ?)`,
			env.IdempotencyKey, string(recorded),
		); err != nil {
			return record.Envelope{}, Internalf("recording the idempotency key: %v", err)
		}
	}
	return env, nil
}

// unknownPayloadField checks names independently of value types: a type error in a
// known field must not mask an unknown field later in the same replayed request.
// The JSON tags on the payload type are the vocabulary, including nested structs;
// a second hand-maintained field list would drift as producers gain fields.
func unknownPayloadField(raw json.RawMessage, shape reflect.Type) (string, bool) {
	for shape.Kind() == reflect.Pointer {
		shape = shape.Elem()
	}
	switch shape.Kind() {
	case reflect.Struct:
		dec := json.NewDecoder(bytes.NewReader(raw))
		if token, err := dec.Token(); err != nil || token != json.Delim('{') {
			return "", false // Typed decoding owns malformed values, after replay.
		}
		// Inspect every occurrence, not a map's last value for each key: a second
		// criteria array must not erase an unknown field in the first one.
		for dec.More() {
			token, err := dec.Token()
			if err != nil {
				return "", false
			}
			key, ok := token.(string)
			if !ok {
				return "", false
			}
			var value json.RawMessage
			if err := dec.Decode(&value); err != nil {
				return "", false
			}
			var fieldType reflect.Type
			for i := 0; i < shape.NumField(); i++ {
				field := shape.Field(i)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "" {
					name = field.Name
				}
				// Like encoding/json, accept case-insensitive spellings of known keys.
				if field.IsExported() && name != "-" && strings.EqualFold(key, name) {
					fieldType = field.Type
					break
				}
			}
			if fieldType == nil {
				return key, true
			}
			if name, unknown := unknownPayloadField(value, fieldType); unknown {
				return name, true
			}
		}
	case reflect.Slice, reflect.Array:
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return "", false
		}
		for _, entry := range entries {
			if name, unknown := unknownPayloadField(entry, shape.Elem()); unknown {
				return name, true
			}
		}
	}
	return "", false
}

// hasPayloadField reports whether the top-level JSON object in raw declares key
// (matching encoding/json's case-insensitive key lookup).
func hasPayloadField(raw json.RawMessage, target string) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok {
			return false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return false
		}
		if strings.EqualFold(key, target) {
			return true
		}
	}
	return false
}

// sameTerminalDefinition compares the fields Apply writes, not read-through questions or
// store-owned timestamps and lifecycle metadata. Criteria and blockers are stored
// by id, so presenting those lists in another order does not change the item.
func sameTerminalDefinition(a, b itemPayload) bool {
	ac, bc := slices.Clone(a.Criteria), slices.Clone(b.Criteria)
	slices.SortFunc(ac, func(a, b criterion) int { return strings.Compare(a.CriterionID, b.CriterionID) })
	slices.SortFunc(bc, func(a, b criterion) int { return strings.Compare(a.CriterionID, b.CriterionID) })
	ab, bb := slices.Clone(a.BlockedBy), slices.Clone(b.BlockedBy)
	slices.Sort(ab)
	slices.Sort(bb)
	return a.State == b.State && a.Title == b.Title && a.BodyRef == b.BodyRef &&
		a.DesignDoc == b.DesignDoc && a.Rank == b.Rank && a.Seat == b.Seat &&
		a.Prose == b.Prose && a.Workflow == b.Workflow && sameWorkflowArgs(a, b) && a.JudgeMode == b.JudgeMode && a.Express == b.Express && slices.Equal(ac, bc) &&
		slices.Equal(ab, bb) && slices.Equal(a.AllowedPaths, b.AllowedPaths)
}

// replay answers the record a request with this key already produced, and false when
// the key is new.
func replay(ctx context.Context, tx *store.Tx, key string) (record.Envelope, bool, error) {
	var stored string
	err := tx.Reader().QueryRow(`SELECT record FROM item_requests WHERE idempotency_key = ?`, key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return record.Envelope{}, false, nil
	}
	if err != nil {
		return record.Envelope{}, false, Internalf("reading the idempotency key: %v", err)
	}
	var env record.Envelope
	if err := json.Unmarshal([]byte(stored), &env); err != nil {
		return record.Envelope{}, false, Internalf("reading the stored record: %v", err)
	}
	return env, true, nil
}

// carryCriteria answers the criteria as this record will store them. An edit replaces
// an item's criteria wholesale (replaceCriteria), so the rows the store already holds
// are the only record of what was presented before; this is where an edit is compared
// with them, before any row is written.
//
// Two freezes ride that one comparison, and both answer "may this edit move something a
// judge has already been shown":
//
//   - state: criterion state moves only on an item.verdict (board-utilities.md §5.3;
//     work-control-judge-in-tillr.md "Retirement in the end state", D-UT16), so an edit
//     that presents a different state is refused by name rather than overwriting it. A
//     record that states no state is asking for the text alone, and the stored state
//     stands.
//   - text: a criterion that has been presented is frozen as to its text and may not be
//     dropped. It may be kept as it stands, or appended after; rewriting it would make a
//     verdict that names its text stop meaning what it said (D-UT16: frozen criterion
//     text "cannot be rewritten through `item apply` (append only)").
//
// "Presented" is read from the state column, because that is the only per-criterion fact
// this store holds besides the text: the row has no presented-at column, and no event
// marks a criterion's first store. The event log's item.verdict understates presentation
// — a criterion written already carrying a state (the seed materializer and the test
// fixtures do exactly that) has no verdict event while its text is the text a judge will
// read. A non-empty state says the criterion arrived carrying a judgement or can acquire
// one, and it cannot lie as a freeze signal, because nothing in this utility writes a
// stored state back to empty: this function only ever carries a non-empty stored state
// forward, and RecordVerdict only writes one of the four verdicts. The freeze is
// therefore monotone — no edit can clear a state in order to unfreeze the text. A
// criterion stored with an empty state was never presented with one, so there is nothing
// to free and its text stays editable.
func carryCriteria(r *store.Reader, itemID string, criteria []criterion) ([]criterion, error) {
	stored, err := readCriteria(r, itemID)
	if err != nil {
		return nil, err
	}
	if err := checkClaimMoves(itemID, stored, criteria); err != nil {
		return nil, err
	}
	presented := make(map[string]criterion, len(stored))
	for _, c := range stored {
		presented[c.CriterionID] = c
	}
	kept := make(map[string]bool, len(criteria))
	for _, c := range criteria {
		kept[c.CriterionID] = true
	}
	// A criterion the store holds and this record omits is not an edit of it at all:
	// replaceCriteria would delete the row a verdict was recorded against.
	for _, c := range stored {
		if c.State == "" || kept[c.CriterionID] {
			continue
		}
		return nil, Rejectionf("criterion-frozen", itemID,
			"criterion %q of item %q is frozen and this edit omits it: a presented criterion stays on the item, so keep it as the store holds it and record the change as a new criterion appended after it",
			c.CriterionID, itemID)
	}
	carried := make([]criterion, len(criteria))
	for i, c := range criteria {
		held, known := presented[c.CriterionID]
		if !known {
			carried[i] = c
			continue
		}
		// A record that states no criterion state is asking for the text alone, so the
		// stored state stands; a record that states a different one is asking for a
		// state change this writer does not make.
		if c.State != "" && c.State != held.State {
			return nil, Rejectionf("criterion-state", itemID,
				"criterion %q is %q in the store and this edit presents %q: a criterion's state moves only on an item.verdict",
				c.CriterionID, held.State, c.State)
		}
		if held.State != "" && c.Text != held.Text {
			return nil, Rejectionf("criterion-frozen", itemID,
				"criterion %q of item %q is frozen as %q and this edit presents %q: a presented criterion's text does not move, so keep it as the store holds it and record the change as a new criterion appended after it",
				c.CriterionID, itemID, held.Text, c.Text)
		}
		c.State = held.State
		carried[i] = c
	}
	return carried, nil
}

// checkSeat refuses a seat outside the item model's vocabulary (seat.go). An empty seat is
// legal: it is the item asking for no seat, and the planner then uses the tick's default
// backend. Apply checks only the name's shape; the planner checks the backend policy.
func checkSeat(itemID, seat string) error {
	if seat == "" || ValidSeatName(seat) {
		return nil
	}
	return Usagef("item %q: "+invalidSeatName, itemID, seat)
}

// checkCriteria refuses a criterion id that appears twice: the table's primary key
// would refuse it too, but as a write failure rather than as the rule it breaks.
func checkCriteria(itemID string, criteria []criterion) error {
	seen := make(map[string]bool, len(criteria))
	for i := range criteria {
		c := &criteria[i]
		if c.ClaimClass == "" {
			c.ClaimClass = ClaimVerification
		}
		if err := validateClaimClass(c.ClaimClass, itemID); err != nil {
			return err
		}
		if c.ClaimClass == ClaimVerification && ownerCriterion.MatchString(c.Text) {
			return Rejectionf("owner-criterion-untyped", itemID,
				"criterion %q records an owner decision but is verification-only: declare its claim class", c.CriterionID)
		}
		if c.CriterionID == "" {
			return Usagef("a criterion needs a criterion_id")
		}
		if seen[c.CriterionID] {
			return Rejectionf("criterion-duplicate", itemID, "criterion %q appears twice", c.CriterionID)
		}
		seen[c.CriterionID] = true
	}
	return nil
}

// checkAllowedPaths refuses a fence that fences nothing. An item naming no paths has no
// fence and is stored as such: staging is where a run needs one, so the absence is staged
// against rather than forbidden here. A wildcard, an absolute path or a path leaving the
// repository is refused in the store instead of at staging, because a stored `*` reads
// like a fence every later reader would trust.
func checkAllowedPaths(itemID string, paths []string) error {
	for _, path := range paths {
		switch {
		case path == "":
			return Rejectionf("fence-wildcard", itemID, "allowed_paths carries an empty entry: a fence is a list of repo-relative path prefixes, and an empty one is not a path")
		case strings.Contains(path, "*") || strings.Contains(path, "?"):
			return Rejectionf("fence-wildcard", itemID, "allowed_paths carries %q: a wildcard makes the fence admit paths it does not name, so a fence is spelled as path prefixes and never as a pattern", path)
		case path == "/" || path == "." || path == "..":
			return Rejectionf("fence-wildcard", itemID, "allowed_paths carries %q: it names the whole tree (or its parent) instead of a fence", path)
		case strings.HasPrefix(path, "/"):
			return Rejectionf("fence-wildcard", itemID, "allowed_paths carries the absolute path %q: a fence is repo-relative, so it means the same thing in every worktree", path)
		case strings.HasPrefix(path, "../") || strings.HasPrefix(path, "./"):
			return Rejectionf("fence-wildcard", itemID, "allowed_paths carries %q: a fence path is a plain repo-relative prefix, not a traversal", path)
		}
	}
	return nil
}

// encodeAllowedPaths is the column's value: the list as JSON text, so a path containing a
// space or a comma survives the round trip that a delimiter-separated string would not.
// The empty list and the absent list are the same value, which is what makes them the same
// refusal at staging.
func encodeAllowedPaths(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	body, err := json.Marshal(paths)
	if err != nil {
		return "", Internalf("encoding allowed_paths: %v", err)
	}
	return string(body), nil
}

// decodeAllowedPaths reads the column back. An empty value is no fence, not a malformed
// one: that is every item stored before the column existed.
func decodeAllowedPaths(stored string) ([]string, error) {
	if stored == "" {
		return nil, nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(stored), &paths); err != nil {
		return nil, Internalf("reading allowed_paths: %v", err)
	}
	return paths, nil
}

// itemExists reports whether the store holds this item.
func itemExists(ctx context.Context, r *store.Reader, id string) (bool, error) {
	var found int
	if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id = ?`, id).Scan(&found); err != nil {
		return false, Internalf("reading item %q: %v", id, err)
	}
	return found > 0, nil
}

// replaceCriteria writes the item's criteria as the record states them.
func replaceCriteria(ctx context.Context, tx *store.Tx, payload itemPayload) error {
	if _, err := tx.Exec(`DELETE FROM item_criteria WHERE item_id = ?`, payload.ItemID); err != nil {
		return Internalf("clearing the criteria: %v", err)
	}
	for _, c := range payload.Criteria {
		if _, err := tx.Exec(
			`INSERT INTO item_criteria (item_id, criterion_id, text, state, claim_class) VALUES (?, ?, ?, ?, ?)`,
			payload.ItemID, c.CriterionID, c.Text, c.State, c.ClaimClass,
		); err != nil {
			return Internalf("writing a criterion: %v", err)
		}
	}
	return nil
}

// replaceEdges writes the item's edges, refusing the three edge rules. The item's old
// edges are cleared first, so the cycle walk sees the graph as it will be rather than
// as it was: an item that drops an edge and adds the reverse of it is not a cycle.
func replaceEdges(ctx context.Context, tx *store.Tx, payload itemPayload) error {
	if _, err := tx.Exec(`DELETE FROM item_edges WHERE item_id = ?`, payload.ItemID); err != nil {
		return Internalf("clearing the edges: %v", err)
	}
	for _, blockedBy := range payload.BlockedBy {
		if blockedBy == payload.ItemID {
			return Rejectionf("edge-self", payload.ItemID, "item %q cannot be blocked by itself", payload.ItemID)
		}
		exists, err := itemExists(ctx, tx.Reader(), blockedBy)
		if err != nil {
			return err
		}
		if !exists {
			return Rejectionf("edge-target-missing", payload.ItemID,
				"item %q is blocked by %q, which does not exist", payload.ItemID, blockedBy)
		}
		reaches, err := reachesItem(ctx, tx.Reader(), blockedBy, payload.ItemID)
		if err != nil {
			return err
		}
		if reaches {
			return Rejectionf("edge-cycle", payload.ItemID,
				"item %q blocked by %q closes a cycle", payload.ItemID, blockedBy)
		}
		// The edge is written as it is validated, so a later edge in the same record is
		// checked against the graph this record is building and not only the stored one.
		if _, err := tx.Exec(`INSERT INTO item_edges (item_id, blocked_by) VALUES (?, ?)`, payload.ItemID, blockedBy); err != nil {
			return Internalf("writing an edge: %v", err)
		}
	}
	return nil
}

// reachesItem reports whether target is reachable from start by following blocked_by
// edges, which is what makes a new edge a cycle. The walk is breadth-first over the
// item's own table, so it terminates on a graph that is already acyclic and does not
// recurse on a deep chain.
func reachesItem(ctx context.Context, r *store.Reader, start, target string) (bool, error) {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true, nil
		}
		rows, err := r.Query(`SELECT blocked_by FROM item_edges WHERE item_id = ?`, current)
		if err != nil {
			return false, Internalf("reading the edges of %q: %v", current, err)
		}
		for rows.Next() {
			var next string
			if err := rows.Scan(&next); err != nil {
				rows.Close()
				return false, Internalf("reading an edge of %q: %v", current, err)
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, Internalf("reading the edges of %q: %v", current, err)
		}
		rows.Close()
	}
	return false, nil
}

// encodeWorkflowArgs answers the column's value: empty when the item supplies no arguments,
// otherwise the arguments as canonical JSON (object keys sorted). Arguments with no workflow
// to bind them are refused: nothing would read them, and the item would read as declared.
func encodeWorkflowArgs(p itemPayload) (string, error) {
	if len(p.WorkflowArgs) == 0 {
		return "", nil
	}
	if !WorkflowRefValid(p.Workflow) {
		return "", Rejectionf("workflow-args-without-workflow", p.ItemID, "workflow_args needs a workflow of the form <name>@v<N> to bind them")
	}
	raw, err := json.Marshal(p.WorkflowArgs)
	if err != nil {
		return "", Rejectionf("workflow-args-without-workflow", p.ItemID, "workflow_args cannot be encoded: %v", err)
	}
	return string(raw), nil
}

func sameWorkflowArgs(a, b itemPayload) bool {
	ra, _ := json.Marshal(a.WorkflowArgs)
	rb, _ := json.Marshal(b.WorkflowArgs)
	return len(a.WorkflowArgs) == len(b.WorkflowArgs) && string(ra) == string(rb)
}

// decodeWorkflowArgs reads the column back; empty is an item that supplies none.
func decodeWorkflowArgs(id, stored string) (map[string]any, error) {
	if stored == "" {
		return nil, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(stored), &args); err != nil {
		return nil, Internalf("reading item %q's workflow_args %q: %v", id, stored, err)
	}
	return args, nil
}
