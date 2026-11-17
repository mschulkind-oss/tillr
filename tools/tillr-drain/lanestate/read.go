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

// KeepReason is the shared worktree-dependency policy. A held repair that has
// already handed off its pending transition is not a candidate to release.
func KeepReason(hasCandidate bool, baseRef string, workflow, boundary bool, reason string) string {
	switch {
	case !hasCandidate:
		return "settlement or continuation needs the original worktree and session"
	case baseRef != "":
		return "fork delivery needs its lane-specific checkout and patch branch"
	case workflow:
		return "workflow progress needs the staged node worktree and inputs"
	case boundary || reason == "host-boundary-violation":
		return "boundary violation needs the original checkout for inspection"
	case reason == "candidate-record-mismatch":
		return "unrecorded lane HEAD needs inspection"
	default:
		return ""
	}
}

// Match the UUID shape accepted by run's continuation contract without importing
// run (whose init registers writable schema in a read-only process).
var sessionIDShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type candidate struct {
	BaseRef string `json:"base_ref"`
}
type pending struct {
	Decision struct {
		ItemID     string `json:"item_id"`
		LaneID     string `json:"lane_id"`
		RunRequest *struct {
			RunID   string `json:"run_id"`
			Backend string `json:"backend"`
		} `json:"run_request"`
		Workflow json.RawMessage `json:"workflow"`
	} `json:"decision"`
	Candidate      *candidate `json:"candidate"`
	LaneReleased   bool       `json:"lane_released"`
	LaneKeepReason string     `json:"lane_keep_reason"`
	BoundaryFailed bool       `json:"boundary_failed"`
	done           bool
	gated          bool
}

// Read replays the same reservation effects the planner consumes. It projects
// only capacity and explanations; it never decides readiness or opens a writer.
func Read(r *store.Reader) (map[string]State, error) {
	events, err := r.Events(0)
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
			if e.ObjectType != "run" || w == nil || judged[id] || w.gated || w.Decision.RunRequest == nil {
				continue
			}
			req := w.Decision.RunRequest
			if segment.RunID != req.RunID || segment.ItemID != w.Decision.ItemID || segment.LaneID != w.Decision.LaneID || segment.Backend != req.Backend || !sessionIDShape.MatchString(segment.SessionID) || segment.ResumedAt == "" {
				continue
			}
			w.done, w.Candidate, w.LaneReleased, w.LaneKeepReason = false, nil, false, ""
		case "drain.started", "drain.node-advanced":
			var w pending
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return nil, err
			}
			works[w.Decision.ItemID], byRun[id] = &w, &w
		case "drain.candidate-assigned":
			if w := byRun[id]; w != nil {
				if err := json.Unmarshal(e.Payload, w); err != nil {
					return nil, err
				}
			}
		case "drain.candidate", "drain.retirement", "drain.prepared-candidate", "drain.gated":
			if w := byRun[id]; w != nil {
				var c candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return nil, err
				}
				w.Candidate = &c
				if e.Kind == "drain.gated" {
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
		} else if s.Reason == "" {
			baseRef := ""
			if w.Candidate != nil {
				baseRef = w.Candidate.BaseRef
			}
			workflow := len(w.Decision.Workflow) > 0 && string(w.Decision.Workflow) != "null"
			s.Reason = KeepReason(w.Candidate != nil, baseRef, workflow, w.BoundaryFailed, holds[id].Reason)
			if s.Reason == "" {
				s.Reason = "candidate awaits pin verification and lane release"
			}
		}
		out[id] = s
	}
	return out, nil
}
