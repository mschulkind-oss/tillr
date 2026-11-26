package drain

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// Only the pinned report's header supplies window facts. In particular the footer
// tail can exclude the header, and the model's body cannot invent a window.
func refusedWindow(rec run.Record) (kind string, fraction float64, reset time.Time, ok bool) {
	if rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode == 0 || *rec.ExitCode == adapterWallExit {
		return
	}
	var body []byte
	for _, ref := range rec.ArtifactRefs {
		if ref.Kind == "report" {
			body, _ = os.ReadFile(ref.Ref)
			break
		}
	}
	headerText, _, delimited := strings.Cut(string(body), "\n---\n")
	if !delimited || !strings.HasPrefix(headerText, "# run report\n") {
		return
	}
	header := map[string]string{}
	for _, line := range strings.Split(headerText, "\n") {
		key, value, found := strings.Cut(line, ": ")
		if found {
			header[key] = strings.TrimSpace(value)
		}
	}
	kind = header["rate limit kind"]
	if kind == "" || kind == "unknown" || header["status"] != "rejected" {
		return
	}
	fraction, err := strconv.ParseFloat(header["fraction"], 64)
	if err != nil || math.IsNaN(fraction) || math.IsInf(fraction, 0) || fraction < 0 {
		return kind, 0, time.Time{}, false
	}
	seconds, err := strconv.ParseInt(header["reset"], 10, 64)
	if err != nil || seconds <= 0 || seconds > math.MaxInt64/1000 {
		return kind, fraction, time.Time{}, false
	}
	return kind, fraction, time.Unix(seconds, 0).UTC(), true
}

func (l *Loop) recordBackendWindow(ctx context.Context, rec run.Record) error {
	if rec.Backend == "command" {
		return nil
	}
	kind, fraction, reset, ok := refusedWindow(rec)
	if !ok || !reset.After(l.cfg.Now()) {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// Settling a durable refusal again must not undo a successful probe.
		// Compare finish events, not just run IDs: a continued run can refuse again.
		var reopened bool
		if err := tx.Reader().QueryRow(`SELECT EXISTS (
			SELECT 1 FROM events WHERE object_type='backend' AND object_id=? AND kind=? AND seq > (
				SELECT max(seq) FROM events WHERE object_type='run' AND object_id=? AND kind=?
			))`, rec.Backend, backendQuotaReopenedKind, rec.RunID, run.FinishKind).Scan(&reopened); err != nil || reopened {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_windows (backend,kind,fraction,status,reset_at,run_id)
   VALUES (?,?,?,'rejected',?,?) ON CONFLICT(backend) DO UPDATE SET
   kind=excluded.kind,fraction=excluded.fraction,status=excluded.status,reset_at=excluded.reset_at,run_id=excluded.run_id
   WHERE excluded.reset_at >= backend_windows.reset_at`, rec.Backend, kind, fraction, reset.UnixMilli(), rec.RunID)
		if err != nil {
			return err
		}
		return l.pauseQuotaPeers(tx, rec.Backend, rec.RunID)
	})
}

const backendQuotaReopenedKind = "drain.backend-quota-reopened"

func init() {
	if err := event.Register(backendQuotaReopenedKind, Utility); err != nil {
		panic(err)
	}
}

// The probe event and deletion share one write transaction. The window must
// already exist when the success is recorded; a later refusal can close it anew.
func reopenBackendWindow(tx *store.Tx, backend string, probeSeq int64, at time.Time) error {
	reopened := struct {
		ProbeSeq int64   `json:"probe_seq"`
		RunID    string  `json:"run_id"`
		Kind     string  `json:"kind"`
		Fraction float64 `json:"fraction"`
		ResetAt  int64   `json:"reset_at"`
	}{ProbeSeq: probeSeq}
	err := tx.QueryRow(`DELETE FROM backend_windows WHERE backend=? AND reset_at>?
		RETURNING run_id,kind,fraction,reset_at`, backend, at.UnixMilli()).Scan(&reopened.RunID, &reopened.Kind, &reopened.Fraction, &reopened.ResetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return appendEffectOn(tx, backendQuotaReopenedKind, "backend", backend, reopened, at)
}

func readShutBackends(r *store.Reader, at time.Time) (map[string]bool, error) {
	windows, err := readBackendWindows(r, at)
	shut := map[string]bool{}
	for name := range windows {
		shut[name] = true
	}
	return shut, err
}

func readBackendWindows(r *store.Reader, at time.Time) (map[string]time.Time, error) {
	rows, err := r.Query(`SELECT backend, reset_at FROM backend_windows WHERE reset_at > ?`, at.UnixMilli())
	// A read-only planner can inspect stores created before windows were added.
	if err != nil && strings.Contains(err.Error(), "no such table: backend_windows") {
		return map[string]time.Time{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var name string
		var reset int64
		if err := rows.Scan(&name, &reset); err != nil {
			return nil, err
		}
		out[name] = time.UnixMilli(reset).UTC()
	}
	return out, rows.Err()
}

// A preview never changes the named backend. For a fresh shared seat it
// permits a claim to make the actual choice unless every option is shut.
func (p Policy) seatMayStart(itemID, seat, name string, shut map[string]bool) bool {
	if _, override := p.Overrides[itemID]; !override && len(p.SeatShares[seat]) > 0 {
		for _, candidate := range p.seatList(seat) {
			if !shut[candidate] {
				return true
			}
		}
		return false
	}
	return name != "" && !shut[name]
}
