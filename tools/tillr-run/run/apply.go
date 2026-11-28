package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The three terminal states design §5.6 gives a run.
const (
	StateExited        = "exited"
	StateFailedToStart = "failed-to-start"
	StateTimedOut      = "timed-out"
	StatePaused        = "paused"
)

// End causes describe the observed termination, separately from the existing state.
// A normal exit includes nonzero exit codes; lease reclamation means the process
// left no terminal record, not that the runner observed its time limit.
const (
	EndSettledIdle    = "settled-idle"
	EndNormalExit     = "normal-exit"
	EndRestart        = "restart"
	EndTimeLimit      = "time-limit"
	EndLeaseReclaimed = "lease-reclaimed"
	EndSignal         = "signal"
	EndFailedToStart  = "failed-to-start"
	EndNotRecorded    = "end cause not recorded"
	// EndRepeatingCall is a session the harness itself ended because the model kept
	// repeating one identical tool call (pi's `loop_detected` event). The process exits
	// like any ended run; the cause is what says the run is neither an answer nor a
	// harness failure. The record's RepeatingCall names the call.
	EndRepeatingCall = "repeating-call"
	// EndProviderError is a Pi session whose last assistant turn ended with a provider
	// error (stopReason "error"): the provider refused or failed the model request, so the
	// run has no answer however the process exited. The record's ProviderError carries the
	// provider's message, and the run's exit code is never 0.
	EndProviderError = "provider-error"
)

// LoopDetectedEvent is the stream event pi emits, in `--mode json`, when it ends a
// session whose model kept repeating one identical tool call:
// `{"type":"loop_detected","toolName":…,"args":{…},"streak":n}`.
const LoopDetectedEvent = "loop_detected"

// RepeatingCall is the call a session repeated, as the run record carries it. Args is
// the arguments' JSON with sorted keys and no insignificant whitespace, cut to
// RepeatingCallArgsLimit bytes; ArgsDigest is the SHA-256 of that whole uncut text, so two
// records name the same arguments exactly when their digests match. Streak is the number
// of identical calls in a row that pi reported.
type RepeatingCall struct {
	Tool       string `json:"tool"`
	Args       string `json:"args"`
	ArgsDigest string `json:"args_digest"`
	Streak     int    `json:"streak"`
	Action     string `json:"action,omitempty"`
	Shape      string `json:"shape,omitempty"`
}

// LoopRecovery summarizes in-run loop recoveries pi performed during a session
// (`loop_detected` events with `action: "recovered"`).
type LoopRecovery struct {
	Count        int    `json:"count"`
	Shape        string `json:"shape,omitempty"`
	TokensBefore int64  `json:"tokens_before"`
	TokensAfter  int64  `json:"tokens_after"`
}

// RepeatingCallArgsLimit bounds the arguments a record quotes; the digest covers them all.
const RepeatingCallArgsLimit = 400

// LaneStateLeased is the state of a lane a run holds: the claim writes it with the
// lease, and the release clears it (design §5.6).
const LaneStateLeased = "leased"

// ArtifactRef is one artifact a run produced, as the record carries it.
type ArtifactRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// Record is the run.record payload: design §5.6's field list, field for field.
//
// ExitCode is a pointer because "the process ran and exited 0" and "there was no
// process to have a code" are different answers, and a zero value cannot say which.
//
// Backend is the request's backend, carried through unchanged: it is the run's
// provenance, and a record without it is refused for the same reason the request is.
//
// Route is the model id that backend's adapter was handed, copied from the request's
// argv (Request.Route) rather than drawn again: it is what survives when one wire model
// is reached through several client ids, which is the per-run attribution an eval reads
// and the reason day-2's failure criterion 7 counts lost attribution as a failure. A run
// whose argv named no model carries no route key at all, and the runs row holds NULL for
// it — the same answer item_id gives an absence.
//
// ItemID is the request's item_id, carried through unchanged: it is what makes a run
// traceable to the work it was dispatched for (D-UT19). A run dispatched for no item
// carries no item_id key at all rather than an empty string, which would read as an id
// the record failed to fill; the runs row holds NULL for it, the same answer lanes'
// run_id and leased_until give an absence.
//
// Usage is what the run's backend reported it spent, read from the report the backend's
// extractor wrote beside the log. It is a pointer for the same reason ExitCode is: a run
// whose report carries no usage at all is a different answer from one that spent zero,
// and a total must not read "nobody could tell me" as "it was free".
type Record struct {
	RunID   string `json:"run_id"`
	ItemID  string `json:"item_id,omitempty"`
	LaneID  string `json:"lane_id"`
	Backend string `json:"backend"`
	Route   string `json:"route,omitempty"`
	// Harness names the exact harness executable and build identity the adapter ran
	// (for pi: rebuilt path and series key, or installed path and version). Omitted
	// and stored as NULL when the adapter reports none.
	Harness string `json:"harness,omitempty"`
	// Seat, Workflow, and Node record the dispatch provenance from the request.
	// Omitted and stored as NULL when absent or on legacy records.
	Seat     string `json:"seat,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Node     string `json:"node,omitempty"`
	State    string `json:"state"`
	// EndCause is optional on legacy input records. Readers explicitly report its
	// absence rather than inferring it from the less specific state or exit code.
	EndCause string `json:"end_cause,omitempty"`
	// Signal names the signal that ended the process (`SIGKILL`, `SIGSEGV`), whether the
	// runner saw the process die by it or an adapter forwarded it as exit 128 plus the
	// signal number. It is recorded for every signaled exit. end_cause is `signal` only for
	// an external SIGTERM or SIGINT; any other signal (a crash signal, SIGKILL) stays a
	// `normal-exit` that counts against the backend.
	Signal string `json:"signal,omitempty"`
	// StderrTail carries the last lines of the worker's standard error (stream.err) when
	// the process ended with a non-zero exit code, bounded to StderrTailBytes.
	StderrTail string `json:"stderr_tail,omitempty"`
	// Only settled-idle carries these observations. The finish event preserves
	// them, and status reads that same event rather than inferring timestamps.
	SettledAt      string `json:"settled_at,omitempty"`
	HarnessEndedAt string `json:"harness_ended_at,omitempty"`
	// Resumed says this record is a segment of a run that was continued rather than a first
	// execution. It is the terminal record's half of the run.resume event: the event says a
	// resume happened, and this says how the resumed segment ended. It is not a column — the
	// run's row is one row either way, which is the whole point of continuing the same run id
	// — so it travels in the event payload and in the record, and a reader that asks "was this
	// run resumed" answers from the event log rather than from a second runs row.
	Resumed      bool          `json:"resumed,omitempty"`
	ExitCode     *int          `json:"exit_code,omitempty"`
	StartedAt    string        `json:"started_at"`
	FinishedAt   string        `json:"finished_at"`
	EventSeq     int64         `json:"event_seq"`
	ArtifactRefs []ArtifactRef `json:"artifact_refs"`
	Usage        *Usage        `json:"usage,omitempty"`
	SpendStatus  string        `json:"spend_status"`
	SpendNote    string        `json:"spend_note,omitempty"`
	// EndingException states that the adapter, not the harness, ended a session that
	// had already settled: the harness did not exit, and the adapter says what it
	// signalled. The run is still the ordinary exit it reports, judged on its answer;
	// it is neither a failure nor an interruption to replay.
	EndingException string `json:"ending_exception,omitempty"`
	// RepeatingCall names the call of a run that ended for EndRepeatingCall, and is
	// carried only by such a run.
	RepeatingCall *RepeatingCall `json:"repeating_call,omitempty"`
	// ProviderError is the provider's message when the run ended for EndProviderError, and
	// is carried only by such a run.
	ProviderError string `json:"provider_error,omitempty"`
	// LoopRecovery summarizes in-run loop recoveries pi performed during the session.
	LoopRecovery *LoopRecovery `json:"loop_recovery,omitempty"`
	// ExtensionSet records which Pi extension set (agent dir and content key or held
	// status) the run's adapter selected, when applicable.
	ExtensionSet string `json:"extension_set,omitempty"`
}

// Usage is one run's spend, as its backend reported it. Each field is a pointer because
// each has three readings, and the third is the one a sum must not swallow:
//
//	nil        the backend reported nothing for this field
//	0          the backend reported zero, which is a measurement
//	any other  the backend reported it
//
// Tokens is every token counter the report carries, summed: input, output, cache reads
// and reasoning all count, because a cache read is billed. CostUSD is dollars, and ONLY
// the backends that report a cost fill it — pi does, muse and agy do not, so their runs
// carry nil rather than a zero that would read as a free run.
type Usage struct {
	Details *UsageDetails `json:"details,omitempty"`
	Tokens  *int64        `json:"tokens,omitempty"`
	CostUSD *float64      `json:"cost_usd,omitempty"`
}

// Apply is run's one record→row mapping: it validates the record, writes the lane and
// run rows, appends run.finish in the same transaction, and answers the record as
// stored. The write face and the seed materializer both call it, so a fixture cannot
// construct a state the write face would reject (lanes §1, OQ-L2).
func Apply(ctx context.Context, tx *store.Tx, env record.Envelope) (record.Envelope, error) {
	var payload Record
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return record.Envelope{}, Usagef("reading the run payload: %v", err)
	}
	if err := checkRecord(payload); err != nil {
		return record.Envelope{}, err
	}
	started, err := time.Parse(time.RFC3339, payload.StartedAt)
	if err != nil {
		return record.Envelope{}, Usagef("started_at %q is not an RFC 3339 timestamp: %v", payload.StartedAt, err)
	}
	finished, err := time.Parse(time.RFC3339, payload.FinishedAt)
	if err != nil {
		return record.Envelope{}, Usagef("finished_at %q is not an RFC 3339 timestamp: %v", payload.FinishedAt, err)
	}
	if payload.ArtifactRefs == nil {
		payload.ArtifactRefs = []ArtifactRef{}
	}
	if payload.Usage != nil && payload.Usage.Tokens == nil && payload.Usage.CostUSD == nil && payload.Usage.Details == nil {
		// A usage block with neither number is not a measurement: it encodes to `{}`,
		// which a reader cannot tell from a report that carried nothing. The absence is
		// spelled by the field being null, so an empty block is refused rather than
		// stored as one.
		return record.Envelope{}, Usagef("a run.record's usage carries neither tokens nor cost: an unreported spend is no usage block at all, not an empty one")
	}
	DescribeSpend(&payload)
	refs, err := json.Marshal(payload.ArtifactRefs)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the artifact refs: %v", err)
	}

	// The lane row is written before the run row: the run's foreign key names it, and
	// the store opens with foreign keys enforced.
	//
	// The terminal record is the release (design §5.6): the lane goes free and its lease
	// is cleared in this same transaction, so no reader ever sees a finished run holding
	// a lane. The update is guarded by the lane's run id, so a run that finishes after
	// its lease was reaped cannot free the lane a newer run now holds — the guarded clear
	// is the whole of that rule, and it is why the release is here rather than in the
	// runner.
	leaseID := payload.RunID
	var groupID string
	groupErr := tx.Reader().QueryRow(`SELECT group_id FROM read_only_runs WHERE run_id=?`, payload.RunID).Scan(&groupID)
	if groupErr != nil && groupErr != sql.ErrNoRows {
		return record.Envelope{}, groupErr
	}
	if groupErr == nil {
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, payload.RunID); err != nil {
			return record.Envelope{}, err
		}
		var remaining int
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE group_id=? AND settled=0`, groupID).Scan(&remaining); err != nil {
			return record.Envelope{}, err
		}
		// Only the last member releases the item's lane.
		if remaining == 0 {
			leaseID = groupID
		} else {
			leaseID = ""
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES (?, ?, NULL, NULL)
		 ON CONFLICT(lane_id) DO UPDATE SET
		   state = ?, run_id = NULL, leased_until = NULL
		 WHERE lanes.run_id = ?`,
		payload.LaneID, LaneStateIdle, LaneStateIdle, leaseID,
	); err != nil {
		return record.Envelope{}, Internalf("writing the lane: %v", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO runs (run_id, item_id, lane_id, backend, route, harness, seat, workflow, node, state, exit_code, started_at, finished_at, artifact_refs, usage_tokens, usage_cost_usd, end_cause, extension_set)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id) DO UPDATE SET
		   item_id = excluded.item_id, lane_id = excluded.lane_id, backend = excluded.backend,
		   route = excluded.route, harness = excluded.harness,
		   seat = excluded.seat, workflow = excluded.workflow, node = excluded.node,
		   state = excluded.state, exit_code = excluded.exit_code,
		   started_at = excluded.started_at, finished_at = excluded.finished_at,
		   artifact_refs = excluded.artifact_refs,
		   usage_tokens = excluded.usage_tokens, usage_cost_usd = excluded.usage_cost_usd,
		   end_cause = excluded.end_cause,
		   extension_set = excluded.extension_set`,
		payload.RunID, nullableText(payload.ItemID), payload.LaneID, payload.Backend, nullableText(payload.Route), nullableText(payload.Harness),
		nullableText(payload.Seat), nullableText(payload.Workflow), nullableText(payload.Node), payload.State,
		exitCodeArg(payload.ExitCode), started.UnixMilli(), finished.UnixMilli(), string(refs),
		usageTokensArg(payload.Usage), usageCostArg(payload.Usage), nullableText(payload.EndCause), nullableText(payload.ExtensionSet),
	); err != nil {
		return record.Envelope{}, Internalf("writing the run: %v", err)
	}

	if payload.State == StatePaused {
		// A replacement loop may hold this run before another settlement pass.
		// Make the quota wait visible in the same transaction as the paused finish.
		if _, err := tx.Exec(`UPDATE quota_pauses SET state='waiting' WHERE run_id=? AND backend=?`, payload.RunID, payload.Backend); err != nil {
			return record.Envelope{}, Internalf("recording quota pause: %v", err)
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the finish event: %v", err)
	}
	seq, err := tx.Append(event.Event{
		Kind:       FinishKind,
		ObjectType: "run",
		ObjectID:   &payload.RunID,
		Payload:    body,
		At:         env.CreatedAt,
	})
	if err != nil {
		return record.Envelope{}, Internalf("appending the %s event: %v", FinishKind, err)
	}
	// The row records the sequence of the event that last wrote it, so a reader answers
	// the record's event_seq from the row rather than by reading the log.
	payload.EventSeq = seq
	if _, err := tx.Exec(`UPDATE runs SET event_seq = ? WHERE run_id = ?`, seq, payload.RunID); err != nil {
		return record.Envelope{}, Internalf("recording the event sequence: %v", err)
	}
	stored, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the stored record: %v", err)
	}
	env.Payload = stored
	return env, nil
}

// checkRecord refuses a record the tables cannot describe. It is the validation the
// seed path shares with the write face, which is what makes a fixture unable to
// construct a state the face would reject.
func checkRecord(payload Record) error {
	if payload.RunID == "" {
		return Usagef("a run.record needs a run_id")
	}
	if payload.LaneID == "" {
		return Usagef("a run.record needs a lane_id")
	}
	if payload.Backend == "" {
		return Usagef("a run.record needs a backend: the adapter program that produced the run")
	}
	switch payload.EndCause {
	case "", EndNotRecorded, EndNormalExit, EndTimeLimit, EndLeaseReclaimed, EndSignal, EndFailedToStart, EndRestart, EndSettledIdle, EndRepeatingCall, EndProviderError:
	default:
		return Usagef("%q is not a run end cause", payload.EndCause)
	}
	if payload.EndCause == EndSettledIdle {
		if payload.State != StateExited {
			return Usagef("settled-idle requires an exited run")
		}
		for _, field := range []struct{ name, value string }{{"settled_at", payload.SettledAt}, {"harness_ended_at", payload.HarnessEndedAt}} {
			if _, err := time.Parse(time.RFC3339Nano, field.value); err != nil {
				return Usagef("settled-idle needs a valid %s: %v", field.name, err)
			}
		}
	} else if payload.SettledAt != "" || payload.HarnessEndedAt != "" {
		return Usagef("settlement observations require settled-idle")
	}
	if payload.EndCause == EndRepeatingCall && (payload.State != StateExited || payload.RepeatingCall == nil || (payload.RepeatingCall.Tool == "" && payload.RepeatingCall.Shape != "no-tool")) {
		return Usagef("repeating-call requires an exited run that names the repeated call")
	}
	if payload.EndCause != EndRepeatingCall && payload.RepeatingCall != nil {
		return Usagef("a repeating_call requires the repeating-call end cause")
	}
	if payload.EndCause == EndProviderError && (payload.State != StateExited || payload.ProviderError == "") {
		return Usagef("provider-error requires an exited run that carries the provider's message")
	}
	if payload.EndCause != EndProviderError && payload.ProviderError != "" {
		return Usagef("a provider_error requires the provider-error end cause")
	}
	if payload.LoopRecovery != nil && payload.LoopRecovery.Count <= 0 {
		return Usagef("a loop_recovery requires a positive count")
	}
	if payload.EndingException != "" && payload.State != StateExited {
		return Usagef("an ending_exception requires an exited run")
	}
	switch payload.State {
	case StateExited:
		if payload.ExitCode == nil {
			return Usagef("a run.record in state %q needs an exit_code: the process ran", StateExited)
		}
	case StateFailedToStart, StateTimedOut, StatePaused:
		if payload.ExitCode != nil {
			return Usagef("a run.record in state %q carries no exit_code: there was no exit to record", payload.State)
		}
	default:
		return Usagef("%q is not a run state: want one of %s, %s, %s",
			payload.State, StateExited, StateFailedToStart, StateTimedOut)
	}
	return nil
}

// exitCodeArg answers the exit code as a nullable column value.
func exitCodeArg(code *int) any {
	if code == nil {
		return nil
	}
	return *code
}

// nullableText answers a record's optional text as a column value: a run dispatched for
// no item and a run whose argv named no model both have nothing to name, and NULL says
// so where an empty string would look like a value the record failed to fill.
func nullableText(text string) any {
	if text == "" {
		return nil
	}
	return text
}

// usageTokensArg and usageCostArg answer the spend as nullable column values, so a
// store keeps the same three readings the record does: NULL is "not reported", 0 is a
// reported zero.
func usageTokensArg(usage *Usage) any {
	if usage == nil || usage.Tokens == nil {
		return nil
	}
	return *usage.Tokens
}

func usageCostArg(usage *Usage) any {
	if usage == nil || usage.CostUSD == nil {
		return nil
	}
	return *usage.CostUSD
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGILL: "SIGILL", syscall.SIGTRAP: "SIGTRAP", syscall.SIGABRT: "SIGABRT",
	syscall.SIGBUS: "SIGBUS", syscall.SIGFPE: "SIGFPE", syscall.SIGKILL: "SIGKILL",
	syscall.SIGUSR1: "SIGUSR1", syscall.SIGSEGV: "SIGSEGV", syscall.SIGUSR2: "SIGUSR2",
	syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM",
	syscall.SIGSYS: "SIGSYS",
}

// SignalName is the conventional name of sig, or "" for one this package does not name.
func SignalName(sig syscall.Signal) string { return signalNames[sig] }
