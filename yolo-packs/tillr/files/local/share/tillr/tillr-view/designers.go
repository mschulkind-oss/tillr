// Architecture and invariants: docs/reference/tillr-designer-sessions.md
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type designerView struct {
	SessionID            string          `json:"session_id"`
	LastRequest          string          `json:"last_request"`
	CacheLifetimeSeconds *int            `json:"cache_lifetime_seconds"`
	ContextTokens        *int            `json:"context_tokens"`
	Compacted            bool            `json:"compacted"`
	State                string          `json:"state"`
	WarmUntil            *string         `json:"warm_until"`
	Batches              []designerBatch `json:"batches"`
	CostUSD              *float64        `json:"cost_usd"`
	MissingCosts         int             `json:"missing_costs"`
}
type designerBatch struct {
	ItemID  string   `json:"item_id"`
	RunID   string   `json:"run_id"`
	Reason  string   `json:"reason"`
	CostUSD *float64 `json:"cost_usd"`
}
type designView struct {
	Path         string        `json:"path"`
	LastDesigner *designerView `json:"last_designer"`
}
type arrivalCounts struct {
	WithinLifetime int `json:"within_lifetime"`
	AfterLifetime  int `json:"after_lifetime"`
	NoSession      int `json:"no_session"`
	Unmeasured     int `json:"unmeasured"`
}
type designsData struct {
	Designs  []designView  `json:"designs"`
	Arrivals arrivalCounts `json:"arrivals"`
}

func hasTable(r *store.Reader, name string) (bool, error) {
	var exists bool
	err := r.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", name).Scan(&exists)
	return exists, err
}

func readLastDesigner(r *store.Reader, doc string) (*designerView, error) {
	exists, err := hasTable(r, "last_designers")
	if err != nil || !exists {
		return nil, err
	}
	var raw string
	if err := r.QueryRow("SELECT record FROM last_designers WHERE document=?", doc).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	d := &designerView{Batches: []designerBatch{}, State: "expired"}
	if err := json.Unmarshal([]byte(raw), d); err != nil {
		return nil, err
	}
	if d.Compacted {
		d.State = "compacted"
	} else if last, err := time.Parse(time.RFC3339Nano, d.LastRequest); err == nil && d.CacheLifetimeSeconds != nil {
		until := last.Add(time.Duration(*d.CacheLifetimeSeconds) * time.Second)
		if time.Now().Before(until) {
			d.State = "warm"
			stamp := until.Format(time.RFC3339Nano)
			d.WarmUntil = &stamp
		}
	}
	exists, err = hasTable(r, "comment_runs")
	if err != nil || !exists {
		return d, err
	}
	rows, err := r.Query(`SELECT c.item_id,c.run_id,c.reason,r.usage_cost_usd FROM comment_runs c LEFT JOIN runs r USING(run_id) WHERE c.session_id=? ORDER BY c.run_id`, d.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sum := 0.0
	for rows.Next() {
		var b designerBatch
		var cost sql.NullFloat64
		if err := rows.Scan(&b.ItemID, &b.RunID, &b.Reason, &cost); err != nil {
			return nil, err
		}
		if cost.Valid {
			v := cost.Float64
			b.CostUSD = &v
			sum += v
		} else {
			d.MissingCosts++
		}
		d.Batches = append(d.Batches, b)
	}
	if d.MissingCosts == 0 {
		d.CostUSD = &sum
	}
	return d, rows.Err()
}

func (s *server) designsPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	out := designsData{Designs: []designView{}}
	paths := map[string]bool{}
	for _, query := range []string{"SELECT document FROM last_designers", "SELECT document FROM comment_batches", "SELECT design_doc FROM items WHERE design_doc IS NOT NULL AND design_doc<>''"} {
		rows, err := r.Query(query)
		if isMissingTable(err) {
			continue
		}
		if err != nil {
			return nil, unreadable(err)
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				rows.Close()
				return nil, unreadable(err)
			}
			paths[path] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, unreadable(err)
		}
	}
	names := []string{}
	for path := range paths {
		if filter := req.URL.Query().Get("path"); filter == "" || filter == path {
			names = append(names, path)
		}
	}
	sort.Strings(names)
	for _, path := range names {
		d, err := readLastDesigner(r, path)
		if err != nil {
			return nil, unreadable(err)
		}
		out.Designs = append(out.Designs, designView{path, d})
	}
	exists, err := hasTable(r, "comment_arrivals")
	if err != nil {
		return nil, unreadable(err)
	}
	if exists {
		err = r.QueryRow(`SELECT COALESCE(sum(CASE WHEN session_id<>'' AND seconds_since_request<=cache_lifetime_seconds THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN session_id<>'' AND seconds_since_request>cache_lifetime_seconds THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN session_id='' THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN session_id<>'' AND (seconds_since_request IS NULL OR cache_lifetime_seconds IS NULL) THEN 1 ELSE 0 END),0) FROM comment_arrivals`).Scan(&out.Arrivals.WithinLifetime, &out.Arrivals.AfterLifetime, &out.Arrivals.NoSession, &out.Arrivals.Unmeasured)
		if err != nil {
			return nil, unreadable(err)
		}
	}
	return out, nil
}

type commentThreadView struct {
	ID        string            `json:"id"`
	Comment   string            `json:"comment"`
	Reactions []json.RawMessage `json:"reactions"`
	Replies   []replyView       `json:"replies"`
}
type replyView struct {
	ID      string `json:"id"`
	RunID   string `json:"run_id"`
	Round   int    `json:"round"`
	Summary string `json:"summary"`
	State   string `json:"state"`
}
type commentItemView struct {
	Document         string              `json:"document"`
	Runs             []designerBatch     `json:"runs"`
	Comments         []commentThreadView `json:"comments"`
	ConfirmationNote string              `json:"confirmation_note"`
}

func readCommentItem(r *store.Reader, id string) (*commentItemView, error) {
	exists, err := hasTable(r, "comment_batches")
	if err != nil || !exists {
		return nil, err
	}
	var raw string
	if err := r.QueryRow("SELECT batch FROM comment_batches WHERE item_id=?", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	out := &commentItemView{Runs: []designerBatch{}, Comments: []commentThreadView{}, ConfirmationNote: "replies are not being confirmed because no Vantage server is set"}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, err
	}
	for i := range out.Comments {
		out.Comments[i].Replies = []replyView{}
	}
	exists, err = hasTable(r, "comment_confirmation")
	if err != nil {
		return nil, err
	}
	if exists {
		var configured bool
		err = r.QueryRow("SELECT configured FROM comment_confirmation WHERE id=1").Scan(&configured)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if configured {
			out.ConfirmationNote = ""
		}
	}
	exists, err = hasTable(r, "comment_runs")
	if err != nil || !exists {
		return out, err
	}
	rows, err := r.Query("SELECT item_id,run_id,reason FROM comment_runs WHERE item_id=? ORDER BY run_id", id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b designerBatch
		if err := rows.Scan(&b.ItemID, &b.RunID, &b.Reason); err != nil {
			rows.Close()
			return nil, err
		}
		out.Runs = append(out.Runs, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	exists, err = hasTable(r, "comment_deliveries")
	if err != nil || !exists {
		return out, err
	}
	// Old stores can have deliveries but no confirmation measurements yet.
	stateTable, err := hasTable(r, "comment_reply_states")
	if err != nil {
		return nil, err
	}
	states := map[string]string{}
	if stateTable {
		rows, err = r.Query("SELECT s.run_id,s.comment_id,s.state FROM comment_reply_states s JOIN comment_deliveries d USING(run_id) WHERE d.item_id=?", id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var run, comment, state string
			if err := rows.Scan(&run, &comment, &state); err != nil {
				rows.Close()
				return nil, err
			}
			states[run+"\x00"+comment] = state
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	rows, err = r.Query("SELECT run_id,body,written FROM comment_deliveries WHERE item_id=? ORDER BY run_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var run, body string
		var written bool
		if err := rows.Scan(&run, &body, &written); err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(strings.NewReader(body))
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			var reply replyView
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				return nil, err
			}
			reply.RunID = run
			reply.State = "waiting on the served checkout"
			if written {
				reply.State = "written"
				if state := states[run+"\x00"+reply.ID]; state != "" {
					reply.State = state
				}
			}
			for i := range out.Comments {
				if out.Comments[i].ID == reply.ID {
					out.Comments[i].Replies = append(out.Comments[i].Replies, reply)
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return out, rows.Err()
}
