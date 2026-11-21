package drain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// A tooling obstacle names a defective tool: a hung suite, a broken adapter, a missing
// binary. The loop files a defect item for it, the way it files a trunk fixer for a red
// trunk, and the worker's item waits on that item with the tooling as its mover. The defect
// item is identified by the command it names, so a second report of the same command —
// from the same item or another — attaches to the one item and both wait on it.

const (
	// DefectItemPrefix is the item-id prefix of a generated tooling-defect item.
	DefectItemPrefix = "defect-"
	// DefectCriterion is the one criterion a defect item carries.
	DefectCriterion = "tooling-works"
	// RefusalToolingDefect is the `drain.refusal` step a tooling obstacle writes: the item
	// is acquitted of its run and held behind the defect item.
	RefusalToolingDefect = "tooling-defect"
	// ReasonToolingDefect is a hold's reason: the item waits on the defect item named in
	// DeferredBy.
	ReasonToolingDefect = "tooling-defect"
)

// DefectItemID answers the defect item for a command: the prefix and the first twelve
// hex digits of the digest of the command with its whitespace collapsed.
func DefectItemID(command string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(command), " ")))
	return fmt.Sprintf("%s%x", DefectItemPrefix, sum[:6])
}

// IsDefectItem answers whether an item is a generated tooling-defect item.
func IsDefectItem(itemID string) bool { return strings.HasPrefix(itemID, DefectItemPrefix) }

// IsGeneratedRepair answers whether the loop filed the item itself: a trunk fixer or a
// tooling-defect item. Neither has a design document, so neither has a block the design
// seat can amend.
func IsGeneratedRepair(itemID string) bool {
	return IsTrunkFixer(itemID) || IsDefectItem(itemID) || IsRemoteSyncItem(itemID)
}

// acquittedRefusal answers whether a refusal step holds its item behind generated repair
// work rather than against its own candidate. Such a run is not an attempt: the item
// starts fresh once the repair lands.
func acquittedRefusal(step string) bool {
	return step == RefusalTrunkAlreadyRed || step == RefusalToolingDefect
}

// defectEvidenceLimit bounds how much of a report's evidence a defect criterion quotes.
const defectEvidenceLimit = 2000

func clip(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + " [clipped]"
}

// defectCriterion is the defect item's one criterion: the command completes, and the
// failure its evidence shows no longer reproduces.
func defectCriterion(command, evidence, sourceItem string) string {
	return fmt.Sprintf("`%s` completes, and the failure below no longer reproduces. Evidence from the tooling obstacle on item %s:\n\n%s", command, sourceItem, clip(evidence, defectEvidenceLimit))
}

// fileDefectItem files the defect item a tooling obstacle names, or attaches the report to
// the open one for the same command: the fence widens to the newly named paths and the
// criterion keeps its first evidence. A landed defect item for the command is reopened by
// nothing; a second report against it files nothing and the item waits on it as landed,
// which the release rule then releases.
func (l *Loop) fileDefectItem(ctx context.Context, reporter string, o Obstacle) (string, error) {
	command := strings.Join(strings.Fields(o.Field("COMMAND")), " ")
	id := DefectItemID(command)
	paths := o.List("PATHS")
	sort.Strings(paths)
	payload, err := json.Marshal(map[string]any{
		"item_id":       id,
		"title":         "make `" + clip(command, 80) + "` work",
		"state":         StateInQueue,
		"blocked_by":    []string{},
		"rank":          TrunkFixerRank,
		"allowed_paths": paths,
		"seat":          TrunkFixerSeat,
		"criteria": []map[string]string{{
			"criterion_id": DefectCriterion,
			"text":         defectCriterion(command, o.Field("EVIDENCE"), reporter),
			"state":        "not-done",
		}},
	})
	if err != nil {
		return "", Internalf("encoding the defect item: %v", err)
	}
	env := record.Envelope{
		RecordVersion: 1,
		Kind:          item.RecordKind,
		ID:            "item-" + id,
		Producer:      record.Producer{System: "tillr", Utility: Utility, Version: Version},
		CreatedAt:     l.cfg.Now().UTC(),
		Payload:       payload,
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return "", Incompatiblef("%v", err)
	}
	defer s.Close()
	created := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var state, fence string
		scanErr := tx.QueryRow(`SELECT state, COALESCE(allowed_paths, '[]') FROM items WHERE item_id = ?`, id).Scan(&state, &fence)
		switch {
		case scanErr == nil:
			declared, err := definition.ForReader(tx.Reader())
			if err != nil {
				return err
			}
			if finishedState(declared.Parsed, state) {
				return nil
			}
			var old []string
			if err := json.Unmarshal([]byte(fence), &old); err != nil {
				return err
			}
			merged := TrunkFixerFence(old, paths)
			raw, err := json.Marshal(merged)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET allowed_paths=? WHERE item_id=?`, string(raw), id); err != nil {
				return err
			}
			return appendEffect(tx, FixerAttachedKind, id, map[string]any{"defect_item": id, "reporter": reporter, "allowed_paths": merged}, l.cfg.Now())
		case errors.Is(scanErr, sql.ErrNoRows):
		default:
			return readFailure(scanErr)
		}
		if _, err := item.Apply(ctx, tx, env); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return "", classifyWrite(err)
	}
	if created {
		l.reportf("loop: filed %s for the tooling defect %s reported (fence %s)\n", id, reporter, strings.Join(paths, ", "))
	}
	return id, nil
}

// holdBehindDefect records the tooling refusal and holds the item blocked on its defect
// item, with the tooling as mover.
func (l *Loop) holdBehindDefect(ctx context.Context, c Candidate, defectID, command string) error {
	if err := appendJudgment(ctx, l.cfg.Store, RefusalKind, "run", c.RunID, refusalPayload{
		Step: RefusalToolingDefect, ItemID: c.ItemID, LaneID: c.LaneID, TickID: c.TickID,
		DefectItem: defectID, Detail: "tooling defect: " + command,
	}, l.cfg.Now().UTC()); err != nil {
		return err
	}
	return l.cfg.Steps.ItemState(ctx, c.ItemID, c.RunID, StateBlocked, item.WaitingOn{Waiter: "item:" + defectID, Reason: RefusalToolingDefect + ": " + clip(command, 200)})
}

// IsGeneratedItem answers whether the loop or sync generated the item rather than anyone
// requesting it: a trunk fixer, a tooling-defect item, a gate-budget alarm an older build
// filed, a prose rewrite sync produced, or a revert's landing id. None has a block of its
// own a request is declared in, so none is ever triaged for a document, and none reaches
// the owner except by an explicit objection naming him. This is the one list of those
// prefixes; a caller that needs "generated" asks here.
func IsGeneratedItem(itemID string) bool {
	return IsGeneratedRepair(itemID) || IsGateBudgetItem(itemID) || IsRevertItem(itemID) ||
		item.ProseRewriteOriginal(itemID) != ""
}
