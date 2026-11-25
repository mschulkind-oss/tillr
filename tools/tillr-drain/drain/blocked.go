package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonConflictDesign   = "conflict-design"
	ReasonConflictOwner    = "conflict-owner"
	ReasonQuestionRemoved  = "question-removed"
	ReasonConflictAnswered = "conflict-answered"
)

// conflictDecision gives the design seat exactly one dispatch for a raised report.
// The pending-work record takes over after claim. A quota refusal raises the
// conflict again; other unanswered terminal attempts keep their disposition.
//
// The attempt is the planner's one count (nextAttempt), the same one the run id is made
// from. A count of prior runs alone skips a judged run, so it once named run a13 under
// attempt 12 and reused the a12 dispatch's decision id.
func (p *planner) conflictDecision(row itemRow, c item.Conflict) Decision {
	d := Decision{ItemID: row.ItemID, Seat: c.WaitingOn, ConflictID: c.ConflictID, Attempt: p.nextAttempt(row.ItemID), From: "held", Action: ActionHold, Reason: ReasonConflictDesign}
	switch {
	case c.Status == "question":
		_, d.QuestionID, _ = strings.Cut(c.QuestionRef, "#")
		d.Reason = ReasonQuestionOpen
		if c.QuestionRemovedHead != "" {
			d.Reason = ReasonQuestionRemoved
			d.Seat = "operator"
		}
	case c.WaitingOn == "owner" && strings.Contains(c.Text, repeatedObstacleMarker):
		d.Reason = ReasonConflictOwner
		d.Seat = repeatedObstacleActor(c)
	case c.WaitingOn == "owner" && p.tick.ownerMigration:
		// This is a real command run, not a fabricated terminal record or an
		// ungated store repair. Its report travels through the same settlement.
		if lane, ok := p.freeLane(row.Express); ok {
			p.assignLane(lane)
			d.Action, d.From, d.To, d.LaneID = ActionDispatch, "queued", "running", lane

			d.Seat, d.BlockConflict = "design", &c
			origin := c.DesignRunID
			if origin == "" {
				origin = c.RunID
			}
			report := "Original report from run `" + origin + "`:\n\n" + ownerReport(c) + "\nNEXT-ACTOR: owner\nRESULT: blocked\n"
			d.RunRequest = commandRequest(WorkflowStage{Command: []string{"sh", "-c", "printf '%s' \"$1\"", "owner-question", report}}, row.ItemID, lane, attemptRunID(row.ItemID, d.Attempt))
		} else {
			d.Reason = ReasonNoLane
		}
	case c.WaitingOn == "owner":
		d.Reason = ReasonConflictOwner
		// The design run that handed the conflict over is named beside it, so a view
		// says which run ended without an answer (design_unanswered.go).
		d.DeferredBy = c.DesignRunID
	case c.Status != "raised" && !p.awaitingTransportFailure(row.ItemID, c):
	case p.policy == nil:
		d.Reason = ReasonNoPolicy
	case len(p.policy.seatList("design")) == 0:
		d.Reason = ReasonNoPolicy
	case c.BodyRef == "" && !p.requests[row.ItemID]:
		d.Reason = ReasonConflictOwner
	default:
		// Reopening a failed design run is not a fresh seat choice.
		failedBackend := p.failedBackendFor(row.ItemID, c.DesignRunID)
		backend := failedBackend
		open := !p.shut[backend]
		if backend == "" {
			backend = p.policy.seatList("design")[0]
			open = p.policy.seatMayStart(row.ItemID, "design", backend, p.shut)
		}
		if !policyHasBackend(p.policy, backend) {
			d.Seat = "design"
			d.Reason, d.DeferredBy = backendMissingReason(d.Seat, backend), backend
			break
		}
		if !open {
			d.Reason = ReasonBackendWindow
			d.Seat, d.DeferredBy = "design", backend
			break
		}
		origin := designContinuationWork(c, p.designHistory[c.DesignRunID])
		interrupted := false
		if origin != nil {
			for _, prior := range p.priors[row.ItemID] {
				if prior.RunID == c.DesignRunID {
					interrupted = designInterrupted(origin, prior.Class, prior.EndCause)
					break
				}
			}
		}
		hold := func() {
			d.Reason = ReasonNoLane
			if interrupted {
				d.Reason, d.DeferredBy = ReasonConflictDesign, c.DesignRunID
			}
		}
		if p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches {
			hold()
			break
		}
		lane, ok := "", false
		if origin != nil {
			lane = origin.Decision.LaneID
			ok = !p.tick.OutLanes[lane] && (row.Express || !isExpressLane(lane)) && p.retainedLaneFreeFor(lane, row.ItemID)
			// A killed run can resume only on its old lane. If that lane is
			// unavailable, another eligible lane can start a fresh design session.
			if !ok && (isExpressLane(lane) || interrupted) {
				lane, ok = p.freeLane(row.Express)
			}
		} else {
			lane, ok = p.freeLane(row.Express)
		}
		if ok {
			p.assignLane(lane)
			d.Action, d.From, d.To, d.LaneID = ActionDispatch, "queued", "running", lane
			d.Seat = "design"
			d.SelectSeat = failedBackend == ""
			d.BlockConflict = &c
			d.RunRequest = p.request(row.ItemID, lane, backend)
			stampDecisionRequest(d, d.RunRequest)
		} else {
			hold()
		}
	}
	identifyDecision(&d)
	return d
}

func (p *planner) awaitingTransportFailure(itemID string, c item.Conflict) bool {
	if c.Status != "awaiting" || c.WaitingOn != "design" || c.DesignRunID == "" {
		return false
	}
	work := p.designHistory[c.DesignRunID]
	if work == nil || !work.Done {
		return false
	}
	for _, prior := range p.priors[itemID] {
		if prior.RunID == c.DesignRunID && prior.Finished && prior.TransportError {
			return true
		}
	}
	return false
}

func (p *planner) failedBackendFor(itemID, runID string) string {
	if runID != "" {
		if work := p.designHistory[runID]; work != nil && work.DesignQuotaBackend != "" {
			return work.DesignQuotaBackend
		}
		for _, prior := range p.priors[itemID] {
			if prior.RunID == runID && prior.BackendFailure {
				return prior.Backend
			}
		}
	}
	return ""
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
		// The actor is the value's first word; a reason may follow it ("owner — resolve
		// the obsolete fixer"), and is the worker's, not part of the actor.
		v := strings.ToLower(strings.TrimLeft(strings.TrimSpace(value), "*` "))
		if end := strings.IndexFunc(v, func(r rune) bool { return r < 'a' || r > 'z' }); end >= 0 {
			v = v[:end]
		}
		switch v {
		case DesignNextOwner, DesignNextBuild:
			actor = v
		}
	}
	return actor
}

// GeneratedRepairRule is what a generated repair item (a trunk fixer or a tooling-defect
// item) is told about its endings. It has no design document, so the design seat has no block
// to amend for it: an obstacle that needs a ruling or a wider fence reaches the owner with a
// question the loop writes from the obstacle (generatedQuestion). Any other report goes back
// to the item's own worker with the contract; it never reaches the owner by default.
const GeneratedRepairRule = "This item was generated by the loop or by sync and has no design block of its own. " + ObstacleContract + " For this item a `definition`, `owner` or `tooling` obstacle goes to the owner with the paths or question it names, because no design block exists to amend; a `seat` obstacle moves the item to a stronger backend. A report that is neither the commit nor a complete obstacle goes back to this item's worker."

// generatedQuestion is the owner's question for a generated repair item's obstacle, written
// from the obstacle's own fields so the owner reads one explicit ask.
func generatedQuestion(itemID string, o Obstacle) string {
	switch o.Kind {
	case ObstacleTooling:
		return "Question for the owner: generated item " + itemID + " cannot proceed because `" + o.Field("COMMAND") + "` is defective (paths " + strings.Join(o.List("PATHS"), ", ") + "). Evidence: " + o.Field("EVIDENCE") + " A repair item waiting on another repair item could not pass its own gate, so the loop files no defect item for it. Who repairs the tool?"
	case ObstacleDefinition:
		return "Question for the owner: generated item " + itemID + "'s criterion is wrong or contradicts the tree (" + o.Field("CRITERIA") + "): " + o.Field("EVIDENCE") + " Should the criterion be corrected, or the item closed?"
	case ObstacleOwner:
		return "Question for the owner from generated item " + itemID + ": " + o.Field("QUESTION")
	case ObstacleSeat:
		return "Question for the owner: generated item " + itemID + " exceeds every backend its seat escalates to. Tried: " + o.Field("TRIED") + " Missing: " + o.Field("MISSING") + " Who takes it?"
	}
	return ""
}

func (l *Loop) saveBlockConflict(ctx context.Context, c item.Conflict) error {
	return l.saveBlockConflictWithQuota(ctx, c, "")
}

func (l *Loop) saveBlockConflictWithQuota(ctx context.Context, c item.Conflict, backend string) error {
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
		hasLanded, err := obstacles.HasLanded(tx.Reader(), c.ItemID)
		if err != nil {
			return err
		}
		if hasLanded {
			c.Text = obstacles.StripCloseRemedy(c.Text)
		}
		if err := item.RecordConflict(tx, c, l.cfg.Now().UTC()); err != nil {
			return err
		}
		if backend != "" {
			if err := appendEffect(tx, designQuotaRefusedKind, c.DesignRunID, backend, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		if c.Status == "answered" && isObstacleCallRun(tx.Reader(), c.RunID) {
			return l.answerLandingRefusal(tx, Candidate{RunID: c.RunID}, refusalPayload{Step: IdenticalRepeatPrefix, Detail: c.Text}, refusalEvidence{}, l.cfg.Now().UTC())
		}
		return nil
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
				if c.DesignRunID == "" {
					c.DesignRunID = d.RunRequest.RunID
				}
				c.Status, c.WaitingOn, c.AnswerDetail = "answered", "build", text
				l.reportf("loop: %s conflict %s goes back to its worker: design run %s says the block stands as written\n", c.ItemID, c.ConflictID, d.RunRequest.RunID)
			case designMissingActor:
				// No commit and no actor is a missing output: the design seat states its
				// actor, uncounted, unless this is an identical repeat of an earlier design
				// ending (design_repeat.go). The owner is reached only by an objection naming
				// the owner, the loop's own on a repeat.
				runID := d.RunRequest.RunID
				return l.settleDesignStatement(ctx, c, runID, designCauseMissingActor, "named no next actor", func(c *item.Conflict) {
					c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
					c.Text += "\n\nDesign run " + runID + " committed nothing and named no next actor. A design run either commits its answer, or objects with a report line `" + DesignNextActorField + ": owner` (a ruling only the owner can make) or `" + DesignNextActorField + ": build` (the block stands as written and the worker builds it). The design seat is dispatched again to state it."
					l.reportf("loop: %s conflict %s is raised again for the design seat: design run %s named no next actor\n", c.ItemID, c.ConflictID, runID)
				})
			}
		}
		return l.saveBlockConflict(ctx, c)
	}
	c := item.Conflict{ConflictID: "conflict-" + d.RunRequest.RunID, ItemID: d.ItemID, RunID: d.RunRequest.RunID, Text: text, Status: "raised", WaitingOn: "design", BlockedCriteria: []string{}}
	var isRequest bool
	var previous item.Conflict
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		if err := tx.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id = ?`, d.ItemID).Scan(&c.BodyRef); err != nil {
			return err
		}
		var reqErr error
		if isRequest, reqErr = isFiledRequest(tx, d.ItemID); reqErr != nil {
			return reqErr
		}
		if !IsGeneratedItem(d.ItemID) {
			var raw []byte
			if err := tx.QueryRow(`SELECT COALESCE((SELECT payload FROM events WHERE kind=? AND object_type='item' AND object_id=? ORDER BY seq DESC LIMIT 1), '{}')`, item.ConflictKind, d.ItemID).Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &previous); err != nil {
				return err
			}
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
	obstacle := parseObstacle(text)
	if !IsGeneratedItem(d.ItemID) && obstacle.Problem == "" && obstacle.Kind == ObstacleDefinition {
		if resolved, err := l.resolveDefinitionAlreadyFixed(ctx, d, obstacle); err != nil {
			return err
		} else if resolved {
			return nil
		}
	}
	if IsGeneratedItem(d.ItemID) {
		// A generated item has no design block of its own to amend, so an obstacle that needs a
		// ruling or a wider fence reaches the owner with the question the loop writes from
		// it. A legacy `NEXT-ACTOR: owner` objection is still read. Any other report sends
		// the item back to its own worker with the contract.
		if question := generatedQuestion(d.ItemID, obstacle); obstacle.Problem == "" && question != "" {
			c.WaitingOn, c.Status = "owner", "owner"
			c.Text += "\n\n" + question
		} else if designNextActor(text) == DesignNextOwner {
			c.WaitingOn, c.Status = "owner", "owner"
		} else if earlier, key, err := l.generatedBlockRepeat(ctx, d.ItemID, d.RunRequest.RunID, obstacle); err != nil {
			return err
		} else if earlier != "" {
			// The identical repeat: the same item ended blocked on the same trunk with the
			// same obstacle (or none) as an earlier run, whatever its words. Its worker would
			// be sent the same tree again, so the loop objects to the owner itself.
			c.WaitingOn, c.Status = "owner", "owner"
			c.Text += "\n\n" + generatedRepeatQuestion(d.ItemID, earlier, d.RunRequest.RunID, key)
			l.reportf("loop: %s ended blocked as run %s did (%s): an identical repeat, so it goes to the owner\n", d.ItemID, earlier, key)
		} else {
			c.WaitingOn, c.Status = "build", "answered"
			c.Text += "\n\n" + GeneratedRepairRule
		}
	} else if repeatedAnsweredObstacle(previous, c, obstacle) {
		// The store's person hold is named owner; the planner distinguishes the operator.
		c.Status, c.WaitingOn = "owner", "owner"
		actor := repeatedObstacleActor(c)
		answerRef := previous.AnswerHead
		if answerRef == "" {
			answerRef = previous.DesignRunID
		}
		c.Text += repeatedObstacleMarker + " (run " + previous.RunID + ", answer " + answerRef + "):\n" + workerConflictReport(previous.Text) + "\n\nHeld for the " + actor + ": the next worker reported the same obstacle after the design answer."
	} else if isRequest && obstacle.Problem == "" && obstacle.Kind == ObstacleDefinition && l.cfg.Policy != nil && len(l.cfg.Policy.seatList("design")) > 0 {
		// A filed request has no item block, so a design-overturning definition obstacle
		// goes to the design seat to answer in docs/ without adding one
		// (requestObstacleDesignInstruction). When policy maps no design seat, fall through
		// to the recorded obstacle hold rather than faulting the loop on no-policy.
		if instruction := requestObstacleDesignInstruction(obstacle); instruction != "" {
			c.Text += "\n\n" + instruction
		}
	} else if c.BodyRef == "" {
		if isRequest && obstacle.Kind != "" && obstacle.Problem == "" {
			return l.recordObstacleHold(ctx, d, obstacle)
		}
		c.WaitingOn, c.Status = "owner", "owner"
		if isRequest {
			c.Text += "\n\nDeliver the answer in the next run's brief with: " + retryCommand(d.ItemID, d.RunRequest.RunID, "")
		}
	} else if instruction := obstacleDesignInstruction(obstacle); obstacle.Problem == "" && instruction != "" {
		c.Text += "\n\n" + instruction
	}
	// Legacy conflict holds use the first line as their reason. For an
	// extracted report, lead with the finding, not the adapter's heading.
	if strings.HasPrefix(c.Text, "# run report\n") {
		if _, body, wrapped := strings.Cut(c.Text, "\n---\n"); wrapped {
			c.Text = strings.TrimSpace(body)
			for _, field := range []string{"QUESTION", "EVIDENCE", "MISSING", "CHECKS"} {
				if finding := obstacle.Field(field); finding != "" {
					c.Text = finding + "\n\n" + c.Text
					break
				}
			}
		}
	}
	l.reportf("loop: %s conflict %s waits on %s: %s\n", c.ItemID, c.ConflictID, c.WaitingOn, c.Text)
	if err := l.saveBlockConflict(ctx, c); err != nil {
		return err
	}
	if IsRemoteSyncItem(c.ItemID) && c.Status == "owner" {
		who, reason := remoteSyncObstacleMover(obstacle.Kind)
		l.escalateRemoteSyncPushState(ctx, c.ItemID, who, reason)
	}
	return nil
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
		retired, err := l.retiredOriginal(ctx, conflictFixOriginal(c.RunID))
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
	return l.recordBlockAnswerThen(ctx, candidate, nil)
}

// recordBlockAnswerThen records the answer and, when it is recorded, runs then in the
// same transaction (a held answer's fault is cleared with its answer).
func (l *Loop) recordBlockAnswerThen(ctx context.Context, candidate Candidate, then func(*store.Tx) error) error {
	c := *candidate.BlockConflict
	blockID := c.ItemID
	if original := item.ProseRewriteOriginal(c.ItemID); original != "" {
		blockID = original
	}
	file, _, _ := strings.Cut(c.BodyRef, "#")
	// A filed request has no item block (validateRequest refused any touch of one before the
	// gate), so there is no document to read or sync for its answer.
	var raw string
	if file != "" {
		var err error
		if raw, err = l.git(ctx, l.cfg.Root, "show", TrunkRef+":"+file); err != nil {
			return err
		}
	}
	var answerDetail string
	if c.QuestionRef == "" {
		answerDetail = l.designAnswerSummary(ctx, candidate, file)
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
		if file != "" {
			if err := l.syncAnswerBlock(ctx, tx, c, blockID, file, raw); err != nil {
				return err
			}
		}
		if c.QuestionRef != "" {
			c.QuestionHead, c.AnswerHead, c.AnswerDetail = candidate.HeadSHA, "", ""
			c.Status, c.WaitingOn = "question", "owner"
		} else {
			c.Status, c.WaitingOn, c.AnswerHead, c.AnswerDetail = "answered", "build", candidate.HeadSHA, answerDetail
		}
		if err := item.RecordConflict(tx, c, l.cfg.Now().UTC()); err != nil {
			return err
		}
		if c.Status == "answered" {
			var offeredCall bool
			if err := tx.Reader().QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?)`, callOffered, c.RunID).Scan(&offeredCall); err != nil {
				return err
			}
			if offeredCall && (strings.HasPrefix(c.Text, IdenticalRepeatPrefix) || isObstacleCallRun(tx.Reader(), c.RunID)) {
				design := candidate
				design.RunID = c.RunID
				detail := fmt.Sprintf("Design answer commit: %s", candidate.HeadSHA)
				if c.AnswerDetail != "" {
					detail += "\n" + c.AnswerDetail
				}
				if file != "" {
					detail += "\n" + item.DefinitionBodyContent(c.BodyRef, blockID, []byte(raw))
				}
				if err := l.answerLandingRefusal(tx, design, refusalPayload{Step: IdenticalRepeatPrefix, Detail: detail}, refusalEvidence{}, l.cfg.Now().UTC()); err != nil {
					return err
				}
			}
		}
		if then != nil {
			return then(tx)
		}
		return nil
	})
}

var (
	markdownHeadingRe = regexp.MustCompile(`^(#{1,6}\s+.+?)\s*$`)
	diffHunkStartRe   = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
)

func (l *Loop) designAnswerSummary(ctx context.Context, candidate Candidate, file string) string {
	if l.cfg.Root == "" {
		if file != "" && candidate.BlockConflict != nil {
			return fmt.Sprintf("Updated %s", candidate.BlockConflict.BodyRef)
		}
		return ""
	}
	base, head := candidate.BaseSHA, candidate.HeadSHA
	ref := head
	var pathsOut string
	var err error
	if base != "" && head != "" {
		if base == head {
			if file != "" && candidate.BlockConflict != nil {
				return fmt.Sprintf("Updated %s", candidate.BlockConflict.BodyRef)
			}
			return ""
		}
		pathsOut, err = l.git(ctx, l.cfg.Root, "diff-tree", "--no-commit-id", "--name-only", "-r", base, head)
	}
	if err != nil || strings.TrimSpace(pathsOut) == "" {
		base = ""
		if head != "" {
			pathsOut, err = l.git(ctx, l.cfg.Root, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", head)
		}
		if err != nil || strings.TrimSpace(pathsOut) == "" {
			ref = TrunkRef
			pathsOut, err = l.git(ctx, l.cfg.Root, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", TrunkRef)
		}
	}
	var paths []string
	for _, line := range strings.Split(pathsOut, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	var mdRecords, otherRecords []string
	for _, p := range paths {
		if strings.HasSuffix(strings.ToLower(p), ".md") {
			if heading := l.designRecordHeading(ctx, base, ref, p); heading != "" {
				mdRecords = append(mdRecords, fmt.Sprintf("- `%s` — %s", p, heading))
			} else {
				mdRecords = append(mdRecords, fmt.Sprintf("- `%s`", p))
			}
		} else {
			otherRecords = append(otherRecords, fmt.Sprintf("- `%s`", p))
		}
	}
	records := mdRecords
	if len(records) == 0 {
		records = otherRecords
	}
	if len(records) > 0 {
		return "Landed design record(s):\n" + strings.Join(records, "\n")
	}
	if file != "" && candidate.BlockConflict != nil {
		return fmt.Sprintf("Updated %s", candidate.BlockConflict.BodyRef)
	}
	return ""
}

func (l *Loop) designRecordHeading(ctx context.Context, base, ref, path string) string {
	var diffOut string
	var err error
	if base != "" {
		diffOut, err = l.git(ctx, l.cfg.Root, "diff", "-U0", base, ref, "--", path)
	} else {
		diffOut, err = l.git(ctx, l.cfg.Root, "show", "--format=", "-U0", ref, "--", path)
	}
	firstLine := 0
	if err == nil && diffOut != "" {
		for _, line := range strings.Split(diffOut, "\n") {
			if firstLine == 0 {
				if m := diffHunkStartRe.FindStringSubmatch(line); len(m) == 2 {
					if n, convErr := strconv.Atoi(m[1]); convErr == nil {
						firstLine = n
					}
				}
			}
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				if m := markdownHeadingRe.FindStringSubmatch(line[1:]); len(m) == 2 {
					return m[1]
				}
			}
		}
	}
	raw, err := l.git(ctx, l.cfg.Root, "show", ref+":"+path)
	if err != nil {
		raw, err = l.git(ctx, l.cfg.Root, "show", TrunkRef+":"+path)
	}
	if err != nil || raw == "" {
		return ""
	}
	return headingForLine(raw, firstLine)
}

func headingForLine(content string, lineNum int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if lineNum > 0 {
		idx := lineNum - 1
		if idx >= len(lines) {
			idx = len(lines) - 1
		}
		for i := idx; i >= 0; i-- {
			if m := markdownHeadingRe.FindStringSubmatch(lines[i]); len(m) == 2 {
				return m[1]
			}
		}
	}
	for _, line := range lines {
		if m := markdownHeadingRe.FindStringSubmatch(line); len(m) == 2 {
			return m[1]
		}
	}
	return ""
}

// syncAnswerBlock syncs the landed document and refuses an answer whose document does not
// declare the item as the trunk now holds it.
func (l *Loop) syncAnswerBlock(ctx context.Context, tx *store.Tx, c item.Conflict, blockID, file, raw string) error {
	report, err := item.Sync(ctx, tx.Reader(), tx, item.SyncInput{Root: l.cfg.Root, Partial: true, Documents: []item.SyncDocument{{Path: file, Content: []byte(raw)}}, At: l.cfg.Now().UTC()})
	if err != nil {
		return err
	}
	// A finding skips the whole file, so it fails this answer whichever item it names;
	// a frozen definition is that item's alone, and another item's frozen block in the
	// same document is no reason to refuse this one (item-heavy-revert-time-to-verdict
	// was held on 2026-11-18 for a sibling's frozen block).
	frozen := false
	for _, n := range report.Frozen {
		if n.ItemID == blockID {
			frozen = true
		}
	}
	if len(report.Findings) != 0 || frozen {
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
	return nil
}

func isObstacleCallRun(r *store.Reader, runID string) bool {
	var ok bool
	_ = r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=? AND json_extract(payload,'$.tool')='obstacle')`, callOffered, runID).Scan(&ok)
	return ok
}

func mustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
