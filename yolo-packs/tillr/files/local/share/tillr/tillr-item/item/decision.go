package item

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The closed vocabularies of a decision, taken from the monolith's decision.go and
// db.go rather than re-invented: a new module that spells `owner` as `human` makes
// every existing decision row unreadable to the new reader for no gain.
const (
	// ScopeGlobal is a decision that governs the whole board.
	ScopeGlobal = "global"
	// ScopeSubsystem is a decision that governs one named subsystem.
	ScopeSubsystem = "subsystem"
	// ScopeItems is a decision that governs a named set of items.
	ScopeItems = "items"

	// DecidedByOwner is a decision the owner made.
	DecidedByOwner = "owner"
	// DecidedByWhale is a decision the coordinating agent made.
	DecidedByWhale = "whale"

	// DecisionActive is a decision that binds.
	DecisionActive = "active"
	// DecisionSuperseded is a decision a later one replaced.
	DecisionSuperseded = "superseded"
	// DecisionRevoked is a decision withdrawn without a successor.
	DecisionRevoked = "revoked"
)

var (
	scopeKinds       = map[string]bool{ScopeGlobal: true, ScopeSubsystem: true, ScopeItems: true}
	decidedBys       = map[string]bool{DecidedByOwner: true, DecidedByWhale: true}
	decisionStatuses = map[string]bool{DecisionActive: true, DecisionSuperseded: true, DecisionRevoked: true}
)

// decisionScope is a decision's reach, typed. A free-text scope would be a paragraph,
// and a paragraph is not a record a reader can filter on.
type decisionScope struct {
	Kind string `json:"kind"`
	// Ref is the subsystem name, or the comma-separated item ids, and is empty for a
	// global decision.
	Ref string `json:"ref,omitempty"`
}

// decisionPayload is the decision.record payload: design §5.3's field list, plus the
// two fields the store sets and the input never carries.
type decisionPayload struct {
	DecisionID string        `json:"decision_id"`
	Text       string        `json:"text"`
	Scope      decisionScope `json:"scope"`
	DecidedBy  string        `json:"decided_by"`
	DecidedAt  string        `json:"decided_at,omitempty"`
	Status     string        `json:"status,omitempty"`
	Supersedes string        `json:"supersedes,omitempty"`
	SessionID  string        `json:"session_id"`
}

// decisionListPayload is the record `decision list` emits: one payload per decision,
// in decided-at order, so a caller can diff two lists.
type decisionListPayload struct {
	Decisions []decisionPayload `json:"decisions"`
}

// RecordDecision writes one decision row and appends its event in the same
// transaction, so a reader never sees the row without the log entry that explains it.
func RecordDecision(ctx context.Context, tx *store.Tx, env record.Envelope) (record.Envelope, error) {
	payload, err := decodeDecision(env)
	if err != nil {
		return record.Envelope{}, err
	}
	return writeDecision(ctx, tx, payload, "", env.CreatedAt)
}

// SupersedeDecision marks the named decision superseded and writes its successor with
// the link, in one transaction: a reader that saw the successor without the marked
// predecessor could not tell which decision binds.
func SupersedeDecision(ctx context.Context, tx *store.Tx, env record.Envelope, supersedes string) (record.Envelope, error) {
	payload, err := decodeDecision(env)
	if err != nil {
		return record.Envelope{}, err
	}
	if err := requireActiveDecision(ctx, tx.Reader(), supersedes); err != nil {
		return record.Envelope{}, err
	}
	if err := markDecision(ctx, tx, supersedes, DecisionSuperseded); err != nil {
		return record.Envelope{}, err
	}
	return writeDecision(ctx, tx, payload, supersedes, env.CreatedAt)
}

// RevokeDecision marks one decision revoked. Revoking is terminal: a second revoke is
// a negative decision with a rule, not a silent no-op that lets a caller believe it
// revoked something.
func RevokeDecision(ctx context.Context, tx *store.Tx, id string, at time.Time) (record.Envelope, error) {
	if err := requireActiveDecision(ctx, tx.Reader(), id); err != nil {
		return record.Envelope{}, err
	}
	if err := markDecision(ctx, tx, id, DecisionRevoked); err != nil {
		return record.Envelope{}, err
	}
	payload, err := readDecision(ctx, tx.Reader(), id)
	if err != nil {
		return record.Envelope{}, err
	}
	if err := appendDecisionEvent(ctx, tx, DecisionRevokeKind, id, payload, at); err != nil {
		return record.Envelope{}, err
	}
	return envelopeOf(DecisionRecordKind, "decision-"+id, at, payload)
}

// ListDecisions answers the decisions in one status. The default is the set that
// binds, because a caller that has to filter to find out what governs will not filter.
func ListDecisions(ctx context.Context, r *store.Reader, status string) (record.Envelope, error) {
	if status == "" {
		status = DecisionActive
	}
	if !decisionStatuses[status] {
		return record.Envelope{}, Usagef("%q is not a decision status: want active, superseded or revoked", status)
	}
	rows, err := r.Query(
		`SELECT decision_id, text, scope_kind, COALESCE(scope_ref, ''), decided_by, decided_at,
		        status, COALESCE(supersedes, ''), session_id
		   FROM decisions WHERE status = ? ORDER BY decided_at, decision_id`, status,
	)
	if err != nil {
		return record.Envelope{}, Internalf("reading the %s decisions: %v", status, err)
	}
	defer rows.Close()
	decisions := []decisionPayload{}
	for rows.Next() {
		payload, err := scanDecision(rows)
		if err != nil {
			return record.Envelope{}, err
		}
		decisions = append(decisions, payload)
	}
	if err := rows.Err(); err != nil {
		return record.Envelope{}, Internalf("reading the %s decisions: %v", status, err)
	}
	return envelopeOf(DecisionRecordKind, "decisions-"+status, time.Now().UTC(), decisionListPayload{Decisions: decisions})
}

// decodeDecision validates a new decision.record, not a stored decision. Unknown
// values are malformed input (exit 2); a retired decider or a scoped decision with
// no ref is a negative decision (exit 1). Legacy decisions remain readable targets.
func decodeDecision(env record.Envelope) (decisionPayload, error) {
	var payload decisionPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return decisionPayload{}, Usagef("reading the decision payload: %v", err)
	}
	if payload.DecisionID == "" {
		return decisionPayload{}, Usagef("a decision.record needs a decision_id")
	}
	if !scopeKinds[payload.Scope.Kind] {
		return decisionPayload{}, Usagef("%q is not a scope kind: want one of global, subsystem, items", payload.Scope.Kind)
	}
	if payload.Scope.Kind != ScopeGlobal && payload.Scope.Ref == "" {
		return decisionPayload{}, Rejectionf("scope-ref-required", payload.DecisionID,
			"a %s decision needs a scope ref: it governs nothing without one", payload.Scope.Kind)
	}
	if payload.DecidedBy == "autoqa" {
		return decisionPayload{}, Rejectionf("actor-retired", payload.DecisionID,
			"decider %q is retired: write %q or %q for the seat that made the decision instead",
			payload.DecidedBy, DecidedByOwner, DecidedByWhale)
	}
	if !decidedBys[payload.DecidedBy] {
		return decisionPayload{}, Usagef("%q is not a decider: want owner or whale", payload.DecidedBy)
	}
	return payload, nil
}

// writeDecision inserts one active decision row, appends its event and answers the
// record as stored.
func writeDecision(ctx context.Context, tx *store.Tx, payload decisionPayload, supersedes string, at time.Time) (record.Envelope, error) {
	payload.Status = DecisionActive
	payload.Supersedes = supersedes
	payload.DecidedAt = at.UTC().Format(time.RFC3339)
	if _, err := tx.Exec(
		`INSERT INTO decisions
		   (decision_id, text, scope_kind, scope_ref, decided_by, decided_at, status, supersedes, session_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		payload.DecisionID, payload.Text, payload.Scope.Kind, payload.Scope.Ref, payload.DecidedBy,
		at.UnixMilli(), payload.Status, nullText(supersedes), payload.SessionID,
	); err != nil {
		return record.Envelope{}, Internalf("writing decision %q: %v", payload.DecisionID, err)
	}
	if err := appendDecisionEvent(ctx, tx, DecisionRecordKind, payload.DecisionID, payload, at); err != nil {
		return record.Envelope{}, err
	}
	return envelopeOf(DecisionRecordKind, "decision-"+payload.DecisionID, at, payload)
}

// requireActiveDecision refuses a decision that is not there and one that no longer
// binds: the two need different remedies, so they are different rules.
func requireActiveDecision(ctx context.Context, r *store.Reader, id string) error {
	var status string
	err := r.QueryRow(`SELECT status FROM decisions WHERE decision_id = ?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return Rejectionf("not-found", id, "no decision %q in this store", id)
	}
	if err != nil {
		return Internalf("reading decision %q: %v", id, err)
	}
	if status != DecisionActive {
		return Rejectionf("decision-not-active", id, "decision %q is %s, not active", id, status)
	}
	return nil
}

// markDecision writes one decision's new status.
func markDecision(ctx context.Context, tx *store.Tx, id, status string) error {
	if _, err := tx.Exec(`UPDATE decisions SET status = ? WHERE decision_id = ?`, status, id); err != nil {
		return Internalf("marking decision %q %s: %v", id, status, err)
	}
	return nil
}

// readDecision answers one stored decision row.
func readDecision(ctx context.Context, r *store.Reader, id string) (decisionPayload, error) {
	row := r.QueryRow(
		`SELECT decision_id, text, scope_kind, COALESCE(scope_ref, ''), decided_by, decided_at,
		        status, COALESCE(supersedes, ''), session_id
		   FROM decisions WHERE decision_id = ?`, id,
	)
	payload, err := scanDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return decisionPayload{}, Rejectionf("not-found", id, "no decision %q in this store", id)
	}
	return payload, err
}

// scanner is the one method *sql.Row and *sql.Rows share, so one scan serves the
// single-row and the list read.
type scanner interface {
	Scan(dest ...any) error
}

// scanDecision reads one decision row into its payload, with the stored clock
// rendered as the record's RFC 3339 spelling.
func scanDecision(row scanner) (decisionPayload, error) {
	var (
		payload   decisionPayload
		decidedAt int64
	)
	if err := row.Scan(
		&payload.DecisionID, &payload.Text, &payload.Scope.Kind, &payload.Scope.Ref,
		&payload.DecidedBy, &decidedAt, &payload.Status, &payload.Supersedes, &payload.SessionID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return decisionPayload{}, err
		}
		return decisionPayload{}, Internalf("reading a decision row: %v", err)
	}
	payload.DecidedAt = time.UnixMilli(decidedAt).UTC().Format(time.RFC3339)
	return payload, nil
}

// appendDecisionEvent appends one decision event inside the caller's transaction.
func appendDecisionEvent(ctx context.Context, tx *store.Tx, kind, id string, payload decisionPayload, at time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return Internalf("encoding the %s event: %v", kind, err)
	}
	if _, err := tx.Append(event.Event{
		Kind:       kind,
		ObjectType: "decision",
		ObjectID:   &id,
		Payload:    body,
		At:         at,
	}); err != nil {
		return Internalf("appending the %s event: %v", kind, err)
	}
	return nil
}

// nullText answers a nullable text column: an empty string is no value, not a value
// that happens to be empty, and the supersedes column is a foreign key that would
// refuse one.
func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
