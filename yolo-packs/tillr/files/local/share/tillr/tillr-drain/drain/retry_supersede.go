package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// candidateSupersededKind records that an operator's retry superseded a held candidate: the
// candidate it names stays pinned under its run ref (and is never pruned, refs.go), and its
// run's examination is over, so the item routes by the retry.
const candidateSupersededKind = "drain.candidate-superseded"

// supersededHeadSuffix names the extra pin of a lane HEAD that differs from the recorded
// candidate head and is not on the trunk, such as the loop's own document-retirement commit:
// refs/tillr/runs/<run>-superseded-head.
const supersededHeadSuffix = "-superseded-head"

func init() {
	if err := event.Register(candidateSupersededKind, Utility); err != nil {
		panic(err)
	}
}

// supersededByRetry lists the fault holds under which a held candidate cannot proceed on its
// own: clearing the fault re-examines the same recorded candidate, which answers the same
// fault again. On 2026-11-18 item-gate-pinned-fork-inputs re-gated a retained retirement
// candidate whose tree pinned a stale fork commit (gate-harness-error), and item-ig-view-nodes
// re-raised candidate-record-mismatch at once because the mismatch was in the record. On
// 2026-11-22 two finished fixes (4b533cbda5 and 5e6c7d8ca5) stalled on a gate script that a
// later trunk landing fixed: clear-fault cleared work.GateStall while keeping work.Candidate
// and re-gated the same head tree, and a recorded `tillr drain retry --acknowledge gate-failed`
// was accepted by AuthorizeRetry (runJudged saw the stalled gate.verdict) but skipped here
// because ReasonGateStalled was absent, so plan kept routing to the retained candidate before
// consulting retryFor. An operator's retry of the run is the release such a hold names.
var supersededByRetry = map[string]bool{
	StopGateHarnessError:    true,
	ReasonGateStalled:       true,
	ReasonCandidateMismatch: true,
	ReasonLandHarnessError:  true,
}

type candidateSuperseded struct {
	Candidate Candidate `json:"candidate"`
	RetryID   string    `json:"retry_id"`
	Fault     ItemFault `json:"fault"`
	// PinRef and PinSHA are the run pin the candidate is kept under; PinSHA is what the ref
	// names, which a record that disagrees with its own pin shows beside the candidate head.
	PinRef string `json:"pin_ref"`
	PinSHA string `json:"pin_sha"`
	// HeadRef pins the lane HEAD when it was neither the candidate nor on the trunk.
	HeadRef string `json:"head_ref,omitempty"`
	HeadSHA string `json:"head_sha,omitempty"`
	// LaneReset is the trunk commit the lane was reset to, or "" with LaneNote saying why the
	// lane was not touched.
	LaneReset string `json:"lane_reset,omitempty"`
	LaneNote  string `json:"lane_note,omitempty"`
}

type supersession struct {
	work  *pendingWork
	fault ItemFault
	retry retryRow
}

// supersedableCandidates answers the held candidates an operator's retry supersedes: the
// item's pending candidate is held by a fault in supersededByRetry for its own run, and the
// item's latest retry authorization names that run, which is still the item's latest
// finished run, under the same acknowledgment rule the plan honours (retryFor). Anything
// else is left to the hold's own release.
func supersedableCandidates(r *store.Reader) ([]supersession, error) {
	works, err := readPending(r)
	if err != nil {
		return nil, err
	}
	faults, err := ReadItemFaults(r)
	if err != nil {
		return nil, err
	}
	var found []supersession
	var retries map[string]retryRow
	for id, w := range works {
		f, held := faults[id]
		if w.Done || w.Decision.RunRequest == nil || w.Candidate == nil || !held || !supersededByRetry[f.Reason] || f.RunID != w.Decision.RunRequest.RunID {
			continue
		}
		if retries == nil {
			if retries, err = readRetries(r); err != nil {
				return nil, err
			}
		}
		row, ok := retries[id]
		if !ok || row.SupersedesRunID != f.RunID {
			continue
		}
		found = append(found, supersession{work: w, fault: f, retry: row})
	}
	if len(found) == 0 {
		return nil, nil
	}
	priors, err := readPriorRuns(r)
	if err != nil {
		return nil, err
	}
	classes, latest := indexPriorRuns(priors)
	conflicts, err := item.ReadConflicts(r)
	if err != nil {
		return nil, readFailure(err)
	}
	kept := found[:0]
	for _, s := range found {
		runID := s.fault.RunID
		if latest[s.fault.ItemID] != runID {
			continue
		}
		if c, ok := conflicts[s.fault.ItemID]; ok && c.Status == "answered" {
			continue
		}
		judged, err := runJudged(r, runID)
		if err != nil {
			return nil, err
		}
		if judged && s.retry.Acknowledge != RetryAckGateFailed {
			continue
		}
		if !judged && classes[runID] == AttemptUnjudged && s.retry.Acknowledge != RetryAckUnjudged {
			continue
		}
		kept = append(kept, s)
	}
	return kept, nil
}

// supersedeRetriedCandidates runs before planning. For each held candidate an operator's
// retry supersedes, it pins the candidate under its run ref, saves the lane's uncommitted work
// and resets the lane to the trunk when the lane is still this candidate's, then records the
// candidate superseded, ends its run's examination and clears its fault in one transaction.
// The plan then reads the retry as it reads any other: the item is dispatched fresh on the
// latest trunk. A pass with nothing to supersede writes nothing.
func (l *Loop) supersedeRetriedCandidates(ctx context.Context) error {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var found []supersession
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		var err error
		found, err = supersedableCandidates(tx)
		return err
	})
	r.Close()
	if err != nil || len(found) == 0 {
		return err
	}
	for _, s := range found {
		if err := l.supersedeCandidate(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) supersedeCandidate(ctx context.Context, s supersession) error {
	c := *s.work.Candidate
	runID := s.fault.RunID
	record := candidateSuperseded{Candidate: c, RetryID: s.retry.RetryID, Fault: s.fault, PinRef: pinRef(runID)}
	repo := l.cfg.Root
	if c.BaseRef != "" {
		repo = c.Worktree
		if s.work.LaneReleased {
			// A released held fork may no longer have its original checkout.
			// The separate held-candidate pin is portable across lanes.
			if err := l.pinnedCandidate(ctx, c); err == nil {
				repo, record.PinRef = l.cfg.Root, candidatePinRef(c)
			}
		}
	}
	pinned, err := l.git(ctx, repo, "for-each-ref", "--format=%(objectname)", record.PinRef)
	if repo != l.cfg.Root && (err != nil || strings.TrimSpace(pinned) == "") {
		// Older runs imported their fork pin into root. Read that pin if it
		// still exists (even if the original checkout is gone), never write it.
		if legacy, lookupErr := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(objectname)", record.PinRef); lookupErr == nil && strings.TrimSpace(legacy) != "" {
			pinned, err = legacy, nil
		}
	}
	if err != nil {
		return err
	}
	record.PinSHA = strings.TrimSpace(pinned)
	if record.PinSHA == "" {
		if _, err := l.git(ctx, repo, "cat-file", "-e", c.HeadSHA+"^{commit}"); err != nil {
			// Nothing may be superseded that is not kept: the candidate stays held, and the
			// report says what is missing.
			l.reportf("loop: retry %s cannot supersede %s: candidate head %s is not in this repository and no pin holds it\n", s.retry.RetryID, runID, c.HeadSHA)
			return nil
		}
		if err := writeRunPin(ctx, l.git, repo, runID, c.HeadSHA, false); err != nil {
			// This retry starts from an existing item fault. Keep that hold and
			// its mover; neither supersede the only copy nor stop other work.
			l.reportf("loop: retry %s cannot supersede %s: %v\n", s.retry.RetryID, runID, err)
			return nil
		}
		record.PinSHA = c.HeadSHA
	}
	lane := s.work.Decision.LaneID
	trunk, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return err
	}
	if !s.work.LaneReleased && lane != "" {
		path := l.lanePath(lane)
		if head, err := l.revParse(ctx, path, "HEAD"); err == nil && head != record.PinSHA && head != c.HeadSHA {
			if onTrunk, err := containsCommit(ctx, l.cfg.Root, head, trunk); err != nil {
				return err
			} else if !onTrunk {
				record.HeadRef, record.HeadSHA = record.PinRef+supersededHeadSuffix, head
				if _, err := l.git(ctx, l.cfg.Root, "update-ref", record.HeadRef, head); err != nil {
					return err
				}
			}
		}
		if err := l.preserveWork(ctx, lane, runID, "candidate superseded by retry "+s.retry.RetryID); err != nil {
			l.reportf("loop: retry %s cannot supersede %s yet: the lane's uncommitted work cannot be saved: %v\n", s.retry.RetryID, runID, err)
			return nil
		}
	}
	st, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	at := l.cfg.Now().UTC()
	applied := false
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		// Re-read under the writer: a fault cleared, a newer retry or a newer run since the
		// read means this supersession no longer speaks.
		current, err := supersedableCandidates(tx.Reader())
		if err != nil {
			return err
		}
		still := false
		for _, other := range current {
			if other.fault.RunID == runID && other.fault.Seq == s.fault.Seq && other.retry.RetryID == s.retry.RetryID {
				still = true
			}
		}
		if !still {
			return nil
		}
		if s.work.LaneReleased || lane == "" {
			record.LaneNote = "the candidate had already released its lane"
		} else if note, err := laneSupersedable(tx.Reader(), lane, runID); err != nil {
			return err
		} else if note != "" {
			record.LaneNote = note
		} else {
			if err := l.resetLaneToRegistryLocked(ctx, lane, trunk); err != nil {
				return err
			}
			record.LaneReset = trunk
			if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND (run_id IS NULL OR run_id=?)`, run.LaneStateIdle, lane, runID); err != nil {
				return err
			}
		}
		if err := appendEffect(tx, candidateSupersededKind, runID, record, at); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationDoneKind, runID, struct{}{}, at); err != nil {
			return err
		}
		itemID := s.fault.ItemID
		body := struct {
			ItemFault
			Why string `json:"why"`
		}{s.fault, "superseded by retry " + s.retry.RetryID}
		if err := appendEffectOn(tx, itemFaultClearedKind, "item", itemID, body, at); err != nil {
			return err
		}
		if err := supersedeItemUnfinishedRunsInTxAt(ctx, tx, s.fault.ItemID, s.work.Decision.Attempt, true, nil, "superseded by retry "+s.retry.RetryID, at); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil || !applied {
		return err
	}
	where := "lane " + lane + " reset to " + shortSHA(trunk)
	if record.LaneReset == "" {
		where = "lane " + lane + " untouched: " + record.LaneNote
	}
	kept := record.PinRef + " (" + shortSHA(record.PinSHA) + ")"
	if record.HeadRef != "" {
		kept += " and " + record.HeadRef + " (" + shortSHA(record.HeadSHA) + ")"
	}
	l.reportf("loop: retry %s supersedes %s's candidate %s (%s): kept at %s; %s; %s is dispatched fresh\n", s.retry.RetryID, runID, shortSHA(c.HeadSHA), s.fault.Reason, kept, where, s.fault.ItemID)
	return nil
}

// laneSupersedable answers "" when the superseded run's lane may be reset: it is idle or held
// by that run, and no other unfinished candidate records it. Otherwise it answers why the
// lane is left alone; the candidate is superseded either way, because its pin keeps it.
func laneSupersedable(r *store.Reader, lane, runID string) (string, error) {
	var state string
	var leased *string
	if err := r.QueryRow(`SELECT state,run_id FROM lanes WHERE lane_id=?`, lane).Scan(&state, &leased); err == nil {
		if leased != nil && *leased != runID {
			return fmt.Sprintf("lane %s is leased by run %s", lane, *leased), nil
		}
	}
	works, err := readPending(r)
	if err != nil {
		return "", err
	}
	for _, w := range works {
		if !w.Done && !w.LaneReleased && w.Decision.LaneID == lane && w.Decision.RunRequest != nil && w.Decision.RunRequest.RunID != runID {
			return fmt.Sprintf("lane %s holds run %s's candidate", lane, w.Decision.RunRequest.RunID), nil
		}
	}
	return "", nil
}

func appendEffectOn(tx *store.Tx, kind, objectType, id string, value any, at time.Time) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: objectType, ObjectID: &id, Payload: body, At: at})
	return err
}
