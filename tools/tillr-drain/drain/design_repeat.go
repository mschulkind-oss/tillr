package drain

import (
	"context"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// The design seat's identical repeat. The owner ruled on 2026-11-17 that a design run's
// missing output and its refusals go back to the design seat uncounted. Nothing counts
// them here either. What a count used to stop is stopped by the comparison the worker
// path uses (repeatedEnding): when a design run ends with the same cause and the same
// evidence as an earlier design run of the same item, the design seat would be sent the
// same thing again. Before this rule, a design run that named no next actor was dispatched
// again without end. One test spun for 59 minutes on it, and in production each round is
// a paid design run.
//
// The design seat is already the seat that diagnoses, so a repeat does not go to it a third
// time. The loop raises an objection naming the owner, with a question written from the
// two records (designRepeatQuestion). It is recorded the way a design run's own
// `NEXT-ACTOR: owner` objection is: the conflict is with the owner (the `conflict-owner`
// hold, "needs you"), and its text carries the question. The owner moves it with `tillr
// drain retry --run <the latest design run>` after changing what the design seat is given,
// or by closing the item.

const (
	// designCauseMissingActor is the ending of a design run that committed nothing and
	// named no next actor, whether its report ended blocked (reportedBlock) or the run was
	// refused as a no-op.
	designCauseMissingActor = "missing-actor"
	// designCauseBlockRemoved is the ending of a design run whose commit removed or
	// renamed the item's block.
	designCauseBlockRemoved = "block-removed"
)

// designEndingFacts are what one design run's ending says, as the repeat rule reads it:
// the refusal step or the design cause; the evidence key its refusal recorded, when one
// was recorded; the failing gate checks read from its record directory; and the conflict
// and item-block revision it answered.
type designEndingFacts struct {
	Step       string
	Evidence   string
	Checks     []EndingCheck
	ConflictID string
	Block      string
	RunID      string
}

// designEndingKey answers a design run's ending's cause and evidence key. The cause is
// "" for an ending that is not a statement by the design seat (an interruption, a killed
// or failed run, a backend refusal), and its key is "" too, so it never repeats anything.
//   - A missing actor and a removed block are keyed by the conflict and the block revision
//     the run was given: the same question, asked of the same block, answered the same way.
//   - A gate refusal is keyed by the evidence its refusal recorded, or else by the failing
//     checks and their output digests. A gate refusal with neither has no evidence and
//     never repeats.
//   - A fence escape and a refused landing are keyed by the evidence their refusal
//     recorded.
func designEndingKey(e designEndingFacts) (string, string) {
	switch e.Step {
	case designCauseMissingActor, RefusalNoOp:
		// Not normalizeEvidence: the block revision is a digest, which it strips.
		return designCauseMissingActor, designCauseMissingActor + ":conflict=" + e.ConflictID + ",block=" + e.Block
	case designCauseBlockRemoved:
		return designCauseBlockRemoved, designCauseBlockRemoved + ":conflict=" + e.ConflictID + ",block=" + e.Block
	case RefusalGate:
		if e.Evidence != "" {
			return RefusalGate, e.Evidence
		}
		if len(e.Checks) == 0 {
			return RefusalGate, ""
		}
		return RefusalGate, endingKey(RefusalGate, e.Checks, "", e.RunID)
	case RefusalFence, RefusalLand:
		return e.Step, e.Evidence
	}
	return "", ""
}

// designProducer is the producer every design-seat ending is recorded under. A design run
// is dispatched on whichever backend the seat list answers, so the producer is the seat.
const designProducer = "design"

// designRepeatOf answers the earlier design run whose ending latest repeats, among the
// item's recorded design endings. It is repeatedEnding over the design seat's records and
// holds no count.
func designRepeatOf(marks []designUnanswered, latest designUnanswered) (string, bool) {
	chain := make([]chainEnding, 0, len(marks))
	for _, m := range marks {
		chain = append(chain, chainEnding{RunID: m.RunID, Producer: designProducer, Cause: m.Cause, Key: m.Key})
	}
	return repeatedEnding(chain, chainEnding{RunID: latest.RunID, Producer: designProducer, Cause: latest.Cause, Key: latest.Key})
}

// designBlockRevision is the revision of the item's block the design seat is given now:
// the digest of the block's body on the trunk. An ending is settled right after its run,
// so this is the block the next design run would be given; the item's stored run inputs
// are not used, because a blocked item's design dispatch records none. A block that cannot
// be read leaves the ending keyed by its conflict alone.
func (l *Loop) designBlockRevision(ctx context.Context, c item.Conflict) string {
	file, _, _ := strings.Cut(c.BodyRef, "#")
	if file == "" {
		return ""
	}
	raw, err := l.git(ctx, l.cfg.Root, "show", l.trunkRef()+":"+file)
	if err != nil {
		return ""
	}
	return evidenceDigest(item.DefinitionBodyContent(c.BodyRef, c.ItemID, []byte(raw)), "")
}

// itemDesignMarks answers an item's drain.design-unanswered records, oldest first, across
// all of its conflicts.
func itemDesignMarks(marks map[string][]designUnanswered, itemID string) []designUnanswered {
	out := []designUnanswered{}
	for _, m := range marks {
		for _, mark := range m {
			if mark.ItemID == itemID {
				out = append(out, mark)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// designCauseText says what a design ending was, for the owner's question.
func designCauseText(m designUnanswered) string {
	switch m.Cause {
	case designCauseMissingActor:
		return "each committed nothing and named no next actor, given the same item block"
	case designCauseBlockRemoved:
		return "each committed a change that removed or renamed the item's block, given the same item block"
	case RefusalGate:
		if len(m.FailingChecks) > 0 {
			return "the gate refused each one's commit with the same failing checks (" + strings.Join(m.FailingChecks, ", ") + ") and the same output"
		}
		return "the gate refused each one's commit with the same output"
	case RefusalFence:
		return "each one's commit changed the same paths outside the item's fence"
	case RefusalLand:
		return "the lander refused each one's commit the same way"
	}
	return "each ended " + m.Outcome
}

// designRepeatQuestion is the objection the loop raises to the owner on a design-seat
// identical repeat: what the two records show, why the design seat is not dispatched a
// third time, and the commands that move the item.
func designRepeatQuestion(c item.Conflict, latest designUnanswered, earlier string) string {
	return "Question for the owner, raised by the loop on an identical repeat: design runs " + earlier + " and " + latest.RunID +
		" ended this conflict the same way: " + designCauseText(latest) + ". The design seat was given the same thing both times, so dispatching it again would give it the same thing a third time." +
		" What should the design seat be given that it was not? Change it (amend the item's block or its design document, or state it in the reason), then raise the conflict again with `tillr drain retry --item " + c.ItemID + " --run " + latest.RunID + " --by <you> --why <what changed>`. Or close the item with `tillr item close`.\n" +
		DesignNextActorField + ": owner"
}

// routeDesignRepeat moves a conflict to the owner on an identical repeat: the conflict
// names the latest design run, so `tillr drain retry --run` accepts it, and the mark
// records the repeat in place of a re-raise.
func (l *Loop) routeDesignRepeat(c *item.Conflict, mark *designUnanswered, earlier string) {
	c.Text += "\n\n" + designRepeatQuestion(*c, *mark, earlier)
	c.Status, c.WaitingOn, c.DesignRunID = "owner", "owner", mark.RunID
	mark.Reraised, mark.Repeat, mark.RepeatOf = false, true, earlier
	l.reportf("loop: %s conflict %s goes to the owner: design run %s ended exactly as design run %s did\n", c.ItemID, c.ConflictID, mark.RunID, earlier)
}

// readItemDesignMarks reads an item's design records in a read transaction, outside the
// write lock the routing then takes.
func (l *Loop) readItemDesignMarks(ctx context.Context, itemID string) ([]designUnanswered, error) {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []designUnanswered
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		marks, err := readDesignUnanswered(tx)
		out = itemDesignMarks(marks, itemID)
		return err
	})
	return out, err
}

// saveDesignEnding records a conflict the design seat's ending moves, and the ending's
// drain.design-unanswered record, in one transaction.
func (l *Loop) saveDesignEnding(ctx context.Context, c item.Conflict, mark designUnanswered) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
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
		at := l.cfg.Now().UTC()
		if err := item.RecordConflict(tx, c, at); err != nil {
			return err
		}
		return appendEffect(tx, designUnansweredKind, mark.RunID, mark, at)
	})
}

// settleDesignStatement routes a design run's ending that would otherwise go back to the
// design seat (raise): the design seat again, or the owner on an identical repeat.
func (l *Loop) settleDesignStatement(ctx context.Context, c item.Conflict, runID, cause, outcome string, raise func(*item.Conflict)) error {
	block := l.designBlockRevision(ctx, c)
	mark := designUnanswered{ConflictID: c.ConflictID, ItemID: c.ItemID, RunID: runID, Outcome: outcome, Changes: []string{}, Reraised: true}
	mark.Cause, mark.Key = designEndingKey(designEndingFacts{Step: cause, ConflictID: c.ConflictID, Block: block, RunID: runID})
	marks, err := l.readItemDesignMarks(ctx, c.ItemID)
	if err != nil {
		return err
	}
	if earlier, repeat := designRepeatOf(marks, mark); repeat {
		l.routeDesignRepeat(&c, &mark, earlier)
	} else {
		raise(&c)
	}
	return l.saveDesignEnding(ctx, c, mark)
}
