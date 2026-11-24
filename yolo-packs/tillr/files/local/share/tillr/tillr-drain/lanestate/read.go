// Package lanestate projects candidate lane reservations without registering
// drain's writable schema. Read-only faces may import it without changing fixtures
// or opening the store with migration authority.
package lanestate

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr/store"
)

type State struct {
	Status string `json:"lane_status"`
	Reason string `json:"lane_reason,omitempty"`
}

// HeldLane describes a lane still held by unfinished, unreleased work in the
// same pending reservation history readPassPlans consumes.
type HeldLane struct {
	LaneID string
	ItemID string
	RunID  string
	Reason string
}

// KeepReason is the shared worktree-dependency policy. A held repair that has
// already handed off its pending transition is not a candidate to release.
func KeepReason(hasCandidate, boundary bool, reason string) string {
	switch {
	case boundary || reason == "host-boundary-violation":
		return "boundary violation needs the original checkout for inspection"
	case reason == "candidate-record-mismatch":
		return "unrecorded lane HEAD needs inspection"
	case !hasCandidate:
		return "no recorded candidate to restore; settlement or continuation needs the original worktree and session"
	default:
		return ""
	}
}

// Match the UUID shape accepted by run's continuation contract without importing
// run (whose init registers writable schema in a read-only process).
var sessionIDShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type runReq struct {
	RunID   string `json:"run_id"`
	Backend string `json:"backend"`
}

type candidate struct{}
type pending struct {
	Decision struct {
		ItemID     string  `json:"item_id"`
		LaneID     string  `json:"lane_id"`
		RunRequest *runReq `json:"run_request"`
		Parallel   []struct {
			RunRequest *runReq `json:"run_request"`
		} `json:"parallel"`
	} `json:"decision"`
	Candidate      *candidate `json:"candidate"`
	LaneReleased   bool       `json:"lane_released"`
	LaneKeepReason string     `json:"lane_keep_reason"`
	BoundaryFailed bool       `json:"boundary_failed"`
	done           bool
	gated          bool
	seq            int64
}

func (w *pending) indexRuns(byRun map[string]*pending, primaryID string) {
	byRun[primaryID] = w
	if w.Decision.RunRequest != nil && w.Decision.RunRequest.RunID != "" {
		byRun[w.Decision.RunRequest.RunID] = w
	}
	for _, node := range w.Decision.Parallel {
		if node.RunRequest != nil && node.RunRequest.RunID != "" {
			byRun[node.RunRequest.RunID] = w
		}
	}
}

func (w *pending) findRun(runID string) *runReq {
	if w.Decision.RunRequest != nil && w.Decision.RunRequest.RunID == runID {
		return w.Decision.RunRequest
	}
	for _, node := range w.Decision.Parallel {
		if node.RunRequest != nil && node.RunRequest.RunID == runID {
			return node.RunRequest
		}
	}
	return w.Decision.RunRequest
}

var replayKinds = []string{
	"gate.verdict",
	"land.landing",
	"run.resume",
	"drain.started",
	"drain.node-advanced",
	"drain.graph-changed",
	"drain.candidate-assigned",
	"drain.candidate",
	"drain.retirement",
	"drain.prepared-candidate",
	"drain.gated",
	"drain.judge-landed",
	"drain.candidate-release-started",
	"drain.candidate-lane",
	"drain.trunk-path-dirty",
	"drain.repair-held",
	"drain.gate-stall",
	"drain.red-cap",
	"boundary.check",
	faults.ClearedKind,
	"drain.examined",
}

func replay(r *store.Reader) (map[string]*pending, error) {
	events, err := r.EventsOfKind(0, replayKinds...)
	if err != nil {
		return nil, err
	}
	works, byRun := map[string]*pending{}, map[string]*pending{}
	judged, segments := map[string]bool{}, map[string]bool{}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		id := *e.ObjectID
		switch e.Kind {
		case "gate.verdict":
			if e.ObjectType == "run" {
				judged[id] = true
			}
		case "land.landing":
			var landing struct {
				RunID string `json:"run_id"`
			}
			if err := json.Unmarshal(e.Payload, &landing); err != nil {
				return nil, err
			}
			judged[landing.RunID] = true
		case "run.resume":
			var segment struct {
				RunID     string `json:"run_id"`
				ItemID    string `json:"item_id"`
				LaneID    string `json:"lane_id"`
				Backend   string `json:"backend"`
				SessionID string `json:"session_id"`
				ResumedAt string `json:"resumed_at"`
			}
			if err := json.Unmarshal(e.Payload, &segment); err != nil {
				return nil, err
			}
			key := fmt.Sprintf("%s@%d:%s", id, e.At.UnixMilli(), e.Payload)
			if segments[key] {
				continue
			}
			segments[key] = true
			w := byRun[id]
			if e.ObjectType != "run" || w == nil || judged[id] || w.gated {
				continue
			}
			req := w.findRun(id)
			if req == nil || segment.RunID != req.RunID || segment.ItemID != w.Decision.ItemID || segment.LaneID != w.Decision.LaneID || segment.Backend != req.Backend || !sessionIDShape.MatchString(segment.SessionID) || segment.ResumedAt == "" {
				continue
			}
			w.done, w.Candidate, w.LaneReleased, w.LaneKeepReason, w.seq = false, nil, false, "", e.Seq
		case "drain.started", "drain.node-advanced", "drain.graph-changed":
			var w pending
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return nil, err
			}
			w.seq = e.Seq
			works[w.Decision.ItemID] = &w
			w.indexRuns(byRun, id)
		case "drain.candidate-assigned":
			if w := byRun[id]; w != nil {
				// The payload omits a false lane_released: the assignment ends the
				// earlier release, so clear it before decoding, and the assigned lane
				// reads as occupied.
				w.LaneReleased, w.LaneKeepReason = false, ""
				if err := json.Unmarshal(e.Payload, w); err != nil {
					return nil, err
				}
				w.seq = e.Seq
				w.indexRuns(byRun, id)
			}
		case "drain.candidate", "drain.retirement", "drain.prepared-candidate", "drain.gated", "drain.judge-landed":
			if w := byRun[id]; w != nil {
				var c candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return nil, err
				}
				w.Candidate = &c
				if e.Kind == "drain.gated" || e.Kind == "drain.judge-landed" {
					w.gated = true
				}
			}
		case "drain.candidate-release-started", "drain.candidate-lane":
			if w := byRun[id]; w != nil {
				var lane struct {
					Candidate candidate `json:"candidate"`
					Released  bool      `json:"released"`
					Reason    string    `json:"reason"`
				}
				if err := json.Unmarshal(e.Payload, &lane); err != nil {
					return nil, err
				}
				w.Candidate, w.LaneReleased, w.LaneKeepReason = &lane.Candidate, lane.Released, lane.Reason
			}
		case "drain.trunk-path-dirty", "drain.repair-held", "drain.gate-stall", "drain.red-cap":
			if w := byRun[id]; w != nil {
				var held struct {
					Candidate candidate `json:"candidate"`
				}
				if err := json.Unmarshal(e.Payload, &held); err != nil {
					return nil, err
				}
				w.Candidate = &held.Candidate
				if e.Kind == "drain.red-cap" {
					w.gated = true
				}
			}
		case "boundary.check":
			var check struct {
				Clean bool `json:"clean"`
				Runs  []struct {
					RunID string `json:"run_id"`
				} `json:"runs"`
			}
			if err := json.Unmarshal(e.Payload, &check); err != nil {
				return nil, err
			}
			if !check.Clean {
				for _, ref := range check.Runs {
					if w := byRun[ref.RunID]; w != nil {
						w.BoundaryFailed = true
					}
				}
			}
		case faults.ClearedKind:
			var hold faults.Hold
			if err := json.Unmarshal(e.Payload, &hold); err != nil {
				return nil, err
			}
			if hold.Reason == "host-boundary-violation" {
				if w := byRun[hold.RunID]; w != nil {
					w.BoundaryFailed = false
				}
			}
		case "drain.examined":
			if w := byRun[id]; w != nil {
				w.done = true
			}
		}
	}
	return works, nil
}

func keptReason(w *pending, holdReason string) string {
	reason := w.LaneKeepReason
	if reason == "" {
		reason = KeepReason(w.Candidate != nil, w.BoundaryFailed, holdReason)
		if reason == "" {
			reason = "candidate awaits pin verification and lane release"
		}
	}
	return reason
}

// Read replays the same reservation effects the planner consumes. It projects
// only capacity and explanations; it never decides readiness or opens a writer.
func Read(r *store.Reader) (map[string]State, error) {
	works, err := replay(r)
	if err != nil {
		return nil, err
	}
	holds, err := faults.Read(r)
	if err != nil {
		return nil, err
	}
	out := map[string]State{}
	for id, w := range works {
		if w.done {
			continue
		}
		s := State{Status: "kept", Reason: w.LaneKeepReason}
		if w.LaneReleased {
			s.Status, s.Reason = "released", ""
		} else {
			s.Reason = keptReason(w, holds[id].Reason)
		}
		out[id] = s
	}
	return out, nil
}

// HeldByLane answers each lane still held by unfinished, unreleased work from
// the same pending reservation history readPassPlans consumes.
func HeldByLane(r *store.Reader) (map[string]HeldLane, error) {
	works, err := replay(r)
	if err != nil {
		return nil, err
	}
	holds, err := faults.Read(r)
	if err != nil {
		return nil, err
	}
	out := map[string]HeldLane{}
	bestSeq := map[string]int64{}
	for id, w := range works {
		lane := w.Decision.LaneID
		if w.done || w.LaneReleased || lane == "" {
			continue
		}
		if prev, ok := out[lane]; ok && (w.seq < bestSeq[lane] || (w.seq == bestSeq[lane] && id > prev.ItemID)) {
			continue
		}
		runID := ""
		if w.Decision.RunRequest != nil {
			runID = w.Decision.RunRequest.RunID
		}
		bestSeq[lane] = w.seq
		out[lane] = HeldLane{
			LaneID: lane,
			ItemID: id,
			RunID:  runID,
			Reason: keptReason(w, holds[id].Reason),
		}
	}
	return out, nil
}
