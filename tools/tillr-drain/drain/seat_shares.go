package drain

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

var errSeatWindowClosed = fmt.Errorf("seat backends closed during claim")

// selectSeatDispatch makes and journals the choice inside the claim transaction.
// Planning remains a read-only capacity preview. Counts cannot race another loop's
// claim, and a failed claim rolls back both the draw and its contribution to counts.
func selectSeatDispatch(tx *store.Tx, d *Decision, p *Policy, shut map[string]bool, at time.Time) (bool, error) {
	if p == nil || d.RunRequest == nil || (d.Action != ActionDispatch && d.Action != ActionRetry) {
		return true, nil
	}
	if commandStage(*d) {
		return true, nil
	}
	if d.WorkflowRestart {
		// A parallel group's primary may already be finished. Check each
		// unfinished retained request independently at the atomic claim too.
		rec, err := terminalRecord(tx.Reader(), d.RunRequest.RunID)
		if err != nil {
			return false, err
		}
		return rec != nil || !shut[d.RunRequest.Backend], nil
	}
	if !d.SelectSeat {
		return true, nil
	}
	if _, override := p.Overrides[d.ItemID]; override {
		return true, nil
	}
	list := p.seatList(d.Seat)
	shares := p.SeatShares[d.Seat]
	pick := p.SeatPicks[d.Seat]
	if len(list) == 0 || (len(shares) == 0 && (pick == "" || pick == "in-order")) {
		return true, nil
	}
	var saved string
	err := tx.QueryRow(`SELECT backend FROM seat_dispatches WHERE run_id=?`, d.RunRequest.RunID).Scan(&saved)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	chosen := saved
	if err == sql.ErrNoRows {
		open := []int{}
		for i, name := range list {
			if !shut[name] {
				open = append(open, i)
			}
		}
		if len(open) == 0 {
			return false, nil
		}
		counts := map[string]int{}
		rows, err := tx.Query(`SELECT backend,count(*) FROM seat_dispatches WHERE seat=? GROUP BY backend`, d.Seat)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var name string
			var n int
			if err := rows.Scan(&name, &n); err != nil {
				rows.Close()
				return false, err
			}
			counts[name] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		index := open[0]
		switch pick {
		case "random":
			// Scale before summing so even very large finite shares cannot overflow.
			max := 1.0
			if len(shares) > 0 {
				max = shares[open[0]]
				for _, i := range open {
					if shares[i] > max {
						max = shares[i]
					}
				}
			}
			total := 0.0
			weight := func(i int) float64 {
				if len(shares) == 0 {
					return 1
				}
				return shares[i] / max
			}
			for _, i := range open {
				total += weight(i)
			}
			drawFn := p.seatDraw
			if drawFn == nil {
				drawFn = rand.Float64
			}
			draw := drawFn() * total
			index = open[len(open)-1]
			for _, i := range open {
				draw -= weight(i)
				if draw < 0 {
					index = i
					break
				}
			}
		case "round-robin":
			var last string
			err := tx.QueryRow(`SELECT backend FROM seat_dispatches WHERE seat=? ORDER BY ordinal DESC LIMIT 1`, d.Seat).Scan(&last)
			if err != nil && err != sql.ErrNoRows {
				return false, err
			}
			for i, name := range list {
				if name == last {
					for hop := 1; hop <= len(list); hop++ {
						j := (i + hop) % len(list)
						if !shut[list[j]] {
							index = j
							break
						}
					}
					break
				}
			}
		default:
			// Earliest next proportional slot wins; ties retain policy order.
			// Equal shares alternate, and 3:1 gives three dispatches of every four.
			weight := func(i int) float64 {
				if len(shares) == 0 {
					return 1
				}
				return shares[i]
			}
			for _, i := range open {
				scale := weight(i)
				if weight(index) > scale {
					scale = weight(index)
				}
				if float64(counts[list[i]]+1)*(weight(index)/scale) < float64(counts[list[index]]+1)*(weight(i)/scale) {
					index = i
				}
			}
		}
		chosen = list[index]
	}
	old := d.RunRequest
	if shut[chosen] {
		return false, nil
	}
	request := p.request(d.ItemID, d.LaneID, old.RunID, chosen)
	// Keep node, continuation, timeout, and correlation metadata while replacing
	// the backend-derived adapter command and model fields.
	copy := *old
	copy.Backend, copy.Command, copy.WorkingDir = request.Backend, request.Command, request.WorkingDir
	if old.WorkingDir == PlainDir {
		copy.WorkingDir = PlainDir
	}
	copy.TimeoutMS = request.TimeoutMS
	if d.Workflow != nil && d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
		copy.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
	}
	stampDecisionRequest(*d, &copy)
	d.RunRequest = &copy
	if d.Designer != nil {
		adapter := ""
		if d.Designer.Backend != "" {
			adapter = p.Backends[d.Designer.Backend].Adapter
		}
		d.DesignerSessionID, d.DesignerReason = chooseDesigner(d.Designer, d.RunRequest, at, adapter)
	}
	return true, recordSeatDispatch(tx, *d)
}

func recordSeatDispatch(tx *store.Tx, d Decision) error {
	if d.Seat == "" || d.RunRequest == nil || d.RunRequest.Backend == "" || commandStage(d) {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO seat_dispatches(run_id,seat,backend) VALUES(?,?,?) ON CONFLICT(run_id) DO NOTHING`, d.RunRequest.RunID, d.Seat, d.RunRequest.Backend)
	if err != nil {
		return err
	}
	var seat, backend string
	if err := tx.QueryRow(`SELECT seat,backend FROM seat_dispatches WHERE run_id=?`, d.RunRequest.RunID).Scan(&seat, &backend); err != nil {
		return err
	}
	if seat != d.Seat || backend != d.RunRequest.Backend {
		return fmt.Errorf("run %s already records seat %s backend %s", d.RunRequest.RunID, seat, backend)
	}
	return nil
}
