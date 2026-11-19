package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/patchmoves"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The three judgment events the loop appends, so a run reconstructs from the event log
// alone: the bracket it ran inside, the plan the gate computed for it, and the verdict the
// gate answered. They are the store's own kinds — `boundary.check` is the design's
// (session-recording.md §4.3), and `gate.plan` / `gate.verdict` are the kinds `tillr-gate`
// declares — recorded here because the loop is the one component that knows the run, the
// item, the lane and the tick a judgment belongs to.
//
// The gate's own records remain the artifacts they were: a `gate.plan.json` and a
// `gate.verdict.json` under `.tillr/loop/<run-id>/` are what the observatory reads by path,
// and the event carries the same fields rather than replacing the file.
const (
	// BoundaryCheckKind is one closed boundary bracket: what it covered and whether
	// anything moved outside its targets.
	BoundaryCheckKind = "boundary.check"
	// GatePlanKind is the plan the gate computed for a candidate.
	GatePlanKind = "gate.plan"
	// GateVerdictKind is the verdict the gate answered for it.
	GateVerdictKind = "gate.verdict"
	// RefusalKind is the loop's record of a candidate refused before the gate judged it:
	// the fence refused it, or its run produced no commit. The run's own record cannot say
	// which — an answered run that committed nothing and one that committed outside its
	// fence both exit 0 with a report — so the loop records the step, and the planner reads
	// it to name the item's hold.
	RefusalKind = "drain.refusal"
	// FixerAttachedKind retains a new red tip attached to an unresolved repair.
	FixerAttachedKind = "drain.fixer-attached"
)

// The two scopes a boundary bracket covers, as `boundary.check`'s payload names them. A
// run-scope bracket wraps one run (the failover re-dispatch), and a batch-scope bracket
// wraps every run a tick dispatched at once — which is why a batch event carries a list of
// runs rather than one: the writes inside it are one span and cannot be attributed to one
// run.
const (
	// BoundaryScopeRun is one run's bracket: it wraps `Steps.Run` and nothing else, so a
	// violation names the run that wrote it.
	BoundaryScopeRun = "run"
	// BoundaryScopeResume is one resume's bracket: a segment of a run that was continued,
	// taken at the resume rather than at the original dispatch. It is its own scope because a
	// reader asking "what did this run touch" has to see the segments, and a run killed after
	// its worker committed has a bracket that does not start where the dispatch's did.
	BoundaryScopeResume = "resume"
)

// The names the gate's own records have inside a run's directory. They are the file names
// `tillr-loop` writes and the observatory reads; this constant is the loop's read of the
// same path, not a second spelling of it.
const (
	GatePlanRecord    = "gate.plan.json"
	GateVerdictRecord = "gate.verdict.json"
)

// The two states a gate record can be in when the loop appends its judgment. A record the
// gate did not write is recorded as absent rather than left out: a step's judgment with no
// record beside it is a fact about that gate program, and an event that said nothing would
// be the hole this emitter exists to close.
const (
	GateRecordRead   = "read"
	GateRecordAbsent = "absent"
)

// init registers the kinds this package appends. It is here rather than in a face because
// the registry's content is the set of packages linked into a binary, which is what makes it
// checkable without running anything.
//
// The owner is `drain`: the gate authors the record and drain appends the event, and the
// registry's defect is two writers of one kind. Linking this package and `tillr-gate` into
// one binary would therefore be refused at init, which is the honest answer — the two
// spellings of one judgment would have to be reconciled first.
func init() {
	for _, kind := range []string{BoundaryCheckKind, GatePlanKind, GateVerdictKind, RefusalKind, judgeRequestKind, judgeLandedKind, RetryAckKind, DesignRetryKind, "drain.graph-completed", FixerAttachedKind, RevertKind, callOffered, callAnswered, callLanded, callDelivered, callClosed, endingCapabilitiesKind, piFeaturesKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

// runRef is one run's place in a judgment: the three ids a reader correlates on. It is an
// object rather than three parallel arrays so a batch's runs cannot be zipped wrongly.
type runRef struct {
	RunID  string `json:"run_id"`
	ItemID string `json:"item_id"`
	LaneID string `json:"lane_id"`
}

// boundaryViolation is one difference a bracket found, in the design's `first_violation`
// shape. Violation's Where is the worktree a path belongs to, which is what `target` names.
type boundaryViolation struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Detail string `json:"detail"`
}

// boundaryCheckPayload is the `boundary.check` payload: the design's `scope`, `clean` and
// `first_violation`, plus the correlation the event itself has no columns for — the tick and
// the runs the bracket covered.
type boundaryCheckPayload struct {
	TickID         string             `json:"tick_id"`
	Scope          string             `json:"scope"`
	Clean          bool               `json:"clean"`
	Targets        []string           `json:"targets"`
	Runs           []runRef           `json:"runs"`
	Violations     int                `json:"violations"`
	FirstViolation *boundaryViolation `json:"first_violation,omitempty"`
	// Observed counts the movements outside the run's lane the bracket saw but cannot
	// attribute to the run (CompareRunObserved), and Observations names the first of them
	// (at most maxBoundaryObservations). They are information, never a conviction: Clean
	// reads only Violations.
	Observed     int                 `json:"observed,omitempty"`
	Observations []boundaryViolation `json:"observations,omitempty"`
	// PatchMoves is uncapped: every unexpected patch ref delta holds publication,
	// but never faults the run. It is committed atomically with this bracket.
	PatchMoves []patchmoves.Move `json:"patch_moves,omitempty"`
}

// maxBoundaryObservations bounds how many observations one boundary.check names: a gc or
// a bulk branch deletion moves hundreds of refs, and the count says how many there were.
const maxBoundaryObservations = 20

// gateStepJudgment is what the loop observed of one gate step: which program it started, how
// it exited, how long it took, and the outcome that exit maps onto.
type gateStepJudgment struct {
	FirstStallHold *gateStallHold
	// TickID is the tick the judgment belongs to (plan's clock stamp and the run set).
	TickID string
	// Run is the candidate the gate judged.
	Run runRef
	// Outcome is the loop's reading of the exit: GatePassed, GateFailed or GateErrored.
	Outcome string
	// Program is the argv the step ran, exit code and duration its span.
	Program    []string
	ExitCode   int
	DurationMS int64
}

// recordBoundaryCheck appends one closed bracket's `boundary.check`. A bracket that found
// nothing appends the clean event too: the design's whole point is that a passing check
// leaves a record, and a stream that only logged violations could not distinguish a clean
// tick from one whose check never ran.
func (l *Loop) recordBoundaryCheck(ctx context.Context, scope, passID string, runs []runRef, targets []string, violations []Violation, at time.Time, observed ...Violation) error {
	objectID := passID
	if len(runs) == 1 {
		if scope == BoundaryScopeRun {
			objectID = runs[0].RunID
		}
		if scope == BoundaryScopeResume {
			objectID = runs[0].RunID + "-resume"
		}
	}
	return appendJudgment(ctx, l.cfg.Store, BoundaryCheckKind, scope, objectID, boundaryCheckPayloadOf(scope, passID, runs, targets, violations, observed), at)
}

// appendBoundaryCheck appends one closed bracket whose owner is not the loop: a resume's
// bracket, keyed by the segment it covers (<run-id>@<stamp>) so two real resumes of one run
// are two events and a replayed append of one is still one.
func appendBoundaryCheck(ctx context.Context, storePath, scope, segmentID, runID string, row resumeRun, targets []string, violations []Violation, at time.Time, observed ...Violation) error {
	refs := []runRef{{RunID: runID, ItemID: row.ItemID, LaneID: row.LaneID}}
	return appendJudgment(ctx, storePath, BoundaryCheckKind, scope, segmentID, boundaryCheckPayloadOf(scope, segmentID, refs, targets, violations, observed), at)
}

// boundaryCheckPayloadOf is the payload both bracket writers share, so a resume's event and
// a tick's cannot disagree about its shape.
func boundaryCheckPayloadOf(scope, passID string, runs []runRef, targets []string, violations, observed []Violation) boundaryCheckPayload {
	payload := boundaryCheckPayload{
		TickID: passID, Scope: scope, Clean: len(violations) == 0,
		Targets: targets, Runs: runs, Violations: len(violations),
	}
	if len(violations) > 0 {
		payload.FirstViolation = &boundaryViolation{
			Kind: violations[0].Kind, Target: violations[0].Where, Detail: violations[0].Detail,
		}
	}
	payload.Observed = len(observed)
	for _, o := range observed {
		if o.Kind == "ref" && strings.HasPrefix(o.Ref, "refs/heads/patch/") {
			payload.PatchMoves = append(payload.PatchMoves, patchmoves.Move{Ref: o.Ref, Old: o.Old, New: o.New})
		}
	}
	for i, o := range observed {
		if i == maxBoundaryObservations {
			break
		}
		payload.Observations = append(payload.Observations, boundaryViolation{Kind: o.Kind, Target: o.Where, Detail: o.Detail})
	}
	return payload
}

// refusalPayload is the `drain.refusal` payload: the step that refused the candidate, and
// the correlation a reader needs to find the item, lane and tick it belongs to. Step is the
// one field the planner reads; the rest are what a reader of `tillr view events` needs to
// follow the event back to its run.
type refusalPayload struct {
	WorkerSession *WorkerSession `json:"worker_session,omitempty"`
	Step          string         `json:"step"`
	ItemID        string         `json:"item_id,omitempty"`
	LaneID        string         `json:"lane_id,omitempty"`
	TickID        string         `json:"tick_id,omitempty"`
	// RedSHA, FailingChecks and FixerItem are the trunk-already-red refusal's own fields:
	// the sha the replay found red, the checks that were red there, and the one fixer item
	// the item is now held behind. They are empty on every other refusal, so the event's
	// shape stays one struct rather than a second kind.
	RedSHA        string   `json:"red_sha,omitempty"`
	FailingChecks []string `json:"failing_checks,omitempty"`
	FixerItem     string   `json:"fixer_item,omitempty"`
	// DefectItem is the tooling-defect refusal's own field: the generated item the run's
	// tooling obstacle filed or attached to, which the item is now held behind.
	DefectItem string `json:"defect_item,omitempty"`
	// ConflictPaths and FixRunID are the squash-conflict refusal's own fields: the
	// CONFLICT lines the lander recorded, and the one conflict-fix run staged to answer
	// them. FixRunID is empty on the second conflict, which is the exhausted hold rather
	// than a second fix request.
	ConflictPaths []string `json:"conflict_paths,omitempty"`
	FixRunID      string   `json:"fix_run_id,omitempty"`
	// Reason and Detail are the staging refusal's own fields: the hold reason the loop
	// records for an item whose own content could not be staged, and the refusal's message,
	// which names the field an operator has to edit. No run exists for that refusal, so the
	// event is keyed by the item rather than by a run.
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	// A returned design gate refusal binds the exact pinned checkout and its fence base.
	DesignBaseSHA string `json:"design_base_sha,omitempty"`
	DesignHeadSHA string `json:"design_head_sha,omitempty"`
	// Checks and Evidence are a returned ending's own fields (returned.go): the gate checks
	// that failed, with their logs and normalized digests, and the ending's evidence key,
	// which the identical-repeat rule compares. A refusal recorded before 2026-11-17 carries
	// neither, and its empty key repeats nothing.
	Checks   []EndingCheck `json:"checks,omitempty"`
	Evidence string        `json:"evidence,omitempty"`
	// Selectors are a reverted landing's legacy replay selectors (kind:label) by check
	// id, kept so the item's next gate can run a check a loop restart forgot.
	Selectors map[string]string `json:"selectors,omitempty"`
}

// recordRefusal appends one `drain.refusal`, idempotently by the run it names: a step
// retried after a half-completed tick finds the first append and writes nothing.
func (l *Loop) recordRefusal(ctx context.Context, candidate Candidate, step string) error {
	payload := refusalPayload{
		Detail: candidate.RefusalDetail,
		Step:   step,
		ItemID: candidate.ItemID,
		LaneID: candidate.LaneID,
		TickID: candidate.TickID,
	}
	if step == RefusalGate && candidate.BlockConflict != nil && candidate.BlockConflict.TriageVersion == "" {
		// Do not publish continuation feedback before its candidate is reachable.
		if err := l.pin(ctx, candidate.RunID, candidate.HeadSHA); err != nil {
			return err
		}
		payload.DesignBaseSHA, payload.DesignHeadSHA = candidate.BaseSHA, candidate.HeadSHA
	}
	if returnedCauses[step] {
		payload.Checks, payload.Evidence = l.endingEvidence(ctx, candidate, step)
		if step != RefusalVerifyNotDone {
			var err error
			payload.WorkerSession, err = l.captureWorkerSession(ctx, candidate)
			if err != nil {
				return err
			}
		}
	}
	repeated, err := l.landingRefusalRepeats(ctx, candidate, payload)
	if err != nil {
		return err
	}
	at := l.cfg.Now().UTC()
	evidence := l.readRefusalEvidence(candidate.RunID, payload)
	return appendJudgmentThen(ctx, l.cfg.Store, RefusalKind, "run", candidate.RunID, payload, at, func(tx *store.Tx) error {
		if repeated {
			return nil
		}
		return l.answerLandingRefusal(tx, candidate, payload, evidence, at)
	})
}

// recordItemRefusal appends the `drain.refusal` for an item whose own record refused
// staging. There is no run to key it by — nothing was launched and nothing was spent — so
// the event is keyed by the item, and returning the same item's record to staging is the
// same refusal again rather than a second event.
func (l *Loop) recordItemRefusal(ctx context.Context, itemID, reason, detail string) error {
	return appendJudgment(ctx, l.cfg.Store, RefusalKind, "item", itemID, refusalPayload{
		Step:   RefusalStaging,
		ItemID: itemID,
		Reason: reason,
		Detail: detail,
	}, l.cfg.Now().UTC())
}

// recordGateJudgment appends the gate step's plan and verdict events, in that order: the plan
// the gate computed and then the verdict it answered, each carrying its own record's fields
// plus what the loop observed of the step.
//
// The verdict's transaction is also the spans' transaction. `gate.spans.json` is the gate's
// measurement of its own checks, and the rows that land in `gate_spans` are inserted beside
// the verdict event rather than after it: a verdict recorded with no spans and a spans set
// recorded with no verdict are both a record that disagrees with itself. A spans file that is
// missing or unparseable is not a gate failure — the verdict is the judgment, and the row
// insert degrades to zero rows with `spans_state` saying which of the two happened.
func (s *ProcessSteps) recordGateJudgment(ctx context.Context, candidate Candidate, judgment gateStepJudgment) error {
	if s.Root == "" {
		return nil
	}
	at := s.Now().UTC()
	planPayload, err := gateJudgmentPayload(s.Root, GatePlanKind, judgment, GatePlanRecord)
	if err != nil {
		return err
	}
	if err := appendJudgment(ctx, s.Store, GatePlanKind, "run", candidate.RunID, planPayload, at); err != nil {
		return err
	}
	verdictPayload, err := gateJudgmentPayload(s.Root, GateVerdictKind, judgment, GateVerdictRecord)
	if err != nil {
		return err
	}
	verdictID := payloadString(verdictPayload, "verdict_id")
	epoch, err := s.gateJudgmentEpoch(ctx, candidate.RunID)
	if err != nil {
		return err
	}
	spans, spansState := readGateSpans(s.Root, candidate.RunID, verdictID, candidate.TickID)
	executionID := verdictID
	if epoch > 0 {
		executionID = fmt.Sprintf("%s/after-%d", verdictID, epoch)
		for i := range spans {
			spans[i].SpanID = gateSpanID(executionID, spans[i].CheckID)
			spans[i].ParentSpanID = gateParentSpanID(executionID)
		}
	}
	verdictPayload, err = withPayloadField(verdictPayload, "gate_execution_id", executionID)
	if err != nil {
		return err
	}
	verdictPayload, err = withPayloadField(verdictPayload, "spans_state", spansState)
	if err != nil {
		return err
	}
	return appendJudgmentWithSpans(ctx, s.Store, GateVerdictKind, "run", candidate.RunID,
		verdictPayload, at, spans, judgment.FirstStallHold)
}

// payloadString answers one top-level string field of a payload, and "" when it is absent
// or not a string. It is a read of the gate's own record, never a second statement of it.
func payloadString(payload json.RawMessage, key string) string {
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

// withPayloadField adds one top-level field to a payload the gate wrote. A field the record
// already carries is refused rather than overwritten, the same rule `gateJudgmentPayload`
// applies to the loop's own observations: one of the two has to be renamed.
func withPayloadField(payload json.RawMessage, key, value string) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, Internalf("reading the gate payload to add %q: %v", key, err)
	}
	if _, taken := fields[key]; taken {
		return nil, Internalf("the gate record already carries a %q field, which the loop's span measurement also writes: one of the two has to be renamed", key)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, Internalf("encoding the gate payload field %q: %v", key, err)
	}
	fields[key] = body
	merged, err := json.Marshal(fields)
	if err != nil {
		return nil, Internalf("encoding the gate payload: %v", err)
	}
	return merged, nil
}

// gateJudgmentPayload merges the gate's own record with what the loop observed. The record's
// fields stay at the payload's top level, so `result`, `plan_id` and `checks` are where a
// reader of the design's field list looks for them; a key the record already carries is
// never overwritten, because a record that grew a field named `exit_code` would otherwise
// have the gate's own answer replaced by the loop's silently.
func gateJudgmentPayload(root, kind string, judgment gateStepJudgment, name string) (json.RawMessage, error) {
	payload := map[string]json.RawMessage{}
	state := GateRecordAbsent
	path := filepath.Join(RunDir(root, judgment.Run.RunID), name)
	body, err := os.ReadFile(path)
	switch {
	case err == nil:
		own, readErr := recordPayload(body, kind)
		if readErr != nil {
			return nil, readErr
		}
		payload = own
		state = GateRecordRead
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, Externalf("reading the %s record %s: %v", kind, path, err)
	}
	observed := map[string]any{
		"run_id": judgment.Run.RunID, "item_id": judgment.Run.ItemID,
		"lane_id": judgment.Run.LaneID, "tick_id": judgment.TickID,
		"program": judgment.Program, "exit_code": judgment.ExitCode,
		"duration_ms": judgment.DurationMS, "record_path": path, "record_state": state,
	}
	if state == GateRecordAbsent {
		// A gate that wrote no record still judged the candidate: the loop's reading of its
		// exit is the only result there is, so it is recorded rather than left to the
		// reader's inference from the exit code.
		observed["result"] = judgment.Outcome
	}
	for key, value := range observed {
		if _, taken := payload[key]; taken {
			return nil, Internalf("the %s record at %s carries a %q field, which the loop's own measurement of the step also writes: one of the two has to be renamed", kind, path, key)
		}
		body, err := json.Marshal(value)
		if err != nil {
			return nil, Internalf("encoding the %s field %q: %v", kind, key, err)
		}
		payload[key] = body
	}
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, Internalf("encoding the %s payload: %v", kind, err)
	}
	return merged, nil
}

// recordPayload reads one gate record and answers its payload as the field map the event
// merges into. A record of another kind is refused: the two files differ by one word and a
// plan appended as a verdict is exactly the kind of silent substitution the store's kinds
// exist to prevent.
func recordPayload(body []byte, kind string) (map[string]json.RawMessage, error) {
	envelope, err := record.Decode(body)
	if err != nil {
		return nil, Internalf("reading the %s record: %v", kind, err)
	}
	if envelope.Kind != kind {
		return nil, Internalf("the record the loop read for %s is a %q", kind, envelope.Kind)
	}
	payload := map[string]json.RawMessage{}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, Internalf("reading the %s payload: %v", kind, err)
	}
	return payload, nil
}

// appendJudgment appends one event unless the store already holds an event of that kind about
// that object.
//
// The idempotency is the object. A judgment is one event about one object — a run for
// `gate.plan` and `gate.verdict`, a tick (or a run) for `boundary.check` — so a step that runs
// twice, or a tick that is replayed, finds the first append and writes nothing. The check and
// the append are one transaction, so two callers cannot both find nothing and both write; and
// the events table stays append-only, so the guard costs a read rather than an update.
func appendJudgment(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time) error {
	return appendJudgmentThen(ctx, storePath, kind, objectType, objectID, payload, at, nil)
}

func appendJudgmentThen(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time, then func(*store.Tx) error) error {
	if storePath == "" {
		return Usagef("the loop cannot append %s: it was given no store", kind)
	}
	if objectID == "" {
		return Internalf("appending %s: the event names no object, so a replay cannot be told from a second judgment", kind)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Internalf("encoding the %s payload: %v", kind, err)
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	id := objectID
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		seen, err := judgmentSeen(tx, kind, objectType, id)
		if err != nil {
			return err
		}
		if seen > 0 {
			return nil
		}
		if _, err := tx.Append(event.Event{
			Kind: kind, ObjectType: objectType, ObjectID: &id, Payload: body, At: at,
		}); err != nil {
			return Internalf("appending the %s event: %v", kind, err)
		}
		if then != nil {
			return then(tx)
		}
		return nil
	})
}

// appendJudgmentWithSpans appends one judgment event and inserts its gate spans in the
// same transaction. The event's idempotency guard is the one `appendJudgment` applies; the
// span insert is idempotent by `span_id`, so a replayed verdict rewrites the same rows
// rather than failing or duplicating them, and a verdict whose first attempt recorded no
// spans gains them on the replay.
//
// The rows are inserted whether the event was new or already present: a store that holds
// the verdict and no spans is a half-record, and the replay is the one chance to complete
// it. A failed insert fails the transaction, so the loop stops with both unrecorded rather
// than carrying on as if the verdict's measurement were there.
func appendJudgmentWithSpans(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time, spans []gateSpanRow, firstStall *gateStallHold) error {
	if storePath == "" {
		return Usagef("the loop cannot append %s: it was given no store", kind)
	}
	if objectID == "" {
		return Internalf("appending %s: the event names no object, so a replay cannot be told from a second judgment", kind)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Internalf("encoding the %s payload: %v", kind, err)
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	id := objectID
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		seen, err := judgmentSeen(tx, kind, objectType, id)
		if err != nil {
			return err
		}
		if seen == 0 {
			if _, err := tx.Append(event.Event{
				Kind: kind, ObjectType: objectType, ObjectID: &id, Payload: body, At: at,
			}); err != nil {
				return Internalf("appending the %s event: %v", kind, err)
			}
		}
		if seen == 0 && firstStall != nil {
			if err := recordGateStallHold(tx, *firstStall, at); err != nil {
				return err
			}
		}
		for _, span := range spans {
			if _, err := tx.Exec(
				`INSERT INTO gate_spans (span_id, trace_id, parent_span_id, verdict_id, run_id,
				   tick_id, check_id, kind, label, status, start_unix_nano, end_unix_nano,
				   waited_ms_json, attributes_json)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT(span_id) DO UPDATE SET
				   trace_id = excluded.trace_id, parent_span_id = excluded.parent_span_id,
				   verdict_id = excluded.verdict_id, run_id = excluded.run_id,
				   tick_id = excluded.tick_id, kind = excluded.kind, label = excluded.label,
				   status = excluded.status, start_unix_nano = excluded.start_unix_nano,
				   end_unix_nano = excluded.end_unix_nano,
				   waited_ms_json = excluded.waited_ms_json,
				   attributes_json = excluded.attributes_json`,
				span.SpanID, span.TraceID, span.ParentSpanID, span.VerdictID, span.RunID,
				span.TickID, span.CheckID, span.Kind, span.Label, span.Status,
				span.StartUnixNano, span.EndUnixNano, span.WaitedMSJSON, span.AttributesJSON,
			); err != nil {
				return Internalf("recording the gate span %s for %s: %v", span.CheckID, id, err)
			}
		}
		return nil
	})
}

// Gate judgments may be attempted again after a first stall or explicit fault clearance.
// Other judgments remain idempotent by object; the old gate evidence survives.
func judgmentSeen(tx *store.Tx, kind, objectType, id string) (int, error) {
	var after int64
	if kind == GatePlanKind || kind == GateVerdictKind {
		var err error
		after, err = gateJudgmentEpoch(tx.Reader(), id)
		if err != nil {
			return 0, err
		}
	}
	var seen int
	err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_type=? AND object_id=? AND seq>?`, kind, objectType, id, after).Scan(&seen)
	return seen, err
}

// runRefs answers the decisions' runs in decision order, which is the order the loop's own
// stop detail names them.
func runRefs(decisions []Decision) []runRef {
	refs := make([]runRef, 0, len(decisions))
	for _, decision := range decisions {
		if decision.RunRequest == nil {
			continue
		}
		refs = append(refs, runRef{RunID: decision.RunRequest.RunID, ItemID: decision.ItemID, LaneID: decision.LaneID})
	}
	return refs
}

// A first stall or diagnosed fault authorizes a fresh examination of the same
// run. Its transition sequence distinguishes measurements even when plan+tree
// reproduces the gate's deterministic verdict id.
func gateJudgmentEpoch(r *store.Reader, runID string) (int64, error) {
	var seq int64
	err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE
 (kind=? AND object_id=?) OR
 (kind=? AND json_extract(payload,'$.run_id')=? AND json_extract(payload,'$.reason') IN (?,?))`, gateStallHeldKind, runID, itemFaultClearedKind, runID, StopGateHarnessError, ReasonGateStalled).Scan(&seq)
	return seq, err
}
func (s *ProcessSteps) gateJudgmentEpoch(ctx context.Context, runID string) (int64, error) {
	st, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	var seq int64
	err = st.ReadTx(ctx, func(r *store.Reader) error { var err error; seq, err = gateJudgmentEpoch(r, runID); return err })
	return seq, err
}
