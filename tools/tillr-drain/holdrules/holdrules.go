// Architecture and invariants: docs/reference/observatory-redesign.md
//
// Package holdrules is the one table of how a recorded hold reason is shown: its
// class, where it sits, and what moves it. It is a leaf package (no store, no
// writer DDL) so the read-only view can import it, and the drain package's own
// tests require a row for every exported Reason constant, so a reason cannot
// land without its display rule.
//
// Adding a hold reason: declare the Reason constant in the drain package, then add
// its row to Rules here. TestEveryReasonConstantHasAHoldRule fails until you do.
package holdrules

import "strings"

// Rule is how one reason is displayed: Class is one of "needs you", "waits on work",
// "needs a cause", "ready", "moving" or "waits on the loop"; Where is the stage;
// Mover is what clears it.
type Rule struct{ Class, Where, Mover string }

// Classes lists the valid Rule.Class spellings.
var Classes = []string{"needs you", "waits on work", "needs a cause", "ready", "moving", "waits on the loop"}

// Unknown is the rule for a recorded reason with no row.
var Unknown = Rule{"needs a cause", "queue", "add a history rule for this recorded reason"}

// Rules maps an exact reason spelling to its rule. Entries that no drain pass writes
// any more stay, because a recorded decision that carries one is history.
var Rules = map[string]Rule{
	"words-missing":         {"needs a cause", "fence", "inspect the missing words file and authorize a retry"},
	"words-altered":         {"needs a cause", "fence", "restore the saved words unchanged and authorize a retry"},
	"idea-no-outcome":       {"needs a cause", "fence", "add item blocks carrying from the idea and authorize a retry"},
	"Vantage not answering": {"needs a cause", "queue", "check Vantage availability"},
	"node-output-missing":   {"needs a cause", "lane", "inspect the named node's output declarations and saved artifacts"},
	"owner-only-owed":       {"needs you", "queue", "the owner"},
	"workflow-invalid":      {"needs you", "queue", "correct the named workflow file and run `tillr drain workflow check`"},
	"node-failed":           {"needs you", "lane", "inspect the named node and authorize a retry"},
	// A node recorded blocked whose own report does not say blocked: a misread answer
	// left by a loop built before footers were read through Markdown emphasis. No
	// settlement writes it now; the retry keeps every read-only node that passed.
	"node-answer-unread": {"needs you", "lane", "authorize a retry: `tillr drain retry --item <item> --run <its latest finished run>`; read-only nodes that passed are kept"},
	"gate-stall-rerun":   {"waits on work", "gate", "same candidate reruns when fewer gates are in flight or none other are"},
	"gate-stalled":       {"needs you", "gate", "diagnose the stalled check and clear the fault"},
	// A gate that ran and reached no verdict judged nothing: the next pass gates the same
	// candidate again, uncounted, with no operator.
	"gate-no-verdict": {"waits on the loop", "gate", "the next pass gates the same candidate again on its lane"},
	// Now only the gate STEP failing (the drain could not run the gate or read or record
	// what it answered, an Externalf from Steps.Gate), and holds recorded before 2026-11-18.
	"gate-harness-error":          {"needs you", "gate", "diagnose the gate and clear the fault; or tillr drain retry naming the held run, which supersedes its candidate"},
	"land-harness-error":          {"needs you", "land", "repair what the lander refused and clear the fault; or tillr drain retry naming the held run, which supersedes its candidate"},
	"host-boundary-violation":     {"needs you", "fence", "inspect the recorded violation and clear the fault"},
	"candidate-record-mismatch":   {"needs you", "lane", "restore the recorded candidate and clear the fault; or tillr drain retry naming the held run, which supersedes its candidate"},
	"definition-sync-failed":      {"needs you", "queue", "correct the item's block on the trunk as the detail says; the next document sync applies it and ends the hold"},
	"backend-window":              {"waits on work", "backend", "the reset time"},
	"lane-holds-unlanded-change":  {"needs you", "lane", "land the change, or reset the lane deliberately"},
	"lane-branch-held":            {"needs you", "lane", "remove the worktree holding the lane's branch, or stage a different lane id"},
	"lane-holds-uncommitted-work": {"needs you", "lane", "commit or remove the files, or reset the lane deliberately"},
	"lane-run-children-live":      {"waits on work", "lane", "wait for the named processes to exit, or end them with the recorded kill command"},
	"run-processes-live":          {"needs you", "lane", "end the surviving processes the detail lists, then tillr drain clear-fault"},
	"preserve-failed":             {"needs you", "lane", "the process or cause the detail names (a live index.lock holder exits or is ended); then tillr drain clear-fault"},
	"decision-collision":          {"needs a cause", "loop", "fix the drain defect the fault names, then tillr drain clear-fault"},
	"adapter-refused":             {"needs you", "lane", "correct what the adapter's words name, then tillr drain clear-fault"},
	"settle-failed":               {"needs a cause", "loop", "fix the drain defect the fault names, then tillr drain clear-fault"},
	// Every backend on the seat's list ended a session repeating one identical call (pi's
	// loop_detected): nothing automatic is left to try.
	"repeating-call":       {"needs you", "lane", "the operator: read the repeated call in the run record, then `tillr drain retry --item <item> --run <the held run>`"},
	"tooling-defect":       {"waits on work", "queue", "the tooling defect item to land or close"},
	"triage-no-block":      {"waits on work", "queue", "triage on the design seat"},
	"conflict-design":      {"waits on work", "queue", "design"},
	"design-under-comment": {"waits on work", "queue", "the comment item"},
	"conflict-owner":       {"needs you", "queue", "owner"},
	"conflict-answered":    {"ready", "queue", "the next pass"},
	"restaged: the earlier run's base lacked what made the item ready":              {"ready", "queue", "the next pass"},
	"restarted in a fresh session: the earlier session repeated one identical call": {"ready", "queue", "the next pass"},
	"failover: the earlier sessions repeated one identical call":                    {"ready", "queue", "the next pass"},
	"ready":                        {"ready", "queue", "the next pass"},
	"blocked":                      {"waits on work", "queue", "the owner; clear the item's blocked state"},
	"not-ready":                    {"needs you", "queue", "the owner"},
	"run-link-missing":             {"needs a cause", "lane", "inspect the run the reason names, then `tillr drain retry --item <item> --run <that run>`"},
	"boundary-scope-escape":        {"needs you", "fence", "inspect the named node and escaped path, then authorize a retry"},
	"node-scope-outside-fence":     {"needs you", "fence", "narrow the named node's write_scope to the item's fence"},
	"candidate-no-op":              {"needs a cause", "lane", "inspect the run's report for why it committed nothing, then authorize a retry"},
	"complete-item-fault":          {"needs a cause", "lane", "repair the fault the detail names, then authorize a retry"},
	"criteria-unjudged":            {"needs a cause", "lane", "the judge seat; inspect the judge run and authorize a retry"},
	"run-unjudged":                 {"needs a cause", "lane", "authorize a retry acknowledging the unjudged run: `tillr drain retry --item <item> --run <the held run>`"},
	"judge-grant-absent":           {"needs you", "queue", "issue a grant covering the item's criteria: tillr item grant issue --store <path> --input <record> --now <RFC3339>"},
	"judge-unreadable":             {"needs a cause", "judge", "inspect the judge run"},
	"eval-failed":                  {"waits on work", "judge", "amend the candidate against the judge's reason"},
	"judge-blocked":                {"waits on work", "judge", "the recorded waiter; answer the judge's blocker"},
	"judge-defective":              {"needs you", "judge", "owner; reopen the closed item only if its premise is corrected"},
	"gate-failed":                  {"needs a cause", "gate", "inspect the gate verdict; the item's worker repairs the failing check"},
	"red-cap":                      {"waits on work", "land", "the heavy runner; prove trunk or file its fixer"},
	"land-refused":                 {"needs you", "land", "inspect the refused landing and authorize a retry"},
	"no-policy":                    {"needs you", "queue", "the owner"},
	"capacity":                     {"waits on work", "queue", "the next pass"},
	"no-lane":                      {"waits on work", "queue", "a free lane"},
	"waits on a desk":              {"waits on work", "desk", "a design desk to free up"},
	"waits on the document's desk": {"waits on work", "desk", "the document's desk to finish its current item"},
	"no-fence":                     {"needs you", "queue", "the owner"},
	"run-live":                     {"waits on work", "lane", "the run from the earlier loop to finish; `tillr drain stop --end-runs` ends it"},
	"prose-criteria-count":         {"needs you", "queue", "the owner; reconcile the original criteria"},
	"trunk-foreign-identity":       {"needs you", "trunk", "re-author the commit, or record its id with tillr-drain accept-identity"},
	"awaits-rewrite":               {"waits on work", "queue", "the prose rewrite to land"},
	"fork-held":                    {"waits on work", "queue", "the fork lane landing the shared patch branch; the next pass"},
	"failover":                     {"ready", "queue", "the next pass"},
	"seat-degraded":                {"ready", "queue", "the next pass"},
	"backend-closed":               {"waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"},
	"command-fault":                {"needs a cause", "lane", "repair the command process and authorize a retry"},
	// History only: an infrastructure failure now closes the backend, not the item.
	"run-infra-exhausted": {"waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"},
	"harness-refused":     {"ready", "queue", "the next pass"},
	// A worker run's refused ending goes back to the item's worker with its evidence,
	// uncounted (the owner's ruling of 2026-11-17).
	"returned-with-evidence": {"ready", "queue", "the next pass"},
	// A landing the full suite proved broke the trunk is reverted by tillr and its item
	// goes back to its worker, whose next gate runs the failing check (OQ-GP1).
	"landing-reverted":               {"ready", "queue", "the next pass returns it to its worker with the failing check"},
	"retry-judged":                   {"needs you", "gate", "the owner"},
	"retry-unjudged":                 {"needs you", "lane", "the owner"},
	"question-removed":               {"needs you", "queue", "restore the removed question under the item's block and land it"},
	"question-open":                  {"needs you", "queue", "question not recorded"},
	"questions-unknown":              {"needs a cause", "queue", "restore the Vantage index command (--questions-cmd)"},
	"design-doc-missing":             {"needs you", "queue", "the owner"},
	"trunk-path-dirty":               {"waits on work", "land", "commit, restore, or change your edit; the landing proceeds as soon as it no longer conflicts"},
	"trunk-rewritten":                {"needs you", "trunk", "restore the trunk's history"},
	"trunk-broken":                   {"waits on work", "trunk", "the trunk fixer to land, or a heavy proof green past its red commit"},
	"trunk-unproved":                 {"waits on work", "trunk", "the heavy runner; prove the trunk"},
	"obstacle-owner":                 {"needs you", "queue", "the owner; answer the question with tillr drain retry, or close the item"},
	"obstacle-fence":                 {"needs you", "queue", "widen the item's allowed_paths or split the request, then retry the run"},
	"obstacle-trunk":                 {"waits on work", "trunk", "returns when the named checks are green"},
	"obstacle-tooling":               {"needs you", "queue", "repair the tool or harness and retry the run"},
	"obstacle-seat":                  {"needs you", "queue", "change seat or split the request, then retry the run"},
	"obstacle-definition":            {"needs you", "queue", "refile or close the item, or retry the run"},
	"conflict-repair-staging-failed": {"needs you", "land", "correct the reported staging cause; the next pass retries the retained candidate"},
	"conflict-fix":                   {"ready", "land", "the next pass"},
	"item-unstagleable":              {"needs a cause", "queue", "correct the item's staging fault the detail names; the next pass stages it"},
	"staging-failed":                 {"needs you", "lane", "correct the reported staging cause on the named lane; then tillr drain retry or clear the lane condition"},
	// A terminal item leaves this view, so the rule names the settlement for history reads.
	"item-already-terminal": {"moving", "lane", "the next pass settles the run without judging it"},
	// Historical decisions remain readable after a planner removes a reason.
	"fence-overlap": {"waits on work", "queue", "the item holding the overlapping fence to land"},
	// The owner's ruling of 2026-11-17 removed these three: nothing is re-admitted because
	// an input changed, and a retry that names an old run no longer holds the item. A
	// pass writes none of them; a recorded decision that carries one is history.
	"inputs-changed":        {"ready", "queue", "the next pass"},
	"readmission-exhausted": {"needs you", "lane", "automatic re-admission failed; run `tillr drain retry` after diagnosis"},
	"retry-stale":           {"needs you", "lane", "the owner"},
	// The same ruling removed the one conflict-fix run: a conflict goes back to its worker
	// on the latest trunk, so an item parked under this reason is returned at the next pass.
	"conflict-exhausted":         {"ready", "queue", "the next pass"},
	"pi-extensions-build-failed": {"waits on work", "roll", "the next extension or patch-branch landing, or tools/tillr-run/backends/pi/build-agent-dir"},
}

// For resolves a recorded reason to its rule. Drain appends detail to a few reasons
// ("node-failed: <node>"), so a declared prefix resolves to its base row; any other
// unrecognized reason is Unknown, never guessed.
func For(reason string) Rule {
	if strings.HasPrefix(reason, "workflow-invalid: ") {
		return Rules["workflow-invalid"]
	}
	for _, named := range []string{"node-failed", "run-link-missing", "node-answer-unread", "node-scope-outside-fence", "boundary-scope-escape", "prose-criteria-count"} {
		if strings.HasPrefix(reason, named+": ") {
			return Rules[named]
		}
	}
	if lane, ok := strings.CutPrefix(reason, "staging-failed: "); ok && lane != "" {
		r := Rules["staging-failed"]
		r.Mover = "correct the reported staging cause on " + lane + "; then tillr drain retry or clear the lane condition"
		return r
	}
	if rule, ok := Rules[reason]; ok {
		return rule
	}
	for _, prefix := range []string{"trunk-foreign-identity: ", "trunk-rewritten: ", "red-cap: ", "trunk-path-dirty: ", "inputs-changed: ", "seat-degraded: seat ", "staging-failed: "} {
		if strings.HasPrefix(reason, prefix) {
			return Rules[baseOf(prefix)]
		}
	}
	return Unknown
}

func baseOf(prefix string) string {
	if i := strings.Index(prefix, ":"); i >= 0 {
		return prefix[:i]
	}
	return prefix
}
