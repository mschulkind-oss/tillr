package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watchlinks"
)

const itemLinkedKind = watchlinks.ItemLinkedKind

// Link relation values stored in alert_item_links.relation.
const (
	LinkRelationFiled    = watchlinks.LinkRelationFiled
	LinkRelationSilence  = watchlinks.LinkRelationSilence
	LinkRelationFixFault = watchlinks.LinkRelationFixFault
	LinkRelationFixRule  = watchlinks.LinkRelationFixRule
	LinkRelationTracked  = watchlinks.LinkRelationTracked
)

const alertItemLinksDDL = `
CREATE TABLE IF NOT EXISTS alert_item_links (
    alert_id   TEXT NOT NULL REFERENCES alert_episodes(alert_id),
    item_id    TEXT NOT NULL,
    relation   TEXT NOT NULL,
    linked_at  INTEGER NOT NULL,
    linked_by  TEXT,
    note       TEXT,
    event_seq  INTEGER NOT NULL,
    PRIMARY KEY (alert_id, item_id)
);
CREATE INDEX IF NOT EXISTS alert_item_links_by_item
    ON alert_item_links(item_id, linked_at, alert_id);
`

// LinkedAlert describes one alert linked to an item, returned by LinkedAlertsForItem.
type LinkedAlert = watchlinks.LinkedAlert

// AlertItemLink describes one item linked to an alert, returned by LinkedItemsForAlert
// and LinkedItemsByAlert.
type AlertItemLink struct {
	AlertID       string `json:"alert_id"`
	ItemID        string `json:"item_id"`
	Relation      string `json:"relation"`
	LinkedAt      int64  `json:"linked_at"`
	LinkedBy      string `json:"linked_by,omitempty"`
	Note          string `json:"note,omitempty"`
	EventSeq      int64  `json:"event_seq"`
	ActiveSilence bool   `json:"active_silence"`
}

// AlertFilter selects episodes in ReadAlertEpisodes.
type AlertFilter struct {
	State           string
	RuleID          string
	Severity        string
	SettlementState string
	TriageState     string
}

// AlertTransition describes one state-change row reconstructed from stored events.
type AlertTransition struct {
	Seq         int64  `json:"seq"`
	Timestamp   int64  `json:"timestamp"`
	StateChange string `json:"state_change"`
	Kind        string `json:"kind"`
	Actor       string `json:"actor"`
	Detail      string `json:"detail,omitempty"`
}

// AlertEpisodeRecord is the read-only projection of an alert_episodes row.
type AlertEpisodeRecord struct {
	AlertID               string            `json:"alert_id"`
	RuleID                string            `json:"rule_id"`
	SubjectKey            string            `json:"subject_key"`
	EpisodeNum            int64             `json:"episode_num"`
	State                 string            `json:"state"`
	SettlementState       string            `json:"settlement_state"`
	Severity              string            `json:"severity"`
	ConditionFingerprint  string            `json:"condition_fingerprint"`
	OpenedSeq             int64             `json:"opened_seq"`
	OpenedAt              int64             `json:"opened_at"`
	LastObservedSeq       int64             `json:"last_observed_seq"`
	LastObservedAt        int64             `json:"last_observed_at"`
	ObservationCount      int64             `json:"observation_count"`
	ResolvedSeq           *int64            `json:"resolved_seq,omitempty"`
	ResolvedAt            *int64            `json:"resolved_at,omitempty"`
	ResolvedReason        string            `json:"resolved_reason,omitempty"`
	Variables             map[string]string `json:"variables,omitempty"`
	VariablesJSON         string            `json:"variables_json"`
	Outcome               string            `json:"outcome,omitempty"`
	ItemLinkKind          string            `json:"item_link_kind,omitempty"`
	TrackingItemID        string            `json:"tracking_item_id,omitempty"`
	SilenceID             string            `json:"silence_id,omitempty"`
	InvestigatedBy        string            `json:"investigated_by,omitempty"`
	InvestigatedAt        *int64            `json:"investigated_at,omitempty"`
	InvestigationCause    string            `json:"investigation_cause,omitempty"`
	InvestigationEvidence string            `json:"investigation_evidence,omitempty"`
	SettledBy             string            `json:"settled_by,omitempty"`
	SettledAt             *int64            `json:"settled_at,omitempty"`
	SettledSeq            *int64            `json:"settled_seq,omitempty"`
	AckedBy               string            `json:"acked_by,omitempty"`
	AckedAt               *int64            `json:"acked_at,omitempty"`
	AckedNote             string            `json:"acked_note,omitempty"`
	EscalatedTo           string            `json:"escalated_to,omitempty"`
	TriageState           string            `json:"triage_state,omitempty"`
	TriageOutcome         string            `json:"triage_outcome,omitempty"`
	TriageOutcomeKind     string            `json:"triage_outcome_kind,omitempty"`
	TriageRunID           string            `json:"triage_run_id,omitempty"`
	TriageFinding         string            `json:"triage_finding,omitempty"`
	ActiveTriageRunID     string            `json:"active_triage_run_id,omitempty"`
	TriagedAt             *int64            `json:"triaged_at,omitempty"`
	BundleJSON            string            `json:"bundle_json,omitempty"`
	LinkedItems           []AlertItemLink   `json:"linked_items,omitempty"`
	LatestTransition      *AlertTransition  `json:"latest_transition,omitempty"`
}

// AlertLedger is returned by ReadAlertEpisodes.
type AlertLedger struct {
	Absent         bool                 `json:"absent"`
	Episodes       []AlertEpisodeRecord `json:"episodes"`
	ActiveSilences []SilenceRecord      `json:"active_silences"`
}

// AlertDetail is returned by ReadAlertEpisode.
type AlertDetail struct {
	Absent          bool                 `json:"absent"`
	Found           bool                 `json:"found"`
	Episode         AlertEpisodeRecord   `json:"episode"`
	Transitions     []AlertTransition    `json:"transitions"`
	LinkedItems     []AlertItemLink      `json:"linked_items"`
	RelatedEpisodes []AlertEpisodeRecord `json:"related_episodes"`
}

func nullStringArg(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func upsertAlertItemLink(tx *store.Tx, alertID, itemID, relation, by, note string, linkedAt, eventSeq int64) error {
	return watchlinks.UpsertAlertItemLink(tx, alertID, itemID, relation, by, note, linkedAt, eventSeq)
}

// LinkAlertItemInTx records a durable link between alertID and itemID in the caller's
// write transaction. It verifies alertID exists in alert_episodes (in any state,
// including resolved), appends watch.alert.item-linked, and upserts alert_item_links
// without reopening, muting, or settling the episode.
func LinkAlertItemInTx(tx *store.Tx, alertID, itemID, relation, by, note string, at time.Time) error {
	return watchlinks.LinkAlertItemInTx(tx, alertID, itemID, relation, by, note, at)
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func backfillAlertItemLinks(ctx context.Context, tx store.MigrationExec) error {
	links, err := watchlinks.ProjectAlertItemLinks(ctx, tx)
	if err != nil {
		return err
	}
	for _, l := range links {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO alert_item_links (alert_id, item_id, relation, linked_at, linked_by, note, event_seq)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			l.AlertID, l.ItemID, l.Relation, l.LinkedAt, nullStringArg(l.LinkedBy), nullStringArg(l.Note), l.EventSeq,
		); err != nil {
			return err
		}
	}
	return nil
}

func activeSilenceIndex(rd *store.Reader) (map[string][]SilenceRecord, error) {
	silences, err := ReadActiveSilences(rd)
	if err != nil {
		return nil, err
	}
	byItem := map[string][]SilenceRecord{}
	for _, s := range silences {
		byItem[s.TrackingItemID] = append(byItem[s.TrackingItemID], s)
	}
	return byItem, nil
}

func linkMatchesActiveSilence(byItem map[string][]SilenceRecord, itemID, ruleID, varsJSON, epSilenceID string) bool {
	candidates := byItem[itemID]
	if len(candidates) == 0 {
		return false
	}
	for _, s := range candidates {
		if epSilenceID != "" && s.SilenceID == epSilenceID {
			return true
		}
		if s.RuleID == ruleID && silenceMatches(s.VarsJSON, varsJSON) {
			return true
		}
	}
	return false
}

// LinkedAlertsForItem returns all alerts linked to itemID, ordered by linked_at, alert_id.
// Returns nil, nil when alert_episodes is absent. Projects without writing when
// alert_item_links is absent.
func LinkedAlertsForItem(rd *store.Reader, itemID string) ([]LinkedAlert, error) {
	return watchlinks.LinkedAlertsForItem(rd, itemID)
}

// LinkedItemsForAlert returns all items linked to alertID, ordered by linked_at, item_id.
func LinkedItemsForAlert(rd *store.Reader, alertID string) ([]AlertItemLink, error) {
	if alertID == "" {
		return nil, nil
	}
	byAlert, err := LinkedItemsByAlert(rd, []string{alertID})
	if err != nil {
		return nil, err
	}
	return byAlert[alertID], nil
}

// LinkedItemsByAlert returns linked items keyed by alert_id, ordered by linked_at, item_id.
func LinkedItemsByAlert(rd *store.Reader, alertIDs []string) (map[string][]AlertItemLink, error) {
	result := map[string][]AlertItemLink{}
	if len(alertIDs) == 0 {
		return result, nil
	}
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil || !hasAlerts {
		return result, err
	}
	want := map[string]bool{}
	for _, id := range alertIDs {
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return result, nil
	}
	byItemSilence, err := activeSilenceIndex(rd)
	if err != nil {
		return nil, err
	}
	hasVars, err := alertEpisodesHasColumn(rd, "variables_json")
	if err != nil {
		return nil, err
	}
	hasSilID, err := alertEpisodesHasColumn(rd, "silence_id")
	if err != nil {
		return nil, err
	}
	varsExpr := `'{}'`
	if hasVars {
		varsExpr = `e.variables_json`
	}
	silIDExpr := `''`
	if hasSilID {
		silIDExpr = `COALESCE(e.silence_id, '')`
	}

	hasLinks, err := tableExists(rd, "alert_item_links")
	if err != nil {
		return nil, err
	}
	if hasLinks {
		placeholders := make([]string, 0, len(want))
		args := make([]any, 0, len(want))
		for id := range want {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		rows, err := rd.Query(fmt.Sprintf(`SELECT l.alert_id, l.item_id, l.relation, l.linked_at, COALESCE(l.linked_by, ''), COALESCE(l.note, ''), l.event_seq,
			e.rule_id, %s, %s
			FROM alert_item_links l
			JOIN alert_episodes e ON e.alert_id = l.alert_id
			WHERE l.alert_id IN (%s)
			ORDER BY l.linked_at ASC, l.item_id ASC`, varsExpr, silIDExpr, strings.Join(placeholders, ",")), args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				link                    AlertItemLink
				ruleID, varsJSON, silID string
			)
			if err := rows.Scan(&link.AlertID, &link.ItemID, &link.Relation, &link.LinkedAt, &link.LinkedBy, &link.Note, &link.EventSeq,
				&ruleID, &varsJSON, &silID); err != nil {
				return nil, err
			}
			link.ActiveSilence = linkMatchesActiveSilence(byItemSilence, link.ItemID, ruleID, varsJSON, silID)
			result[link.AlertID] = append(result[link.AlertID], link)
		}
		return result, rows.Err()
	}

	projected, err := watchlinks.ProjectAlertItemLinks(context.Background(), watchlinks.ReaderQuerySurface{Reader: rd})
	if err != nil {
		return nil, err
	}
	type epInfo struct {
		ruleID, varsJSON, silID string
	}
	epCache := map[string]epInfo{}
	for _, p := range projected {
		if !want[p.AlertID] {
			continue
		}
		info, ok := epCache[p.AlertID]
		if !ok {
			if err := rd.QueryRow(fmt.Sprintf(`SELECT e.rule_id, %s, %s FROM alert_episodes e WHERE e.alert_id = ?`, varsExpr, silIDExpr), p.AlertID).
				Scan(&info.ruleID, &info.varsJSON, &info.silID); err != nil {
				continue
			}
			epCache[p.AlertID] = info
		}
		result[p.AlertID] = append(result[p.AlertID], AlertItemLink{
			AlertID:       p.AlertID,
			ItemID:        p.ItemID,
			Relation:      p.Relation,
			LinkedAt:      p.LinkedAt,
			LinkedBy:      p.LinkedBy,
			Note:          p.Note,
			EventSeq:      p.EventSeq,
			ActiveSilence: linkMatchesActiveSilence(byItemSilence, p.ItemID, info.ruleID, info.varsJSON, info.silID),
		})
	}
	for id := range result {
		s := result[id]
		sort.Slice(s, func(i, j int) bool {
			if s[i].LinkedAt != s[j].LinkedAt {
				return s[i].LinkedAt < s[j].LinkedAt
			}
			return s[i].ItemID < s[j].ItemID
		})
		result[id] = s
	}
	return result, nil
}

// ReadActiveSilences returns all un-lifted silences from watch_silences, ordered by created_at DESC.
func ReadActiveSilences(rd *store.Reader) ([]SilenceRecord, error) {
	hasTable, err := tableExists(rd, "watch_silences")
	if err != nil || !hasTable {
		return nil, err
	}
	rows, err := rd.Query(`SELECT silence_id, rule_id, vars_json, tracking_item_id, created_by, created_at, reason,
		investigation_cause, investigation_evidence, COALESCE(source_alert_id,''), silence_until_ms, COALESCE(silence_on_fingerprint,''),
		lifted_at, COALESCE(lifted_by,''), COALESCE(lifted_reason,'')
		FROM watch_silences WHERE lifted_at IS NULL ORDER BY created_at DESC, silence_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SilenceRecord
	for rows.Next() {
		var (
			r                 SilenceRecord
			untilMS, liftedAt sql.NullInt64
		)
		if err := rows.Scan(&r.SilenceID, &r.RuleID, &r.VarsJSON, &r.TrackingItemID, &r.CreatedBy, &r.CreatedAt, &r.Reason,
			&r.InvestigationCause, &r.InvestigationEvidence, &r.SourceAlertID, &untilMS, &r.SilenceOnFingerprint,
			&liftedAt, &r.LiftedBy, &r.LiftedReason); err != nil {
			return nil, err
		}
		if untilMS.Valid {
			v := untilMS.Int64
			r.SilenceUntilMS = &v
		}
		if liftedAt.Valid {
			v := liftedAt.Int64
			r.LiftedAt = &v
		}
		r.Vars = decodeVariablesJSON(r.VarsJSON)
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanEpisodeRecords(rd *store.Reader, whereClause string, args ...any) ([]AlertEpisodeRecord, error) {
	colRows, err := rd.Query(`SELECT name FROM pragma_table_info('alert_episodes')`)
	if err != nil {
		return nil, err
	}
	cols := map[string]bool{}
	for colRows.Next() {
		var name string
		if err := colRows.Scan(&name); err != nil {
			colRows.Close()
			return nil, err
		}
		cols[name] = true
	}
	if err := colRows.Err(); err != nil {
		colRows.Close()
		return nil, err
	}
	colRows.Close()

	colOrNull := func(name, def string) string {
		if cols[name] {
			return name
		}
		return def + " AS " + name
	}

	query := fmt.Sprintf(`SELECT
		alert_id, rule_id, subject_key, episode_num, state, %s, severity, condition_fingerprint,
		opened_seq, opened_at, last_observed_seq, last_observed_at, observation_count,
		resolved_seq, resolved_at, COALESCE(resolved_reason, ''),
		%s, %s, %s, %s, %s,
		%s, %s, %s, %s,
		%s, %s, %s,
		COALESCE(acked_by, ''), acked_at, COALESCE(acked_note, ''),
		%s,
		%s, %s, %s, %s, %s, %s, %s,
		bundle_json
		FROM alert_episodes %s`,
		colOrNull("settlement_state", "'uninvestigated'"),
		colOrNull("variables_json", "'{}'"),
		colOrNull("outcome", "NULL"),
		colOrNull("item_link_kind", "NULL"),
		colOrNull("tracking_item_id", "NULL"),
		colOrNull("silence_id", "NULL"),
		colOrNull("investigated_by", "NULL"),
		colOrNull("investigated_at", "NULL"),
		colOrNull("investigation_cause", "NULL"),
		colOrNull("investigation_evidence", "NULL"),
		colOrNull("settled_by", "NULL"),
		colOrNull("settled_at", "NULL"),
		colOrNull("settled_seq", "NULL"),
		colOrNull("escalated_to", "NULL"),
		colOrNull("triage_state", "''"),
		colOrNull("triage_outcome", "NULL"),
		colOrNull("triage_outcome_kind", "NULL"),
		colOrNull("triage_run_id", "NULL"),
		colOrNull("triage_finding", "NULL"),
		colOrNull("active_triage_run_id", "NULL"),
		colOrNull("triaged_at", "NULL"),
		whereClause,
	)
	rows, err := rd.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertEpisodeRecord
	for rows.Next() {
		var (
			ep                                                         AlertEpisodeRecord
			resSeq, resAt, invAt, setAt, setSeq, ackAt, triagedAt      sql.NullInt64
			outcome, linkKind, tracking, silID, invBy, invCause, invEv sql.NullString
			setBy, escTo                                               sql.NullString
			triageState, triageOutcome, triageOutcomeKind              sql.NullString
			triageRunID, triageFinding, activeTriageRunID              sql.NullString
		)
		if err := rows.Scan(
			&ep.AlertID, &ep.RuleID, &ep.SubjectKey, &ep.EpisodeNum, &ep.State, &ep.SettlementState, &ep.Severity, &ep.ConditionFingerprint,
			&ep.OpenedSeq, &ep.OpenedAt, &ep.LastObservedSeq, &ep.LastObservedAt, &ep.ObservationCount,
			&resSeq, &resAt, &ep.ResolvedReason,
			&ep.VariablesJSON, &outcome, &linkKind, &tracking, &silID,
			&invBy, &invAt, &invCause, &invEv,
			&setBy, &setAt, &setSeq,
			&ep.AckedBy, &ackAt, &ep.AckedNote,
			&escTo,
			&triageState, &triageOutcome, &triageOutcomeKind, &triageRunID, &triageFinding, &activeTriageRunID, &triagedAt,
			&ep.BundleJSON,
		); err != nil {
			return nil, err
		}
		if resSeq.Valid {
			v := resSeq.Int64
			ep.ResolvedSeq = &v
		}
		if resAt.Valid {
			v := resAt.Int64
			ep.ResolvedAt = &v
		}
		if invAt.Valid {
			v := invAt.Int64
			ep.InvestigatedAt = &v
		}
		if setAt.Valid {
			v := setAt.Int64
			ep.SettledAt = &v
		}
		if setSeq.Valid {
			v := setSeq.Int64
			ep.SettledSeq = &v
		}
		if ackAt.Valid {
			v := ackAt.Int64
			ep.AckedAt = &v
		}
		if triagedAt.Valid {
			v := triagedAt.Int64
			ep.TriagedAt = &v
		}
		ep.Outcome = outcome.String
		ep.ItemLinkKind = linkKind.String
		ep.TrackingItemID = tracking.String
		ep.SilenceID = silID.String
		ep.InvestigatedBy = invBy.String
		ep.InvestigationCause = invCause.String
		ep.InvestigationEvidence = invEv.String
		ep.SettledBy = setBy.String
		ep.EscalatedTo = escTo.String
		ep.TriageState = triageState.String
		ep.TriageOutcome = triageOutcome.String
		ep.TriageOutcomeKind = triageOutcomeKind.String
		ep.TriageRunID = triageRunID.String
		ep.TriageFinding = triageFinding.String
		ep.ActiveTriageRunID = activeTriageRunID.String
		ep.Variables = decodeVariablesJSON(ep.VariablesJSON)
		out = append(out, ep)
	}
	return out, rows.Err()
}

// ReadAlertEpisodes returns the read-only alert ledger and active silences.
func ReadAlertEpisodes(rd *store.Reader, filter AlertFilter) (AlertLedger, error) {
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil {
		return AlertLedger{}, err
	}
	if !hasAlerts {
		return AlertLedger{Absent: true}, nil
	}
	var conds []string
	var args []any
	if filter.State != "" {
		conds = append(conds, "state = ?")
		args = append(args, filter.State)
	}
	if filter.RuleID != "" {
		conds = append(conds, "rule_id = ?")
		args = append(args, filter.RuleID)
	}
	if filter.Severity != "" {
		conds = append(conds, "severity = ?")
		args = append(args, filter.Severity)
	}
	hasSettlement, err := hasSettlementColumns(rd)
	if err != nil {
		return AlertLedger{}, err
	}
	hasTriage, err := hasTriageColumns(rd)
	if err != nil {
		return AlertLedger{}, err
	}
	hasEscalated, err := alertEpisodesHasColumn(rd, "escalated_to")
	if err != nil {
		return AlertLedger{}, err
	}
	if filter.SettlementState != "" {
		if hasSettlement {
			conds = append(conds, "settlement_state = ?")
			args = append(args, filter.SettlementState)
		} else if filter.SettlementState != "uninvestigated" {
			return AlertLedger{}, nil
		}
	}
	if filter.TriageState != "" {
		if hasTriage {
			conds = append(conds, "COALESCE(NULLIF(triage_state, ''), 'untriaged') = ?")
			args = append(args, filter.TriageState)
		} else if filter.TriageState != "untriaged" {
			return AlertLedger{}, nil
		}
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	var leadConds []string
	if hasSettlement {
		leadConds = append(leadConds, "COALESCE(NULLIF(settlement_state, ''), 'uninvestigated') != 'settled'")
	}
	if hasTriage {
		leadConds = append(leadConds, "triage_state = 'escalated'")
	}
	if hasEscalated {
		leadConds = append(leadConds, "COALESCE(escalated_to, '') != ''")
	}
	if len(leadConds) > 0 {
		where += " ORDER BY CASE WHEN " + strings.Join(leadConds, " OR ") + " THEN 0 ELSE 1 END, CASE WHEN state != 'resolved' THEN 0 ELSE 1 END, opened_at DESC, alert_id ASC"
	} else {
		where += " ORDER BY CASE WHEN state != 'resolved' THEN 0 ELSE 1 END, opened_at DESC, alert_id ASC"
	}

	episodes, err := scanEpisodeRecords(rd, where, args...)
	if err != nil {
		return AlertLedger{}, err
	}
	ids := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.AlertID)
	}
	linksByAlert, err := LinkedItemsByAlert(rd, ids)
	if err != nil {
		return AlertLedger{}, err
	}
	for i := range episodes {
		episodes[i].LinkedItems = linksByAlert[episodes[i].AlertID]
		transitions, err := readAlertTransitions(rd, episodes[i].AlertID, episodes[i].EpisodeNum, episodes[i].SilenceID)
		if err != nil {
			return AlertLedger{}, err
		}
		if len(transitions) > 0 {
			latest := transitions[len(transitions)-1]
			episodes[i].LatestTransition = &latest
		}
	}
	silences, err := ReadActiveSilences(rd)
	if err != nil {
		return AlertLedger{}, err
	}
	return AlertLedger{
		Episodes:       episodes,
		ActiveSilences: silences,
	}, nil
}

// ReadAlertEpisode returns one episode's metadata, transition history, linked items,
// and related episodes of the same rule and subject.
func ReadAlertEpisode(rd *store.Reader, alertID string) (AlertDetail, error) {
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil {
		return AlertDetail{}, err
	}
	if !hasAlerts {
		return AlertDetail{Absent: true}, nil
	}
	eps, err := scanEpisodeRecords(rd, "WHERE alert_id = ?", alertID)
	if err != nil {
		return AlertDetail{}, err
	}
	if len(eps) == 0 {
		return AlertDetail{Found: false}, fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
	}
	ep := eps[0]
	links, err := LinkedItemsForAlert(rd, alertID)
	if err != nil {
		return AlertDetail{}, err
	}
	ep.LinkedItems = links
	transitions, err := readAlertTransitions(rd, ep.AlertID, ep.EpisodeNum, ep.SilenceID)
	if err != nil {
		return AlertDetail{}, err
	}
	if len(transitions) > 0 {
		latest := transitions[len(transitions)-1]
		ep.LatestTransition = &latest
	}
	related, err := scanEpisodeRecords(rd,
		"WHERE rule_id = ? AND subject_key = ? AND alert_id != ? ORDER BY episode_num DESC, alert_id ASC",
		ep.RuleID, ep.SubjectKey, ep.AlertID)
	if err != nil {
		return AlertDetail{}, err
	}
	return AlertDetail{
		Found:           true,
		Episode:         ep,
		Transitions:     transitions,
		LinkedItems:     links,
		RelatedEpisodes: related,
	}, nil
}

func readAlertTransitions(rd *store.Reader, alertID string, episodeNum int64, silenceID string) ([]AlertTransition, error) {
	hasEvents, err := tableExists(rd, "events")
	if err != nil || !hasEvents {
		return nil, err
	}
	rows, err := rd.Query(
		`SELECT seq, ts, kind, COALESCE(object_id, ''), payload FROM events
		 WHERE (object_id = ? AND kind LIKE 'watch.alert.%')
		    OR (kind = ? AND (? != '' AND object_id = ?))
		 ORDER BY seq ASC`,
		alertID, silenceLiftedKind, silenceID, silenceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertTransition
	for rows.Next() {
		var (
			seq, atMS int64
			kind, oid string
			raw       []byte
		)
		if err := rows.Scan(&seq, &atMS, &kind, &oid, &raw); err != nil {
			return nil, err
		}
		var body map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		tr := AlertTransition{
			Seq:       seq,
			Timestamp: atMS,
			Kind:      kind,
		}
		switch kind {
		case openedKind:
			if episodeNum > 1 {
				tr.StateChange = "re-fired"
			} else {
				tr.StateChange = "fired"
			}
			tr.Actor = "evaluator"
			tr.Detail = stringField(body, "cause")
		case reopenedKind:
			tr.StateChange = "re-fired"
			tr.Actor = "evaluator"
			tr.Detail = stringField(body, "cause")
		case acknowledgedKind:
			tr.StateChange = "acknowledged"
			tr.Actor = stringField(body, "by")
			tr.Detail = stringField(body, "note")
		case investigatedKind:
			tr.StateChange = "investigated"
			tr.Actor = stringField(body, "by")
			tr.Detail = stringField(body, "cause")
		case settledKind:
			tr.StateChange = "settled"
			tr.Actor = stringField(body, "by")
			if outcome := stringField(body, "outcome"); outcome != "" {
				if itemID := stringField(body, "item_id"); itemID != "" {
					tr.Detail = outcome + " (" + itemID + ")"
				} else {
					tr.Detail = outcome
				}
			}
		case itemLinkedKind:
			tr.StateChange = "item-linked"
			tr.Actor = stringField(body, "by")
			if itemID := stringField(body, "item_id"); itemID != "" {
				if rel := stringField(body, "relation"); rel != "" {
					tr.Detail = rel + " (" + itemID + ")"
				} else {
					tr.Detail = itemID
				}
			}
		case trackedKind:
			tr.StateChange = "tracked"
			tr.Actor = stringField(body, "by")
			tr.Detail = stringField(body, "item_id")
		case silencedKind:
			tr.StateChange = "silenced"
			tr.Actor = stringField(body, "by")
			tr.Detail = stringField(body, "reason")
		case silenceLiftedKind:
			tr.StateChange = "silence-lifted"
			tr.Actor = stringField(body, "by")
			if tr.Actor == "" {
				tr.Actor = "evaluator"
			}
			tr.Detail = stringField(body, "reason")
		case resolvedKind:
			tr.StateChange = "resolved"
			tr.Actor = stringField(body, "by")
			tr.Detail = stringField(body, "reason")
			if tr.Actor == "" && (tr.Detail == "condition-cleared" || strings.HasPrefix(tr.Detail, "item-")) {
				tr.Actor = "evaluator"
			}
		case triageStartedKind:
			tr.StateChange = "triage-started"
			tr.Actor = "tillr-triage"
			tr.Detail = stringField(body, "run_id")
		case triagedKind:
			tr.StateChange = "triaged"
			tr.Actor = "tillr-triage"
			tr.Detail = formatTriageTransitionDetail(body)
		case escalatedKind:
			tr.StateChange = "escalated"
			tr.Actor = stringField(body, "by")
			if tr.Actor == "" {
				tr.Actor = "tillr-triage"
			}
			tr.Detail = formatEscalatedTransitionDetail(body)
		default:
			tr.StateChange = strings.TrimPrefix(kind, "watch.alert.")
			tr.Actor = stringField(body, "by")
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

func formatTriageTransitionDetail(body map[string]any) string {
	var parts []string
	outcome := stringField(body, "outcome")
	outcomeKind := stringField(body, "outcome_kind")
	itemID := stringField(body, "item_id")
	runID := stringField(body, "run_id")
	if outcome != "" {
		if outcomeKind != "" {
			parts = append(parts, outcome+" "+outcomeKind)
		} else {
			parts = append(parts, outcome)
		}
	}
	if itemID != "" {
		parts = append(parts, "("+itemID+")")
	}
	if runID != "" {
		parts = append(parts, "run "+runID)
	}
	return strings.Join(parts, " · ")
}

func formatEscalatedTransitionDetail(body map[string]any) string {
	var parts []string
	if to := stringField(body, "escalate_to"); to != "" {
		parts = append(parts, "to "+to)
	}
	if outcome := stringField(body, "outcome"); outcome != "" {
		parts = append(parts, outcome)
	}
	if itemID := stringField(body, "item_id"); itemID != "" {
		parts = append(parts, "("+itemID+")")
	}
	if runID := stringField(body, "run_id"); runID != "" {
		parts = append(parts, "run "+runID)
	}
	return strings.Join(parts, " · ")
}
