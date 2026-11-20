package main

import (
	"time"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// storeDomain is the store's own rows, read inside the same read transaction as the
// snapshot and the events. It is what makes `tillr-view status --store` answer from the
// store rather than from a record stream (VIEW-2): the components are the rows the
// write faces already maintain, not a second copy of them.
type storeDomain struct {
	residue    *syncResidue
	items      []storeItem
	criteria   map[string][]storeCriterion
	edges      map[string][]storeEdge
	candidates []storeCandidate
	attempts   map[string][]storeAttempt
	runs       []storeRun
	dispatches []storeDispatch
	ticks      []storeTick
	// lanes and allocations project the live runs the query tables have no row for yet:
	// the header's runs_live must count the run a lease is working, not only the rows a
	// terminal write has created.
	lanes       []storeLane
	allocations map[string]allocation
	// history is the planner's read of the same rows, so the CLI's Dispatchable and the
	// snapshot's are one arithmetic (design §5.6).
	history *dispatchHistory
}

// readStoreDomain reads every row status assembles from. A table a store does not carry
// is left empty rather than failing the whole report: a store written by a program that
// never linked a utility's package still has a schema version to report.
func readStoreDomain(r *store.Reader) (*storeDomain, error) {
	return readStoreDomainIn(r, "")
}

func readStoreDomainIn(r *store.Reader, loopDir string) (*storeDomain, error) {
	d := &storeDomain{
		criteria: map[string][]storeCriterion{},
		edges:    map[string][]storeEdge{},
		attempts: map[string][]storeAttempt{},
	}
	var err error
	if d.residue, err = readSyncResidue(r); err != nil {
		return nil, err
	}
	if d.items, err = readItems(r); err != nil {
		if !isMissingTable(err) {
			return nil, err
		}
		d.items = nil
	}
	if rows, err := r.Query(`SELECT item_id, criterion_id, text, state FROM item_criteria ORDER BY item_id, criterion_id`); err == nil {
		for rows.Next() {
			var c storeCriterion
			if rows.Scan(&c.ItemID, &c.CriterionID, &c.Text, &c.State) == nil {
				d.criteria[c.ItemID] = append(d.criteria[c.ItemID], c)
			}
		}
		rows.Close()
	}
	if rows, err := r.Query(`SELECT item_id, blocked_by FROM item_edges ORDER BY item_id, blocked_by`); err == nil {
		for rows.Next() {
			var e storeEdge
			if rows.Scan(&e.ItemID, &e.BlockedBy) == nil {
				d.edges[e.ItemID] = append(d.edges[e.ItemID], e)
			}
		}
		rows.Close()
	}
	if d.candidates, err = readCandidates(r); err != nil && !isMissingTable(err) {
		return nil, err
	}
	if d.runs, err = readRuns(r); err != nil {
		if !isMissingTable(err) {
			return nil, err
		}
		d.runs = nil
	}
	if d.dispatches, err = readAllDispatches(r); err != nil && !isMissingTable(err) {
		return nil, err
	}
	if d.ticks, err = readDrainTicks(r); err != nil && !isMissingTable(err) {
		return nil, err
	}
	if d.lanes, err = readLanesIn(r, loopDir); err != nil && !isMissingTable(err) {
		return nil, err
	}
	if d.allocations, err = readAllocations(r); err != nil && !isMissingTable(err) {
		return nil, err
	}
	if d.history, err = readDispatchHistory(r, d.runs); err != nil {
		return nil, err
	}
	for _, c := range d.candidates {
		attempts, aerr := readAttempts(r, c.CandidateID)
		if aerr != nil {
			if isMissingTable(aerr) {
				continue
			}
			return nil, aerr
		}
		d.attempts[c.CandidateID] = attempts
	}
	return d, nil
}

// boardCounts is the store's own four figures, read from the same rows the served
// snapshot reads: the items, the runs (including the live run a lease projects), the
// candidates, the attempts and the blocked_by edges. The snapshot and this face call one
// function, so the two cannot disagree about a number the header leads with (design
// §5.6).
func (d *storeDomain) boardCounts() boardCounts {
	if d == nil {
		return boardCounts{ByState: map[string]int{}}
	}
	waiting := make(map[string][]string, len(d.edges))
	for itemID, edges := range d.edges {
		for _, edge := range edges {
			waiting[itemID] = append(waiting[itemID], edge.BlockedBy)
		}
	}
	attempts := make([]storeAttempt, 0, len(d.attempts))
	for _, rows := range d.attempts {
		attempts = append(attempts, rows...)
	}
	runs := d.runs
	if live := liveRuns(d.lanes, d.runs, d.allocations); len(live) > 0 {
		runs = append(append([]storeRun{}, d.runs...), live...)
	}
	return boardCountsOf(d.items, runs, d.candidates, attempts, waiting, questionHolds(d.dispatches), d.history)
}

// item answers one item row.
func (d *storeDomain) item(itemID string) (storeItem, bool) {
	for _, it := range d.items {
		if it.ItemID == itemID {
			return it, true
		}
	}
	return storeItem{}, false
}

// itemRecordOf is one item row as the record shape, with its criteria and its blocked_by
// edges. It is shared by the subject and the board report, so the two cannot spell one
// row two ways.
func (d *storeDomain) itemRecordOf(it storeItem) itemRecord {
	record := itemRecord{
		ItemID:    it.ItemID,
		Title:     it.Title,
		BodyRef:   it.BodyRef,
		State:     it.State,
		Rank:      it.Rank,
		DesignDoc: it.DesignDoc,
	}
	for _, c := range d.criteria[it.ItemID] {
		record.Criteria = append(record.Criteria, criterion{c.CriterionID, c.Text, c.State})
	}
	for _, e := range d.edges[it.ItemID] {
		record.BlockedBy = append(record.BlockedBy, e.BlockedBy)
	}
	return record
}

// candidateStatusOf is one candidate row with the latest attempt the store holds for
// it. The store keeps no gate verdict, so LatestVerdict stays null here: a verdict lives
// only in the tick's record directory, and a null is that absence rather than a pass.
func (d *storeDomain) candidateStatusOf(c storeCandidate) candidateStatus {
	status := candidateStatus{Candidate: candidate{
		CandidateID: c.CandidateID,
		ItemID:      c.ItemID,
		BaseRef:     c.BaseRef,
		BaseSHA:     c.BaseSHA,
		HeadSHA:     c.HeadSHA,
		TreeSHA:     c.TreeSHA,
		State:       c.State,
		EnqueuedAt:  time.UnixMilli(c.EnqueuedMS).UTC().Format(time.RFC3339Nano),
	}}
	if rows := d.attempts[c.CandidateID]; len(rows) > 0 {
		last := rows[len(rows)-1]
		status.LatestAttempt = &attempt{
			AttemptID:   last.AttemptID,
			CandidateID: last.CandidateID,
			VerdictID:   last.VerdictID,
			Result:      last.Result,
			Reason:      last.Reason,
			TrunkBefore: last.TrunkBefore,
			TrunkAfter:  last.TrunkAfter,
		}
	}
	return status
}

// runRecordOf is one run row as the record shape.
func runRecordOf(run storeRun) runRecord {
	return runRecord{
		RunID:        run.RunID,
		ItemID:       run.ItemID,
		LaneID:       run.LaneID,
		Backend:      run.Backend,
		State:        run.State,
		ExitCode:     run.ExitCode,
		StartedAt:    time.UnixMilli(run.StartedAtMS).UTC().Format(time.RFC3339Nano),
		FinishedAt:   time.UnixMilli(run.FinishedAtMS).UTC().Format(time.RFC3339Nano),
		ArtifactRefs: []artifactRef{},
	}
}

// assembleStoreBoard assembles the board from the store's own rows: every item with its
// criteria and edges, every candidate with its latest attempt, and every run. The three
// lists are empty rather than null for a store that holds none, so a caller can iterate
// them without special-casing an absence.
//
// The items are ordered by rankOrdered, the same rule `assembleBoard` (the record stream's
// assembly) and `serve.go`'s snapshot use, and the same one the drain's planner decides
// in. The store hands the rows over in id order, so without this cut the CLI's board report
// and the board itself would be two orderings of one queue: `tillr view status` answered
// `item-seed-01, 02, 04, 06, 08` for a store whose rank order is `04, 08, 02, 01, 06`.
func (v view) assembleStoreBoard() (boardAssembly, error) {
	board := boardAssembly{
		items:      []itemRecord{},
		candidates: []candidateStatus{},
		runs:       []runRecord{},
	}
	if v.domain == nil {
		return board, nil
	}
	for _, it := range v.domain.items {
		board.items = append(board.items, v.domain.itemRecordOf(it))
	}
	for _, c := range v.domain.candidates {
		board.candidates = append(board.candidates, v.domain.candidateStatusOf(c))
	}
	for _, run := range v.domain.runs {
		board.runs = append(board.runs, runRecordOf(run))
	}
	board.items = rankOrdered(board.items)
	return board, nil
}

// assembleStoreSubject assembles the subject from the store's own rows. It computes
// presentation and never new state: every field is a row the write faces maintain, and a
// component the store does not hold — a gate verdict, which lives only in the tick's
// record directory — stays null rather than being reconstructed from a neighbour.
func (v view) assembleStoreSubject(itemID string, source vantage.QuestionSource) (subjectAssembly, error) {
	var assembly subjectAssembly
	if v.domain == nil {
		return assembly, nil
	}
	it, found := v.domain.item(itemID)
	if !found {
		return assembly, nil
	}
	record := v.domain.itemRecordOf(it)
	record.Place = itemPlaces(v.domain.items)[it.ItemID]
	assembly.item = &record

	lanes := map[string]bool{}
	for _, d := range v.domain.dispatches {
		if d.ItemID == itemID && d.LaneID != "" {
			lanes[d.LaneID] = true
		}
	}
	for _, c := range v.domain.candidates {
		if c.ItemID == itemID {
			assembly.candidates = append(assembly.candidates, v.domain.candidateStatusOf(c))
		}
	}
	for _, run := range v.domain.runs {
		if run.ItemID != itemID && !lanes[run.LaneID] {
			continue
		}
		assembly.runs = append(assembly.runs, runRecordOf(run))
	}

	if record.DesignDoc != "" {
		answer := readQuestions(source, record.DesignDoc)
		record.Questions = answer.Questions
		record.QuestionsSource = record.DesignDoc
		record.QuestionsState = answer.State
		assembly.questions = answer.Questions
		assembly.questionsSource = record.DesignDoc
		assembly.questionsState = answer.State
	}
	return assembly, nil
}
