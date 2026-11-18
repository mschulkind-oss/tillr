package main

import (
	"encoding/json"
	"sort"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/vantage"
)

// The record kinds the assembly reads. They are the kinds other utilities emit,
// spelled here because §5.9 forbids importing the package that declares them.
const (
	itemRecordKind = "item.record"
	candidateKind  = "land.candidate"
	attemptKind    = "land.attempt"
	verdictKind    = "gate.verdict"
	runRecordKind  = "run.record"
	runRequestKind = "run.request"
)

// candidateStatus is one candidate with the records that bind it. LatestAttempt and
// LatestVerdict are null when there is none: an absent attempt is a fact about the
// candidate, not an error.
type candidateStatus struct {
	Candidate     candidate `json:"candidate"`
	LatestAttempt *attempt  `json:"latest_attempt"`
	LatestVerdict *verdict  `json:"latest_verdict"`
}

// subjectAssembly is the assembled subject of a view.status record. Every component is
// copied from a record another utility emitted or from the design document itself; the
// assembly computes presentation and never new state (§5.8).
type subjectAssembly struct {
	item            *itemRecord
	questions       []question
	questionsSource string
	questionsState  string
	candidates      []candidateStatus
	runs            []runRecord
}

// boardAssembly is the assembled board: the same components as a subject assembly,
// widened from the one item a caller named to every one the source holds. It is what
// `status` answers when no --item was given, because a report that names no subject is
// a report about the whole board rather than a snapshot with every component null.
//
// It carries no questions: an open question belongs to one item's design document, and
// reading every item's document would turn one missing file into a refusal of the whole
// board.
type boardAssembly struct {
	items      []itemRecord
	candidates []candidateStatus
	runs       []runRecord
}

// assembleSubject assembles the subject from the record stream. The stream carries no
// foreign keys, so every join is by an id a payload carries:
//
//   - the item record is the newest item.record whose payload's item_id is the subject;
//   - the candidates are every land.candidate whose item_id is the subject, in stream
//     order;
//   - a candidate's latest land.attempt is the last land.attempt whose candidate_id is
//     that candidate's, and its latest gate.verdict is the last gate.verdict whose
//     tree_sha is that candidate's tree_sha — a verdict binds a tree, not a candidate
//     id, which is why two candidates with the same tree share one verdict;
//   - the runs are every run.record whose lane_id is a lane the subject was dispatched
//     on.
//
// "Latest" is stream order, not created_at: the stream is the order the records were
// emitted, and two records can share a timestamp.
//
// It answers an error only for a payload this utility cannot describe. A design document
// the item points at that is not there is the `design-doc-missing` question state, carried
// in the subject rather than refused. Everything else absent is a null component.
func (v view) assembleSubject(itemID string, source vantage.QuestionSource) (subjectAssembly, error) {
	var (
		assembly   subjectAssembly
		candidates []candidate
		runs       []runRecord
		attempts   = map[string]*attempt{}
		verdicts   = map[string]*verdict{}
		lanes      = map[string]bool{}
	)
	for i, env := range v.records {
		switch env.Kind {
		case itemRecordKind:
			var payload itemRecord
			if err := decodePayload(env, i, &payload); err != nil {
				return subjectAssembly{}, err
			}
			if payload.ItemID == itemID {
				// The newest record for the subject is its current state.
				current := payload
				assembly.item = &current
			}
		case candidateKind:
			var payload candidate
			if err := decodePayload(env, i, &payload); err != nil {
				return subjectAssembly{}, err
			}
			if payload.ItemID == itemID {
				candidates = append(candidates, payload)
			}
		case attemptKind:
			var payload attempt
			if err := decodePayload(env, i, &payload); err != nil {
				return subjectAssembly{}, err
			}
			attempts[payload.CandidateID] = &payload
		case verdictKind:
			var payload verdict
			if err := decodePayload(env, i, &payload); err != nil {
				return subjectAssembly{}, err
			}
			verdicts[payload.TreeSHA] = &payload
		case runRecordKind:
			var payload runRecord
			if err := decodePayload(env, i, &payload); err != nil {
				return subjectAssembly{}, err
			}
			runs = append(runs, payload)
		case runRequestKind:
			var payload runRequest
			if err := decodePayload(env, i, &payload); err != nil {
				return subjectAssembly{}, err
			}
			if payload.ItemID == itemID {
				lanes[payload.LaneID] = true
			}
		}
	}

	assembly.candidates = candidateStatuses(candidates, attempts, verdicts)

	assembly.runs = make([]runRecord, 0, len(runs))
	for _, r := range runs {
		if lanes[r.LaneID] {
			assembly.runs = append(assembly.runs, r)
		}
	}

	if assembly.item != nil {
		board, err := v.assembleBoard()
		if err != nil {
			return subjectAssembly{}, err
		}
		for _, it := range board.items {
			if it.ItemID == itemID {
				assembly.item.Place = it.Place
				break
			}
		}
	}
	if assembly.item != nil && assembly.item.DesignDoc != "" {
		answer := readQuestions(source, assembly.item.DesignDoc)
		// The index is the only copy of the questions, so the item component carries the
		// read-through rather than whatever the record happened to hold.
		assembly.item.Questions = answer.Questions
		assembly.item.QuestionsSource = assembly.item.DesignDoc
		assembly.item.QuestionsState = answer.State
		assembly.questions = answer.Questions
		assembly.questionsSource = assembly.item.DesignDoc
		assembly.questionsState = answer.State
	}
	return assembly, nil
}

// candidateStatuses binds each candidate to the records that judge it. The two joins
// are by an id a payload carries: an attempt names its candidate_id, and a verdict
// names the tree_sha it judged.
func candidateStatuses(candidates []candidate, attempts map[string]*attempt, verdicts map[string]*verdict) []candidateStatus {
	out := make([]candidateStatus, 0, len(candidates))
	for _, c := range candidates {
		status := candidateStatus{Candidate: c}
		if c.CandidateID != "" {
			status.LatestAttempt = attempts[c.CandidateID]
		}
		if c.TreeSHA != "" {
			// An empty tree_sha binds nothing: a verdict that carries one is not a
			// verdict about this candidate.
			status.LatestVerdict = verdicts[c.TreeSHA]
		}
		out = append(out, status)
	}
	return out
}

// assembleBoard assembles the board from the record stream: the newest item.record per
// item_id, every land.candidate in stream order, and every run.record in stream order.
// It is the stream-mode counterpart of the store board read, and the two must answer the
// same shape — including the order, which is rankOrdered's rule in both.
func (v view) assembleBoard() (boardAssembly, error) {
	var (
		board      boardAssembly
		items      = map[string]itemRecord{}
		order      []string
		candidates []candidate
		runs       []runRecord
		attempts   = map[string]*attempt{}
		verdicts   = map[string]*verdict{}
	)
	for i, env := range v.records {
		switch env.Kind {
		case itemRecordKind:
			var payload itemRecord
			if err := decodePayload(env, i, &payload); err != nil {
				return boardAssembly{}, err
			}
			if _, seen := items[payload.ItemID]; !seen {
				order = append(order, payload.ItemID)
			}
			items[payload.ItemID] = payload
		case candidateKind:
			var payload candidate
			if err := decodePayload(env, i, &payload); err != nil {
				return boardAssembly{}, err
			}
			candidates = append(candidates, payload)
		case attemptKind:
			var payload attempt
			if err := decodePayload(env, i, &payload); err != nil {
				return boardAssembly{}, err
			}
			attempts[payload.CandidateID] = &payload
		case verdictKind:
			var payload verdict
			if err := decodePayload(env, i, &payload); err != nil {
				return boardAssembly{}, err
			}
			verdicts[payload.TreeSHA] = &payload
		case runRecordKind:
			var payload runRecord
			if err := decodePayload(env, i, &payload); err != nil {
				return boardAssembly{}, err
			}
			runs = append(runs, payload)
		}
	}

	board.items = make([]itemRecord, 0, len(order))
	for _, id := range order {
		board.items = append(board.items, items[id])
	}
	board.items = rankOrdered(board.items)
	board.candidates = candidateStatuses(candidates, attempts, verdicts)
	board.runs = append([]runRecord{}, runs...)
	return board, nil
}

// rankBefore is the board's order, and it is one rule for both assemblies: rank
// ascending, an item carrying no rank after every ranked one, and item_id as the
// tiebreak so two reads of one moment answer the same order. The comparison is a plain
// string comparison because that is what the store's rank alphabet is ordered for —
// every character of it sorts in its own position — and it is the same comparison
// tillr-item makes, so the board and the queue cannot disagree about which item is first.
func rankBefore(rankA, itemA, rankB, itemB string) bool {
	if (rankA == "") != (rankB == "") {
		return rankB == ""
	}
	if rankA != rankB {
		return rankA < rankB
	}
	return itemA < itemB
}

// rankOrdered answers a list of items in the board's order.
func rankOrdered(items []itemRecord) []itemRecord {
	out := append([]itemRecord{}, items...)
	// A presentation stream (for example, Show output) already carries places
	// counted over a larger order. Preserve them, including gaps and a singleton.
	presented := false
	for _, it := range out {
		if it.Place > 0 {
			presented = true
			break
		}
	}
	if presented {
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if (a.Place == 0) != (b.Place == 0) {
				return b.Place == 0
			}
			if a.Place != b.Place {
				return a.Place < b.Place
			}
			return a.ItemID < b.ItemID
		})
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		return rankBefore(out[i].Rank, out[i].ItemID, out[j].Rank, out[j].ItemID)
	})
	place := 0
	for i := range out {
		out[i].Place = 0
		if inOrder(out[i].State) {
			place++
			out[i].Place = place
		}
	}
	return out
}

// decodePayload reads one record's payload into the local shape. A payload this
// utility cannot describe is malformed input: exit 2, nothing assembled.
func decodePayload(env record.Envelope, position int, into any) error {
	if err := json.Unmarshal(env.Payload, into); err != nil {
		return usageErrorf("record %d (%s): reading the payload: %v", position+1, env.Kind, err)
	}
	return nil
}
