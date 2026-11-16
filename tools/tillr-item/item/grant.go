package item

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The claim classes a delegated sign-off grant may name, in the spelling
// agent-runtime.md §12's "The default grant, until the owner rules" gives them: a
// verification-only claim is the class the default grant in force covers, and the other
// three are the classes it leaves escalating rather than signing. The
// vocabulary is closed because a free-text scope is a paragraph, and a paragraph is not a
// class a store can check a verdict against.
const (
	// ClaimVerification is a claim whose truth is settled by applying a named predicate
	// to recorded evidence, with no value choice left to make.
	ClaimVerification = "verification-only"
	// ClaimRedGateTriage is waiving or landing past a red gate: §13's triage sign-off.
	ClaimRedGateTriage = "red-gate-triage"
	// ClaimDirection is choosing between two defensible directions.
	ClaimDirection = "direction"
	// ClaimOwnerOnly is every class §28.1 reserves to the owner.
	ClaimOwnerOnly = "owner-only"
)

var claimClasses = map[string]bool{
	ClaimVerification: true, ClaimRedGateTriage: true, ClaimDirection: true, ClaimOwnerOnly: true,
}

// validateClaimClass checks new writes only; stored grants retain their original scope.
// Both verdicts and grants use the same writable vocabulary (design D-JL7).
func validateClaimClass(class, id string) error {
	if class == "qa-verification" {
		return Rejectionf("actor-retired", id,
			"claim class %q is retired: write %q for verification claims instead", class, ClaimVerification)
	}
	if !claimClasses[class] {
		return Usagef("%q is not a claim class: want one of %s, %s, %s, %s",
			class, ClaimVerification, ClaimRedGateTriage, ClaimDirection, ClaimOwnerOnly)
	}
	return nil
}

// unboundedScopes are the spellings that name every claim rather than a class, which
// §12 refuses as unbounded: an unbounded delegation is not a decision.
var unboundedScopes = map[string]bool{"all": true, "any": true, "everything": true, "*": true}

// grantPayload is the item.grant payload: the grant §12 settles — its scope as claim
// classes, its grantor and its expiry — plus the issue time the store sets.
type grantPayload struct {
	GrantID      string   `json:"grant_id"`
	ClaimClasses []string `json:"claim_classes"`
	Grantor      string   `json:"grantor"`
	IssuedAt     string   `json:"issued_at,omitempty"`
	ExpiresAt    string   `json:"expires_at"`
}

// IssueGrant records one delegated sign-off grant and appends its event in the same
// transaction, so a verdict can never cite a grant no reader can find. A grant that
// names every claim is refused as unbounded and one that has expired at its own issue
// time as licensing nothing: both are well-formed records that delegate nothing, which
// is a negative decision rather than malformed input.
func IssueGrant(ctx context.Context, tx *store.Tx, env record.Envelope) (record.Envelope, error) {
	payload, expiresAt, err := decodeGrant(env)
	if err != nil {
		return record.Envelope{}, err
	}
	issuedAt := env.CreatedAt.UTC()
	if !expiresAt.After(issuedAt) {
		return record.Envelope{}, Rejectionf("grant-expired", payload.GrantID,
			"grant %q expires at %s, at or before its issue time %s: it licenses nothing",
			payload.GrantID, expiresAt.Format(time.RFC3339), issuedAt.Format(time.RFC3339))
	}
	payload.IssuedAt = issuedAt.Format(time.RFC3339)
	payload.ExpiresAt = expiresAt.Format(time.RFC3339)
	if _, err := tx.Exec(
		`INSERT INTO item_grants (grant_id, claim_classes, grantor, issued_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		payload.GrantID, strings.Join(payload.ClaimClasses, ","), payload.Grantor,
		issuedAt.UnixMilli(), expiresAt.UnixMilli(),
	); err != nil {
		return record.Envelope{}, Internalf("writing grant %q: %v", payload.GrantID, err)
	}
	if err := appendGrantEvent(ctx, tx, payload, issuedAt); err != nil {
		return record.Envelope{}, err
	}
	return envelopeOf(GrantRecordKind, "grant-"+payload.GrantID, issuedAt, payload)
}

// decodeGrant reads and validates the item.grant payload. A missing field, an unknown
// claim class or an unparsable expiry is malformed input (exit 2); a scope that names
// every claim instead of a class is the unbounded grant §12 refuses (exit 1).
func decodeGrant(env record.Envelope) (grantPayload, time.Time, error) {
	var payload grantPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return grantPayload{}, time.Time{}, Usagef("reading the grant payload: %v", err)
	}
	if payload.GrantID == "" {
		return grantPayload{}, time.Time{}, Usagef("an item.grant needs a grant_id")
	}
	if payload.Grantor == "" {
		return grantPayload{}, time.Time{}, Usagef("grant %q needs a grantor: a delegation with nobody behind it is nobody's", payload.GrantID)
	}
	if payload.ExpiresAt == "" {
		return grantPayload{}, time.Time{}, Usagef("grant %q needs an expires_at: a grant with no end date is unbounded", payload.GrantID)
	}
	expiresAt, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		return grantPayload{}, time.Time{}, Usagef("grant %q's expires_at %q is not an RFC 3339 timestamp: %v", payload.GrantID, payload.ExpiresAt, err)
	}
	if len(payload.ClaimClasses) == 0 {
		return grantPayload{}, time.Time{}, Rejectionf("grant-unbounded", payload.GrantID,
			"grant %q names no claim class: an unbounded delegation is not a decision", payload.GrantID)
	}
	for _, class := range payload.ClaimClasses {
		if unboundedScopes[class] {
			return grantPayload{}, time.Time{}, Rejectionf("grant-unbounded", payload.GrantID,
				"grant %q names every claim (%q): a grant names the claim classes it covers, and one reading all claims is unbounded", payload.GrantID, class)
		}
		if err := validateClaimClass(class, payload.GrantID); err != nil {
			return grantPayload{}, time.Time{}, err
		}
	}
	return payload, expiresAt.UTC(), nil
}

// appendGrantEvent appends the grant's issue event inside the caller's transaction.
func appendGrantEvent(ctx context.Context, tx *store.Tx, payload grantPayload, at time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return Internalf("encoding the %s event: %v", GrantRecordKind, err)
	}
	id := payload.GrantID
	if _, err := tx.Append(event.Event{
		Kind:       GrantRecordKind,
		ObjectType: "grant",
		ObjectID:   &id,
		Payload:    body,
		At:         at,
	}); err != nil {
		return Internalf("appending the %s event: %v", GrantRecordKind, err)
	}
	return nil
}

// grant is one recorded grant, as the intake check reads it.
type grant struct {
	GrantID      string
	ClaimClasses []string
	Grantor      string
	ExpiresAt    time.Time
}

// covers reports whether the grant names this class of claim.
func (g grant) covers(class string) bool {
	for _, named := range g.ClaimClasses {
		if named == class {
			return true
		}
	}
	return false
}

// readGrant answers one stored grant, and false when the store holds none under that id.
func readGrant(ctx context.Context, r *store.Reader, id string) (grant, bool, error) {
	var (
		found   grant
		classes string
		expires int64
	)
	err := r.QueryRow(
		`SELECT grant_id, claim_classes, grantor, expires_at FROM item_grants WHERE grant_id = ?`, id,
	).Scan(&found.GrantID, &classes, &found.Grantor, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return grant{}, false, nil
	}
	if err != nil {
		return grant{}, false, Internalf("reading grant %q: %v", id, err)
	}
	found.ClaimClasses = strings.Split(classes, ",")
	found.ExpiresAt = time.UnixMilli(expires).UTC()
	return found, true, nil
}
