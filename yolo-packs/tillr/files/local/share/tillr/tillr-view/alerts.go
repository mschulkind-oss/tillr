package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

const unrecordedAlertActor = "not recorded"

// alertItemLinkView is one item linked to an alert, enriched with the item's title
// and current projected state when the item exists in the store.
type alertItemLinkView struct {
	AlertID       string `json:"alert_id"`
	ItemID        string `json:"item_id"`
	ItemTitle     string `json:"item_title,omitempty"`
	ItemState     string `json:"item_state,omitempty"`
	Relation      string `json:"relation"`
	LinkedAt      int64  `json:"linked_at"`
	LinkedBy      string `json:"linked_by,omitempty"`
	Note          string `json:"note,omitempty"`
	EventSeq      int64  `json:"event_seq"`
	ActiveSilence bool   `json:"active_silence"`
}

// alertTransitionView is one state-change row reconstructed from stored events.
// Actor is always recorded or "not recorded", never blank or invented.
type alertTransitionView struct {
	Seq         int64  `json:"seq"`
	Timestamp   int64  `json:"timestamp"`
	StateChange string `json:"state_change"`
	Kind        string `json:"kind"`
	Actor       string `json:"actor"`
	Detail      string `json:"detail,omitempty"`
}

// alertEpisodeView is the read-only projection of an alert episode shared by CLI and HTTP.
type alertEpisodeView struct {
	AlertID               string               `json:"alert_id"`
	RuleID                string               `json:"rule_id"`
	SubjectKey            string               `json:"subject_key"`
	EpisodeNum            int64                `json:"episode_num"`
	State                 string               `json:"state"`
	SettlementState       string               `json:"settlement_state"`
	Severity              string               `json:"severity"`
	ConditionFingerprint  string               `json:"condition_fingerprint"`
	OpenedSeq             int64                `json:"opened_seq"`
	OpenedAt              int64                `json:"opened_at"`
	LastObservedSeq       int64                `json:"last_observed_seq"`
	LastObservedAt        int64                `json:"last_observed_at"`
	ObservationCount      int64                `json:"observation_count"`
	UnsettledForMS        *int64               `json:"unsettled_for_ms,omitempty"`
	ResolvedSeq           *int64               `json:"resolved_seq,omitempty"`
	ResolvedAt            *int64               `json:"resolved_at,omitempty"`
	ResolvedReason        string               `json:"resolved_reason,omitempty"`
	Variables             map[string]string    `json:"variables"`
	VariablesJSON         string               `json:"variables_json"`
	Outcome               string               `json:"outcome,omitempty"`
	ItemLinkKind          string               `json:"item_link_kind,omitempty"`
	TrackingItemID        string               `json:"tracking_item_id,omitempty"`
	SilenceID             string               `json:"silence_id,omitempty"`
	InvestigatedBy        string               `json:"investigated_by,omitempty"`
	InvestigatedAt        *int64               `json:"investigated_at,omitempty"`
	InvestigationCause    string               `json:"investigation_cause,omitempty"`
	InvestigationEvidence string               `json:"investigation_evidence,omitempty"`
	SettledBy             string               `json:"settled_by,omitempty"`
	SettledAt             *int64               `json:"settled_at,omitempty"`
	SettledSeq            *int64               `json:"settled_seq,omitempty"`
	AckedBy               string               `json:"acked_by,omitempty"`
	AckedAt               *int64               `json:"acked_at,omitempty"`
	AckedNote             string               `json:"acked_note,omitempty"`
	EscalatedTo           string               `json:"escalated_to,omitempty"`
	BundleJSON            string               `json:"bundle_json,omitempty"`
	LinkedItems           []alertItemLinkView  `json:"linked_items"`
	LatestTransition      *alertTransitionView `json:"latest_transition,omitempty"`
}

// alertSilenceView is an active silence row enriched with its tracking item's title and state.
type alertSilenceView struct {
	SilenceID             string            `json:"silence_id"`
	RuleID                string            `json:"rule_id"`
	Vars                  map[string]string `json:"vars"`
	VarsJSON              string            `json:"vars_json"`
	TrackingItemID        string            `json:"tracking_item_id"`
	TrackingItemTitle     string            `json:"tracking_item_title,omitempty"`
	TrackingItemState     string            `json:"tracking_item_state,omitempty"`
	CreatedBy             string            `json:"created_by"`
	CreatedAt             int64             `json:"created_at"`
	Reason                string            `json:"reason"`
	InvestigationCause    string            `json:"investigation_cause"`
	InvestigationEvidence string            `json:"investigation_evidence"`
	SourceAlertID         string            `json:"source_alert_id,omitempty"`
	SilenceUntilMS        *int64            `json:"silence_until_ms,omitempty"`
	SilenceOnFingerprint  string            `json:"silence_on_fingerprint,omitempty"`
}

// alertCounts summarizes the episodes and active silences in an alertsData response.
type alertCounts struct {
	Total          int `json:"total"`
	Active         int `json:"active"`
	Open           int `json:"open"`
	Acknowledged   int `json:"acknowledged"`
	Silenced       int `json:"silenced"`
	Resolved       int `json:"resolved"`
	Unsettled      int `json:"unsettled"`
	Uninvestigated int `json:"uninvestigated"`
	Investigating  int `json:"investigating"`
	Investigated   int `json:"investigated"`
	Settled        int `json:"settled"`
	ActiveSilences int `json:"active_silences"`
}

// alertsData is the payload of `GET /api/v1/alerts` and `tillr-view alerts --json`.
type alertsData struct {
	Absent         bool               `json:"absent"`
	Counts         alertCounts        `json:"counts"`
	Episodes       []alertEpisodeView `json:"episodes"`
	ActiveSilences []alertSilenceView `json:"active_silences"`
}

// alertDetailData is the payload of `GET /api/v1/alerts/{id}` and `tillr-view alerts --alert <id> --json`.
type alertDetailData struct {
	Absent          bool                  `json:"absent"`
	Episode         alertEpisodeView      `json:"episode"`
	Transitions     []alertTransitionView `json:"transitions"`
	LinkedItems     []alertItemLinkView   `json:"linked_items"`
	RelatedEpisodes []alertEpisodeView    `json:"related_episodes"`
}

type linkedItemMeta struct {
	title string
	state string
}

func formatAlertActor(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return unrecordedAlertActor
	}
	return actor
}

func projectAlertTransition(tr watch.AlertTransition) alertTransitionView {
	return alertTransitionView{
		Seq:         tr.Seq,
		Timestamp:   tr.Timestamp,
		StateChange: tr.StateChange,
		Kind:        tr.Kind,
		Actor:       formatAlertActor(tr.Actor),
		Detail:      tr.Detail,
	}
}

func projectAlertItemLinks(links []watch.AlertItemLink, meta map[string]linkedItemMeta) []alertItemLinkView {
	out := make([]alertItemLinkView, 0, len(links))
	for _, l := range links {
		m := meta[l.ItemID]
		out = append(out, alertItemLinkView{
			AlertID:       l.AlertID,
			ItemID:        l.ItemID,
			ItemTitle:     m.title,
			ItemState:     m.state,
			Relation:      l.Relation,
			LinkedAt:      l.LinkedAt,
			LinkedBy:      l.LinkedBy,
			Note:          l.Note,
			EventSeq:      l.EventSeq,
			ActiveSilence: l.ActiveSilence,
		})
	}
	return out
}

func projectAlertEpisode(ep watch.AlertEpisodeRecord, meta map[string]linkedItemMeta, nowMS int64) alertEpisodeView {
	vars := ep.Variables
	if vars == nil {
		vars = map[string]string{}
	}
	var unsettledFor *int64
	if ep.SettlementState != "settled" {
		age := max(nowMS-ep.OpenedAt, int64(0))
		unsettledFor = &age
	}
	var latest *alertTransitionView
	if ep.LatestTransition != nil {
		tr := projectAlertTransition(*ep.LatestTransition)
		latest = &tr
	}
	return alertEpisodeView{
		AlertID:               ep.AlertID,
		RuleID:                ep.RuleID,
		SubjectKey:            ep.SubjectKey,
		EpisodeNum:            ep.EpisodeNum,
		State:                 ep.State,
		SettlementState:       ep.SettlementState,
		Severity:              ep.Severity,
		ConditionFingerprint:  ep.ConditionFingerprint,
		OpenedSeq:             ep.OpenedSeq,
		OpenedAt:              ep.OpenedAt,
		LastObservedSeq:       ep.LastObservedSeq,
		LastObservedAt:        ep.LastObservedAt,
		ObservationCount:      ep.ObservationCount,
		UnsettledForMS:        unsettledFor,
		ResolvedSeq:           ep.ResolvedSeq,
		ResolvedAt:            ep.ResolvedAt,
		ResolvedReason:        ep.ResolvedReason,
		Variables:             vars,
		VariablesJSON:         ep.VariablesJSON,
		Outcome:               ep.Outcome,
		ItemLinkKind:          ep.ItemLinkKind,
		TrackingItemID:        ep.TrackingItemID,
		SilenceID:             ep.SilenceID,
		InvestigatedBy:        ep.InvestigatedBy,
		InvestigatedAt:        ep.InvestigatedAt,
		InvestigationCause:    ep.InvestigationCause,
		InvestigationEvidence: ep.InvestigationEvidence,
		SettledBy:             ep.SettledBy,
		SettledAt:             ep.SettledAt,
		SettledSeq:            ep.SettledSeq,
		AckedBy:               ep.AckedBy,
		AckedAt:               ep.AckedAt,
		AckedNote:             ep.AckedNote,
		EscalatedTo:           ep.EscalatedTo,
		BundleJSON:            ep.BundleJSON,
		LinkedItems:           projectAlertItemLinks(ep.LinkedItems, meta),
		LatestTransition:      latest,
	}
}

func projectAlertSilences(silences []watch.SilenceRecord, meta map[string]linkedItemMeta) []alertSilenceView {
	out := make([]alertSilenceView, 0, len(silences))
	for _, s := range silences {
		vars := s.Vars
		if vars == nil {
			vars = map[string]string{}
		}
		m := meta[s.TrackingItemID]
		out = append(out, alertSilenceView{
			SilenceID:             s.SilenceID,
			RuleID:                s.RuleID,
			Vars:                  vars,
			VarsJSON:              s.VarsJSON,
			TrackingItemID:        s.TrackingItemID,
			TrackingItemTitle:     m.title,
			TrackingItemState:     m.state,
			CreatedBy:             s.CreatedBy,
			CreatedAt:             s.CreatedAt,
			Reason:                s.Reason,
			InvestigationCause:    s.InvestigationCause,
			InvestigationEvidence: s.InvestigationEvidence,
			SourceAlertID:         s.SourceAlertID,
			SilenceUntilMS:        s.SilenceUntilMS,
			SilenceOnFingerprint:  s.SilenceOnFingerprint,
		})
	}
	return out
}

func computeAlertCounts(episodes []alertEpisodeView, silences []alertSilenceView) alertCounts {
	c := alertCounts{
		Total:          len(episodes),
		ActiveSilences: len(silences),
	}
	for _, ep := range episodes {
		switch ep.State {
		case "open":
			c.Open++
			c.Active++
		case "acknowledged":
			c.Acknowledged++
			c.Active++
		case "silenced":
			c.Silenced++
			c.Active++
		case "resolved":
			c.Resolved++
		default:
			c.Active++
		}
		switch ep.SettlementState {
		case "settled":
			c.Settled++
		case "investigating":
			c.Investigating++
			c.Unsettled++
		case "investigated":
			c.Investigated++
			c.Unsettled++
		default:
			c.Uninvestigated++
			c.Unsettled++
		}
	}
	return c
}

func readLinkedItemMeta(r *store.Reader, itemIDs []string) (map[string]linkedItemMeta, error) {
	out := map[string]linkedItemMeta{}
	want := map[string]bool{}
	for _, id := range itemIDs {
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	items, err := readItems(r)
	if err != nil {
		if isMissingTable(err) {
			return out, nil
		}
		return nil, err
	}
	byID := make(map[string]storeItem, len(items))
	states := make(map[string]string, len(items))
	for _, it := range items {
		byID[it.ItemID] = it
		states[it.ItemID] = it.State
	}
	for id := range want {
		it, ok := byID[id]
		if !ok {
			continue
		}
		edges, _ := readEdges(r, id)
		waitingOn := waitingOnTargets(edges, states)
		var enqueued *storeCandidate
		if candidates, err := readItemCandidates(r, id); err == nil {
			for i := range candidates {
				if candidates[i].State == "enqueued" {
					enqueued = &candidates[i]
					break
				}
			}
		}
		var active *storeRun
		if runs, err := readItemRuns(r, id); err == nil {
			for i := range runs {
				if runs[i].State == "running" {
					active = &runs[i]
					break
				}
			}
		}
		hold := ""
		if dispatches, err := readItemDispatches(r, id); err == nil {
			hold = questionHoldOf(dispatches)
		}
		proj := ProjectItemState(it, enqueued, active, waitingOn, hold)
		closedBlockers := closedBlockersFor(it, edges, states)
		projectClosedBlockers(&proj, closedBlockers)
		out[id] = linkedItemMeta{title: it.Title, state: proj.State}
	}
	return out, nil
}

// readAlertsData builds the shared read-only alerts ledger projection for CLI and HTTP.
func readAlertsData(r *store.Reader, filter watch.AlertFilter, now time.Time) (alertsData, error) {
	ledger, err := watch.ReadAlertEpisodes(r, filter)
	if err != nil {
		return alertsData{}, err
	}
	if ledger.Absent {
		return alertsData{
			Absent:         true,
			Episodes:       []alertEpisodeView{},
			ActiveSilences: []alertSilenceView{},
		}, nil
	}
	var itemIDs []string
	for _, ep := range ledger.Episodes {
		for _, l := range ep.LinkedItems {
			itemIDs = append(itemIDs, l.ItemID)
		}
	}
	for _, s := range ledger.ActiveSilences {
		itemIDs = append(itemIDs, s.TrackingItemID)
	}
	meta, err := readLinkedItemMeta(r, itemIDs)
	if err != nil {
		return alertsData{}, err
	}
	nowMS := now.UTC().UnixMilli()
	episodes := make([]alertEpisodeView, 0, len(ledger.Episodes))
	for _, ep := range ledger.Episodes {
		episodes = append(episodes, projectAlertEpisode(ep, meta, nowMS))
	}
	silences := projectAlertSilences(ledger.ActiveSilences, meta)
	return alertsData{
		Absent:         false,
		Counts:         computeAlertCounts(episodes, silences),
		Episodes:       episodes,
		ActiveSilences: silences,
	}, nil
}

// readAlertDetailData builds the shared read-only single-alert projection for CLI and HTTP.
func readAlertDetailData(r *store.Reader, alertID string, now time.Time) (alertDetailData, bool, error) {
	detail, err := watch.ReadAlertEpisode(r, alertID)
	if err != nil {
		if errors.Is(err, watch.ErrAlertNotFound) {
			return alertDetailData{}, false, nil
		}
		return alertDetailData{}, false, err
	}
	if detail.Absent || !detail.Found {
		return alertDetailData{Absent: detail.Absent}, false, nil
	}
	var itemIDs []string
	for _, l := range detail.LinkedItems {
		itemIDs = append(itemIDs, l.ItemID)
	}
	for _, rel := range detail.RelatedEpisodes {
		for _, l := range rel.LinkedItems {
			itemIDs = append(itemIDs, l.ItemID)
		}
	}
	meta, err := readLinkedItemMeta(r, itemIDs)
	if err != nil {
		return alertDetailData{}, false, err
	}
	nowMS := now.UTC().UnixMilli()
	transitions := make([]alertTransitionView, 0, len(detail.Transitions))
	for _, tr := range detail.Transitions {
		transitions = append(transitions, projectAlertTransition(tr))
	}
	related := make([]alertEpisodeView, 0, len(detail.RelatedEpisodes))
	for _, rel := range detail.RelatedEpisodes {
		related = append(related, projectAlertEpisode(rel, meta, nowMS))
	}
	linked := projectAlertItemLinks(detail.LinkedItems, meta)
	ep := projectAlertEpisode(detail.Episode, meta, nowMS)
	ep.LinkedItems = linked
	return alertDetailData{
		Absent:          false,
		Episode:         ep,
		Transitions:     transitions,
		LinkedItems:     linked,
		RelatedEpisodes: related,
	}, true, nil
}

// alertsPayload is `GET /api/v1/alerts`.
func (s *server) alertsPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	q := req.URL.Query()
	filter := watch.AlertFilter{
		State:           strings.TrimSpace(q.Get("state")),
		RuleID:          strings.TrimSpace(q.Get("rule")),
		Severity:        strings.TrimSpace(q.Get("severity")),
		SettlementState: strings.TrimSpace(q.Get("settlement")),
	}
	data, err := readAlertsData(r, filter, s.now().UTC())
	if err != nil {
		return nil, unreadable(err)
	}
	return data, nil
}

// alertPayload is `GET /api/v1/alerts/{id}`.
func (s *server) alertPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	data, found, err := readAlertDetailData(r, id, s.now().UTC())
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, alertNotFound(id)
	}
	return data, nil
}

// alertsFace is `tillr-view alerts`: read-only alert ledger or single-alert detail.
func alertsFace(args []string) int {
	fs := newFlagSet("alerts")
	path := fs.String("store", "", "the store to read (required)")
	alertID := fs.String("alert", "", "inspect one alert episode by id")
	state := fs.String("state", "", "filter by condition state (open, acknowledged, silenced, resolved)")
	rule := fs.String("rule", "", "filter by rule id")
	severity := fs.String("severity", "", "filter by severity")
	settlement := fs.String("settlement", "", "filter by settlement state (uninvestigated, investigating, investigated, settled)")
	clock := fs.String("now", "", "observation time in RFC 3339 (default: now)")
	asJSON := fs.Bool("json", false, "print the alert projection as JSON")
	if code := parseFlags(fs, args); code != record.ExitOK {
		if code == helpExit {
			return record.ExitOK
		}
		return code
	}
	if *path == "" {
		return usagef("alerts needs --store <path>")
	}
	now := time.Now().UTC()
	if *clock != "" {
		parsed, err := time.Parse(time.RFC3339, *clock)
		if err != nil {
			return usagef("--now %q is not an RFC 3339 timestamp: %v", *clock, err)
		}
		now = parsed.UTC()
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return exitFor(err)
	}
	defer s.Close()

	if *alertID != "" {
		var (
			detail alertDetailData
			found  bool
		)
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			version, err := r.SchemaVersion()
			if err != nil {
				return err
			}
			if !schemaSupported(version) {
				return schemaMismatch{version: version}
			}
			detail, found, err = readAlertDetailData(r, *alertID, now)
			return err
		})
		if err != nil {
			return exitFor(err)
		}
		if !found {
			fmt.Fprintf(os.Stderr, "tillr-view: alert %q is not in the store\n", *alertID)
			return record.ExitNegative
		}
		if *asJSON {
			err = json.NewEncoder(os.Stdout).Encode(detail)
		} else {
			err = printAlertDetail(os.Stdout, detail)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-view: writing alert detail: %v\n", err)
			return record.ExitExternal
		}
		return record.ExitOK
	}

	var data alertsData
	filter := watch.AlertFilter{
		State:           strings.TrimSpace(*state),
		RuleID:          strings.TrimSpace(*rule),
		Severity:        strings.TrimSpace(*severity),
		SettlementState: strings.TrimSpace(*settlement),
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		data, err = readAlertsData(r, filter, now)
		return err
	})
	if err != nil {
		return exitFor(err)
	}
	if *asJSON {
		err = json.NewEncoder(os.Stdout).Encode(data)
	} else {
		err = printAlertsLedger(os.Stdout, data)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: writing alerts: %v\n", err)
		return record.ExitExternal
	}
	return record.ExitOK
}

func printAlertsLedger(w io.Writer, data alertsData) error {
	if data.Absent {
		_, err := fmt.Fprintln(w, "alerts: none (store has no alert_episodes table)")
		return err
	}
	c := data.Counts
	if _, err := fmt.Fprintf(w, "summary unsettled=%d (uninvestigated=%d investigating=%d investigated=%d) settled=%d active=%d resolved=%d active_silences=%d\n",
		c.Unsettled, c.Uninvestigated, c.Investigating, c.Investigated, c.Settled, c.Active, c.Resolved, c.ActiveSilences); err != nil {
		return err
	}
	if len(data.Episodes) == 0 {
		if _, err := fmt.Fprintln(w, "no alert episodes"); err != nil {
			return err
		}
	} else {
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ALERT_ID\tRULE\tSUBJECT\tSEVERITY\tSTATE\tSETTLEMENT\tEP\tOBS\tLATEST\tITEMS")
		for _, ep := range data.Episodes {
			latest := "-"
			if ep.LatestTransition != nil {
				latest = fmt.Sprintf("%s by %s", ep.LatestTransition.StateChange, ep.LatestTransition.Actor)
			}
			items := formatLinkedItemsSummary(ep.LinkedItems)
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
				ep.AlertID, ep.RuleID, ep.SubjectKey, ep.Severity, ep.State, ep.SettlementState,
				ep.EpisodeNum, ep.ObservationCount, latest, items)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(data.ActiveSilences) > 0 {
		if _, err := fmt.Fprintln(w, "\nACTIVE SILENCES"); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SILENCE_ID\tRULE\tVARS\tITEM\tBY\tREASON")
		for _, sil := range data.ActiveSilences {
			itemText := sil.TrackingItemID
			if sil.TrackingItemState != "" {
				itemText += " (" + sil.TrackingItemState + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				sil.SilenceID, sil.RuleID, sil.VarsJSON, itemText, sil.CreatedBy, sil.Reason)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func formatLinkedItemsSummary(links []alertItemLinkView) string {
	if len(links) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(links))
	for _, l := range links {
		s := l.ItemID + "[" + l.Relation
		if l.ItemState != "" {
			s += "," + l.ItemState
		}
		s += "]"
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func printAlertDetail(w io.Writer, detail alertDetailData) error {
	ep := detail.Episode
	if _, err := fmt.Fprintf(w, "alert %s  rule %s  subject %s  severity %s  state %s  settlement %s  episode %d  obs %d\n",
		ep.AlertID, ep.RuleID, ep.SubjectKey, ep.Severity, ep.State, ep.SettlementState, ep.EpisodeNum, ep.ObservationCount); err != nil {
		return err
	}
	if ep.VariablesJSON != "" && ep.VariablesJSON != "{}" {
		fmt.Fprintf(w, "variables %s\n", ep.VariablesJSON)
	}
	fmt.Fprintf(w, "opened %s (seq %d)  last_observed %s (seq %d)\n",
		time.UnixMilli(ep.OpenedAt).UTC().Format(time.RFC3339), ep.OpenedSeq,
		time.UnixMilli(ep.LastObservedAt).UTC().Format(time.RFC3339), ep.LastObservedSeq)
	if ep.InvestigationCause != "" {
		fmt.Fprintf(w, "investigation by %s: %s (evidence: %s)\n", formatAlertActor(ep.InvestigatedBy), ep.InvestigationCause, ep.InvestigationEvidence)
	}
	if ep.Outcome != "" {
		fmt.Fprintf(w, "outcome %s  by %s  item %s\n", ep.Outcome, formatAlertActor(ep.SettledBy), ep.TrackingItemID)
	}

	fmt.Fprintln(w, "\nTRANSITIONS")
	if len(detail.Transitions) == 0 {
		fmt.Fprintln(w, "  (no transition events recorded)")
	} else {
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SEQ\tTIME\tCHANGE\tACTOR\tDETAIL")
		for _, tr := range detail.Transitions {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
				tr.Seq, time.UnixMilli(tr.Timestamp).UTC().Format(time.RFC3339), tr.StateChange, tr.Actor, tr.Detail)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintln(w, "\nLINKED ITEMS")
	if len(detail.LinkedItems) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ITEM_ID\tSTATE\tRELATION\tSILENCE\tBY\tTITLE")
		for _, l := range detail.LinkedItems {
			state := l.ItemState
			if state == "" {
				state = "-"
			}
			by := l.LinkedBy
			if by == "" {
				by = unrecordedAlertActor
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\t%s\n",
				l.ItemID, state, l.Relation, l.ActiveSilence, by, l.ItemTitle)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(detail.RelatedEpisodes) > 0 {
		fmt.Fprintln(w, "\nRELATED EPISODES")
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ALERT_ID\tEP\tSTATE\tSETTLEMENT\tOPENED")
		for _, rel := range detail.RelatedEpisodes {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n",
				rel.AlertID, rel.EpisodeNum, rel.State, rel.SettlementState,
				time.UnixMilli(rel.OpenedAt).UTC().Format(time.RFC3339))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}
