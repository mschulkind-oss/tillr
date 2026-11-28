// Architecture and invariants: docs/design/tillr-per-step-question-holds.md Section 7.2 (Slice 2)
package item

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// companionResult is what reading the companion plans decided: the item writes it adds to
// the design mirror's, the findings it refuses plans with, and the question waits each
// imported task records.
type companionResult struct {
	creates    []designMirrorTransition
	updates    []designMirrorTransition
	unchanged  []string
	frozen     []SyncNote
	findings   []SyncNote
	skipped    []string
	ids        []string
	placements []sourcePlacement
	parked     []string
	unparked   []string
	// waits maps every task's item id to its plan-declared waits; a task that waits on
	// nothing is present with no refs, which releases it from the whole-document hold.
	waits map[string][]QuestionRef
}

// planCompanionPlans reads the DECIDED companion plan of every outstanding routed design
// and plans the items its tasks declare. A plan with a finding writes nothing.
// declared names the ids document blocks already declare.
func planCompanionPlans(r *store.Reader, in SyncInput, rd routedDesigns, index vantage.VantageIndex, held map[string]storedState, d definition.Definition, finished func(string) bool, declared map[string]bool) (companionResult, error) {
	out := companionResult{waits: map[string][]QuestionRef{}}
	var plans []companionPlan
	for _, planPath := range companionCandidates(rd) {
		parent := companionParentPath(planPath)
		planDoc := rd.byPath[planPath]
		if line := planHasQuestionDirective(planDoc.Content); line > 0 {
			out.findings = append(out.findings, SyncNote{File: planPath, Line: line, Rule: ruleHasQuestion,
				Message: "a companion plan carries no `question` or `oq` directive: owner questions live in " + parent})
			out.skipped = append(out.skipped, planPath)
			continue
		}
		planRole, err := index.StageRole(planPath)
		if err != nil {
			return out, Externalf("index-unreadable: %v", err)
		}
		if planRole != vantage.StageRoleReady {
			continue
		}
		parentRole, err := designRole(index, rd.byPath, parent)
		if err != nil {
			return out, Externalf("index-unreadable: %v", err)
		}
		if parentRole != "" && parentRole != vantage.StageRoleOpen && parentRole != vantage.StageRoleReady {
			continue
		}
		plan := parseCompanionPlan(planDoc, parent, rd.byPath)
		if len(plan.findings) > 0 {
			out.findings = append(out.findings, plan.findings...)
			out.skipped = append(out.skipped, planPath)
			continue
		}
		plans = append(plans, plan)
	}

	// One id, one declaration: across plans, and against document blocks.
	owners := map[string][]string{}
	for _, plan := range plans {
		for _, t := range plan.tasks {
			owners[t.payload.ItemID] = append(owners[t.payload.ItemID], plan.path)
		}
	}
	bad := map[string]bool{}
	for _, plan := range plans {
		for _, t := range plan.tasks {
			id := t.payload.ItemID
			if len(owners[id]) > 1 || declared[id] {
				out.findings = append(out.findings, SyncNote{File: plan.path, Heading: t.heading, Line: t.line, ItemID: id, Rule: ruleTaskInvalid,
					Message: fmt.Sprintf("item %q is declared more than once (%s)", id, strings.Join(owners[id], ", "))})
				bad[plan.path] = true
			}
		}
	}
	// A blocker is a task of a plan that imports, a block, or an item the store holds.
	for changed := true; changed; {
		changed = false
		avail := map[string]bool{}
		for _, plan := range plans {
			if bad[plan.path] {
				continue
			}
			for _, t := range plan.tasks {
				avail[t.payload.ItemID] = true
			}
		}
		for _, plan := range plans {
			if bad[plan.path] {
				continue
			}
			for _, t := range plan.tasks {
				for _, blocker := range t.payload.BlockedBy {
					if avail[blocker] || declared[blocker] {
						continue
					}
					if _, ok := held[blocker]; ok {
						continue
					}
					out.findings = append(out.findings, SyncNote{File: plan.path, Heading: t.heading, Line: t.line, ItemID: t.payload.ItemID, Rule: ruleTaskInvalid,
						Message: fmt.Sprintf("After names %q, which no plan task declares and the store does not hold", blocker)})
					bad[plan.path], changed = true, true
				}
			}
		}
	}
	var live []companionPlan
	for _, plan := range plans {
		if bad[plan.path] {
			out.skipped = append(out.skipped, plan.path)
			continue
		}
		live = append(live, plan)
	}

	// Blockers first, so a blocker is written before the item it blocks.
	var ordered []planTask
	for _, plan := range live {
		for _, t := range plan.tasks {
			ordered = append(ordered, planTask{plan.parent, t})
		}
	}
	ordered = blockersFirst(ordered)

	for _, pt := range ordered {
		parent, t := pt.parent, pt.task
		id := t.payload.ItemID
		out.ids = append(out.ids, id)
		parkedOnly := rd.parkedLinked[parent] && !rd.spineLinked[parent]
		desired := t.payload
		st, exists := held[id]
		if exists && finished(st.state) {
			out.unchanged = append(out.unchanged, id)
			continue
		}
		out.waits[id] = t.waits
		if !exists {
			desired.State = d.Ready
			var wait *WaitingOn
			if parkedOnly {
				desired.State = d.Blocked
				wait = &WaitingOn{Waiter: "owner", Reason: "icebox"}
			} else {
				out.placements = append(out.placements, sourcePlacement{id, designItemID(parent)})
			}
			out.creates = append(out.creates, designMirrorTransition{id: id, toState: desired.State, waitingOn: wait, isCreate: true, applyUpdate: true, payload: desired})
			continue
		}
		stored, found, err := storedItem(r, id)
		if err != nil {
			return out, err
		}
		if !found {
			continue
		}
		waiting, _, err := ReadWaitingOn(r, id)
		if err != nil {
			return out, err
		}
		park := stored.State == d.Ready && parkedOnly
		unpark := stored.State == d.Blocked && waiting != nil && waiting.Reason == "icebox" && !parkedOnly
		desired.AllowedPaths, desired.After = stored.AllowedPaths, stored.After
		field := definitionDiff(desired, stored)
		if field == "" && !park && !unpark {
			out.unchanged = append(out.unchanged, id)
			continue
		}
		if judged := judgedCriterion(stored); judged != "" && field != "" {
			out.frozen = append(out.frozen, SyncNote{File: pt.file(), Heading: t.heading, Line: t.line, ItemID: id, Rule: "definition-frozen",
				Message: fmt.Sprintf("the plan's %s differs from the store's, and criterion %q already carries the state %q, so the stored definition stands: a presented criterion is not rewritten", field, judged, criterionState(stored, judged))})
			if !park && !unpark {
				continue
			}
			desired = stored
		}
		desired.State, desired.Rank = stored.State, stored.Rank
		var wait *WaitingOn
		if park {
			desired.State, desired.Rank = d.Blocked, ""
			wait = &WaitingOn{Waiter: "owner", Reason: "icebox"}
			out.parked = append(out.parked, id)
		}
		if unpark {
			desired.State, desired.Rank = d.Ready, ""
			out.unparked = append(out.unparked, id)
			out.placements = append(out.placements, sourcePlacement{id, designItemID(parent)})
		}
		out.updates = append(out.updates, designMirrorTransition{id: id, toState: desired.State, waitingOn: wait, isPark: park, isUnpark: unpark, applyUpdate: true, payload: desired})
	}
	return out, nil
}

type planTask struct {
	parent string
	task   companionTask
}

func (p planTask) file() string { return companionPlanPath(p.parent) }

// blockersFirst orders tasks so each follows the tasks that block it; plan order breaks ties.
func blockersFirst(tasks []planTask) []planTask {
	byID := map[string]bool{}
	for _, t := range tasks {
		byID[t.task.payload.ItemID] = true
	}
	done := map[string]bool{}
	var out []planTask
	for len(out) < len(tasks) {
		progressed := false
		for _, t := range tasks {
			if done[t.task.payload.ItemID] {
				continue
			}
			ready := true
			for _, b := range t.task.payload.BlockedBy {
				if byID[b] && !done[b] {
					ready = false
				}
			}
			if ready {
				done[t.task.payload.ItemID] = true
				out = append(out, t)
				progressed = true
			}
		}
		if !progressed {
			// A cycle across plans: keep the rest in plan order; Apply refuses the edge.
			for _, t := range tasks {
				if !done[t.task.payload.ItemID] {
					done[t.task.payload.ItemID] = true
					out = append(out, t)
				}
			}
		}
	}
	return out
}

// companionPlanIDs lists the ids the companion plans among docs declare, whatever their
// stage: those items are shared through the plan, not private store residue.
func companionPlanIDs(docs []SyncDocument) map[string]bool {
	byPath := make(map[string]SyncDocument, len(docs))
	for _, doc := range docs {
		byPath[doc.Path] = doc
	}
	ids := map[string]bool{}
	for _, doc := range docs {
		parent := companionParentPath(doc.Path)
		if parent == "" {
			continue
		}
		if _, ok := byPath[parent]; !ok {
			continue
		}
		plan := parseCompanionPlan(doc, parent, byPath)
		for _, t := range plan.tasks {
			ids[t.payload.ItemID] = true
		}
	}
	return ids
}

// writeCompanionWaits replaces the question waits each imported task's plan declares,
// leaving worker-added waits and overrides alone. A task that waits on nothing keeps one
// sentinel row, so the reader treats it as recorded rather than legacy.
func writeCompanionWaits(tx *store.Tx, waits map[string][]QuestionRef) error {
	ids := make([]string, 0, len(waits))
	for id := range waits {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var exists bool
		if err := tx.Reader().QueryRow(`SELECT EXISTS (SELECT 1 FROM items WHERE item_id = ?)`, id).Scan(&exists); err != nil {
			return Internalf("checking item %s: %v", id, err)
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM item_question_waits WHERE item_id = ? AND source = 'plan'`, id); err != nil {
			return Internalf("replacing the plan waits of %s: %v", id, err)
		}
		refs := waits[id]
		if len(refs) == 0 {
			refs = []QuestionRef{{Key: planWaitSentinel}}
		}
		for _, q := range refs {
			if _, err := tx.Exec(`INSERT INTO item_question_waits(item_id, ref_key, doc_path, question_id, heading_slug, quoted_question, section_sha256, reason, source)
 VALUES(?,?,?,?,?,?,?,?,'plan') ON CONFLICT(item_id, ref_key) DO NOTHING`,
				id, q.Key, q.DocPath, q.QuestionID, q.HeadingSlug, q.QuotedQuestion, q.SectionSHA256, q.Reason); err != nil {
				return Internalf("writing the plan waits of %s: %v", id, err)
			}
		}
	}
	return nil
}
