package main

// The payload shapes in this file are copies of shapes other utilities own. Design §5.9
// makes the record stream the only coupling between utilities — "no utility calls
// another in-process" — so tillr-view decodes what it needs with its own structs rather
// than importing tools/tillr-item/item, tools/tillr-land/land, tools/tillr-gate or
// tools/tillr-run/run. The duplication is the boundary, not an oversight: a change to
// one of those payloads is a change to the record contract, and this copy is where a
// reader sees that the change was not honoured here.
//
// Each struct names the utility that owns its input shape. Item presentation hides
// the legacy rank key and adds the computed place.

// itemRecord is the item.record payload, owned by tools/tillr-item/item (itemPayload):
// design §5.3's field list, plus the two fields the store sets and the input never
// carries.
type itemRecord struct {
	ItemID    string      `json:"item_id"`
	Title     string      `json:"title"`
	BodyRef   string      `json:"body_ref"`
	State     string      `json:"state"`
	Criteria  []criterion `json:"criteria"`
	BlockedBy []string    `json:"blocked_by"`
	// Rank is the item's place in the queue's order as the item record carries it: a
	// sparse lexical token over the store's rank alphabet, compared as a plain string,
	// and empty for an item the queue sorts after every ranked one. It is the same
	// spelling the store column has, because the two are the same fact.
	Rank      string `json:"rank"` // decoded from legacy records; hidden by MarshalJSON
	Place     int    `json:"place,omitempty"`
	DesignDoc string `json:"design_doc,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	EventSeq  int64  `json:"event_seq,omitempty"`
	// Questions and QuestionsSource are the read-through of the design document the
	// item points at. They are never stored: the document is the only copy, so the
	// assembly overwrites whatever the record carried with what the document says now.
	Questions       []question `json:"questions,omitempty"`
	QuestionsSource string     `json:"questions_source,omitempty"`
	// QuestionsState is which of the three answers the index gave for the document:
	// questions-ok (the tool's list), questions-unknown (the tool was absent, failed or
	// timed out), or design-doc-missing (the tree does not hold the file). It is never
	// empty for an item that names a document, and the empty list above is only the
	// first of those.
	QuestionsState string `json:"questions_state,omitempty"`
}

// criterion is one acceptance criterion of an item.
type criterion struct {
	CriterionID string `json:"criterion_id"`
	Text        string `json:"text"`
	State       string `json:"state"`
}

// candidate is the land.candidate payload, owned by tools/tillr-land/land (Candidate):
// design §5.4's field list, field for field.
type candidate struct {
	CandidateID string `json:"candidate_id"`
	ItemID      string `json:"item_id"`
	BaseRef     string `json:"base_ref"`
	BaseSHA     string `json:"base_sha"`
	HeadSHA     string `json:"head_sha"`
	TreeSHA     string `json:"tree_sha"`
	State       string `json:"state"`
	EnqueuedAt  string `json:"enqueued_at"`
}

// attempt is the land.attempt payload, owned by tools/tillr-land/land (Attempt):
// design §5.4's field list, field for field. Result is one of landed, isolated or
// refused, and Reason is empty on a landed attempt.
type attempt struct {
	AttemptID   string `json:"attempt_id"`
	CandidateID string `json:"candidate_id"`
	VerdictID   string `json:"verdict_id"`
	Result      string `json:"result"`
	Reason      string `json:"reason"`
	TrunkBefore string `json:"trunk_before"`
	TrunkAfter  string `json:"trunk_after"`
}

// verdict is the gate.verdict payload, owned by tools/tillr-land/land (Verdict):
// design §5.7's field list, field for field. It binds a tree, not a candidate id.
type verdict struct {
	VerdictID      string         `json:"verdict_id"`
	PlanID         string         `json:"plan_id"`
	RegistryDigest string         `json:"registry_digest"`
	BaseSHA        string         `json:"base_sha"`
	TreeSHA        string         `json:"tree_sha"`
	Result         string         `json:"result"`
	Checks         []verdictCheck `json:"checks"`
}

// verdictCheck is one check's outcome as the verdict carries it.
type verdictCheck struct {
	CheckID    string `json:"check_id"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	LogRef     string `json:"log_ref"`
}

// runRecord is the run.record payload, owned by tools/tillr-run/run (Record): design
// §5.6's field list, field for field. ExitCode is a pointer because "the process ran
// and exited 0" and "there was no process to have a code" are different answers.
// ItemID is the item the run was dispatched for, and it is absent when the run named
// none (D-UT19): the field is optional, so an absent one is an absence rather than an
// empty id.
type runRecord struct {
	RunID        string        `json:"run_id"`
	ItemID       string        `json:"item_id,omitempty"`
	LaneID       string        `json:"lane_id"`
	Backend      string        `json:"backend"`
	State        string        `json:"state"`
	ExitCode     *int          `json:"exit_code,omitempty"`
	StartedAt    string        `json:"started_at"`
	FinishedAt   string        `json:"finished_at"`
	EventSeq     int64         `json:"event_seq"`
	ArtifactRefs []artifactRef `json:"artifact_refs"`
}

// artifactRef is one artifact a run produced, as the record carries it.
type artifactRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// runRequest is the run.request payload, owned by tools/tillr-run/run (Request). Only
// the two fields the run join needs are read: it is the one record in the stream that
// names both the item a run was dispatched for and the lane it ran on.
type runRequest struct {
	RunID  string `json:"run_id"`
	ItemID string `json:"item_id"`
	LaneID string `json:"lane_id"`
}
