package policy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

var ErrSeatWindowClosed = fmt.Errorf("seat backends closed during claim")

// A closed draw is kept even though the rest of the claim must roll back.
// It does not count as a dispatch until the run actually claims a lane.
type SeatWait struct {
	RunID   string
	Seat    string
	Backend string
}

func (w *SeatWait) Error() string {
	return fmt.Sprintf("seat %s chose backend %s, which is closed", w.Seat, w.Backend)
}

func KeepSeatWait(tx *store.Tx, claim func() error) error {
	if _, err := tx.Exec("SAVEPOINT seat_claim"); err != nil {
		return err
	}
	if err := claim(); err != nil {
		var wait *SeatWait
		if !errors.As(err, &wait) {
			return err
		}
		if _, err := tx.Exec("ROLLBACK TO seat_claim"); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO seat_waiting_choices(run_id,seat,backend) VALUES(?,?,?)
			ON CONFLICT(run_id) DO UPDATE SET seat=excluded.seat,backend=excluded.backend
			WHERE seat!=excluded.seat OR backend!=excluded.backend`, wait.RunID, wait.Seat, wait.Backend); err != nil {
			return err
		}
	}
	_, err := tx.Exec("RELEASE seat_claim")
	return err
}

func SeatClaim(p *Policy, claim func(context.Context, *store.Tx) error) func(context.Context, *store.Tx) error {
	if p == nil || len(p.SeatShares) == 0 {
		return claim
	}
	return func(ctx context.Context, tx *store.Tx) error {
		return KeepSeatWait(tx, func() error { return claim(ctx, tx) })
	}
}

func WaitingSeatBackend(r *store.Reader, runID, seat string, list []string) (string, error) {
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

func (p *Policy) UsesSeatShares(itemID, seat string, selectSeat, workflowRestart bool) bool {
	if p == nil || !selectSeat || workflowRestart || len(p.SeatShares[seat]) == 0 {
		return false
	}
	_, override := p.Overrides[itemID]
	return !override
}

// ChooseSeatBackend selects a backend for a shared seat inside the claim
// transaction, honoring any previously saved or waiting choice. If the chosen
// backend is shut, it returns *SeatWait so KeepSeatWait can persist the choice.
func ChooseSeatBackend(tx *store.Tx, p *Policy, runID, seat string, shut map[string]bool) (string, error) {
	list := p.SeatList(seat)
	shares := p.SeatShares[seat]
	pick := p.SeatPicks[seat]
	var saved string
	err := tx.QueryRow(`SELECT backend FROM seat_dispatches WHERE run_id=?`, runID).Scan(&saved)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == sql.ErrNoRows {
		saved, err = WaitingSeatBackend(tx.Reader(), runID, seat, list)
		if err != nil {
			return "", err
		}
	}
	chosen := saved
	if chosen == "" {
		counts := map[string]int{}
		rows, err := tx.Query(`SELECT backend,count(*) FROM seat_dispatches WHERE seat=? GROUP BY backend`, seat)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var name string
			var n int
			if err := rows.Scan(&name, &n); err != nil {
				rows.Close()
				return "", err
			}
			counts[name] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		index := 0
		switch pick {
		case "random":
			// Scale before summing so even very large finite shares cannot overflow.
			maxShare := slices.Max(shares)
			total := 0.0
			for _, share := range shares {
				total += share / maxShare
			}
			drawFn := p.SeatDraw
			if drawFn == nil {
				drawFn = rand.Float64
			}
			draw := drawFn() * total
			index = len(list) - 1
			for i, share := range shares {
				draw -= share / maxShare
				if draw < 0 {
					index = i
					break
				}
			}
		case "round-robin":
			var last string
			err := tx.QueryRow(`SELECT backend FROM seat_dispatches WHERE seat=? ORDER BY ordinal DESC LIMIT 1`, seat).Scan(&last)
			if err != nil && err != sql.ErrNoRows {
				return "", err
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
	if shut[chosen] {
		return "", &SeatWait{RunID: runID, Seat: seat, Backend: chosen}
	}
	return chosen, nil
}

func RecordSeatDispatchRow(tx *store.Tx, runID, seat, backend string) error {
	_, err := tx.Exec(`INSERT INTO seat_dispatches(run_id,seat,backend) VALUES(?,?,?) ON CONFLICT(run_id) DO NOTHING`, runID, seat, backend)
	if err != nil {
		return err
	}
	var recordedSeat, recordedBackend string
	if err := tx.QueryRow(`SELECT seat,backend FROM seat_dispatches WHERE run_id=?`, runID).Scan(&recordedSeat, &recordedBackend); err != nil {
		return err
	}
	if recordedSeat != seat || recordedBackend != backend {
		return fmt.Errorf("run %s already records seat %s backend %s", runID, recordedSeat, recordedBackend)
	}
	return nil
}
