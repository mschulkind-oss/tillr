package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

var errSeatWindowClosed = fmt.Errorf("seat backends closed during claim")

// A closed draw is kept even though the rest of the claim must roll back.
// It does not count as a dispatch until the run actually claims a lane.
type seatWait struct{ runID, seat, backend string }

func (w *seatWait) Error() string {
	return fmt.Sprintf("seat %s chose backend %s, which is closed", w.seat, w.backend)
}

func keepSeatWait(tx *store.Tx, claim func() error) error {
	if _, err := tx.Exec("SAVEPOINT seat_claim"); err != nil {
		return err
	}
	if err := claim(); err != nil {
		var wait *seatWait
		if !errors.As(err, &wait) {
			return err
		}
		if _, err := tx.Exec("ROLLBACK TO seat_claim"); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO seat_waiting_choices(run_id,seat,backend) VALUES(?,?,?)
			ON CONFLICT(run_id) DO UPDATE SET seat=excluded.seat,backend=excluded.backend
			WHERE seat!=excluded.seat OR backend!=excluded.backend`, wait.runID, wait.seat, wait.backend); err != nil {
			return err
		}
	}
	_, err := tx.Exec("RELEASE seat_claim")
	return err
}

func seatClaim(p *Policy, claim func(context.Context, *store.Tx) error) func(context.Context, *store.Tx) error {
	if p == nil || len(p.SeatShares) == 0 {
		return claim
	}
	return func(ctx context.Context, tx *store.Tx) error {
		return keepSeatWait(tx, func() error { return claim(ctx, tx) })
	}
}

func waitingSeatBackend(r *store.Reader, runID, seat string, list []string) (string, error) {
	var saved string
	err := r.QueryRow(`SELECT backend FROM seat_waiting_choices WHERE run_id=? AND seat=?`, runID, seat).Scan(&saved)
	if err == sql.ErrNoRows || (err != nil && strings.Contains(err.Error(), "no such table: seat_waiting_choices")) {
		return "", nil
	}
	if slices.Contains(list, saved) {
		return saved, err
	}
	return "", err
}

func (p *Policy) usesSeatShares(d Decision) bool {
	if p == nil || !d.SelectSeat || d.WorkflowRestart || len(p.SeatShares[d.Seat]) == 0 {
		return false
	}
	_, override := p.Overrides[d.ItemID]
	return !override
}

// selectSeatDispatch makes and journals the choice inside the claim transaction.
// Planning remains a read-only capacity preview. Choose before checking availability;
// a closed draw keeps its backend without creating a dispatch.
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
	if !p.usesSeatShares(*d) {
		return true, nil
	}
	list := p.seatList(d.Seat)
	shares := p.SeatShares[d.Seat]
	pick := p.SeatPicks[d.Seat]
	var saved string
	err := tx.QueryRow(`SELECT backend FROM seat_dispatches WHERE run_id=?`, d.RunRequest.RunID).Scan(&saved)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == sql.ErrNoRows {
		saved, err = waitingSeatBackend(tx.Reader(), d.RunRequest.RunID, d.Seat, list)
		if err != nil {
			return false, err
		}
	}
	chosen := saved
	if chosen == "" {
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
		index := 0
		switch pick {
		case "random":
			// Scale before summing so even very large finite shares cannot overflow.
			max := slices.Max(shares)
			total := 0.0
			for _, share := range shares {
				total += share / max
			}
			drawFn := p.seatDraw
			if drawFn == nil {
				drawFn = rand.Float64
			}
			draw := drawFn() * total
			index = len(list) - 1
			for i, share := range shares {
				draw -= share / max
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
					index = (i + 1) % len(list)
					break
				}
			}
		default:
			// Earliest next proportional slot wins; ties retain policy order.
			// Equal shares alternate, and 3:1 gives three dispatches of every four.
			for i, weight := range shares {
				scale := max(weight, shares[index])
				if float64(counts[list[i]]+1)*(shares[index]/scale) < float64(counts[list[index]]+1)*(weight/scale) {
					index = i
				}
			}
		}
		chosen = list[index]
	}
	old := d.RunRequest
	if shut[chosen] {
		return false, &seatWait{old.RunID, d.Seat, chosen}
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
	if _, err := tx.Exec(`DELETE FROM seat_waiting_choices WHERE run_id=?`, old.RunID); err != nil {
		return false, err
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
