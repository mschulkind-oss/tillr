package main

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mschulkind-oss/tillr/store"
)

// These read-only payloads mirror the adapter's measurements, not its prices.
type modelUsage struct {
	Backend    string  `json:"backend"`
	Model      *string `json:"model"`
	Tokens     *int64  `json:"tokens,omitempty"`
	Input      *int64  `json:"input,omitempty"`
	Output     *int64  `json:"output,omitempty"`
	CacheRead  *int64  `json:"cache_read,omitempty"`
	CacheWrite *int64  `json:"cache_write,omitempty"`
}
type turnUsage struct {
	modelUsage
	ID            string   `json:"id"`
	StartedMS     *float64 `json:"started_at_ms,omitempty"`
	FinishedMS    *float64 `json:"finished_at_ms,omitempty"`
	FirstOutputMS *float64 `json:"first_output_ms,omitempty"`
	OutputTPS     *float64 `json:"output_tokens_per_second,omitempty"`
}
type runUsage struct {
	Backend       string       `json:"backend"`
	Tokens        *int64       `json:"tokens"`
	Models        []modelUsage `json:"models"`
	Turns         []turnUsage  `json:"turns"`
	FirstOutputMS *float64     `json:"first_output_ms,omitempty"`
	OutputTPS     *float64     `json:"output_tokens_per_second,omitempty"`
}
type usageSum struct {
	Tokens         *int64       `json:"tokens"`
	Models         []modelUsage `json:"models"`
	UnrecordedRuns int          `json:"unrecorded_runs"`
	LowerBound     bool         `json:"lower_bound"`
}

func readUsage(r *store.Reader, run storeRun) (runUsage, error) {
	out := runUsage{Backend: run.Backend, Tokens: run.UsageTokens, Models: []modelUsage{}, Turns: []turnUsage{}}
	if run.EventSeq > 0 {
		var body []byte
		// Old fixtures may have an event_seq naming another event or none at all.
		rows, err := r.Query(`SELECT payload FROM events WHERE seq=? AND kind='run.finish'`, run.EventSeq)
		if err != nil {
			return out, err
		}
		if rows.Next() {
			err = rows.Scan(&body)
		}
		closeErr := rows.Close()
		if err != nil {
			return out, err
		}
		if closeErr != nil {
			return out, closeErr
		}
		if len(body) > 0 {
			var finish struct {
				Usage *struct {
					Details *runUsage `json:"details"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(body, &finish); err != nil {
				return out, fmt.Errorf("reading run %s usage: %w", run.RunID, err)
			}
			if finish.Usage != nil && finish.Usage.Details != nil {
				d := finish.Usage.Details
				out.Models, out.Turns, out.FirstOutputMS, out.OutputTPS = d.Models, d.Turns, d.FirstOutputMS, d.OutputTPS
			}
		}
	}
	for i := range out.Models {
		out.Models[i].Backend = run.Backend
		if len(out.Models) == 1 && out.Models[i].Tokens == nil {
			out.Models[i].Tokens = out.Tokens
		}
	}
	for i := range out.Turns {
		out.Turns[i].Backend = run.Backend
	}
	if len(out.Models) == 0 && run.UsageTokens != nil {
		// Historical aggregate tokens have no known kinds or wire model.
		out.Models = append(out.Models, modelUsage{Backend: run.Backend, Tokens: run.UsageTokens})
	}
	return out, nil
}

// Allocated runs with no terminal row are unrecorded, not free. Include them
// even after a lease is lost: absence of a finish is exactly a missing measurement.
func readUsageRuns(r *store.Reader) ([]storeRun, error) {
	runs, err := readRuns(r)
	if err != nil {
		return nil, err
	}
	allocated, err := readAllocations(r)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, run := range runs {
		known[run.RunID] = true
	}
	for id, a := range allocated {
		if !known[id] {
			runs = append(runs, storeRun{RunID: id, ItemID: a.ItemID, LaneID: a.LaneID, Backend: a.Backend, Seat: a.Seat, Workflow: a.Workflow, Node: a.Node, StartedAtMS: a.StartedAtMS})
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].RunID < runs[j].RunID })
	return runs, nil
}

func usageClock(v *float64) *int64 {
	if v == nil {
		return nil
	}
	n := int64(*v)
	return &n
}
func usageInRange(ms int64, p performanceRange) bool {
	if (p.FromMS != nil || p.ToMS != nil) && ms <= 0 {
		return false
	}
	return (p.FromMS == nil || ms >= *p.FromMS) && (p.ToMS == nil || ms < *p.ToMS)
}

func addToken(a, b *int64) *int64 {
	if b == nil {
		return a
	}
	v := *b
	if a != nil {
		v += *a
	}
	return &v
}
func sumUsage(r *store.Reader, runs []storeRun) (usageSum, error) {
	out := usageSum{Models: []modelUsage{}}
	groups := map[string][]modelUsage{}
	for _, run := range runs {
		u, err := readUsage(r, run)
		if err != nil {
			return out, err
		}
		if u.Tokens == nil {
			out.UnrecordedRuns++
		} else {
			out.Tokens = addToken(out.Tokens, u.Tokens)
		}
		for _, m := range u.Models {
			name := ""
			if m.Model != nil {
				name = *m.Model
			}
			key := m.Backend + "\x00" + name
			groups[key] = append(groups[key], m)
		}
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ms := groups[key]
		m := modelUsage{Backend: ms[0].Backend, Model: ms[0].Model}
		fields := []struct {
			get  func(modelUsage) *int64
			dest **int64
		}{
			{func(m modelUsage) *int64 { return m.Tokens }, &m.Tokens},
			{func(m modelUsage) *int64 { return m.Input }, &m.Input},
			{func(m modelUsage) *int64 { return m.Output }, &m.Output},
			{func(m modelUsage) *int64 { return m.CacheRead }, &m.CacheRead},
			{func(m modelUsage) *int64 { return m.CacheWrite }, &m.CacheWrite},
		}
		for _, f := range fields {
			complete := true
			for _, part := range ms {
				n := f.get(part)
				if n == nil {
					complete = false
					break
				}
				*f.dest = addToken(*f.dest, n)
			}
			if !complete {
				*f.dest = nil
			}
		}
		out.Models = append(out.Models, m)
	}
	out.LowerBound = out.UnrecordedRuns > 0
	return out, nil
}
