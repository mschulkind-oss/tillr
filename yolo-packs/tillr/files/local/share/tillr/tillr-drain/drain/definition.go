package drain

import (
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonDesignUnderComment = "design-under-comment"
	ReasonWorkflowInvalid    = "workflow-invalid"
	ReasonNodeFailed         = "node-failed"
	// ReasonNodeAnswerUnread holds a workflow item at a node recorded blocked whose
	// retained report does not say blocked: its answer was misread, not given
	// (workflow_order.go, unreadAnswerHold). It is written "node-answer-unread: <node>".
	ReasonNodeAnswerUnread   = "node-answer-unread"
	ReasonVantageUnavailable = "Vantage not answering"
	ReasonNodeOutputMissing  = "node-output-missing"
	ReasonAwaitsRewrite      = "awaits-rewrite"
	ReasonProseCriteriaCount = "prose-criteria-count"
)

// landingHolds is the single classification of holds that belong to Git landing,
// including its gate, conflict repair and retained-candidate checks. A prerequisite
// entry additionally requires that check to be named by the definition.
var landingHolds = map[string]string{
	ReasonBoundaryScopeEscape: "",
	ReasonGateFailed:          "",
	ReasonLandRefused:         "",
	ReasonNoFence:             definition.PrerequisiteFence,
	ReasonForkHeld:            "",
	ReasonTrunkBroken:         "",
	ReasonConflictFix:         "",
	ReasonConflictExhausted:   "",
	ReasonConflictDesign:      "",
	ReasonConflictOwner:       "",
	ReasonQuestionRemoved:     "",
	ReasonTrunkPathDirty:      definition.PrerequisiteOwnerClean,
	ReasonTrunkRewritten:      "",
	ReasonCandidateMismatch:   "",
	ReasonLandHarnessError:    "",
	StopGateHarnessError:      "",
	ReasonTrunkUnproved:       "",
	ReasonRedCap:              "",
	ReasonLandingReverted:     "",
	ReasonRepairStagingFailed: "",
	ReasonGateStallRerun:      "",
	ReasonGateStalled:         "",
	ReasonGateNoVerdict:       "",
	ReasonTriageNoBlock:       definition.PrerequisiteDesignDocument,
	ReasonAwaitsRewrite:       definition.PrerequisiteDesignDocument,
	ReasonProseCriteriaCount:  definition.PrerequisiteDesignDocument,
}

// generalHolds covers execution, capacity, item readiness and optional design
// checks. These are not landing-specific; the value names an optional prerequisite.
var generalHolds = map[string]string{
	ReasonCompleteItemFault: "", ReasonCriteriaUnjudged: "", ReasonCandidateNoOp: "",
	ReasonBackendWindow: "", ReasonBackendClosed: "", ReasonBlocked: "", ReasonNotReady: "",
	ReasonRunLinkMissing: "", ReasonRunUnjudged: "", ReasonNoPolicy: "", ReasonOwnerOnlyOwed: "",
	ReasonNoLane: "", ReasonRunLive: "", ReasonInfraExhausted: "", ReasonCommandFault: "",
	ReasonDeskCapacity: "", ReasonDocumentDeskBusy: "",
	ReasonRetryJudged: "", ReasonRetryUnjudged: "", ReasonToolingDefect: "",
	ReasonDecisionCollision: "", ReasonSettleFailed: "", ReasonRepeatingCall: "",
	ReasonQuestionOpen:       definition.PrerequisiteDesignQuestions,
	ReasonQuestionsUnknown:   definition.PrerequisiteDesignQuestions,
	ReasonDesignDocMissing:   definition.PrerequisiteDesignDocument,
	ReasonDesignUnderComment: definition.PrerequisiteDesignQuestions,
	ReasonVantageUnavailable: definition.PrerequisiteDesignQuestions,
	ReasonItemUnstagleable:   "", ReasonDefinitionSyncFailed: "",
	ReasonNodeScopeOutsideFence: "",
	ReasonLaneUnlandedChange:    "", ReasonLaneBranchHeld: "", ReasonLaneUncommittedWork: "", ReasonLaneChildren: "",
	ReasonPreserveFailed:      "",
	StopHostBoundaryViolation: "",
	ReasonWorkflowInvalid:     "", ReasonNodeFailed: "", ReasonNodeAnswerUnread: "", ReasonNodeOutputMissing: "",
	ReasonJudgeGrantAbsent: "", ReasonJudgeUnreadable: "", ReasonEvalFailed: "",
	ReasonJudgeBlocked: "", ReasonJudgeDefective: "",
}

func hasPrerequisite(d definition.Definition, name string) bool {
	for _, prerequisite := range d.Prerequisites {
		if prerequisite == name {
			return true
		}
	}
	return false
}

func finishedState(d definition.Definition, state string) bool {
	for _, finished := range d.Finished {
		if finished == state {
			return true
		}
	}
	return false
}

func holdEnabled(d definition.Definition, reason string) bool {
	name, _, _ := strings.Cut(reason, ":")
	prerequisite, landing := landingHolds[name]
	if landing && d.Completes != "landing" {
		return false
	}
	if !landing {
		prerequisite = generalHolds[name]
	}
	return prerequisite == "" || hasPrerequisite(d, prerequisite)
}

// requireHold guards durable hold writes in the same transaction as the write.
// An incompatible landing path is a configuration error, not a hold against the item.
func requireHold(r *store.Reader, reason string) error {
	d, err := definition.ForReader(r)
	if err != nil {
		return err
	}
	if !holdEnabled(d.Parsed, reason) {
		return Usagef("definition %q does not enable hold %q", d.Name, reason)
	}
	return nil
}

// Direct planner unit fixtures predate definitions. Public Plan always supplies
// the declaration read from its pinned transaction; only those fixtures use this default.
func (p *planner) rules() definition.Definition {
	if p.definition != nil {
		return *p.definition
	}
	return defaultDefinition()
}

func defaultDefinition() definition.Definition {
	d, err := definition.GitLanding()
	if err != nil {
		panic(err)
	} // The embedded declaration is validated by its package tests.
	return d
}
func (p *planner) lands() bool                { return p.rules().Completes == "landing" }
func (p *planner) finished(state string) bool { return finishedState(p.rules(), state) }

// Ignore persisted landing checks that are not part of this definition, without
// mutating the store or the pending record. Settlement/completion remains the loop's job.
func definitionWork(d definition.Definition, work *pendingWork) *pendingWork {
	copy := *work
	if !holdEnabled(d, ReasonRepairStagingFailed) {
		copy.RepairHold = nil
	}
	if !holdEnabled(d, ReasonGateStallRerun) {
		copy.GateStall = nil
	}
	if !holdEnabled(d, ReasonRedCap) {
		copy.RedCapHold = nil
	}
	if !holdEnabled(d, ReasonTrunkPathDirty) {
		copy.DirtyHold = nil
	}
	if !holdEnabled(d, ReasonTrunkRewritten) {
		copy.Gated = false
	}
	return &copy
}
