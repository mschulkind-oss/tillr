package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// A pass is the decisions sharing dispatched_at, as defined in the observatory
// redesign's entity glossary. Phase pointers preserve unrecorded measurements.
type passRow struct {
	DispatchedAtMS int64          `json:"dispatched_at_ms"`
	Decisions      []passDecision `json:"decisions"`
	Phases         *passPhases    `json:"phases"`
	SpendStarted   *passSpend     `json:"spend_started_in_loop"`
	SpendCarried   *passSpend     `json:"spend_carried_over"`
	SessionID      string         `json:"session_id,omitempty"`
	Stop           string         `json:"stop,omitempty"`
	Detail         string         `json:"detail,omitempty"`
}

type passDecision struct {
	ItemID string `json:"item_id"`
	Place  int    `json:"place,omitempty"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	LaneID string `json:"lane_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	Seat   string `json:"seat,omitempty"`
	passID string
}

type passPhases struct {
	ClaimMS    *int64 `json:"claim_ms"`
	StageMS    *int64 `json:"stage_ms"`
	DispatchMS *int64 `json:"dispatch_ms"`
	GateMS     *int64 `json:"gate_ms"`
	LandMS     *int64 `json:"land_ms"`
}

type passBounds struct {
	before, since *int64
	limit         int
}

func parsePassTime(value string) (int64, error) {
	if ms, err := strconv.ParseInt(value, 10, 64); err == nil && ms >= 0 {
		return ms, nil
	}
	if at, err := time.Parse(time.RFC3339Nano, value); err == nil && at.UnixMilli() >= 0 {
		return at.UnixMilli(), nil
	}
	return 0, fmt.Errorf("want a nonnegative Unix millisecond integer or RFC 3339 time")
}

func passesFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("passes")
	path := fs.String("store", "", "the store to read (required)")
	last := fs.Int("last", 3, "the number of newest passes to print (positive)")
	loopDir := fs.String("loop-dir", "", "the loop result directory (default: <store dir>/loop)")
	asJSON := fs.Bool("json", false, "print the pass rows as JSON instead of a table")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" || *last < 1 {
		return record.Envelope{}, usagef("passes needs --store <path> and a positive --last")
	}
	if *loopDir == "" {
		*loopDir = filepath.Join(filepath.Dir(*path), "loop")
	}
	ctx := context.Background()
	db, err := store.OpenRead(ctx, *path)
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	defer db.Close()
	var rows []passRow
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		rows, err = assemblePasses(r, *loopDir, passBounds{limit: *last})
		return err
	})
	if err != nil {
		var mismatch schemaMismatch
		if errors.As(err, &mismatch) {
			fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
			return record.Envelope{}, record.ExitIncompatible
		}
		return record.Envelope{}, exitFor(err)
	}
	if *asJSON {
		err = json.NewEncoder(os.Stdout).Encode(rows)
	} else {
		err = printPasses(os.Stdout, rows)
	}
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	return record.Envelope{}, record.ExitOK
}

func (s *server) passesPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	bounds := passBounds{limit: 50}
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return nil, failure(codeBadQuery, "invalid query: %v", err)
	}
	for _, field := range []struct {
		name string
		dest **int64
	}{{"before", &bounds.before}, {"since", &bounds.since}} {
		values, present := query[field.name]
		if !present {
			continue
		}
		if len(values) != 1 {
			return nil, failure(codeBadQuery, "%s must appear once", field.name)
		}
		value, err := parsePassTime(values[0])
		if err != nil {
			return nil, failure(codeBadQuery, "%s: %v", field.name, err)
		}
		*field.dest = &value
	}
	if bounds.before != nil && bounds.since != nil && *bounds.since >= *bounds.before {
		return nil, failure(codeBadQuery, "since must be less than before (before is exclusive)")
	}
	// The answer is a function of the store, the query and the loop's drain result files
	// (the session stops it names), so it is shared until one of them changes (shared.go).
	return s.passesShare.get(req.Context(), r, query.Encode(), func() string { return passesExtraStamp(r, s.obs.loopDir) }, func() shareBuild {
		rows, err := assemblePasses(r, s.obs.loopDir, bounds)
		if err != nil {
			return shareBuild{failure: failure(codeStoreUnreadable, "passes could not read its sources: %v", err)}
		}
		return shareBuild{value: rows}
	})
}

// passesExtraStamp fingerprints the non-event tables assemblePasses reads (hold-only
// dispatches, recent drain_passes phase timings and drain_sessions intervals without
// refreshed_at) alongside loopResultsStamp, so 1s session heartbeats do not rebuild passes.
func passesExtraStamp(r *store.Reader, loopDir string) string {
	var b strings.Builder
	b.WriteString(loopResultsStamp(loopDir))
	var maxDispatch int64
	if err := r.QueryRow(`SELECT COALESCE(MAX(rowid), 0) FROM dispatches`).Scan(&maxDispatch); err == nil {
		fmt.Fprintf(&b, "|d:%d", maxDispatch)
	}
	var maxPass int64
	if err := r.QueryRow(`SELECT COALESCE(MAX(rowid), 0) FROM drain_passes`).Scan(&maxPass); err == nil {
		fmt.Fprintf(&b, "|p:%d", maxPass)
	}
	if rows, err := r.Query(`SELECT * FROM drain_passes ORDER BY rowid DESC LIMIT 10`); err == nil {
		if cols, err := rows.Columns(); err == nil {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			for rows.Next() {
				if rows.Scan(ptrs...) == nil {
					fmt.Fprintf(&b, "|pr:%v", vals)
				}
			}
		}
		rows.Close()
	}
	if rows, err := r.Query(`SELECT session_id, started_at, COALESCE(ended_at, -1) FROM drain_sessions ORDER BY rowid DESC LIMIT 10`); err == nil {
		for rows.Next() {
			var sid string
			var started, ended int64
			if rows.Scan(&sid, &started, &ended) == nil {
				fmt.Fprintf(&b, "|s:%s:%d:%d", sid, started, ended)
			}
		}
		rows.Close()
	}
	return b.String()
}

func assemblePasses(r *store.Reader, loopDir string, b passBounds) ([]passRow, error) {
	clause := " WHERE 1=1"
	args := []any{}
	if b.before != nil {
		clause += " AND dispatched_at < ?"
		args = append(args, *b.before)
	}
	if b.since != nil {
		clause += " AND dispatched_at >= ?"
		args = append(args, *b.since)
	}
	args = append(args, b.limit)
	clocks, err := r.Query(`SELECT DISTINCT dispatched_at FROM dispatches`+clause+` ORDER BY dispatched_at DESC LIMIT ?`, args...)
	out := []passRow{}
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for clocks.Next() {
		var row passRow
		if err := clocks.Scan(&row.DispatchedAtMS); err != nil {
			clocks.Close()
			return nil, err
		}
		out = append(out, row)
	}
	err = clocks.Err()
	clocks.Close()
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	items, err := readItems(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	places := itemPlaces(items)
	sessions, err := readPassSessions(r, loopDir)
	if err != nil {
		return nil, err
	}
	for i := range out {
		row := &out[i]
		row.Decisions, err = readPassDecisions(r, row.DispatchedAtMS, places)
		if err != nil {
			return nil, err
		}
		row.SpendStarted, row.SpendCarried, err = readPassSpend(r, row.Decisions)
		if err != nil {
			return nil, err
		}
		row.Phases, err = readPassPhases(r, *row)
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			if row.DispatchedAtMS < session.start || session.upper != nil && row.DispatchedAtMS >= *session.upper {
				continue
			}
			row.SessionID = session.id
			var newest sql.NullInt64
			if session.upper == nil {
				err = r.QueryRow(`SELECT MAX(dispatched_at) FROM dispatches WHERE dispatched_at >= ?`, session.start).Scan(&newest)
			} else {
				err = r.QueryRow(`SELECT MAX(dispatched_at) FROM dispatches WHERE dispatched_at >= ? AND dispatched_at < ?`, session.start, *session.upper).Scan(&newest)
			}
			if err != nil {
				return nil, err
			}
			if newest.Valid && newest.Int64 == row.DispatchedAtMS {
				row.Stop, row.Detail = session.stop, session.detail
			}
			break
		}
	}
	return out, nil
}

func readPassDecisions(r *store.Reader, at int64, places map[string]int) ([]passDecision, error) {
	query := `SELECT item_id,action,reason,COALESCE(lane_id,''),COALESCE(run_id,''),COALESCE(seat,''),COALESCE(pass_id,'') FROM dispatches WHERE dispatched_at = ? ORDER BY rowid`
	rows, err := r.Query(query, at)
	for isMissingColumn(err) {
		switch {
		case strings.Contains(err.Error(), "no such column: seat"):
			query = strings.Replace(query, "COALESCE(seat,'')", "''", 1)
		case strings.Contains(err.Error(), "no such column: pass_id"):
			query = strings.Replace(query, "COALESCE(pass_id,'')", "''", 1)
		default:
			return nil, err
		}
		rows, err = r.Query(query, at)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []passDecision{}
	for rows.Next() {
		var d passDecision
		if err := rows.Scan(&d.ItemID, &d.Action, &d.Reason, &d.LaneID, &d.RunID, &d.Seat, &d.passID); err != nil {
			return nil, err
		}
		d.Place = places[d.ItemID]
		if !passDispatchAction(d.Action) {
			d.LaneID, d.RunID, d.Seat = "", "", ""
		} else if d.Seat == "" && d.RunID != "" {
			var seat string
			err := r.QueryRow(`SELECT COALESCE(seat, '') FROM runs WHERE run_id = ?`, d.RunID).Scan(&seat)
			if err != nil && !isMissingColumn(err) && !isMissingTable(err) && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				d.Seat = seat
			}
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Place != out[j].Place {
			if out[i].Place == 0 {
				return false
			}
			if out[j].Place == 0 {
				return true
			}
			return out[i].Place < out[j].Place
		}
		return out[i].ItemID < out[j].ItemID
	})
	return out, rows.Err()
}

func passDispatchAction(action string) bool {
	return action == "dispatch" || action == "retry"
}

func readPassPhases(r *store.Reader, row passRow) (*passPhases, error) {
	dispatched := false
	ids := map[string]bool{}
	for _, d := range row.Decisions {
		if passDispatchAction(d.Action) {
			dispatched = true
		}
		if d.passID != "" {
			ids[d.passID] = true
		}
	}
	// New writers also measure hold-only rounds. Those are not dispatched phases.
	if !dispatched {
		return nil, nil
	}
	predicate := "started_at = ?"
	args := []any{row.DispatchedAtMS}
	if len(ids) > 0 {
		predicate = "tick_id IN ("
		args = nil
		for id := range ids {
			if len(args) > 0 {
				predicate += ","
			}
			predicate += "?"
			args = append(args, id)
		}
		predicate += ")"
	}
	var claim sql.NullInt64
	var values [4]sql.NullInt64
	err := r.QueryRow(`SELECT MAX(claim_ms),SUM(stage_ms),SUM(dispatch_ms),
  SUM(CASE WHEN COALESCE(gate_count,1) > 0 THEN gate_ms END),SUM(land_ms)
  FROM drain_passes WHERE `+predicate, args...).Scan(&claim, &values[0], &values[1], &values[2], &values[3])
	if isMissingColumn(err) {
		err = r.QueryRow(`SELECT MAX(claim_ms),SUM(stage_ms),SUM(dispatch_ms),SUM(gate_ms),SUM(land_ms) FROM drain_passes WHERE `+predicate, args...).Scan(&claim, &values[0], &values[1], &values[2], &values[3])
	}
	if isMissingColumn(err) {
		claim = sql.NullInt64{}
		err = r.QueryRow(`SELECT SUM(stage_ms),SUM(dispatch_ms),
  SUM(CASE WHEN COALESCE(gate_count,1) > 0 THEN gate_ms END),SUM(land_ms)
  FROM drain_passes WHERE `+predicate, args...).Scan(&values[0], &values[1], &values[2], &values[3])
	}
	if isMissingColumn(err) {
		claim = sql.NullInt64{}
		err = r.QueryRow(`SELECT SUM(stage_ms),SUM(dispatch_ms),SUM(gate_ms),SUM(land_ms) FROM drain_passes WHERE `+predicate, args...).Scan(&values[0], &values[1], &values[2], &values[3])
	}
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !claim.Valid && !values[0].Valid && !values[1].Valid && !values[2].Valid && !values[3].Valid {
		return nil, nil
	}
	phases := &passPhases{}
	if claim.Valid {
		v := claim.Int64
		phases.ClaimMS = &v
	}
	dest := []**int64{&phases.StageMS, &phases.DispatchMS, &phases.GateMS, &phases.LandMS}
	for i := range values {
		if values[i].Valid {
			value := values[i].Int64
			*dest[i] = &value
		}
	}
	return phases, nil
}

type passSession struct {
	id           string
	start        int64
	upper        *int64
	ended        *int64
	stop, detail string
}
type passStop struct {
	at           int64
	stop, detail string
}

// Result records are stamped at stop, not at session start, and carry no session
// id. Associate them by the recorded session interval; legacy stores use result
// clocks as interval ends. Never borrow the next session's stop for a missing file.
func readPassSessions(r *store.Reader, loopDir string) ([]passSession, error) {
	rows, err := r.Query(`SELECT session_id,started_at,ended_at FROM drain_sessions ORDER BY started_at,session_id`)
	legacy := isMissingTable(err)
	if err != nil && !legacy {
		return nil, err
	}
	sessions := []passSession{}
	if !legacy {
		for rows.Next() {
			var s passSession
			var ended sql.NullInt64
			if err := rows.Scan(&s.id, &s.start, &ended); err != nil {
				rows.Close()
				return nil, err
			}
			if ended.Valid {
				s.ended = &ended.Int64
			}
			s.stop = "stop not recorded"
			sessions = append(sessions, s)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(loopDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading loop directory: %w", err)
	}
	stops := []passStop{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		matched, _ := filepath.Match("drain-*.json", name)
		if !matched {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(loopDir, name))
		if err != nil {
			return nil, err
		}
		env, err := record.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		if env.Kind != loopResultKind {
			return nil, fmt.Errorf("%s is not a %s record", name, loopResultKind)
		}
		var payload struct {
			Stop   string `json:"stop"`
			Detail string `json:"detail"`
			Ticks  *int   `json:"ticks"`
		}
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return nil, fmt.Errorf("reading %s payload: %w", name, err)
		}
		if payload.Stop == "" {
			return nil, fmt.Errorf("%s names no stop reason", name)
		}
		// Startup faults write a result without creating a session or a pass.
		// They must not fill an earlier session's missing result. Older records
		// without the tick count remain usable as historical stop evidence.
		if payload.Ticks != nil && *payload.Ticks == 0 {
			continue
		}
		stops = append(stops, passStop{env.CreatedAt.UnixMilli(), payload.Stop, payload.Detail})
	}
	sort.Slice(stops, func(i, j int) bool { return stops[i].at < stops[j].at })
	if legacy || len(sessions) == 0 {
		return legacyPassSessions(stops, nil), nil
	}
	for i := range sessions {
		s := &sessions[i]
		if i+1 < len(sessions) {
			s.upper = &sessions[i+1].start
		}
		if s.ended == nil {
			continue
		}
		for _, stop := range stops {
			if stop.at < *s.ended || s.upper != nil && stop.at >= *s.upper {
				continue
			}
			s.stop, s.detail = stop.stop, stop.detail
			break
		}
		// Decisions made after a recorded end do not belong to that session.
		if s.upper == nil || *s.ended < *s.upper {
			upper := *s.ended + 1
			s.upper = &upper
		}
	}
	// Adding session observations must not hide older result-file history.
	firstStart := sessions[0].start
	return append(legacyPassSessions(stops, &firstStart), sessions...), nil
}

func legacyPassSessions(stops []passStop, before *int64) []passSession {
	sessions := []passSession{}
	start := int64(0)
	for _, stop := range stops {
		if before != nil && stop.at >= *before {
			break
		}
		upper := stop.at + 1
		sessions = append(sessions, passSession{start: start, upper: &upper, stop: stop.stop, detail: stop.detail})
		start = upper
	}
	if before == nil || start < *before {
		sessions = append(sessions, passSession{start: start, upper: before, stop: "stop not recorded"})
	}
	return sessions
}

func printPasses(w io.Writer, rows []passRow) error {
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "pass %s (%d)\n", time.UnixMilli(row.DispatchedAtMS).UTC().Format(time.RFC3339Nano), row.DispatchedAtMS); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "  item_id\taction\treason\tlane_id\trun_id\tseat"); err != nil {
			return err
		}
		for _, d := range row.Decisions {
			if _, err := fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", d.ItemID, d.Action, d.Reason, d.LaneID, d.RunID, d.Seat); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if row.Phases == nil {
			if _, err := fmt.Fprintln(w, "  phases: absent"); err != nil {
				return err
			}
		} else {
			for _, phase := range []struct {
				name string
				ms   *int64
			}{{"claim", row.Phases.ClaimMS}, {"stage", row.Phases.StageMS}, {"dispatch", row.Phases.DispatchMS}, {"gate", row.Phases.GateMS}, {"land", row.Phases.LandMS}} {
				value := "absent"
				if phase.ms != nil {
					value = fmt.Sprintf("%d ms", *phase.ms)
				}
				if _, err := fmt.Fprintf(w, "  %s: %s\n", phase.name, value); err != nil {
					return err
				}
			}
		}
		for _, spend := range []struct {
			label string
			total *passSpend
		}{
			{"spend started in this loop", row.SpendStarted}, {"spend carried over from earlier loops", row.SpendCarried},
		} {
			value := "not recorded"
			if spend.total != nil {
				value = spend.total.String()
			}
			if _, err := fmt.Fprintf(w, "  %s: %s\n", spend.label, value); err != nil {
				return err
			}
		}
		if row.Stop != "" {
			if _, err := fmt.Fprintf(w, "  stop: %s\n", row.Stop); err != nil {
				return err
			}
			if row.Detail != "" {
				if _, err := fmt.Fprintf(w, "  detail: %s\n", row.Detail); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
