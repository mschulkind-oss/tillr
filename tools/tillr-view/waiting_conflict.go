package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// The view reads conflict records without importing their writer or migrating a store.
type waitingConflict struct {
	ConflictID          string   `json:"conflict_id"`
	ItemID              string   `json:"item_id"`
	RunID               string   `json:"run_id"`
	BodyRef             string   `json:"body_ref"`
	Text                string   `json:"text"`
	BlockedCriteria     []string `json:"blocked_criteria"`
	WaitingOn           string   `json:"waiting_on"`
	Status              string   `json:"status"`
	DesignRunID         string   `json:"design_run_id"`
	AnswerHead          string   `json:"answer_head"`
	QuestionRef         string   `json:"question_ref"`
	QuestionRemovedHead string   `json:"question_removed_head"`
	QuestionHead        string   `json:"question_head"`

	TriageVersion string               `json:"triage_version"`
	Since         int64                `json:"-"`
	History       []conflictTransition `json:"-"`
}

type conflictTransition struct {
	Seq   int64     `json:"seq"`
	At    time.Time `json:"at"`
	Label string    `json:"label"`
	RunID string    `json:"run_id"`
	// Reports appear only when changed; routing changes do not repeat their evidence.
	Report string `json:"report,omitempty"`
}

type conflictHistory struct {
	ConflictID  string               `json:"conflict_id"`
	ItemID      string               `json:"item_id"`
	FirstSeq    int64                `json:"first_seq"`
	Transitions []conflictTransition `json:"transitions"`
	last        waitingConflict
}

func conflictChange(previous *waitingConflict, c waitingConflict) string {
	if previous == nil {
		return c.Status
	}
	var changes []string
	if previous.Status != c.Status {
		changes = append(changes, previous.Status+" → "+c.Status)
	}
	if previous.DesignRunID != c.DesignRunID {
		label := "design run " + c.DesignRunID
		if c.DesignRunID == "" {
			label = "design run cleared"
		}
		changes = append(changes, label)
	}
	if previous.WaitingOn != c.WaitingOn {
		changes = append(changes, "waiting on "+c.WaitingOn)
	}
	if previous.AnswerHead != c.AnswerHead {
		changes = append(changes, "answer head "+c.AnswerHead)
	}
	if previous.BodyRef != c.BodyRef {
		changes = append(changes, "body reference changed")
	}
	if !reflect.DeepEqual(previous.BlockedCriteria, c.BlockedCriteria) {
		changes = append(changes, "blocked criteria changed")
	}
	if previous.Text != c.Text || previous.RunID != c.RunID {
		changes = append(changes, "evidence added from run "+c.RunID)
	}
	if len(changes) == 0 {
		return "unchanged record"
	}
	return strings.Join(changes, "; ")
}

// Group by conflict identity, not just item: separate conflicts on an item remain separate.
func conflictHistories(events []eventRecord) ([]conflictHistory, error) {
	groups := []conflictHistory{}
	indexes := map[string]int{}
	for _, e := range events {
		if e.Kind != "item.conflict" {
			continue
		}
		var c waitingConflict
		if err := json.Unmarshal(e.Payload, &c); err != nil {
			return nil, err
		}
		key := c.ItemID + "\x00" + c.ConflictID
		i, exists := indexes[key]
		if !exists {
			i = len(groups)
			indexes[key] = i
			groups = append(groups, conflictHistory{ConflictID: c.ConflictID, ItemID: c.ItemID, FirstSeq: e.Seq})
		}
		g := &groups[i]
		var previous *waitingConflict
		if exists {
			previous = &g.last
		}
		tr := conflictTransition{Seq: e.Seq, At: e.At, Label: conflictChange(previous, c), RunID: c.RunID}
		if previous == nil || previous.Text != c.Text {
			tr.Report = c.Text
		}
		g.Transitions = append(g.Transitions, tr)
		c.Since = e.At.UnixMilli()
		g.last = c
	}
	return groups, nil
}

// Raw events remain unchanged for stream consumers. Labels and grouped transitions
// are view metadata, and only transitions after the caller's cursor are returned.
func annotateConflictEvents(events []eventRecord, groups []conflictHistory, since int64) []conflictHistory {
	out := []conflictHistory{}
	type annotation struct {
		id    string
		first int64
		label string
	}
	labels := map[int64]annotation{}
	for _, g := range groups {
		visible := g
		visible.Transitions = nil
		for _, tr := range g.Transitions {
			if tr.Seq > since {
				visible.Transitions = append(visible.Transitions, tr)
				labels[tr.Seq] = annotation{g.ConflictID, g.FirstSeq, tr.Label}
			}
		}
		if len(visible.Transitions) > 0 {
			out = append(out, visible)
		}
	}
	for i := range events {
		if label, ok := labels[events[i].Seq]; ok {
			events[i].ConflictID = label.id
			events[i].FirstSeq = label.first
			events[i].Transition = label.label
		}
	}
	return out
}

func readConflictHistories(r *store.Reader) ([]conflictHistory, error) {
	rows, err := r.Query(`SELECT seq, ts, payload FROM events WHERE kind='item.conflict' ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []eventRecord
	for rows.Next() {
		e := eventRecord{Kind: "item.conflict"}
		var ts int64
		var raw []byte
		if err := rows.Scan(&e.Seq, &ts, &raw); err != nil {
			return nil, err
		}
		e.Payload = raw
		e.At = time.UnixMilli(ts).UTC()
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return conflictHistories(events)
}

func waitingConflicts(r *store.Reader) (map[string]waitingConflict, error) {
	groups, err := readConflictHistories(r)
	if err != nil {
		return nil, err
	}
	out := map[string]waitingConflict{}
	for _, g := range groups {
		c := g.last
		c.History = g.Transitions
		if prior, ok := out[c.ItemID]; !ok || prior.History[len(prior.History)-1].Seq < g.Transitions[len(g.Transitions)-1].Seq {
			out[c.ItemID] = c
		}
	}
	return out, nil
}

func waitingConflictsForItems(r *store.Reader, itemIDs []string) (map[string]waitingConflict, error) {
	out := map[string]waitingConflict{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	evList, err := r.EventsForObjects(0, itemIDs, "item.conflict")
	if err != nil {
		return nil, err
	}
	events := make([]eventRecord, 0, len(evList))
	for _, ev := range evList {
		events = append(events, eventRecord{
			Seq:     ev.Seq,
			Kind:    ev.Kind,
			Payload: ev.Payload,
			At:      ev.At,
		})
	}
	groups, err := conflictHistories(events)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		c := g.last
		c.History = g.Transitions
		if prior, ok := out[c.ItemID]; !ok || prior.History[len(prior.History)-1].Seq < g.Transitions[len(g.Transitions)-1].Seq {
			out[c.ItemID] = c
		}
	}
	return out, nil
}

func conflictSummary(c waitingConflict) string {
	labels := make([]string, 0, len(c.History))
	for _, tr := range c.History {
		labels = append(labels, fmt.Sprintf("%s (%s)", tr.Label, tr.At.UTC().Format(time.RFC3339Nano)))
	}
	return c.ConflictID + ": " + strings.Join(labels, " → ")
}

// Append after selecting the reason: a fault, repair or planner reason must not hide history.
func addConflictHistory(rows []waitingRow, conflicts map[string]waitingConflict) {
	for i := range rows {
		if c, ok := conflicts[rows[i].ItemID]; ok {
			if strings.HasPrefix(rows[i].Why, c.ConflictID+": ") {
				rows[i].Why = strings.TrimPrefix(rows[i].Why, c.ConflictID+": ")
			}
			rows[i].Why += "; conflict transitions: " + conflictSummary(c)
		}
	}
}
