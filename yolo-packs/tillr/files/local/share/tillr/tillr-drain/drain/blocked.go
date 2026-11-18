package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonConflictDesign   = "conflict-design"
	ReasonConflictOwner    = "conflict-owner"
	ReasonConflictAnswered = "conflict-answered"
)

// conflictDecision gives the design seat exactly one dispatch for a raised report.
// The pending-work record takes over after claim. A quota refusal raises the
// conflict again; other unanswered terminal attempts keep their disposition.
func (p *planner) conflictDecision(row itemRow, c item.Conflict) Decision {
	d := Decision{ItemID: row.ItemID, Seat: c.WaitingOn, ConflictID: c.ConflictID, Attempt: len(p.priors[row.ItemID]) + 1, From: "held", Action: ActionHold, Reason: ReasonConflictDesign}
	switch {
	case c.WaitingOn == "owner":
		d.Reason = ReasonConflictOwner
		// The design run that handed the conflict over is named beside it, so a view
		// says which run ended without an answer (design_unanswered.go).
		d.DeferredBy = c.DesignRunID
	case c.Status != "raised":
	case p.policy == nil:
		d.Reason = ReasonNoPolicy
	case len(p.policy.seatList("design")) == 0:
		d.Reason = ReasonNoPolicy
	case c.BodyRef == "":
		d.Reason = ReasonConflictOwner
	case p.fixerOpen != "":
		d.Reason = ReasonTrunkBroken
		d.DeferredBy = p.fixerOpen
	default:
		// Unlike ordinary failover, a single design backend must not spill
		// onto the build preference. Only this seat's declared backends qualify.
		backend := ""
		for _, name := range p.policy.seatList("design") {
			if !p.shut[name] {
				backend = name
				break
			}
		}
		if backend == "" {
			d.Reason = ReasonBackendWindow
			break
		}
		if p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches {
			d.Reason = ReasonNoLane
			break
		}
		if lane, ok := p.freeLane(row.Express); ok {
			p.assigned[lane] = true
			p.dispatched++
			d.Action, d.From, d.To, d.LaneID = ActionDispatch, "queued", "running", lane
			d.Seat = "design"
			d.SelectSeat = true
			d.BlockConflict = &c
			d.RunRequest = p.request(row.ItemID, lane, backend)
		} else {
			d.Reason = ReasonNoLane
		}
	}
	identifyDecision(&d)
	return d
}

// DesignNextActorField is the report line by which a design run that commits nothing names
// who acts next on its conflict: an objection, in the owner's ruling of 2026-11-17. The
// line is `NEXT-ACTOR: <actor>`, its last occurrence wins, and two actors are read:
//
//   - DesignNextOwner: a ruling only the owner can make. The conflict goes to the owner.
//   - DesignNextBuild: the block stands as written and the worker was wrong. The conflict is
//     answered and the item goes back to its worker.
//
// A report that names no actor is a missing output, not an objection: it goes back to the
// design seat to state its actor, and never to the owner by default. Any other value names
// no actor.
const (
	DesignNextActorField = "NEXT-ACTOR"
	DesignNextOwner      = "owner"
	DesignNextBuild      = "build"
)

// designReading is what a design run's ending means for its conflict, read from the run's
// pinned evidence alone (designReadingOf). Each is one route: an amendment is gated and
// landed, an objection goes to the actor it names, and a removed block or a missing actor
// goes back to the design seat.
type designReading string

const (
	designAmended        designReading = "amended"
	designObjectionBuild designReading = "objection-to-build"
	designObjectionOwner designReading = "objection-to-owner"
	designBlockRemoved   designReading = "block-removed"
	designMissingActor   designReading = "missing-actor"
)

// designReadingOf answers what a non-triage design run's ending means: whether it left a
// commit, whether that commit still holds the item block, and the next actor its report
// names. A commit is the answer unless it removed the block; a run with no commit is an
// objection only when it names its actor.
func designReadingOf(committed, blockExists bool, actor string) designReading {
	switch {
	case committed && blockExists:
		return designAmended
	case committed:
		return designBlockRemoved
	case actor == DesignNextBuild:
		return designObjectionBuild
	case actor == DesignNextOwner:
		return designObjectionOwner
	}
	return designMissingActor
}

// designNextActor answers the next actor a design run's report names, or "".
func designNextActor(log string) string {
	actor := ""
	for _, line := range strings.Split(log, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(strings.Trim(name, "*`")), DesignNextActorField) {
			continue
		}
		switch v := strings.ToLower(strings.Trim(strings.TrimSpace(value), "*`")); v {
		case DesignNextOwner, DesignNextBuild:
			actor = v
		}
	}
	return actor
}

func (l *Loop) saveBlockConflict(ctx context.Context, c item.Conflict) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// Recording a conflict moves the item to blocked, which item refuses for a
		// terminal item. Such an item gets no conflict: its run is settled unjudged.
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "blocked-report"); err != nil {
			return err
		}
		return item.RecordConflict(tx, c, l.cfg.Now().UTC())
	})
}

func (l *Loop) reportedBlock(ctx context.Context, d Decision, log string) error {
	// Settlement supplies this run's pinned extracted report, or its legacy
	// plain-text log. Never read the reusable lane's report from here.
	text := strings.TrimSpace(log)
	if d.BlockConflict != nil {
		c := *d.BlockConflict
		c.Text += "\n\nDesign run " + d.RunRequest.RunID + ":\n" + text
		if c.TriageVersion != "" {
			c.DesignRunID = d.RunRequest.RunID
		}
		c.Status, c.WaitingOn = "owner", "owner"
		if c.TriageVersion == "" {
			switch designReadingOf(false, true, designNextActor(text)) {
			case designObjectionBuild:
				// The design seat says the block stands as written: the worker builds it,
				// with no owner act and no design commit.
				c.Status, c.WaitingOn = "answered", "build"
				l.reportf("loop: %s conflict %s goes back to its worker: design run %s says the block stands as written\n", c.ItemID, c.ConflictID, d.RunRequest.RunID)
			case designMissingActor:
				// No commit and no actor is a missing output: the design seat states its
				// actor. The owner is reached only by an objection naming the owner.
				c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
				c.Text += "\n\nDesign run " + d.RunRequest.RunID + " committed nothing and named no next actor. A design run either commits its answer, or objects with a report line `" + DesignNextActorField + ": owner` (a ruling only the owner can make) or `" + DesignNextActorField + ": build` (the block stands as written and the worker builds it). The design seat is dispatched again to state it."
				l.reportf("loop: %s conflict %s is raised again for the design seat: design run %s named no next actor\n", c.ItemID, c.ConflictID, d.RunRequest.RunID)
			}
		}
		return l.saveBlockConflict(ctx, c)
	}
	c := item.Conflict{ConflictID: "conflict-" + d.RunRequest.RunID, ItemID: d.ItemID, RunID: d.RunRequest.RunID, Text: text, Status: "raised", WaitingOn: "design", BlockedCriteria: []string{}}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		if err := tx.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id = ?`, d.ItemID).Scan(&c.BodyRef); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT criterion_id FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`, d.ItemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`).MatchString(text) {
				c.BlockedCriteria = append(c.BlockedCriteria, id)
			}
		}
		return rows.Err()
	})
	r.Close()
	if err != nil {
		return err
	}
	// A report that names no criterion is not permission to guess which one it
	// meant. Keep that absence explicit alongside the full report for the designer.
	if len(c.BlockedCriteria) == 0 {
		c.BlockedCriteria = []string{"not stated in report"}
	}
	if c.BodyRef == "" || IsTrunkFixer(d.ItemID) {
		// A generated repair has no design decision to dispatch. In particular a
		// fence refusal goes straight to the owner with the needed paths in the
		// pinned report, rather than waiting behind its own broken-trunk hold.
		c.WaitingOn, c.Status = "owner", "owner"
	}
	l.reportf("loop: %s conflict %s waits on %s: %s\n", c.ItemID, c.ConflictID, c.WaitingOn, c.Text)
	return l.saveBlockConflict(ctx, c)
}

func (l *Loop) blockReference(ctx context.Context, id string) (string, error) {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer r.Close()
	var ref string
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		return tx.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id = ?`, id).Scan(&ref)
	})
	return ref, err
}

// definitionChanged compares the pinned blobs, not the lane's mutable filesystem.
// Unrelated sections of the same document remain outside the protected block.
func (l *Loop) definitionChanged(ctx context.Context, c Candidate, ref string) (bool, error) {
	file, _, _ := strings.Cut(ref, "#")
	if file == "" {
		return false, nil
	}
	if filepath.IsAbs(file) || strings.HasPrefix(filepath.Clean(file), "..") {
		return false, Usagef("item block %s is not repository-relative", ref)
	}
	bodies, raws := []string{}, []string{}
	for _, sha := range []string{c.BaseSHA, c.HeadSHA} {
		// A deleted/new blob is an explicit input; other git failures are errors.
		files, err := l.git(ctx, c.Worktree, "ls-tree", "--name-only", sha, "--", file)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(files) == "" {
			bodies, raws = append(bodies, "document missing"), append(raws, "")
			continue
		}
		raw, err := l.git(ctx, c.Worktree, "show", sha+":"+file)
		if err != nil {
			return false, err
		}
		bodies, raws = append(bodies, item.DefinitionBodyContent(ref, c.ItemID, []byte(raw))), append(raws, raw)
	}
	if bodies[0] == bodies[1] {
		return false, nil
	}
	// A conflict-fix run of a candidate the loop retired may carry that retirement: the
	// prepared head it re-applies holds the loop's own commit removing this block. That
	// removal is the loop's, not the worker's, so the head is compared against exactly what
	// the loop's retirement makes of this base — never excused as any edit to the block.
	if raws[0] != "" && raws[1] != "" && isConflictFixRun(c.RunID) {
		retired, err := l.retiredOriginal(ctx, strings.TrimSuffix(c.RunID, ConflictFixSuffix))
		if err != nil {
			return false, err
		}
		if retired {
			if text, found := item.RetireItemBlock(raws[0], c.ItemID); found && item.DefinitionBodyContent(ref, c.ItemID, []byte(text)) == bodies[1] {
				return false, nil
			}
		}
	}
	return true, nil
}

// answerDefinitionError is a landed answer whose document does not declare its item as
// the trunk now holds it: that one item's condition, held as its fault, never the loop's.
type answerDefinitionError struct{ detail string }

func (e *answerDefinitionError) Error() string { return e.detail }

// answerBlock records a landed design answer: the trunk's document is synced and the
// conflict answered for the build seat. A prose-rewrite item is generated, never declared,
// and its block is its original item's (item.ProseRewriteOriginal), so the document is
// read for that id. A document that does not declare the item holds only that item
// (definition-sync-failed, whose mover corrects the definition): on 2026-11-17 the check
// read a rewrite item's own id, failed, and stopped the loop twice.
func (l *Loop) answerBlock(ctx context.Context, candidate Candidate) error {
	err := l.recordBlockAnswer(ctx, candidate)
	var held *answerDefinitionError
	if errors.As(err, &held) {
		return l.holdItemFault(ctx, candidate, ReasonDefinitionSyncFailed, held.detail)
	}
	return err
}

func (l *Loop) recordBlockAnswer(ctx context.Context, candidate Candidate) error {
	c := *candidate.BlockConflict
	blockID := c.ItemID
	if original := item.ProseRewriteOriginal(c.ItemID); original != "" {
		blockID = original
	}
	file, _, _ := strings.Cut(c.BodyRef, "#")
	raw, err := l.git(ctx, l.cfg.Root, "show", TrunkRef+":"+file)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// An answered conflict returns its item to the queue; a terminal item stays put.
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "conflict-answer"); err != nil {
			return err
		}
		report, err := item.Sync(ctx, tx.Reader(), tx, item.SyncInput{Root: l.cfg.Root, Partial: true, Documents: []item.SyncDocument{{Path: file, Content: []byte(raw)}}, At: l.cfg.Now().UTC()})
		if err != nil {
			return err
		}
		if len(report.Findings) != 0 || len(report.Frozen) != 0 {
			return &answerDefinitionError{detail: fmt.Sprintf("landed conflict answer %s could not sync its definition: %s", c.ConflictID, mustJSON(report))}
		}
		found := false
		for _, ids := range [][]string{report.Created, report.Updated, report.Unchanged} {
			for _, id := range ids {
				if id == blockID {
					found = true
				}
			}
		}
		if !found {
			return &answerDefinitionError{detail: fmt.Sprintf("landed conflict answer %s does not declare item %s in %s", c.ConflictID, blockID, file)}
		}
		c.Status, c.WaitingOn, c.AnswerHead = "answered", "build", candidate.HeadSHA
		return item.RecordConflict(tx, c, l.cfg.Now().UTC())
	})
}

func mustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
