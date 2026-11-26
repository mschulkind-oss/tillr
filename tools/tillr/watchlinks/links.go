package watchlinks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// ItemLinkedKind is the event kind recorded when an alert is linked to an item.
const ItemLinkedKind = "watch.alert.item-linked"

const (
	openedKind   = "watch.alert.opened"
	trackedKind  = "watch.alert.tracked"
	silencedKind = "watch.alert.silenced"
	settledKind  = "watch.alert.settled"
)

func init() {
	if err := event.Register(ItemLinkedKind, "watch"); err != nil {
		panic(err)
	}
}

// ErrAlertNotFound reports that no episode has the given alert ID.
var ErrAlertNotFound = errors.New("not found")

// Link relation values stored in alert_item_links.relation.
const (
	LinkRelationFiled    = "filed"
	LinkRelationSilence  = "silence"
	LinkRelationFixFault = "fix-fault"
	LinkRelationFixRule  = "fix-rule"
	LinkRelationTracked  = "tracked"
)

// LinkedAlert describes one alert linked to an item, returned by LinkedAlertsForItem.
type LinkedAlert struct {
	AlertID         string `json:"alert_id"`
	RuleID          string `json:"rule_id"`
	SubjectKey      string `json:"subject_key"`
	State           string `json:"state"`
	SettlementState string `json:"settlement_state"`
	Severity        string `json:"severity"`
	Relation        string `json:"relation"`
	LinkedAt        int64  `json:"linked_at"`
	LinkedBy        string `json:"linked_by,omitempty"`
	Note            string `json:"note,omitempty"`
	EventSeq        int64  `json:"event_seq"`
}

// IsFixRelation reports whether rel is a settlement fix relation.
func IsFixRelation(rel string) bool {
	return rel == LinkRelationFixFault || rel == LinkRelationFixRule
}

func nullStringArg(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func tableExists(rd *store.Reader, table string) (bool, error) {
	var n int
	err := rd.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
	return n > 0, err
}

func hasSettlementStateColumn(rd *store.Reader) (bool, error) {
	var n int
	err := rd.QueryRow(`SELECT count(*) FROM pragma_table_info('alert_episodes') WHERE name='settlement_state'`).Scan(&n)
	return n > 0, err
}

// UpsertAlertItemLink inserts or updates an alert_item_links row when the table exists.
func UpsertAlertItemLink(tx *store.Tx, alertID, itemID, relation, by, note string, linkedAt, eventSeq int64) error {
	if alertID == "" || itemID == "" || relation == "" {
		return nil
	}
	hasTable, err := tableExists(tx.Reader(), "alert_item_links")
	if err != nil || !hasTable {
		return err
	}
	if IsFixRelation(relation) {
		_, err = tx.Exec(`INSERT INTO alert_item_links (alert_id, item_id, relation, linked_at, linked_by, note, event_seq)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(alert_id, item_id) DO UPDATE SET
				relation  = excluded.relation,
				linked_at = excluded.linked_at,
				linked_by = excluded.linked_by,
				note      = excluded.note,
				event_seq = excluded.event_seq`,
			alertID, itemID, relation, linkedAt, nullStringArg(by), nullStringArg(note), eventSeq)
		return err
	}
	_, err = tx.Exec(`INSERT INTO alert_item_links (alert_id, item_id, relation, linked_at, linked_by, note, event_seq)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(alert_id, item_id) DO UPDATE SET
			relation  = excluded.relation,
			linked_at = excluded.linked_at,
			linked_by = excluded.linked_by,
			note      = excluded.note,
			event_seq = excluded.event_seq
		WHERE alert_item_links.relation NOT IN ('fix-fault', 'fix-rule')`,
		alertID, itemID, relation, linkedAt, nullStringArg(by), nullStringArg(note), eventSeq)
	return err
}

// LinkAlertItemInTx records a durable link between alertID and itemID in the caller's
// write transaction. It verifies alertID exists in alert_episodes (in any state,
// including resolved), appends watch.alert.item-linked, and upserts alert_item_links
// without reopening, muting, or settling the episode.
func LinkAlertItemInTx(tx *store.Tx, alertID, itemID, relation, by, note string, at time.Time) error {
	if alertID == "" {
		return fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
	}
	if itemID == "" {
		return fmt.Errorf("watch: item_id is required")
	}
	if relation == "" {
		relation = LinkRelationFiled
	}
	hasAlerts, err := tableExists(tx.Reader(), "alert_episodes")
	if err != nil {
		return err
	}
	if !hasAlerts {
		return fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
	}
	var ruleID, subjectKey, severity string
	err = tx.Reader().QueryRow(`SELECT rule_id, subject_key, severity FROM alert_episodes WHERE alert_id = ?`, alertID).
		Scan(&ruleID, &subjectKey, &severity)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
	}
	if err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now()
	}
	payload := map[string]any{
		"alert_id":    alertID,
		"rule_id":     ruleID,
		"subject_key": subjectKey,
		"severity":    severity,
		"item_id":     itemID,
		"relation":    relation,
	}
	if by != "" {
		payload["by"] = by
	}
	if note != "" {
		payload["note"] = note
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	seq, err := tx.Append(event.Event{Kind: ItemLinkedKind, At: at, ObjectType: "alert", ObjectID: &alertID, Payload: raw})
	if err != nil {
		return err
	}
	return UpsertAlertItemLink(tx, alertID, itemID, relation, by, note, at.UnixMilli(), seq)
}

// QuerySurface abstracts *store.Reader and store.MigrationExec for read-only link projection.
type QuerySurface interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ReaderQuerySurface adapts *store.Reader to QuerySurface.
type ReaderQuerySurface struct {
	Reader *store.Reader
}

func (r ReaderQuerySurface) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	return r.Reader.Query(query, args...)
}

func (r ReaderQuerySurface) QueryRowContext(_ context.Context, query string, args ...any) *sql.Row {
	return r.Reader.QueryRow(query, args...)
}

// ProjectedLink is one alert-to-item link projected from episodes and events.
type ProjectedLink struct {
	AlertID  string
	ItemID   string
	Relation string
	LinkedAt int64
	LinkedBy string
	Note     string
	EventSeq int64
}

func mergeProjectedLink(m map[[2]string]ProjectedLink, cand ProjectedLink) {
	if cand.AlertID == "" || cand.ItemID == "" || cand.Relation == "" {
		return
	}
	key := [2]string{cand.AlertID, cand.ItemID}
	existing, ok := m[key]
	if !ok {
		m[key] = cand
		return
	}
	if IsFixRelation(existing.Relation) && !IsFixRelation(cand.Relation) {
		return
	}
	if cand.EventSeq >= existing.EventSeq || IsFixRelation(cand.Relation) {
		m[key] = cand
	}
}

// ProjectAlertItemLinks reconstructs alert-to-item links from alert_episodes and events.
func ProjectAlertItemLinks(ctx context.Context, q QuerySurface) ([]ProjectedLink, error) {
	var hasAlerts int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='alert_episodes'`).Scan(&hasAlerts); err != nil {
		return nil, err
	}
	if hasAlerts == 0 {
		return nil, nil
	}

	colRows, err := q.QueryContext(ctx, `SELECT name FROM pragma_table_info('alert_episodes')`)
	if err != nil {
		return nil, err
	}
	epCols := map[string]bool{}
	for colRows.Next() {
		var name string
		if err := colRows.Scan(&name); err != nil {
			colRows.Close()
			return nil, err
		}
		epCols[name] = true
	}
	if err := colRows.Err(); err != nil {
		colRows.Close()
		return nil, err
	}
	colRows.Close()

	selectCols := []string{"alert_id", "opened_seq", "opened_at"}
	for _, opt := range []string{"tracking_item_id", "outcome", "item_link_kind", "settled_by", "settled_at", "settled_seq", "silence_id"} {
		if epCols[opt] {
			selectCols = append(selectCols, opt)
		} else {
			selectCols = append(selectCols, "NULL AS "+opt)
		}
	}
	epRows, err := q.QueryContext(ctx, `SELECT `+strings.Join(selectCols, ", ")+` FROM alert_episodes`)
	if err != nil {
		return nil, err
	}
	type epMeta struct {
		openedSeq, openedAt int64
		trackingItemID      string
		outcome             string
		itemLinkKind        string
		settledBy           string
		settledAt           int64
		settledSeq          int64
		silenceID           string
	}
	episodes := map[string]epMeta{}
	for epRows.Next() {
		var (
			id                                         string
			openedSeq, openedAt                        int64
			tracking, outcome, linkKind, settledBy, si sql.NullString
			settledAt, settledSeq                      sql.NullInt64
		)
		if err := epRows.Scan(&id, &openedSeq, &openedAt, &tracking, &outcome, &linkKind, &settledBy, &settledAt, &settledSeq, &si); err != nil {
			epRows.Close()
			return nil, err
		}
		episodes[id] = epMeta{
			openedSeq:      openedSeq,
			openedAt:       openedAt,
			trackingItemID: tracking.String,
			outcome:        outcome.String,
			itemLinkKind:   linkKind.String,
			settledBy:      settledBy.String,
			settledAt:      settledAt.Int64,
			settledSeq:     settledSeq.Int64,
			silenceID:      si.String,
		}
	}
	if err := epRows.Err(); err != nil {
		epRows.Close()
		return nil, err
	}
	epRows.Close()

	links := map[[2]string]ProjectedLink{}

	var hasEvents int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='events'`).Scan(&hasEvents); err != nil {
		return nil, err
	}
	if hasEvents > 0 {
		evRows, err := q.QueryContext(ctx,
			`SELECT seq, kind, ts, COALESCE(object_id, ''), payload FROM events
			 WHERE kind IN (?, ?, ?, ?, ?) ORDER BY seq ASC`,
			trackedKind, settledKind, silencedKind, ItemLinkedKind, openedKind,
		)
		if err != nil {
			return nil, err
		}
		for evRows.Next() {
			var (
				seq, atMS int64
				kind, oid string
				raw       []byte
			)
			if err := evRows.Scan(&seq, &kind, &atMS, &oid, &raw); err != nil {
				evRows.Close()
				return nil, err
			}
			var body map[string]any
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			alertID := stringField(body, "alert_id")
			if alertID == "" {
				alertID = oid
			}
			if _, ok := episodes[alertID]; !ok {
				continue
			}
			switch kind {
			case trackedKind:
				itemID := stringField(body, "item_id")
				if itemID == "" {
					itemID = stringField(body, "tracking_item_id")
				}
				if itemID != "" {
					mergeProjectedLink(links, ProjectedLink{
						AlertID:  alertID,
						ItemID:   itemID,
						Relation: LinkRelationTracked,
						LinkedAt: atMS,
						LinkedBy: stringField(body, "by"),
						EventSeq: seq,
					})
				}
			case settledKind:
				itemID := stringField(body, "item_id")
				outcome := stringField(body, "outcome")
				if itemID != "" && IsFixRelation(outcome) {
					mergeProjectedLink(links, ProjectedLink{
						AlertID:  alertID,
						ItemID:   itemID,
						Relation: outcome,
						LinkedAt: atMS,
						LinkedBy: stringField(body, "by"),
						EventSeq: seq,
					})
				}
			case silencedKind:
				itemID := stringField(body, "item_id")
				if itemID != "" {
					mergeProjectedLink(links, ProjectedLink{
						AlertID:  alertID,
						ItemID:   itemID,
						Relation: LinkRelationSilence,
						LinkedAt: atMS,
						LinkedBy: stringField(body, "by"),
						Note:     stringField(body, "reason"),
						EventSeq: seq,
					})
				}
			case ItemLinkedKind:
				itemID := stringField(body, "item_id")
				rel := stringField(body, "relation")
				if rel == "" {
					rel = LinkRelationFiled
				}
				if itemID != "" {
					mergeProjectedLink(links, ProjectedLink{
						AlertID:  alertID,
						ItemID:   itemID,
						Relation: rel,
						LinkedAt: atMS,
						LinkedBy: stringField(body, "by"),
						Note:     stringField(body, "note"),
						EventSeq: seq,
					})
				}
			case openedKind:
				if stringField(body, "state") == "silenced" {
					sid := stringField(body, "silence_id")
					if sid != "" {
						var itemID, createdBy, reason string
						if err := q.QueryRowContext(ctx,
							`SELECT tracking_item_id, created_by, reason FROM watch_silences WHERE silence_id = ?`, sid,
						).Scan(&itemID, &createdBy, &reason); err == nil && itemID != "" {
							mergeProjectedLink(links, ProjectedLink{
								AlertID:  alertID,
								ItemID:   itemID,
								Relation: LinkRelationSilence,
								LinkedAt: atMS,
								LinkedBy: createdBy,
								Note:     reason,
								EventSeq: seq,
							})
						}
					}
				}
			}
		}
		if err := evRows.Err(); err != nil {
			evRows.Close()
			return nil, err
		}
		evRows.Close()
	}

	for alertID, ep := range episodes {
		if ep.trackingItemID == "" {
			continue
		}
		key := [2]string{alertID, ep.trackingItemID}
		if _, exists := links[key]; exists {
			continue
		}
		rel := LinkRelationTracked
		switch {
		case IsFixRelation(ep.outcome) || ep.outcome == LinkRelationSilence:
			rel = ep.outcome
		case ep.itemLinkKind != "":
			rel = ep.itemLinkKind
		}
		linkedAt := ep.settledAt
		if linkedAt == 0 {
			linkedAt = ep.openedAt
		}
		eventSeq := ep.settledSeq
		if eventSeq == 0 {
			eventSeq = ep.openedSeq
		}
		links[key] = ProjectedLink{
			AlertID:  alertID,
			ItemID:   ep.trackingItemID,
			Relation: rel,
			LinkedAt: linkedAt,
			LinkedBy: ep.settledBy,
			EventSeq: eventSeq,
		}
	}

	out := make([]ProjectedLink, 0, len(links))
	for _, l := range links {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LinkedAt != out[j].LinkedAt {
			return out[i].LinkedAt < out[j].LinkedAt
		}
		if out[i].AlertID != out[j].AlertID {
			return out[i].AlertID < out[j].AlertID
		}
		return out[i].ItemID < out[j].ItemID
	})
	return out, nil
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// LinkedAlertsForItem returns all alerts linked to itemID, ordered by linked_at, alert_id.
// Returns nil, nil when alert_episodes is absent. Projects without writing when
// alert_item_links is absent.
func LinkedAlertsForItem(rd *store.Reader, itemID string) ([]LinkedAlert, error) {
	if itemID == "" {
		return nil, nil
	}
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil || !hasAlerts {
		return nil, err
	}
	hasSettlement, err := hasSettlementStateColumn(rd)
	if err != nil {
		return nil, err
	}
	settlementExpr := `'uninvestigated'`
	if hasSettlement {
		settlementExpr = `e.settlement_state`
	}
	hasLinks, err := tableExists(rd, "alert_item_links")
	if err != nil {
		return nil, err
	}
	if hasLinks {
		rows, err := rd.Query(fmt.Sprintf(`SELECT l.alert_id, e.rule_id, e.subject_key, e.state, %s, e.severity,
			l.relation, l.linked_at, COALESCE(l.linked_by, ''), COALESCE(l.note, ''), l.event_seq
			FROM alert_item_links l
			JOIN alert_episodes e ON e.alert_id = l.alert_id
			WHERE l.item_id = ?
			ORDER BY l.linked_at ASC, l.alert_id ASC`, settlementExpr), itemID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []LinkedAlert
		for rows.Next() {
			var la LinkedAlert
			if err := rows.Scan(&la.AlertID, &la.RuleID, &la.SubjectKey, &la.State, &la.SettlementState, &la.Severity,
				&la.Relation, &la.LinkedAt, &la.LinkedBy, &la.Note, &la.EventSeq); err != nil {
				return nil, err
			}
			out = append(out, la)
		}
		return out, rows.Err()
	}

	projected, err := ProjectAlertItemLinks(context.Background(), ReaderQuerySurface{Reader: rd})
	if err != nil {
		return nil, err
	}
	var out []LinkedAlert
	for _, p := range projected {
		if p.ItemID != itemID {
			continue
		}
		var la LinkedAlert
		err := rd.QueryRow(fmt.Sprintf(`SELECT e.alert_id, e.rule_id, e.subject_key, e.state, %s, e.severity
			FROM alert_episodes e WHERE e.alert_id = ?`, settlementExpr), p.AlertID).
			Scan(&la.AlertID, &la.RuleID, &la.SubjectKey, &la.State, &la.SettlementState, &la.Severity)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		la.Relation = p.Relation
		la.LinkedAt = p.LinkedAt
		la.LinkedBy = p.LinkedBy
		la.Note = p.Note
		la.EventSeq = p.EventSeq
		out = append(out, la)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LinkedAt != out[j].LinkedAt {
			return out[i].LinkedAt < out[j].LinkedAt
		}
		return out[i].AlertID < out[j].AlertID
	})
	return out, nil
}
