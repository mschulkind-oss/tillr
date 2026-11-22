package run

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The two read faces' record kinds. Both are output-only: neither is a state
// transition, so neither is registered with the event registry — the same reason
// run.request is not registered.
const (
	// LanesKind is the record `run lanes` answers with.
	LanesKind = "run.lanes"
	// StatusKind is the record `run status` answers with.
	StatusKind = "run.status"
)

// Lane is one row of design §5.6's lane list: the four columns the design names, and
// nothing else.
//
// RunID and LeasedUntil are pointers because both columns are nullable, and "no run"
// and "no lease" are answers rather than absences to be spelled as an empty string. A
// null LeasedUntil means the lane holds no lease — it is free, or it is a lane written
// before leases existed — and never an unknown expiry: every lease this utility writes
// carries one (design §5.6).
type Lane struct {
	LaneID      string  `json:"lane_id"`
	State       string  `json:"state"`
	RunID       *string `json:"run_id"`
	LeasedUntil *string `json:"leased_until"`
}

// lanesPayload is the run.lanes payload.
type lanesPayload struct {
	Lanes []Lane `json:"lanes"`
}

// statusPayload is the run.status payload. Run is null when the store holds no such
// run: that is the negative answer, not an error.

// ReleaseRecord wraps a release in design §5.1's envelope. It is the record `run
// release` answers with, and it is the same payload the release event carries, so a
// reader of the log and a caller of the face read the same words about the same lane.
func ReleaseRecord(release Release) (record.Envelope, error) {
	body, err := json.Marshal(release)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the release of lane %q: %v", release.LaneID, err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          ReleaseKind,
		ID:            "release-" + release.LaneID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     mustParse(release.ReleasedAt),
		Subject:       &record.Subject{Kind: "lane", ID: release.LaneID},
		Payload:       body,
	}, nil
}

type statusPayload struct {
	RunID string  `json:"run_id"`
	Found bool    `json:"found"`
	Run   *Record `json:"run"`
}

// LanesRecord answers design §5.6's `run lanes` list as a run.lanes record, in lane-id
// order so two reads of one store produce the same list.
//
// A read face has no injected clock, so the envelope's created_at is when the answer
// was assembled; the payload carries no time of its own.
func LanesRecord(r *store.Reader) (record.Envelope, error) {
	lanes, err := readLanes(r)
	if err != nil {
		return record.Envelope{}, err
	}
	body, err := json.Marshal(lanesPayload{Lanes: lanes})
	if err != nil {
		return record.Envelope{}, Internalf("encoding the lane list: %v", err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          LanesKind,
		ID:            "lanes",
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     time.Now().UTC(),
		Payload:       body,
	}, nil
}

// readLanes answers every lane row in the store.
func readLanes(r *store.Reader) ([]Lane, error) {
	rows, err := r.Query(`SELECT lane_id, state, run_id, leased_until FROM lanes ORDER BY lane_id`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	lanes := []Lane{}
	for rows.Next() {
		var (
			lane        Lane
			runID       sql.NullString
			leasedUntil sql.NullInt64
		)
		if err := rows.Scan(&lane.LaneID, &lane.State, &runID, &leasedUntil); err != nil {
			return nil, readFailure(err)
		}
		if runID.Valid {
			id := runID.String
			lane.RunID = &id
		}
		if leasedUntil.Valid {
			at := time.UnixMilli(leasedUntil.Int64).UTC().Format(time.RFC3339)
			lane.LeasedUntil = &at
		}
		lanes = append(lanes, lane)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return lanes, nil
}

// StatusRecord answers design §5.6's `run status` for one run as a run.status record,
// read from the row rather than from the newest record in the log: the row is the run's
// current state and the log is the history of how it got there.
//
// found is false when the store holds no such run. The face answers that as a negative
// decision with this record, because the utility ran and the answer is no.
func StatusRecord(r *store.Reader, runID string) (record.Envelope, bool, error) {
	var (
		payload    Record
		itemID     sql.NullString
		route      sql.NullString
		harness    sql.NullString
		seat       sql.NullString
		workflow   sql.NullString
		node       sql.NullString
		exitCode   sql.NullInt64
		startedAt  int64
		finishedAt int64
		refs       string
		tokens     sql.NullInt64
		costUSD    sql.NullFloat64
		endCause   sql.NullString
		extSet     sql.NullString
	)
	read := func(causeColumn, harnessColumn, extSetColumn, seatColumn, workflowColumn, nodeColumn string) error {
		return r.QueryRow(
			`SELECT run_id, item_id, lane_id, backend, route, `+harnessColumn+`, `+seatColumn+`, `+workflowColumn+`, `+nodeColumn+`, state, exit_code, started_at, finished_at, artifact_refs, event_seq,
			        usage_tokens, usage_cost_usd, `+causeColumn+`, `+extSetColumn+`
			   FROM runs WHERE run_id = ?`, runID,
		).Scan(&payload.RunID, &itemID, &payload.LaneID, &payload.Backend, &route, &harness, &seat, &workflow, &node, &payload.State, &exitCode,
			&startedAt, &finishedAt, &refs, &payload.EventSeq, &tokens, &costUSD, &endCause, &extSet)
	}
	cols := map[string]string{"end_cause": "end_cause", "harness": "harness", "extension_set": "extension_set", "seat": "seat", "workflow": "workflow", "node": "node"}
	var err error
	for range len(cols) + 1 {
		err = read(cols["end_cause"], cols["harness"], cols["extension_set"], cols["seat"], cols["workflow"], cols["node"])
		if err == nil {
			break
		}
		// Read-only opens cannot migrate a store written before optional columns existed.
		missing := false
		for name, col := range cols {
			if col != "NULL" && strings.Contains(err.Error(), "no such column: "+name) {
				cols[name] = "NULL"
				missing = true
				break
			}
		}
		if !missing {
			break
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return statusEnvelope(statusPayload{RunID: runID}, time.Now().UTC())
	}
	if err != nil {
		return record.Envelope{}, false, readFailure(err)
	}
	payload.EndCause = endCause.String
	if payload.EndCause == "" {
		payload.EndCause = EndNotRecorded
	}
	if extSet.Valid {
		payload.ExtensionSet = extSet.String
	}
	// A NULL item_id stays an empty string, which the record's own encoding omits: the
	// run named no item, and the answer carries no id rather than an invented one.
	if itemID.Valid {
		payload.ItemID = itemID.String
	}
	// A NULL route is the same answer: the run's argv named no model, and the record then
	// omits the key rather than carrying an empty route.
	if route.Valid {
		payload.Route = route.String
	}
	if harness.Valid {
		payload.Harness = harness.String
	}
	if seat.Valid {
		payload.Seat = seat.String
	}
	if workflow.Valid {
		payload.Workflow = workflow.String
	}
	if node.Valid {
		payload.Node = node.String
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		payload.ExitCode = &code
	}
	payload.StartedAt = time.UnixMilli(startedAt).UTC().Format(time.RFC3339)
	payload.FinishedAt = time.UnixMilli(finishedAt).UTC().Format(time.RFC3339)
	// A NULL spend column is a run whose backend reported nothing, and the record then
	// carries no usage block at all rather than a block of nulls.
	if tokens.Valid || costUSD.Valid {
		payload.Usage = &Usage{}
		if tokens.Valid {
			value := tokens.Int64
			payload.Usage.Tokens = &value
		}
		if costUSD.Valid {
			value := costUSD.Float64
			payload.Usage.CostUSD = &value
		}
	}
	if payload.EventSeq > 0 {
		var body []byte
		err := r.QueryRow(`SELECT payload FROM events WHERE seq = ? AND kind = ?`, payload.EventSeq, FinishKind).Scan(&body)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return record.Envelope{}, false, readFailure(err)
		}
		if err == nil {
			var finish Record
			if err := json.Unmarshal(body, &finish); err != nil {
				return record.Envelope{}, false, Internalf("reading spend reason: %v", err)
			}
			if finish.Usage != nil && finish.Usage.Details != nil {
				if payload.Usage == nil {
					payload.Usage = &Usage{}
				}
				payload.Usage.Details = finish.Usage.Details
			}
			payload.SpendNote = finish.SpendNote
			if payload.Harness == "" {
				payload.Harness = finish.Harness
			}
			if payload.Seat == "" {
				payload.Seat = finish.Seat
			}
			if payload.Workflow == "" {
				payload.Workflow = finish.Workflow
			}
			if payload.Node == "" {
				payload.Node = finish.Node
			}
			payload.EndingException = finish.EndingException
			payload.Signal = finish.Signal
			payload.StderrTail = finish.StderrTail
			payload.RepeatingCall = finish.RepeatingCall
			payload.LoopRecovery = finish.LoopRecovery
			payload.SettledAt = finish.SettledAt
			payload.HarnessEndedAt = finish.HarnessEndedAt
			if payload.ExtensionSet == "" {
				payload.ExtensionSet = finish.ExtensionSet
			}
		}
	}
	DescribeSpend(&payload)
	if err := json.Unmarshal([]byte(refs), &payload.ArtifactRefs); err != nil {
		return record.Envelope{}, false, Internalf("reading the artifact refs of %q: %v", runID, err)
	}
	if payload.ArtifactRefs == nil {
		payload.ArtifactRefs = []ArtifactRef{}
	}
	// The record's own time is the run's: a status read of one run is about a moment
	// that already happened, so the answer is the same bytes whenever it is asked.
	return statusEnvelope(statusPayload{RunID: runID, Found: true, Run: &payload}, time.UnixMilli(finishedAt).UTC())
}

// statusEnvelope wraps a run.status payload in design §5.1's envelope.
func statusEnvelope(payload statusPayload, at time.Time) (record.Envelope, bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, false, Internalf("encoding the run status: %v", err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          StatusKind,
		ID:            "status-" + payload.RunID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at,
		Payload:       body,
	}, payload.Found, nil
}

// readFailure classifies a failed read. A store that does not carry run's tables, or that
// carries one of them without a column this binary selects, is incompatible with this
// binary rather than a bug in it: exit 3 sends the caller to the store it named, and exit
// 5 would send it looking for a defect that is not there.
//
// The missing column is the same class as the missing table, and it is the likelier one
// for a moment: a run column added after a store was created reaches that store on its
// next write-open, so a read-only face pointed at it first sees `no such column` (a read
// face never migrates).
func readFailure(err error) error {
	if strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "no such column") {
		return Incompatiblef("%v", err)
	}
	return Internalf("%v", err)
}

// mustParse reads a timestamp the release payload already holds in the record's own
// spelling. It is the release's, not a second reading of the clock: the face stamped
// that instant and the record repeats it.
func mustParse(text string) time.Time {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}
	}
	return at.UTC()
}
