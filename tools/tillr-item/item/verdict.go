package item

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The actor kinds a verdict may be recorded under, in board-utilities.md §5.3's closed
// vocabulary: the item record's own. agent-runtime.md §12 spells the middle one "gate"
// because it is describing a gate's command verdict; the record owner's spelling is the
// one written here, and a mechanical verdict is exactly that gate verdict — its evidence
// carries it, so it needs neither a judge nor a grant.
const (
	// ActorMechanical is the composed pipeline retiring what it landed: the landed
	// attempt and the gate verdict that admitted it, with no agent judging anything.
	ActorMechanical = "mechanical"
	// ActorAgent is a delegated sign-off: an actor other than the one whose work is
	// judged, judging under a recorded grant.
	ActorAgent = "agent"
	// ActorHuman is a person issuing the verdict explicitly.
	ActorHuman = "human"
)

// The four verdicts an item's criterion can be judged under. Only a verdict retires an
// item, which is why this is the vocabulary a criterion's state is written in.
const (
	VerdictDone      = "done"
	VerdictNotDone   = "not-done"
	VerdictBlocked   = "blocked"
	VerdictDefective = "defective"
)

var (
	actorKinds    = map[string]bool{ActorMechanical: true, ActorAgent: true, ActorHuman: true}
	verdictValues = map[string]bool{VerdictDone: true, VerdictNotDone: true, VerdictBlocked: true, VerdictDefective: true}
)

// verdictPayload is the item.verdict payload: board-utilities.md §5.3's field list —
// item, criterion hash, verdict, reason, the landed refs and evidence pointers it read,
// the evaluated head and its actor kind — plus the two identities the independence rule
// needs, the grant a delegated verdict was recorded under, and the clock the store sets.
type verdictPayload struct {
	ItemID        string   `json:"item_id"`
	CriterionID   string   `json:"criterion_id"`
	CriterionHash string   `json:"criterion_hash"`
	Verdict       string   `json:"verdict"`
	ClaimClass    string   `json:"claim_class"`
	Reason        string   `json:"reason"`
	Landed        []string `json:"landed,omitempty"`
	Evidence      []string `json:"evidence"`
	EvaluatedHead string   `json:"evaluated_head,omitempty"`
	ActorKind     string   `json:"actor_kind"`
	Actor         string   `json:"actor,omitempty"`
	JudgedActor   string   `json:"judged_actor,omitempty"`
	GrantID       string   `json:"grant_id,omitempty"`
	RecordedAt    string   `json:"recorded_at,omitempty"`
}

// CriterionHash is the binding a verdict names: the SHA-256 of the criterion text the
// store froze, in lowercase hex. The store owns the spelling because the store owns the
// freeze — a verdict naming any other value is a verdict about a criterion this store
// does not hold.
func CriterionHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// RecordVerdict is the one writer of a criterion's state. It binds the verdict to the
// criterion text the store froze, refuses a verdict no evidence or no independent actor
// stands behind, refuses a delegated verdict whose grant is absent, expired or out of
// scope, and only then moves the criterion's state and appends the event that records
// why — all in the caller's transaction, so a refused verdict moves nothing.
func RecordVerdict(ctx context.Context, tx *store.Tx, env record.Envelope) (record.Envelope, error) {
	payload, err := decodeVerdict(env)
	if err != nil {
		return record.Envelope{}, err
	}
	text, class, err := frozenCriterion(ctx, tx.Reader(), payload.ItemID, payload.CriterionID)
	if err != nil {
		return record.Envelope{}, err
	}
	if payload.ClaimClass != class {
		return record.Envelope{}, Rejectionf("claim-class-mismatch", payload.ItemID,
			"criterion %q is stored as %q, but this verdict claims %q", payload.CriterionID, class, payload.ClaimClass)
	}
	if payload.ActorKind == ActorMechanical && class != ClaimVerification || payload.ActorKind == ActorAgent && class == ClaimOwnerOnly {
		return record.Envelope{}, Rejectionf("claim-class-actor", payload.ItemID,
			"actor kind %q cannot judge criterion %q with stored class %q", payload.ActorKind, payload.CriterionID, class)
	}
	if hash := CriterionHash(text); payload.CriterionHash != hash {
		return record.Envelope{}, Rejectionf("criterion-hash", payload.ItemID,
			"criterion %q of item %q is frozen as %q, which hashes to %s: this verdict names %s",
			payload.CriterionID, payload.ItemID, text, hash, payload.CriterionHash)
	}
	if len(payload.Evidence) == 0 {
		return record.Envelope{}, Rejectionf("verdict-evidence", payload.ItemID,
			"a verdict records the evidence it read as pointers to records: this %s verdict names none",
			payload.ActorKind)
	}
	if err := checkIndependence(payload); err != nil {
		return record.Envelope{}, err
	}
	if err := checkDelegation(ctx, tx.Reader(), payload, env.CreatedAt); err != nil {
		return record.Envelope{}, err
	}
	if _, err := tx.Exec(
		`UPDATE item_criteria SET state = ? WHERE item_id = ? AND criterion_id = ?`,
		payload.Verdict, payload.ItemID, payload.CriterionID,
	); err != nil {
		return record.Envelope{}, Internalf("recording the verdict on criterion %q: %v", payload.CriterionID, err)
	}
	payload.RecordedAt = env.CreatedAt.UTC().Format(time.RFC3339)
	if err := appendVerdictEvent(ctx, tx, payload, env.CreatedAt); err != nil {
		return record.Envelope{}, err
	}
	id := env.ID
	if id == "" {
		id = "verdict-" + payload.ItemID + "-" + payload.CriterionID
	}
	return envelopeOf(VerdictRecordKind, id, env.CreatedAt, payload)
}

// decodeVerdict reads and validates a new item.verdict payload. Unknown values and
// missing fields are malformed input (exit 2); retired AutoQA values are negative
// decisions (exit 1) with replacements. Existing events are never decoded here.
// The binding, evidence, independence and grant checks need the store to answer them.
func decodeVerdict(env record.Envelope) (verdictPayload, error) {
	var payload verdictPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return verdictPayload{}, Usagef("reading the verdict payload: %v", err)
	}
	if payload.ItemID == "" {
		return verdictPayload{}, Usagef("an item.verdict needs an item_id")
	}
	if payload.CriterionID == "" {
		return verdictPayload{}, Usagef("a verdict on item %q needs a criterion_id", payload.ItemID)
	}
	if !verdictValues[payload.Verdict] {
		return verdictPayload{}, Usagef("%q is not a verdict: want one of done, not-done, blocked, defective", payload.Verdict)
	}
	if payload.ActorKind == "autoqa" {
		return verdictPayload{}, Rejectionf("actor-retired", payload.ItemID,
			"actor kind %q is retired: write %q for a runner or %q under a recorded grant for a reviewer, with the AutoQA identity in actor",
			payload.ActorKind, ActorMechanical, ActorAgent)
	}
	if err := validateClaimClass(payload.ClaimClass, payload.ItemID); err != nil {
		return verdictPayload{}, err
	}
	if !actorKinds[payload.ActorKind] {
		return verdictPayload{}, Usagef("%q is not an actor kind: want one of %s, %s, %s",
			payload.ActorKind, ActorMechanical, ActorAgent, ActorHuman)
	}
	if payload.ActorKind == ActorAgent {
		if payload.Actor == "" {
			return verdictPayload{}, Usagef("an agent-issued verdict needs an actor: a verdict with no author is not auditable")
		}
		if payload.JudgedActor == "" {
			return verdictPayload{}, Usagef("an agent-issued verdict needs a judged_actor: it records whose work the criterion judges")
		}
	}
	return payload, nil
}

// frozenCriterion answers the criterion text the store froze, refusing an item or a
// criterion the store does not hold: a verdict is about a criterion that exists, and a
// store that invented one to answer against would bind nothing.
func frozenCriterion(ctx context.Context, r *store.Reader, itemID, criterionID string) (string, string, error) {
	exists, err := itemExists(ctx, r, itemID)
	if err != nil {
		return "", "", err
	}
	if !exists {
		return "", "", Rejectionf("not-found", itemID, "no item %q in this store", itemID)
	}
	var text, class string
	err = r.QueryRow(
		`SELECT text, claim_class FROM item_criteria WHERE item_id = ? AND criterion_id = ?`, itemID, criterionID,
	).Scan(&text, &class)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", Rejectionf("criterion-missing", itemID,
			"item %q carries no criterion %q: a verdict names the criterion the store froze", itemID, criterionID)
	}
	if err != nil {
		return "", "", Internalf("reading criterion %q of item %q: %v", criterionID, itemID, err)
	}
	return text, class, nil
}

// checkIndependence is the independence rule for a delegated verdict: the judge is never
// the actor whose work is judged (agent-runtime.md §12, "the independence rule is neither
// waived nor widened"; §14 states it for sign-off). A mechanical verdict is exempt
// because its evidence, not its actor, is what carries it, and a human verdict is exempt
// because the rule is about delegation — a person signing off does not need a judge.
func checkIndependence(v verdictPayload) error {
	if v.ActorKind != ActorAgent {
		return nil
	}
	if v.Actor == v.JudgedActor {
		return Rejectionf("self-judging", v.ItemID,
			"actor %q issued this verdict on its own work: a delegated verdict needs an actor other than the one whose work criterion %q judges",
			v.Actor, v.CriterionID)
	}
	return nil
}

// checkDelegation is the store-enforced sign-off grant. A mechanical verdict needs none,
// a human verdict needs none; an agent's verdict counts only under a recorded grant
// that is present, unexpired, and names the class of
// claim the verdict makes — the three ways agent-runtime.md §12 says a delegation stops
// short of a sign-off.
func checkDelegation(ctx context.Context, r *store.Reader, v verdictPayload, at time.Time) error {
	if v.ActorKind != ActorAgent {
		return nil
	}
	if v.GrantID == "" {
		return Rejectionf("grant-absent", v.ItemID,
			"an agent-issued verdict records the grant it was recorded under: actor %q presents none", v.Actor)
	}
	g, present, err := readGrant(ctx, r, v.GrantID)
	if err != nil {
		return err
	}
	if !present {
		return Rejectionf("grant-absent", v.ItemID,
			"no grant %q in this store: an agent-issued verdict records the grant it was recorded under", v.GrantID)
	}
	if !at.UTC().Before(g.ExpiresAt) {
		return Rejectionf("grant-expired", v.ItemID,
			"grant %q from %q expired at %s: a verdict recorded after it does not satisfy a gate",
			g.GrantID, g.Grantor, g.ExpiresAt.Format(time.RFC3339))
	}
	if !g.covers(v.ClaimClass) {
		return Rejectionf("grant-scope", v.ItemID,
			"grant %q covers %s and this verdict claims %q: out of scope is an escalation, not a sign-off",
			g.GrantID, strings.Join(g.ClaimClasses, ", "), v.ClaimClass)
	}
	return nil
}

// appendVerdictEvent appends the verdict's event inside the caller's transaction. The
// event is the verdict's record: the criterion row holds the state it moved, and the log
// holds who moved it, on what evidence and under what grant.
func appendVerdictEvent(ctx context.Context, tx *store.Tx, payload verdictPayload, at time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return Internalf("encoding the %s event: %v", VerdictRecordKind, err)
	}
	id := payload.ItemID
	if _, err := tx.Append(event.Event{
		Kind:       VerdictRecordKind,
		ObjectType: "item",
		ObjectID:   &id,
		Payload:    body,
		At:         at,
	}); err != nil {
		return Internalf("appending the %s event: %v", VerdictRecordKind, err)
	}
	return nil
}
