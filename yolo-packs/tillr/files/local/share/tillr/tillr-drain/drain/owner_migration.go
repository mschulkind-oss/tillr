package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// ownerReport is the latest design statement, not the worker report it answered.
func ownerReport(c item.Conflict) string {
	report := c.Text
	if i := strings.LastIndex(report, "\n\nDesign run "); i >= 0 {
		if _, tail, ok := strings.Cut(report[i+2:], ":\n"); ok {
			return tail
		}
	}
	return report
}

// migrateOwnerQuestions spends no model run. Each eligible report is replayed by a
// command run with an ordinary lane claim, settlement, gate, and landing. Skipped
// reports stay conflicts with a named reason; converted reports are answered by
// answerBlock only after publication.
func (l *Loop) migrateOwnerQuestions(ctx context.Context, result *LoopResult) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var conflicts map[string]item.Conflict
	err = s.ReadTx(ctx, func(r *store.Reader) error { var e error; conflicts, e = item.ReadConflicts(r); return e })
	s.Close()
	if err != nil {
		return err
	}
	ids := []string{}
	for id, c := range conflicts {
		if c.WaitingOn == "owner" && c.Status == "owner" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	defer func() { l.ownerMigrationItem = "" }()
	for _, id := range ids {
		c := conflicts[id]
		if _, ok := item.OwnerDecision(ownerReport(c)); !ok {
			l.reportf("migration: %s unchanged: report states no question with alternatives (a stale base is a staging fault, not an owner choice)\n", id)
			continue
		}
		file, _, _ := strings.Cut(c.BodyRef, "#")
		blockID := id
		if original := item.ProseRewriteOriginal(id); original != "" {
			blockID = original
		}
		if file == "" {
			l.reportf("migration: %s unchanged: no document exists; triage must name or create one\n", id)
			continue
		}
		if err := commandPath(l.cfg.Root, file); err != nil {
			return err
		}
		raw, e := os.ReadFile(filepath.Join(l.cfg.Root, file))
		if os.IsNotExist(e) {
			l.reportf("migration: %s unchanged: no document exists at %s\n", id, file)
			continue
		}
		if e != nil {
			return e
		}
		if !item.DefinitionBlockExists(file, blockID, raw) {
			l.reportf("migration: %s unchanged: no item block exists in %s; triage must declare it\n", id, file)
			continue
		}
		l.ownerMigrationItem = id
		at := l.cfg.Now()
		plans, _, e := l.passPlans(ctx, at)
		if e != nil {
			return e
		}
		var plan *plannedApplication
		for i := range plans {
			if plans[i].decision.ItemID == id {
				plan = &plans[i]
				break
			}
		}
		if plan == nil || plan.decision.Action != ActionDispatch {
			return fmt.Errorf("migration: %s cannot dispatch its document conversion: %v", id, plan)
		}
		base, e := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if e != nil {
			return e
		}
		applied, work, e := l.applyPlan(ctx, *plan, base, at)
		if e != nil {
			return e
		}
		if !applied || work == nil {
			return fmt.Errorf("migration: %s changed before its lane claim; no conversion was published", id)
		}
		staged, e := l.stageDispatches(ctx, []Decision{work.Decision}, base, result)
		if e != nil {
			return e
		}
		if len(staged) != 1 {
			return fmt.Errorf("migration: %s could not stage its document conversion", id)
		}
		d := staged[0]
		before, e := l.snapshot(ctx, []string{l.lanePath(d.LaneID)}, nil)
		if e != nil {
			return e
		}
		rec, log, e := l.cfg.Steps.Run(ctx, *d.RunRequest)
		if e != nil {
			return e
		}
		result.Dispatched++
		l.countSpend(result, rec)
		stop, e := l.closeRunBracket(ctx, l.lanePath(d.LaneID), *d.RunRequest, d.PassID, at, before, result, nil)
		if e != nil {
			return e
		}
		if stop != "" {
			return fmt.Errorf("migration: %s run violated its boundary: %s", id, stop)
		}
		outcome := l.settle(ctx, d, rec, log, base, d.PassID, at, result, &PassTiming{}, nil, false)
		if outcome.err != nil {
			return outcome.err
		}
		if outcome.stop != "" {
			return fmt.Errorf("migration: %s stopped before publishing its document question: %s", id, outcome.stop)
		}
		if e := l.markExamined(ctx, d); e != nil {
			return e
		}
		current, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return err
		}
		var updated map[string]item.Conflict
		err = current.ReadTx(ctx, func(r *store.Reader) error { var e error; updated, e = item.ReadConflicts(r); return e })
		current.Close()
		if err != nil {
			return err
		}
		if updated[id].Status != "question" || updated[id].QuestionRef == "" {
			return fmt.Errorf("migration: %s document question did not land; conflict remains %s", id, updated[id].Status)
		}
		l.reportf("migration: %s wrote %s; conflict %s remains open\n", id, updated[id].QuestionRef, c.ConflictID)
	}
	if len(ids) == 0 {
		l.reportf("migration: no owner conflicts to convert; wrote nothing\n")
	}
	return nil
}
