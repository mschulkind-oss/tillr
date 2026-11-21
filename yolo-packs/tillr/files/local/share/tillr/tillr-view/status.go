// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package main

import (
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/heavy"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/vantage"
)

// statusPayload is the payload of view.status: the moment, whether the subject is
// there, and the assembled subject (design §5.8). Every component is copied from a
// record another utility emitted or from the design document the item points at; the
// assembly computes presentation, never new state.
//
// The components are assembled from whichever source the face was given: a record stream
// assembles from its records, and a store assembles from its own rows (VIEW-2), so a
// store report answers the item, its criteria, its candidates and its runs rather than
// leaving them null.
//
// --item sets the level of detail. A report that names one is a subject report: Item,
// Questions, Candidates and Runs answer that item. A report that names none is a board
// report: Items, Candidates and Runs answer every item, candidate and run the source
// holds, and the subject components stay null because no single item was asked about.
type statusPayload struct {
	Alarms []alarms.Alarm `json:"alarms,omitempty"`

	Heavy       *heavy.HeavyStatus `json:"heavy,omitempty"`
	HeavyFault  string             `json:"heavy_fault,omitempty"`
	GeneratedAt time.Time          `json:"generated_at"`
	Snapshot    snapshot           `json:"snapshot"`
	Subject     *record.Subject    `json:"subject,omitempty"`
	Found       *bool              `json:"found,omitempty"`
	// Sync residue is the last whole writing sync's persisted observation, never a
	// fresh document scan. Null counts mean no observation, not zero residue.
	UnsharedOpen       *int     `json:"unshared_open"`
	UnsharedOpenIDs    []string `json:"unshared_open_ids"`
	UnsharedOpenOldest string   `json:"unshared_open_oldest,omitempty"`
	StaleDocBlocks     *int     `json:"stale_doc_blocks"`
	StaleDocBlockIDs   []string `json:"stale_doc_block_ids"`
	SyncedAt           string   `json:"synced_at,omitempty"`
	// Item is the subject's item.record payload, or null when the stream carries none.
	Item *itemRecord `json:"item"`
	// Items are every item the source holds, ordered by item_id for a store and by first
	// appearance for a record stream, and only in a board report: with a subject the
	// answer is Item, and repeating the board beside it would answer a narrower question
	// twice. A board item carries no questions — an open question belongs to one item's
	// design document, and reading every item's would turn one missing file into a
	// refusal of the whole board.
	Items []itemRecord `json:"items"`
	// Questions are the open questions read from the design document the item points
	// at, or null when it points at none. An empty list is a finished design and null
	// is no design document at all: the two need different remedies, so they are
	// different answers.
	Questions []question `json:"questions"`
	// QuestionsSource is the design document the questions were read for.
	QuestionsSource string `json:"questions_source"`
	// QuestionsState is which of the three answers the index gave: questions-ok, the
	// tool's list (which may be empty for a finished design); questions-unknown, the
	// tool was absent, failed or timed out; or design-doc-missing, the tree does not hold
	// the document. Unknown is never read as "no questions".
	QuestionsState string `json:"questions_state"`
	// Candidates are the candidates of what this report is about — the subject, or the
	// whole board when no subject was named — each with the records that bind it.
	Candidates []candidateStatus `json:"candidates"`
	// Runs are the runs of what this report is about: the run.record payloads of the
	// lanes the subject was dispatched on, or every run in a board report.
	Runs []runRecord `json:"runs"`
	// Gate is the gate's own wall time: the most recent tick's, the budget it was measured
	// against, and the recent trend, in a board report. A subject report carries none,
	// because the gate is a property of the tick and not of one item. It is null for a source
	// that holds no tick measurement, which is a different answer from a gate that took no
	// time.
	Gate *gateSummary `json:"gate"`
	// Counts are the header's four figures in a board report read from a store, computed by
	// the same function the served snapshot uses (design §5.6). A record stream has no store
	// rows to count, and a subject report is one item rather than the board, so both answer
	// null rather than a second arithmetic.
	Counts *boardCounts `json:"counts"`
	// Only a board report from a store carries publication state.
	Push *pushSummary `json:"push,omitempty"`
}

// gateSummary is the gate's measurement as a report reads it: the last tick's gate wall
// time, the budget it was measured against, and the recent trend oldest first. It is the
// board's answer to "did the gate get slower", and it is the gate's number alone: staging,
// dispatch and landing are recorded beside it and are not this number.
type gateSummary struct {
	LastMS   *int64     `json:"last_ms"`
	BudgetMS *int64     `json:"budget_ms"`
	Trend    []gateTick `json:"trend"`
}

// ticks is the tick measurement the source holds, or nil for a source that holds none (a
// record stream carries no store table).
func (v view) ticks() []storeTick {
	if v.domain == nil {
		return nil
	}
	return v.domain.ticks
}

// gateSummaryOf is the board's gate summary from the tick rows, or nil when the source holds
// none. The last row is the most recent tick, and the trend keeps the last gateTrendLimit of
// them: the board shows the recent shape and the table keeps the archive.
func gateSummaryOf(ticks []storeTick) *gateSummary {
	if len(ticks) == 0 {
		return nil
	}
	last := ticks[len(ticks)-1]
	lastMS, budgetMS := last.GateMS, last.GateBudgetMS
	summary := &gateSummary{LastMS: &lastMS, BudgetMS: &budgetMS}
	start := 0
	if len(ticks) > gateTrendLimit {
		start = len(ticks) - gateTrendLimit
	}
	summary.Trend = make([]gateTick, 0, len(ticks)-start)
	for _, tick := range ticks[start:] {
		summary.Trend = append(summary.Trend, gateTick{
			TickID: tick.TickID, AtMS: tick.StartedAtMS, GateMS: tick.GateMS,
			BudgetMS: tick.GateBudgetMS, OverBudget: tick.OverBudget,
		})
	}
	return summary
}

// assemble is the one status assembly, over whichever source the face was given. It
// computes presentation and never new state: every field is copied from the snapshot,
// from the subject the caller named, or from a record in the stream.
func assemble(v view, subject *record.Subject, source vantage.QuestionSource) (statusPayload, int) {
	p := statusPayload{GeneratedAt: time.Now().UTC(), Snapshot: v.snapshot}
	for _, e := range v.records {
		if e.Kind == "item.sync" {
			var observed struct {
				Count    *int     `json:"unshared_open"`
				IDs      []string `json:"unshared_open_ids"`
				Oldest   string   `json:"unshared_open_oldest"`
				Stale    *int     `json:"stale_doc_blocks"`
				StaleIDs []string `json:"stale_doc_block_ids"`
			}
			if err := json.Unmarshal(e.Payload, &observed); err != nil {
				return statusPayload{}, usagef("reading item.sync: %v", err)
			}
			p.UnsharedOpen, p.StaleDocBlocks = observed.Count, observed.Stale
			p.UnsharedOpenIDs, p.StaleDocBlockIDs = observed.IDs, observed.StaleIDs
			p.UnsharedOpenOldest = observed.Oldest
			p.SyncedAt = e.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	if v.domain != nil && v.domain.residue != nil {
		s := v.domain.residue
		open, stale := len(s.UnsharedOpenIDs), len(s.StaleDocBlockIDs)
		p.UnsharedOpen, p.StaleDocBlocks = &open, &stale
		p.UnsharedOpenIDs, p.StaleDocBlockIDs = s.UnsharedOpenIDs, s.StaleDocBlockIDs
		p.UnsharedOpenOldest, p.SyncedAt = s.UnsharedOpenOldest, s.SyncedAt
	}
	if subject == nil {
		// No subject named is a report about the board, not an empty report: the same
		// source the snapshot was read from holds the items, candidates and runs, and a
		// caller that asked for none of them in particular asked for all of them.
		assembleBoard := v.assembleBoard
		if v.domain != nil {
			assembleBoard = v.assembleStoreBoard
		}
		board, err := assembleBoard()
		if err != nil {
			return statusPayload{}, usagef("%v", err)
		}
		p.Items = board.items
		p.Candidates = board.candidates
		p.Runs = board.runs
		p.Gate = gateSummaryOf(v.ticks())
		if v.domain != nil {
			counts := v.domain.boardCounts()
			p.Counts = &counts
		}
		return p, record.ExitOK
	}
	p.Subject = subject

	assembleSubject := v.assembleSubject
	if v.domain != nil {
		assembleSubject = v.assembleStoreSubject
	}
	assembly, err := assembleSubject(subject.ID, source)
	if err != nil {
		// A payload this utility cannot describe is malformed input. A design document
		// that is not there is not an error: it is the `design-doc-missing` state, and
		// the report carries it rather than refusing the whole subject.
		return statusPayload{}, usagef("%v", err)
	}

	found := v.namesSubject(*subject, assembly.item)
	p.Found = &found
	if !found {
		// A subject that is not in the snapshot is a negative decision, not an error:
		// the utility ran and the answer is no.
		return p, record.ExitNegative
	}

	p.Item = assembly.item
	p.Questions = assembly.questions
	p.QuestionsSource = assembly.questionsSource
	p.QuestionsState = assembly.questionsState
	p.Candidates = assembly.candidates
	p.Runs = assembly.runs
	return p, record.ExitOK
}

// namesSubject answers whether the snapshot names the subject. The item record is the
// authority in records mode — §5.8 makes it the newest item.record whose payload's
// item_id is the subject — and a stream that carries no item.record for it can still
// name it as an event's object. A store answers from its items table, and falls back to
// the event's object for a store that has no items row for it.
func (v view) namesSubject(s record.Subject, item *itemRecord) bool {
	if item != nil {
		return true
	}
	if v.domain != nil {
		if _, found := v.domain.item(s.ID); found {
			return true
		}
	}
	for _, e := range v.events {
		if e.ObjectType == s.Kind && e.ObjectID != nil && *e.ObjectID == s.ID {
			return true
		}
	}
	return false
}
