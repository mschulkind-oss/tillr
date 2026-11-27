package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// These are the persisted drain graph fields, not a second workflow parser.
type itemGraph struct {
	Stages []struct {
		ID         string   `json:"id"`
		Parent     string   `json:"parent"`
		Kind       string   `json:"kind"`
		Seat       string   `json:"seat"`
		Access     string   `json:"access"`
		CommandRef string   `json:"command_ref"`
		Command    []string `json:"resolved_argv"`
	} `json:"stages"`
	Edges []struct {
		From string `json:"from_node"`
		To   string `json:"to_node"`
	} `json:"edges"`
}
type itemGraphNode struct {
	ID         string                  `json:"id"`
	Parent     string                  `json:"parent"`
	Kind       string                  `json:"kind"`
	Seat       string                  `json:"seat"`
	CommandRef string                  `json:"command_ref"`
	Command    []string                `json:"command"`
	Access     string                  `json:"access"`
	State      string                  `json:"state"`
	Level      int                     `json:"level"`
	Runs       []string                `json:"runs"`
	Executions []itemNodeExecution     `json:"executions"`
	Children   map[string]childrenView `json:"children"`
	Outputs    []itemNodeOutput        `json:"outputs"`
}
type itemNodeSegment struct {
	Ordinal     int   `json:"ordinal"`
	StartedAtMS int64 `json:"started_at_ms,omitempty"`
}
type itemNodeLog struct {
	Path          string `json:"path,omitempty"`
	Present       bool   `json:"present"`
	Bytes         int64  `json:"bytes,omitempty"`
	TranscriptURL string `json:"transcript_url,omitempty"`
}
type itemNodeLease struct {
	LaneID        string `json:"lane_id"`
	Active        bool   `json:"active"`
	LeasedUntilMS *int64 `json:"leased_until_ms,omitempty"`
	LeaseNoExpiry bool   `json:"lease_no_expiry,omitempty"`
}
type itemNodeEnding struct {
	State        string `json:"state"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	EndCause     string `json:"end_cause,omitempty"`
	StartedAtMS  int64  `json:"started_at_ms,omitempty"`
	FinishedAtMS int64  `json:"finished_at_ms,omitempty"`
}
type itemNodeSpend struct {
	Turns   *int     `json:"turns,omitempty"`
	Tokens  *int64   `json:"tokens,omitempty"`
	CostUSD *float64 `json:"cost_usd,omitempty"`
	Live    bool     `json:"live,omitempty"`
	Note    string   `json:"note,omitempty"`
}
type itemNodeExecution struct {
	RunID        string            `json:"run_id"`
	Backend      string            `json:"backend,omitempty"`
	Seat         string            `json:"seat,omitempty"`
	SegmentCount int               `json:"segment_count"`
	Segments     []itemNodeSegment `json:"segments"`
	Log          *itemNodeLog      `json:"log,omitempty"`
	Lease        *itemNodeLease    `json:"lease,omitempty"`
	Ending       *itemNodeEnding   `json:"ending,omitempty"`
	Spend        *itemNodeSpend    `json:"spend,omitempty"`
}
type itemNodeOutput struct {
	Name  string  `json:"name"`
	Type  string  `json:"type"`
	RunID string  `json:"run_id"`
	Body  *string `json:"body"`
	Note  string  `json:"note,omitempty"`
}
type itemGraphAttempt struct {
	Attempt int             `json:"attempt"`
	Nodes   []itemGraphNode `json:"nodes"`
}

func readItemGraph(r *store.Reader, id, selection string, runs []storeRun, children map[string]childrenView) ([]itemGraphAttempt, error) {
	return readItemGraphIn(r, id, selection, runs, children, "")
}

func readItemGraphIn(r *store.Reader, id, selection string, runs []storeRun, children map[string]childrenView, loopDir string) ([]itemGraphAttempt, error) {
	var base itemGraph
	if selection != "" {
		if err := json.Unmarshal([]byte(selection), &base); err != nil {
			return nil, err
		}
	}
	recorded, err := laneNodesIn(r, id, loopDir)
	if err != nil {
		return nil, err
	}
	// Reused nodes point back to a prior attempt's accepted run, not a new run.
	type reuse struct {
		attempt, source int
		node            string
	}
	reused := []reuse{}
	reuseRows, err := r.Query(`SELECT attempt,source_attempt,node_id FROM node_reused WHERE item_id=? ORDER BY rowid`, id)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	if err == nil {
		for reuseRows.Next() {
			var x reuse
			if err := reuseRows.Scan(&x.attempt, &x.source, &x.node); err != nil {
				reuseRows.Close()
				return nil, err
			}
			reused = append(reused, x)
		}
		err = reuseRows.Err()
		reuseRows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, x := range reused {
		for _, row := range recorded {
			if row.Attempt == x.source && row.NodeID == x.node {
				copy := row
				copy.Attempt = x.attempt
				recorded = append(recorded, copy)
				break
			}
		}
	}
	// A started event preserves the graph and node identity of each dispatch, even
	// when a later retry replaces node_runs' one row for the node.
	type dispatch struct {
		Decision struct {
			ItemID    string     `json:"item_id"`
			Attempt   int        `json:"attempt"`
			NodeIndex int        `json:"node_index"`
			Workflow  *itemGraph `json:"workflow"`
		} `json:"decision"`
	}
	type start struct {
		attempt   int
		node, run string
		graph     *itemGraph
	}
	starts := []start{}
	workerAttempts := map[string]int{}
	rows, err := r.Query(`SELECT object_id,payload FROM events WHERE kind='drain.started' AND object_type='run' AND json_extract(payload,'$.decision.item_id')=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var run string
		var raw []byte
		var d dispatch
		if err := rows.Scan(&run, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			rows.Close()
			return nil, err
		}
		w := d.Decision.Workflow
		if w == nil && d.Decision.Attempt > 0 {
			workerAttempts[run] = d.Decision.Attempt
		} else if w != nil && d.Decision.NodeIndex >= 0 && d.Decision.NodeIndex < len(w.Stages) {
			starts = append(starts, start{d.Decision.Attempt, w.Stages[d.Decision.NodeIndex].ID, run, w})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	graphs := map[int]itemGraph{}
	attempts := map[int]bool{}
	for _, n := range recorded {
		attempts[n.Attempt] = true
	}
	for _, s := range starts {
		attempts[s.attempt] = true
		graphs[s.attempt] = *s.graph
	}
	// Proposal rows hold the exact graph after each revision, including refusals.
	rows, err = r.Query(`SELECT attempt,after_graph FROM graph_revisions WHERE item_id=? ORDER BY revision_id`, id)
	if err != nil && !isMissingTable(err) && !isMissingColumn(err) {
		return nil, err
	}
	if err == nil {
		for rows.Next() {
			var attempt int
			var raw string
			if err := rows.Scan(&attempt, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var g itemGraph
			if err := json.Unmarshal([]byte(raw), &g); err != nil {
				rows.Close()
				return nil, err
			}
			graphs[attempt] = g
			attempts[attempt] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(attempts) == 0 && len(base.Stages) > 0 {
		attempts[1] = true
	}
	workers := groupWorkerRuns(id, runs, workerAttempts)
	if len(attempts) == 0 {
		for attempt := range workers {
			attempts[attempt] = true
		}
		if len(attempts) == 0 {
			attempts[1] = true
		}
	}
	numbers := make([]int, 0, len(attempts))
	for n := range attempts {
		numbers = append(numbers, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(numbers)))
	result := []itemGraphAttempt{}
	for _, attempt := range numbers {
		graph, ok := graphs[attempt]
		if !ok {
			graph = base
		}
		a := itemGraphAttempt{Attempt: attempt, Nodes: []itemGraphNode{}}
		if len(graph.Stages) == 0 {
			n := itemGraphNode{ID: "worker", Kind: "agent", State: "not started", Runs: []string{}, Executions: []itemNodeExecution{}, Children: map[string]childrenView{}, Outputs: []itemNodeOutput{}}
			for _, run := range workers[attempt] {
				// The latest execution supplies the node state; all runs retain their links.
				if run.Backend == "command" {
					n.Kind = "command"
					if run.Node != "" {
						n.ID = run.Node
					}
				} else {
					n.Kind = "agent"
				}
				n.Seat = run.Seat
				n.Runs = append(n.Runs, run.RunID)
				n.State = singleRunState(run)
				if n.Kind == "agent" {
					if c, ok := children[run.RunID]; ok {
						n.Children[run.RunID] = c
					}
				}
			}
			a.Nodes = append(a.Nodes, n)
			result = append(result, a)
			continue
		}
		levels := map[string]int{}
		for range graph.Stages {
			changed := false
			for _, e := range graph.Edges {
				if levels[e.To] < levels[e.From]+1 {
					levels[e.To] = levels[e.From] + 1
					changed = true
				}
			}
			if !changed {
				break
			}
		}
		for _, stage := range graph.Stages {
			n := itemGraphNode{ID: stage.ID, Parent: stage.Parent, Kind: stage.Kind, Seat: stage.Seat, CommandRef: stage.CommandRef, Command: stage.Command, Access: stage.Access, State: "not started", Level: levels[stage.ID], Runs: []string{}, Executions: []itemNodeExecution{}, Children: map[string]childrenView{}, Outputs: []itemNodeOutput{}}
			addRun := func(run string) {
				if run == "" {
					return
				}
				for _, existing := range n.Runs {
					if existing == run {
						return
					}
				}
				n.Runs = append(n.Runs, run)
				if c, ok := children[run]; ok {
					n.Children[run] = c
				}
			}
			for _, s := range starts {
				if s.attempt == attempt && s.node == n.ID {
					addRun(s.run)
				}
			}
			for _, row := range recorded {
				if row.Attempt == attempt && row.NodeID == n.ID {
					n.State = row.State
					if row.State != "skipped" {
						addRun(row.RunID)
					}
				}
			}
			a.Nodes = append(a.Nodes, n)
		}
		sort.SliceStable(a.Nodes, func(i, j int) bool { return a.Nodes[i].Level < a.Nodes[j].Level })
		result = append(result, a)
	}
	rows, err = r.Query(`SELECT attempt,node_id,run_id,name,type,location,sha256 FROM node_artifacts WHERE item_id=? ORDER BY rowid`, id)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	if err == nil {
		for rows.Next() {
			var attempt int
			var node, run, name, typ, path, hash string
			if err := rows.Scan(&attempt, &node, &run, &name, &typ, &path, &hash); err != nil {
				rows.Close()
				return nil, err
			}
			p := readArtifact(name, path, "text/plain", true)
			if hash != "" && p.Present && p.SHA256 != hash {
				p.Body = nil
				p.Note = "output differs from the recorded sha256"
			}
			output := itemNodeOutput{Name: name, Type: typ, RunID: run, Body: p.Body, Note: p.Note}
			for i := range result {
				matches := result[i].Attempt == attempt
				for _, x := range reused {
					if x.attempt == result[i].Attempt && x.source == attempt && x.node == node {
						matches = true
					}
				}
				if matches {
					for j := range result[i].Nodes {
						if result[i].Nodes[j].ID == node {
							result[i].Nodes[j].Outputs = append(result[i].Nodes[j].Outputs, output)
						}
					}
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func enrichItemGraphExecutions(r *store.Reader, attempts []itemGraphAttempt, runs []storeRun, lanes []storeLane, laneRoot string) {
	runsByID := make(map[string]storeRun, len(runs))
	for _, run := range runs {
		runsByID[run.RunID] = run
	}
	leaseByRun := make(map[string]storeLane)
	for _, l := range lanes {
		if l.State != laneStateLeased {
			continue
		}
		if l.RunID != "" {
			leaseByRun[l.RunID] = l
		}
		for _, activeRunID := range l.ActiveNodes {
			if activeRunID != "" {
				leaseByRun[activeRunID] = l
			}
		}
	}
	for i := range attempts {
		for j := range attempts[i].Nodes {
			node := &attempts[i].Nodes[j]
			node.Executions = make([]itemNodeExecution, 0, len(node.Runs))
			for _, runID := range node.Runs {
				run, hasRun := runsByID[runID]
				laneID := run.LaneID
				var lease *itemNodeLease
				if l, ok := leaseByRun[runID]; ok {
					if laneID == "" {
						laneID = l.LaneID
					}
					lease = &itemNodeLease{
						LaneID:        l.LaneID,
						Active:        true,
						LeasedUntilMS: l.LeasedUntilMS,
						LeaseNoExpiry: l.LeasedUntilMS == nil,
					}
				}
				segs := readNodeRunSegments(r, runID, run.StartedAtMS)
				logInfo := resolveNodeLog(laneRoot, laneID, runID)
				var ending *itemNodeEnding
				if hasRun {
					ending = &itemNodeEnding{
						State:        run.State,
						ExitCode:     run.ExitCode,
						EndCause:     readRunEndCause(r, runID),
						StartedAtMS:  run.StartedAtMS,
						FinishedAtMS: run.FinishedAtMS,
					}
				}
				var spend *itemNodeSpend
				if hasRun && (run.UsageTokens != nil || run.CostUSD != nil) {
					spend = &itemNodeSpend{
						Tokens:  run.UsageTokens,
						CostUSD: run.CostUSD,
					}
				} else if hasRun && run.State == "running" {
					logPath := runLogPath(laneRoot, laneID, runID)
					live, _ := readLiveRunStream(logPath)
					spend = &itemNodeSpend{
						Turns:   live.Turns,
						Tokens:  live.Tokens,
						CostUSD: live.CostUSD,
						Live:    true,
						Note:    live.Note,
					}
				}
				node.Executions = append(node.Executions, itemNodeExecution{
					RunID:        runID,
					Backend:      run.Backend,
					Seat:         run.Seat,
					SegmentCount: len(segs),
					Segments:     segs,
					Log:          logInfo,
					Lease:        lease,
					Ending:       ending,
					Spend:        spend,
				})
			}
		}
	}
}

// groupWorkerRuns receives runs in chronological order. An attempt can have
// several executions; a restart suffix doesn't change its attempt number.
func groupWorkerRuns(itemID string, runs []storeRun, dispatched map[string]int) map[int][]storeRun {
	// Match drain's runItemSlug for runs whose dispatch event is unavailable.
	slug := itemID
	if len(slug) > 56 {
		sum := sha256.Sum256([]byte(itemID))
		slug = fmt.Sprintf("%s-%x", strings.TrimRight(itemID[:39], "-"), sum[:8])
	}
	numbers := make([]int, len(runs))
	groups := map[int][]storeRun{}
	for i, run := range runs {
		n := dispatched[run.RunID]
		if n == 0 {
			rest, ok := strings.CutPrefix(run.RunID, "run-"+itemID+"-a")
			if !ok && slug != itemID {
				rest, ok = strings.CutPrefix(run.RunID, "run-"+slug+"-a")
			}
			if ok {
				digits, _, _ := strings.Cut(rest, "-")
				if parsed, err := strconv.Atoi(digits); err == nil && parsed > 0 {
					n = parsed
				}
			}
		}
		if n > 0 {
			numbers[i] = n
			groups[n] = nil // reserve known attempts before numbering opaque runs
		}
	}
	legacy := 1
	for i, run := range runs {
		n := numbers[i]
		if n == 0 {
			for {
				if _, taken := groups[legacy]; !taken {
					n = legacy
					break
				}
				legacy++
			}
		}
		groups[n] = append(groups[n], run)
	}
	return groups
}

func singleRunState(run storeRun) string {
	if run.State == "running" || run.FinishedAtMS == 0 && run.State == "" {
		return "running"
	}
	if run.ExitCode != nil && *run.ExitCode == 0 {
		return "passed"
	}
	if run.ExitCode != nil {
		return "failed"
	}
	return "failed"
}
