package main

import (
	"database/sql"
	"fmt"
	"io"
	"sort"

	"github.com/mschulkind-oss/tillr/store"
)

type costSum struct {
	value   float64
	runs    int
	missing bool
}

func (s *costSum) add(v sql.NullFloat64) {
	s.runs++
	if !v.Valid {
		s.missing = true
	} else {
		s.value += v.Float64
	}
}
func (s costSum) String() string {
	if s.missing || s.runs == 0 {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f", s.value)
}

type landedCost struct {
	id, seat, workflow string
	sum                costSum
}

func resolveRunSeat(itemSeat string, nodeID, nodeSeat, dispatchSeat sql.NullString) string {
	if nodeSeat.Valid && nodeSeat.String != "" {
		return nodeSeat.String
	}
	if dispatchSeat.Valid && dispatchSeat.String != "" {
		return dispatchSeat.String
	}
	if nodeID.Valid {
		return "unknown"
	}
	return itemSeat
}

// landedCosts reads the run ledger, not the current policy. A legacy node's seat is
// recoverable from seat_dispatches where present; it is never invented from its item.
func landedCosts(r *store.Reader, out io.Writer) error {
	// A read-open must not migrate an old store. Missing tables and columns mean
	// unrecorded facts, not an error or a zero-dollar measurement.
	columns := func(table, column string) (bool, error) {
		var exists int
		err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, table, column).Scan(&exists)
		return exists != 0, err
	}
	hasLandings, err := columns("landings", "item_id")
	if err != nil {
		return err
	}
	if !hasLandings {
		_, err = fmt.Fprintln(out, "median cost, last 20 landed build items: unknown")
		return err
	}
	hasNodes, err := columns("node_runs", "run_id")
	if err != nil {
		return err
	}
	hasSeat, err := columns("node_runs", "seat")
	if err != nil {
		return err
	}
	hasDispatch, err := columns("seat_dispatches", "seat")
	if err != nil {
		return err
	}
	hasWorkflow, err := columns("workflow_selections", "workflow")
	if err != nil {
		return err
	}
	hasCost, err := columns("runs", "usage_cost_usd")
	if err != nil {
		return err
	}
	nodeID, nodeSeat, dispatchSeat, workflow, cost := "NULL", "NULL", "NULL", "''", "NULL"
	runJoins := ""
	if hasNodes {
		runJoins += " LEFT JOIN node_runs n ON n.run_id=r.run_id"
		nodeID = "n.run_id"
		if hasSeat {
			nodeSeat = "n.seat"
		}
	}
	if hasDispatch {
		runJoins += " LEFT JOIN seat_dispatches sd ON sd.run_id=r.run_id"
		dispatchSeat = "sd.seat"
	}
	joins := runJoins
	if hasWorkflow {
		joins += " LEFT JOIN workflow_selections w ON w.item_id=i.item_id"
		workflow = "COALESCE(w.workflow,'')"
	}
	if hasCost {
		cost = "r.usage_cost_usd"
	}
	q := `WITH latest AS (SELECT item_id, MAX(event_seq) seq FROM landings WHERE result='landed' GROUP BY item_id)
 SELECT i.item_id,COALESCE(NULLIF(i.seat,''),'build'),` + workflow + `,r.run_id,` + nodeID + `,` + nodeSeat + `,` + dispatchSeat + `,` + cost + `
 FROM latest l JOIN items i ON i.item_id=l.item_id
 LEFT JOIN events e ON e.seq=l.seq AND e.object_id=i.item_id
 LEFT JOIN runs r ON r.item_id=i.item_id AND (e.ts IS NULL OR r.started_at<=e.ts)` + joins + `
 ORDER BY l.seq DESC,i.item_id,r.run_id`
	rows, err := r.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	var items []*landedCost
	landedSeats := map[string]*costSum{}
	for rows.Next() {
		var id, seat, workflow string
		var runID, nodeID, nodeSeat, dispatchSeat sql.NullString
		var value sql.NullFloat64
		if err := rows.Scan(&id, &seat, &workflow, &runID, &nodeID, &nodeSeat, &dispatchSeat, &value); err != nil {
			return err
		}
		if len(items) == 0 || items[len(items)-1].id != id {
			items = append(items, &landedCost{id: id, seat: seat, workflow: workflow})
		}
		if !runID.Valid {
			continue
		}
		current := items[len(items)-1]
		current.sum.add(value)
		runSeat := resolveRunSeat(seat, nodeID, nodeSeat, dispatchSeat)
		if landedSeats[runSeat] == nil {
			landedSeats[runSeat] = &costSum{}
		}
		landedSeats[runSeat].add(value)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	allSeats := map[string]*costSum{}
	allQ := `SELECT COALESCE(NULLIF(i.seat,''),'build'),` + nodeID + `,` + nodeSeat + `,` + dispatchSeat + `,` + cost + `
 FROM runs r
 LEFT JOIN items i ON i.item_id=r.item_id` + runJoins + `
 ORDER BY r.run_id`
	allRows, err := r.Query(allQ)
	if err != nil {
		return err
	}
	defer allRows.Close()
	for allRows.Next() {
		var seat string
		var nodeID, nodeSeat, dispatchSeat sql.NullString
		var value sql.NullFloat64
		if err := allRows.Scan(&seat, &nodeID, &nodeSeat, &dispatchSeat, &value); err != nil {
			return err
		}
		runSeat := resolveRunSeat(seat, nodeID, nodeSeat, dispatchSeat)
		if allSeats[runSeat] == nil {
			allSeats[runSeat] = &costSum{}
		}
		allSeats[runSeat].add(value)
	}
	if err := allRows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		fmt.Fprintf(out, "cost per landed item: %s %s\n", item.id, item.sum.String())
	}
	names := make([]string, 0, len(allSeats))
	for name := range allSeats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		landed := "$0.00"
		if ls := landedSeats[name]; ls != nil && ls.runs > 0 {
			landed = ls.String()
		}
		fmt.Fprintf(out, "cost per seat: %s %s (landed: %s)\n", name, allSeats[name].String(), landed)
	}
	var builds, planned []costSum
	for _, item := range items {
		if item.seat == "build" || item.workflow == "planned-build@v1" {
			builds = append(builds, item.sum)
		}
		if item.workflow == "planned-build@v1" {
			planned = append(planned, item.sum)
		}
	}
	fmt.Fprintf(out, "median cost, last 20 landed build items: %s\n", medianCost(builds))
	if len(planned) >= 10 {
		fmt.Fprintf(out, "median cost, last 20 landed planned-build@v1 items: %s\n", medianCost(planned))
	}
	return nil
}
func medianCost(sums []costSum) string {
	if len(sums) > 20 {
		sums = sums[:20]
	}
	if len(sums) == 0 {
		return "unknown"
	}
	values := make([]float64, 0, len(sums))
	for _, s := range sums {
		if s.missing || s.runs == 0 {
			return "unknown"
		}
		values = append(values, s.value)
	}
	sort.Float64s(values)
	n := len(values)
	median := values[n/2]
	if n%2 == 0 {
		median = (values[n/2-1] + median) / 2
	}
	return fmt.Sprintf("$%.2f", median)
}
