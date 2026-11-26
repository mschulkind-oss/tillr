package storelog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// BoundaryCheckKind is one closed boundary bracket: what it covered and whether
	// anything moved outside its targets.
	BoundaryCheckKind = "boundary.check"
	// GateStartedKind records that the drain started a candidate's gate.
	GateStartedKind = "gate.started"
	// GateStoppedKind records that a started gate ended without a gate.verdict
	// because the drain stopped it (trunk conflict, loop stop, cancelled revert)
	// or the gate subprocess exited before producing a verdict.
	GateStoppedKind = "gate.stopped"
	// GatePlanKind is the plan the gate computed for a candidate.
	GatePlanKind = "gate.plan"
	// GateVerdictKind is the verdict the gate answered for it.
	GateVerdictKind = "gate.verdict"
	// RefusalKind is the loop's record of a candidate refused before the gate judged it.
	RefusalKind = "drain.refusal"
	// FixerAttachedKind retains a new red tip attached to an unresolved repair.
	FixerAttachedKind = "drain.fixer-attached"
	// TrunkFailureLinkedKind records a trunk proof failure linked to an open item
	// whose title or words already name that check's test as the thing it fixes,
	// instead of filing a duplicate trunk fixer.
	TrunkFailureLinkedKind = "drain.trunk-failure-linked"
	// HeavyFaultKind records an attempt that faulted before the tip had a verdict.
	HeavyFaultKind = "drain.heavy-fault"
	// FastGateTimingKind records one measured ordinary fast-gate invocation.
	FastGateTimingKind = "drain.fast-gate-timing"
)

const (
	// BoundaryScopeRun is one run's bracket.
	BoundaryScopeRun = "run"
	// BoundaryScopeResume is one resume's bracket.
	BoundaryScopeResume = "resume"
)

const (
	GatePlanRecord    = "gate.plan.json"
	GateVerdictRecord = "gate.verdict.json"
)

const (
	GateRecordRead   = "read"
	GateRecordAbsent = "absent"
)

// MaxBoundaryObservations bounds how many observations one boundary.check names.
const MaxBoundaryObservations = 20

const (
	gateStallHeldKind      = "drain.gate-stall"
	stopGateHarnessError   = "gate-harness-error"
	reasonGateStalled      = "gate-stalled"
	refusalStaging         = "staging"
	refusalTrunkAlreadyRed = "trunk-already-red"
)

func init() {
	for _, kind := range []string{
		BoundaryCheckKind,
		GateStartedKind,
		GateStoppedKind,
		GatePlanKind,
		GateVerdictKind,
		RefusalKind,
		FixerAttachedKind,
		TrunkFailureLinkedKind,
		HeavyFaultKind,
		FastGateTimingKind,
	} {
		if err := event.Register(kind, drainmodel.Utility); err != nil {
			panic(err)
		}
	}
}

// RunRef is one run's place in a judgment: the three ids a reader correlates on.
type RunRef struct {
	RunID  string `json:"run_id"`
	ItemID string `json:"item_id"`
	LaneID string `json:"lane_id"`
}

// BoundaryViolation is one difference a bracket found, in the design's first_violation shape.
type BoundaryViolation struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Detail string `json:"detail"`
}

// BoundaryCheckPayload is the boundary.check payload.
type BoundaryCheckPayload struct {
	TickID         string              `json:"tick_id"`
	Scope          string              `json:"scope"`
	Clean          bool                `json:"clean"`
	Targets        []string            `json:"targets"`
	Runs           []RunRef            `json:"runs"`
	Violations     int                 `json:"violations"`
	FirstViolation *BoundaryViolation  `json:"first_violation,omitempty"`
	Observed       int                 `json:"observed,omitempty"`
	Observations   []BoundaryViolation `json:"observations,omitempty"`
	PatchMoves     []patchmoves.Move   `json:"patch_moves,omitempty"`
}

// RefusalCause is one countable shared-cause key recorded on a drain.refusal event.
type RefusalCause struct {
	Cause string `json:"cause"`
}

// GateJudgmentRecordError distinguishes a failure to record the gate's answer
// from a gate failure about a particular candidate.
type GateJudgmentRecordError struct{ Err error }

func (e *GateJudgmentRecordError) Error() string { return e.Err.Error() }
func (e *GateJudgmentRecordError) Unwrap() error { return e.Err }

// GateStartedPayload is the gate.started payload.
type GateStartedPayload struct {
	ItemID  string `json:"item_id"`
	RunID   string `json:"run_id"`
	LaneID  string `json:"lane_id"`
	BaseSHA string `json:"base_sha"`
}

// RecordGateStarted appends one gate.started event when the drain starts a candidate's gate.
func RecordGateStarted(ctx context.Context, storePath string, now func() time.Time, payload GateStartedPayload) (err error) {
	defer func() {
		if err != nil {
			err = &GateJudgmentRecordError{Err: err}
		}
	}()
	if storePath == "" || payload.RunID == "" {
		return nil
	}
	at := time.Now().UTC()
	if now != nil {
		at = now().UTC()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return drainmodel.Internalf("encoding the %s payload: %v", GateStartedKind, err)
	}
	st, err := store.Open(ctx, storePath, true)
	if err != nil {
		return drainmodel.Incompatiblef("%v", err)
	}
	defer st.Close()
	id := payload.RunID
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Append(event.Event{
			Kind: GateStartedKind, ObjectType: "run", ObjectID: &id, Payload: body, At: at,
		}); err != nil {
			return drainmodel.Internalf("appending the %s event: %v", GateStartedKind, err)
		}
		return nil
	})
}

// GateStoppedPayload is the gate.stopped payload.
type GateStoppedPayload struct {
	ItemID  string `json:"item_id"`
	RunID   string `json:"run_id"`
	LaneID  string `json:"lane_id"`
	BaseSHA string `json:"base_sha,omitempty"`
	Reason  string `json:"reason"`
}

// RecordGateStopped appends one gate.stopped event when a started gate ends without a verdict.
func RecordGateStopped(ctx context.Context, storePath string, now func() time.Time, payload GateStoppedPayload) {
	if storePath == "" || payload.RunID == "" {
		return
	}
	at := time.Now().UTC()
	if now != nil {
		at = now().UTC()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	st, err := store.Open(writeCtx, storePath, true)
	if err != nil {
		return
	}
	defer st.Close()
	id := payload.RunID
	_ = st.WriteTx(writeCtx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind: GateStoppedKind, ObjectType: "run", ObjectID: &id, Payload: body, At: at,
		})
		return err
	})
}

// RecordHeavyFault appends once per attempt, not once per commit.
func RecordHeavyFault(ctx context.Context, storePath, tip string, fault error, at time.Time) error {
	body, err := json.Marshal(map[string]string{"tip_sha": tip, "detail": fault.Error()})
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: HeavyFaultKind, ObjectType: "commit", ObjectID: &tip, Payload: body, At: at})
		return err
	})
}

type itemRefusalPayload struct {
	Step          string         `json:"step"`
	RefusalCauses []RefusalCause `json:"refusal_causes"`
	ItemID        string         `json:"item_id,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	Detail        string         `json:"detail,omitempty"`
}

// RecordItemRefusal appends the drain.refusal event for an item whose own record refused staging.
func RecordItemRefusal(ctx context.Context, storePath, itemID, reason, detail string, at time.Time) error {
	cause := "staging"
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		cause = "staging:" + trimmed
	}
	payload := itemRefusalPayload{
		Step:          refusalStaging,
		RefusalCauses: []RefusalCause{{Cause: cause}},
		ItemID:        itemID,
		Reason:        reason,
		Detail:        detail,
	}
	return AppendJudgment(ctx, storePath, RefusalKind, "item", itemID, payload, at)
}

// PayloadString answers one top-level string field of a payload, and "" when absent or not a string.
func PayloadString(payload json.RawMessage, key string) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return ""
	}
	raw, taken := fields[key]
	if !taken {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return text
}

// WithPayloadField adds one top-level field to a payload the gate wrote, refusing collisions.
func WithPayloadField(payload json.RawMessage, key, value string) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, drainmodel.Internalf("reading the gate payload to add %q: %v", key, err)
	}
	if _, taken := fields[key]; taken {
		return nil, drainmodel.Internalf("the gate record already carries a %q field, which the loop's span measurement also writes: one of the two has to be renamed", key)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, drainmodel.Internalf("encoding the gate payload field %q: %v", key, err)
	}
	fields[key] = body
	merged, err := json.Marshal(fields)
	if err != nil {
		return nil, drainmodel.Internalf("encoding the gate payload: %v", err)
	}
	return merged, nil
}

// GateStepObservation carries the Layer-1 fields observed of one gate step.
type GateStepObservation struct {
	TickID     string
	Run        RunRef
	Outcome    string
	Program    []string
	ExitCode   int
	DurationMS int64
}

// GateJudgmentPayload merges the gate's own record with what the loop observed.
func GateJudgmentPayload(root, kind string, obs GateStepObservation, name string) (json.RawMessage, error) {
	payload := map[string]json.RawMessage{}
	state := GateRecordAbsent
	path := filepath.Join(drainmodel.RunDir(root, obs.Run.RunID), name)
	body, err := os.ReadFile(path)
	switch {
	case err == nil:
		own, readErr := RecordPayload(body, kind)
		if readErr != nil {
			return nil, readErr
		}
		payload = own
		state = GateRecordRead
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, drainmodel.Externalf("reading the %s record %s: %v", kind, path, err)
	}
	observed := map[string]any{
		"run_id": obs.Run.RunID, "item_id": obs.Run.ItemID,
		"lane_id": obs.Run.LaneID, "tick_id": obs.TickID,
		"program": obs.Program, "exit_code": obs.ExitCode,
		"duration_ms": obs.DurationMS, "record_path": path, "record_state": state,
	}
	if state == GateRecordAbsent {
		observed["result"] = obs.Outcome
	}
	for key, value := range observed {
		if _, taken := payload[key]; taken {
			return nil, drainmodel.Internalf("the %s record at %s carries a %q field, which the loop's own measurement of the step also writes: one of the two has to be renamed", kind, path, key)
		}
		body, err := json.Marshal(value)
		if err != nil {
			return nil, drainmodel.Internalf("encoding the %s field %q: %v", kind, key, err)
		}
		payload[key] = body
	}
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, drainmodel.Internalf("encoding the %s payload: %v", kind, err)
	}
	return merged, nil
}

// RecordPayload reads one gate record and answers its payload as a field map.
func RecordPayload(body []byte, kind string) (map[string]json.RawMessage, error) {
	envelope, err := record.Decode(body)
	if err != nil {
		return nil, drainmodel.Internalf("reading the %s record: %v", kind, err)
	}
	if envelope.Kind != kind {
		return nil, drainmodel.Internalf("the record the loop read for %s is a %q", kind, envelope.Kind)
	}
	payload := map[string]json.RawMessage{}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, drainmodel.Internalf("reading the %s payload: %v", kind, err)
	}
	return payload, nil
}

// JudgmentSeen returns how many events of kind for (objectType, id) exist in the current epoch.
func JudgmentSeen(tx *store.Tx, kind, objectType, id string) (int, error) {
	var after int64
	if kind == GatePlanKind || kind == GateVerdictKind {
		var err error
		after, err = GateJudgmentEpoch(tx.Reader(), id)
		if err != nil {
			return 0, err
		}
	}
	var seen int
	err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_type=? AND object_id=? AND seq>?`, kind, objectType, id, after).Scan(&seen)
	return seen, err
}

// GateJudgmentEpoch returns the highest transition sequence authorizing a fresh gate examination.
func GateJudgmentEpoch(r *store.Reader, runID string) (int64, error) {
	var seq int64
	err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE
 (kind=? AND object_id=?) OR
 (kind=? AND json_extract(payload,'$.run_id')=? AND json_extract(payload,'$.reason') IN (?,?)) OR
 (kind=? AND object_id=? AND json_extract(payload,'$.kept_candidate')=1) OR
 (kind=? AND object_id=? AND json_extract(payload,'$.step')=?)`,
		gateStallHeldKind, runID,
		faults.ClearedKind, runID, stopGateHarnessError, reasonGateStalled,
		FixerAttachedKind, runID,
		RefusalKind, runID, refusalTrunkAlreadyRed,
	).Scan(&seq)
	return seq, err
}

// WriteJudgmentTx opens the store, checks JudgmentSeen, appends the event if unseen,
// and invokes after(tx, seen) inside the same write transaction.
func WriteJudgmentTx(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time, after func(tx *store.Tx, seen int) error) error {
	if storePath == "" {
		return drainmodel.Usagef("the loop cannot append %s: it was given no store", kind)
	}
	if objectID == "" {
		return drainmodel.Internalf("appending %s: the event names no object, so a replay cannot be told from a second judgment", kind)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return drainmodel.Internalf("encoding the %s payload: %v", kind, err)
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return drainmodel.Incompatiblef("%v", err)
	}
	defer s.Close()
	id := objectID
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		seen, err := JudgmentSeen(tx, kind, objectType, id)
		if err != nil {
			return err
		}
		if seen == 0 {
			if _, err := tx.Append(event.Event{
				Kind: kind, ObjectType: objectType, ObjectID: &id, Payload: body, At: at,
			}); err != nil {
				return drainmodel.Internalf("appending the %s event: %v", kind, err)
			}
		}
		if after != nil {
			return after(tx, seen)
		}
		return nil
	})
}

// AppendJudgment appends one event unless the store already holds an event of that kind about that object.
func AppendJudgment(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time) error {
	return AppendJudgmentThen(ctx, storePath, kind, objectType, objectID, payload, at, nil)
}

// AppendJudgmentThen appends one event if unseen and runs then(tx) only when newly appended.
func AppendJudgmentThen(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time, then func(*store.Tx) error) error {
	return WriteJudgmentTx(ctx, storePath, kind, objectType, objectID, payload, at, func(tx *store.Tx, seen int) error {
		if seen > 0 || then == nil {
			return nil
		}
		return then(tx)
	})
}
