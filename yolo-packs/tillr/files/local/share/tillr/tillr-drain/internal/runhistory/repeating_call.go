package runhistory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// RepeatingCallKind records the ending and the route chosen for it, on the run.
const RepeatingCallKind = "drain.repeating-call"

const (
	ReasonRepeatingCall      = "repeating-call"
	ReasonRepeatingCallFresh = "restarted in a fresh session: the earlier session repeated one identical call"
)

const (
	RepeatActionFreshAttempt = "fresh"
	RepeatActionHold         = "hold"
)

// RepeatedCall is one repeating-call ending, as its event records it and as the dispatch
// that answers it carries it into staging.
type RepeatedCall struct {
	RunID       string   `json:"run_id"`
	ItemID      string   `json:"item_id"`
	NodeID      string   `json:"node_id,omitempty"`
	Backend     string   `json:"backend"`
	Tool        string   `json:"tool"`
	Args        string   `json:"args"`
	ArgsDigest  string   `json:"args_digest"`
	Streak      int      `json:"streak"`
	Action      string   `json:"action,omitempty"`
	Shape       string   `json:"shape,omitempty"`
	Backends    []string `json:"backends"`
	Route       string   `json:"route"`
	NextBackend string   `json:"next_backend,omitempty"`
}

func init() {
	if err := event.Register(RepeatingCallKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// RepeatingRoute answers the route for a run that ended for a repeating call on backend,
// given the RepeatedCall its own dispatch answered (nil when it answered none) and whether
// the run already recovered from a loop in-run.
func RepeatingRoute(prior *RepeatedCall, backend string, recovered bool) (route, next string, chain []string) {
	if prior != nil {
		chain = append(chain, prior.Backends...)
	}
	if len(chain) == 0 || chain[len(chain)-1] != backend {
		chain = append(chain, backend)
	}
	if prior == nil && !recovered {
		return RepeatActionFreshAttempt, backend, chain
	}
	return RepeatActionHold, "", chain
}

func (rc RepeatedCall) CallLabel() string {
	if rc.Tool != "" {
		return rc.Tool
	}
	if rc.Shape != "" {
		return "(" + rc.Shape + ")"
	}
	return "(no tool)"
}

// Describe is the call in one line, for the loop's report and a preservation reason.
func (rc RepeatedCall) Describe() string {
	return fmt.Sprintf("pi ended the session: the model repeated one identical call %d times in a row (%s %s)", rc.Streak, rc.CallLabel(), rc.Args)
}

// ReadRepeatedCalls answers every recorded repeating-call ending, keyed by run id.
func ReadRepeatedCalls(r *store.Reader) (map[string]RepeatedCall, error) {
	out := map[string]RepeatedCall{}
	rows, err := r.Query(`SELECT object_id, payload FROM events WHERE kind=? ORDER BY seq`, RepeatingCallKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id sql.NullString
		var body []byte
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		var rc RepeatedCall
		if err := json.Unmarshal(body, &rc); err != nil {
			return nil, drainmodel.Internalf("reading the repeating-call ending of %q: %v", id.String, err)
		}
		if rc.Route == "failover" {
			rc.Route, rc.NextBackend = RepeatActionHold, ""
		}
		out[id.String] = rc
	}
	return out, rows.Err()
}

// ReadRepeatedCallsForRuns reads repeating-call endings scoped to runIDs via events_by_object.
func ReadRepeatedCallsForRuns(r *store.Reader, runIDs []string) (map[string]RepeatedCall, error) {
	out := map[string]RepeatedCall{}
	if len(runIDs) == 0 {
		return out, nil
	}
	events, err := r.EventsForObjects(0, runIDs, RepeatingCallKind)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		var rc RepeatedCall
		if err := json.Unmarshal(e.Payload, &rc); err != nil {
			return nil, drainmodel.Internalf("reading the repeating-call ending of %q: %v", *e.ObjectID, err)
		}
		if rc.Route == "failover" {
			rc.Route, rc.NextBackend = RepeatActionHold, ""
		}
		out[*e.ObjectID] = rc
	}
	return out, nil
}

// RepeatedCallBrief is the brief's section for a run dispatched because an earlier
// session repeated one identical call.
func RepeatedCallBrief(rc RepeatedCall) string {
	var b strings.Builder
	b.WriteString("\n## Your previous session\n\n")
	fmt.Fprintf(&b, "This work's previous run, %s, was ended by pi because its session repeated one identical tool call %d times in a row, each time with the same result:\n\n```\n%s %s\n```\n\n", rc.RunID, rc.Streak, rc.CallLabel(), rc.Args)
	fmt.Fprintf(&b, "This run is a new session on %s; nothing from that conversation carries over. This is not a count against the item. That call's result has not changed, so do not make it again expecting a different one.\n", rc.NextBackend)
	fmt.Fprintf(&b, "\nThe previous run's committed head, when it committed, is pinned at `refs/tillr/runs/%s`, and uncommitted work it left is kept at `%s%s`. Read them with `git show` or `git diff`, and reuse what is sound; do not move either ref.\n", rc.RunID, preserved.Prefix, rc.RunID)
	return b.String()
}
