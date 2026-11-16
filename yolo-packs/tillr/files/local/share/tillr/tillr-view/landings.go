package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// landingRow is a recorded landing, an acquittal (a gate failure attributed to
// the trunk rather than the candidate), or a gap between recorded trunk heads.
// A gap has no clock: neither commit's time is a measurement of the missing move.
type landingRow struct {
	Kind              string   `json:"kind"`
	LandingID         string   `json:"landing_id,omitempty"`
	ItemID            string   `json:"item_id,omitempty"`
	RunID             string   `json:"run_id,omitempty"`
	PassID            string   `json:"pass_id,omitempty"`
	VerdictID         string   `json:"verdict_id,omitempty"`
	Result            string   `json:"result,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	BaseSHA           string   `json:"base_sha,omitempty"`
	HeadSHA           string   `json:"head_sha,omitempty"`
	TrunkBefore       string   `json:"trunk_before,omitempty"`
	TrunkAfter        string   `json:"trunk_after,omitempty"`
	GatedTree         string   `json:"gated_tree,omitempty"`
	LandedTree        string   `json:"landed_tree,omitempty"`
	ConflictPaths     []string `json:"conflict_paths,omitempty"`
	GateSkippedReason string   `json:"gate_skipped_reason,omitempty"`
	EventSeq          int64    `json:"event_seq"`
	AtMS              *int64   `json:"at_ms"`
	EpistemicState    string   `json:"epistemic_state"`
	FixRunID          string   `json:"fix_run_id,omitempty"`
	FixerItem         string   `json:"fixer_item,omitempty"`
	RedSHA            string   `json:"red_sha,omitempty"`
	FailingChecks     []string `json:"failing_checks,omitempty"`
}

// readLandingHistory reads the ledger, not the retired candidate queue. Events
// provide both clocks and repair relationships; sequence order stays authoritative
// even when a producer's wall clock went backward.
func readLandingHistory(r *store.Reader) ([]landingRow, error) {
	rows, err := r.Query(`SELECT landing_id, item_id, run_id, verdict_id, result, reason,
 base_sha, head_sha, trunk_before, trunk_after, gated_tree, landed_tree,
 conflict_paths, gate_skipped_reason, event_seq FROM landings ORDER BY event_seq, landing_id`)
	if err != nil {
		return nil, err
	}
	out := []landingRow{}
	for rows.Next() {
		l := landingRow{Kind: "landing", EpistemicState: stateUnobservedSeam}
		var paths string
		if err := rows.Scan(&l.LandingID, &l.ItemID, &l.RunID, &l.VerdictID, &l.Result, &l.Reason,
			&l.BaseSHA, &l.HeadSHA, &l.TrunkBefore, &l.TrunkAfter, &l.GatedTree, &l.LandedTree,
			&paths, &l.GateSkippedReason, &l.EventSeq); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(paths), &l.ConflictPaths); err != nil {
			rows.Close()
			return nil, fmt.Errorf("landing %s conflict paths: %w", l.LandingID, err)
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	clocks := map[int64]int64{}
	passes := map[string]string{}
	dispatches, err := r.Query(`SELECT run_id, dispatched_at FROM dispatches WHERE run_id IS NOT NULL ORDER BY dispatched_at, decision_id`)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	if err == nil {
		for dispatches.Next() {
			var runID string
			var ms int64
			if err := dispatches.Scan(&runID, &ms); err != nil {
				dispatches.Close()
				return nil, err
			}
			passes[runID] = time.UnixMilli(ms).UTC().Format("20060102T150405Z")
		}
		err = dispatches.Err()
		dispatches.Close()
		if err != nil {
			return nil, err
		}
	}
	repairs := map[string]landingRow{}
	for _, e := range events {
		if e.Kind == "land.landing" {
			clocks[e.Seq] = e.At.UnixMilli()
		}
		if e.Kind != "drain.refusal" || e.ObjectType != "run" || e.ObjectID == nil {
			continue
		}
		var p struct {
			Step          string   `json:"step"`
			ItemID        string   `json:"item_id"`
			FixRunID      string   `json:"fix_run_id"`
			FixerItem     string   `json:"fixer_item"`
			RedSHA        string   `json:"red_sha"`
			FailingChecks []string `json:"failing_checks"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return nil, fmt.Errorf("event %d: %w", e.Seq, err)
		}

		if p.Step == "conflict" {
			repairs[*e.ObjectID] = landingRow{FixRunID: p.FixRunID}
		}
		if p.Step == "trunk-already-red" {
			ms := e.At.UnixMilli()
			out = append(out, landingRow{Kind: "acquittal", ItemID: p.ItemID, RunID: *e.ObjectID,
				PassID: passes[*e.ObjectID], Result: p.Step, EventSeq: e.Seq, AtMS: &ms, EpistemicState: stateObserved,
				FixerItem: p.FixerItem, RedSHA: p.RedSHA, FailingChecks: p.FailingChecks})
		}
	}
	// A conflict repair is launched inside the original pass, without another
	// dispatch row. Its recorded refusal edge preserves that pass association.
	for runID, repair := range repairs {
		if repair.FixRunID != "" && passes[repair.FixRunID] == "" {
			passes[repair.FixRunID] = passes[runID]
		}
	}
	for i := range out {
		l := &out[i]
		l.PassID = passes[l.RunID]
		if l.Kind != "landing" {
			continue
		}
		if ms, ok := clocks[l.EventSeq]; ok {
			l.AtMS = &ms
			l.EpistemicState = stateObserved
		}
		if l.Result == "squash-conflict" {
			l.FixRunID = repairs[l.RunID].FixRunID
		}
	}
	// Rows whose event is absent remain readable, but cannot be placed in the
	// measured sequence. Put them last; their trunk heads still describe adjacency.
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].AtMS == nil) != (out[j].AtMS == nil) {
			return out[i].AtMS != nil
		}
		return out[i].EventSeq < out[j].EventSeq
	})
	history := []landingRow{}
	var previous *landingRow
	for _, l := range out {
		if l.Kind == "landing" {
			if previous != nil && previous.TrunkAfter != l.TrunkBefore {
				history = append(history, landingRow{Kind: "gap", TrunkBefore: previous.TrunkAfter, TrunkAfter: l.TrunkBefore, EpistemicState: stateUnobservedSeam})
			}
			copy := l
			previous = &copy
		}
		history = append(history, l)
	}
	return history, nil
}

func (s *server) landingsPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	rows, err := readLandingHistory(r)
	if err != nil {
		if isMissingTable(err) {
			return nil, failure(codeTableAbsent, "the landings history is unavailable: %v", err)
		}
		return nil, failure(codeStoreUnreadable, "reading the landings history: %v", err)
	}
	if id := req.PathValue("id"); id != "" {
		for _, row := range rows {
			if row.LandingID == id {
				return row, nil
			}
		}
		return nil, failure(codeLandingNotFound, "the store carries no landing %q: read /api/v1/landings for the history it carries", id)
	}
	return struct {
		Rows []landingRow `json:"rows"`
	}{rows}, nil
}
