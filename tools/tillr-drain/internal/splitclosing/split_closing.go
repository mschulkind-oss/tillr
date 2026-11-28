package splitclosing

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	StartedKind = "drain.split-closing-started"
	FindingKind = "drain.split-closing-finding"
	RefusedKind = "drain.split-closing-refused"
	HeldKind    = "drain.split-closing-held"

	RunKindCheck           = "check"
	RunKindExtend          = "extend"
	RunKindDefectiveExtend = "defective-extend"

	briefFile            = "brief.md"
	braceLiteralsFile    = "brace-literals"
	endingCapabilityFile = "ending-capability"
)

type StartedPayload struct {
	ItemID         string                       `json:"item_id"`
	RunID          string                       `json:"run_id"`
	Kind           string                       `json:"kind"`
	StatementHash  string                       `json:"statement_hash"`
	TrunkSHA       string                       `json:"trunk_sha,omitempty"`
	TrunkTree      string                       `json:"trunk_tree,omitempty"`
	LaneID         string                       `json:"lane_id"`
	OutputPath     string                       `json:"output_path"`
	Route          string                       `json:"route"`
	Seat           string                       `json:"seat"`
	Workflow       string                       `json:"workflow"`
	NodeID         string                       `json:"node_id"`
	Backend        string                       `json:"backend"`
	Landings       []item.SplitPartLanding      `json:"landings,omitempty"`
	DefectiveParts []item.DefectivePartFindings `json:"defective_parts,omitempty"`
}

type FindingPayload struct {
	ItemID        string         `json:"item_id"`
	RunID         string         `json:"run_id"`
	StatementHash string         `json:"statement_hash"`
	TrunkSHA      string         `json:"trunk_sha"`
	Findings      []FindingEntry `json:"findings,omitempty"`
	Reasons       []string       `json:"reasons,omitempty"`
}

type FindingEntry struct {
	CriterionID string               `json:"criterion_id"`
	Reason      string               `json:"reason"`
	Findings    item.VerdictFindings `json:"findings,omitempty"`
}

type RefusedPayload struct {
	ItemID        string `json:"item_id"`
	RunID         string `json:"run_id"`
	StatementHash string `json:"statement_hash"`
	Reason        string `json:"reason"`
}

type HeldPayload struct {
	ItemID        string `json:"item_id"`
	RunID         string `json:"run_id"`
	StatementHash string `json:"statement_hash"`
	Reason        string `json:"reason"`
	Mover         string `json:"mover"`
}

type State struct {
	StatementHash string
	Started       *StartedPayload
	Held          *HeldPayload
	Finding       *FindingPayload
	Refused       *RefusedPayload
}

type RunState int

const (
	RunStateRunning RunState = iota
	RunStateFinished
)

type RunResult struct {
	Record run.Record
	Err    error
}

type AdvanceResult struct {
	Worked  bool
	Pending bool
}

type Criterion struct {
	ID         string
	Text       string
	ClaimClass string
	Hash       string
}

type Record struct {
	ItemID    string                  `json:"item_id"`
	TrunkSHA  string                  `json:"trunk_sha"`
	TrunkTree string                  `json:"trunk_tree"`
	RunID     string                  `json:"run_id"`
	Reason    string                  `json:"reason"`
	Landed    bool                    `json:"landed"`
	Parts     []item.SplitPartLanding `json:"parts"`
}

type SeatSelection struct {
	Backends  []string
	SeatLines string
	Request   func(itemID, laneID, runID, backend string) run.Request
}

type Env struct {
	NonLanding         bool
	Root               string
	Store              string
	LaneRoot           string
	TrunkRef           string
	Now                func() time.Time
	LaneDir            func(laneID string) string
	RevParse           func(ctx context.Context, dir, rev string) (string, error)
	SelectSeats        func(seat string) (SeatSelection, bool)
	CanStart           func(ctx context.Context) bool
	StartRun           func(ctx context.Context, req run.Request) error
	CheckRun           func(ctx context.Context, runID string) (RunResult, RunState, error)
	ConsumeRun         func(runID string)
	CountSpend         func(rec run.Record)
	SetItemState       func(ctx context.Context, id, runID, state string) error
	RequestWordsSuffix func(words []byte) string
	BraceLiteralCount  func(brief string) int
}

func ReadState(r *store.Reader, parent string) (State, error) {
	var rawStmt []byte
	err := r.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&rawStmt)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	stmtHash := item.CriterionHash(string(rawStmt))
	st := State{StatementHash: stmtHash}
	rows, err := r.Query(
		`SELECT kind, payload FROM events WHERE object_type='item' AND object_id=? AND kind IN (?,?,?,?) ORDER BY seq ASC`,
		parent, StartedKind, FindingKind, RefusedKind, HeldKind,
	)
	if err != nil {
		return State{}, err
	}
	defer rows.Close()
	var latestStarted *StartedPayload
	settledRuns := map[string]bool{}
	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			return State{}, err
		}
		switch kind {
		case StartedKind:
			var p StartedPayload
			if err := json.Unmarshal(raw, &p); err == nil && p.StatementHash == stmtHash {
				cp := p
				latestStarted = &cp
			}
		case FindingKind:
			var p FindingPayload
			if err := json.Unmarshal(raw, &p); err == nil && p.StatementHash == stmtHash {
				cp := p
				st.Finding = &cp
				if p.RunID != "" {
					settledRuns[p.RunID] = true
				}
			}
		case RefusedKind:
			var p RefusedPayload
			if err := json.Unmarshal(raw, &p); err == nil && p.StatementHash == stmtHash {
				cp := p
				st.Refused = &cp
				if p.RunID != "" {
					settledRuns[p.RunID] = true
				}
			}
		case HeldKind:
			var p HeldPayload
			if err := json.Unmarshal(raw, &p); err == nil && p.StatementHash == stmtHash {
				cp := p
				st.Held = &cp
				if p.RunID != "" {
					settledRuns[p.RunID] = true
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return State{}, err
	}
	if latestStarted != nil && !settledRuns[latestStarted.RunID] {
		st.Started = latestStarted
	}
	return st, nil
}

func FormatFindingWhy(f *FindingPayload) string {
	if f == nil {
		return ""
	}
	parts := make([]string, 0, len(f.Reasons))
	for _, r := range f.Reasons {
		if trimmed := strings.TrimSpace(r); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		return "closing check found missing work; waiting on design seat to add a part"
	}
	return "closing check found missing work: " + strings.Join(parts, "; ")
}

func Dirs(root, laneRoot string, laneDirFn func(string) string, runID string) (laneID, laneDir, durableDir string) {
	laneID = "lane_" + runID
	if laneRoot != "" {
		laneDir = laneDirFn(laneID)
		durableDir = run.DurableDir(laneDir, runID)
	} else {
		laneDir = drainmodel.RunDir(root, runID)
		durableDir = drainmodel.RunDir(root, runID)
	}
	return laneID, laneDir, durableDir
}

func StageBriefFiles(root, runID, laneDir, durableDir, brief string, braceLiterals int) error {
	dirs := []string{durableDir}
	if laneDir != durableDir {
		dirs = append(dirs, laneDir)
	}
	if runDir := drainmodel.RunDir(root, runID); runDir != durableDir && runDir != laneDir {
		dirs = append(dirs, runDir)
	}
	braceCount := strconv.Itoa(braceLiterals) + "\n"
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, briefFile), []byte(brief), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, braceLiteralsFile), []byte(braceCount), 0o644); err != nil {
			return err
		}
	}
	session, err := drainmodel.NewSessionID()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(laneDir, drainmodel.SessionIDFile), []byte(session+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(laneDir, endingCapabilityFile), []byte(run.EndingFooter+"\n"), 0o644)
}

func nextRunID(ctx context.Context, s *store.Store, root, prefix, parent string) (string, error) {
	base := fmt.Sprintf("%s-%s-", prefix, parent)
	var existing int
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var runCount, evCount int
		if err := r.QueryRow(`SELECT count(*) FROM runs WHERE item_id=? AND run_id LIKE ?`, parent, base+"%").Scan(&runCount); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE object_type='item' AND object_id=? AND kind IN (?,?,?,?)`, parent, StartedKind, FindingKind, RefusedKind, HeldKind).Scan(&evCount); err != nil {
			return err
		}
		if runCount > evCount {
			existing = runCount
		} else {
			existing = evCount
		}
		return nil
	}); err != nil {
		return "", err
	}
	for seq := existing + 1; ; seq++ {
		candidate := fmt.Sprintf("%s%d", base, seq)
		var taken bool
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, candidate).Scan(&taken)
		}); err != nil {
			return "", err
		}
		if !taken {
			if _, statErr := os.Stat(drainmodel.RunDir(root, candidate)); errors.Is(statErr, os.ErrNotExist) {
				return candidate, nil
			}
		}
	}
}

func readInputs(r *store.Reader, parent string) (words []byte, criteria []Criterion, statement *item.SplitStatement, landings []item.SplitPartLanding, err error) {
	words, err = item.RequestWords(r, parent)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, parent)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var c Criterion
		if err := rows.Scan(&c.ID, &c.Text, &c.ClaimClass); err != nil {
			rows.Close()
			return nil, nil, nil, nil, err
		}
		c.Hash = item.CriterionHash(c.Text)
		criteria = append(criteria, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, nil, nil, err
	}
	rows.Close()
	statement, _, err = item.ReadSplit(r, parent)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	landings, err = item.ReadSplitPartLandings(r, parent)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return words, criteria, statement, landings, nil
}

func FormatClosingBrief(parent, runID, target, head, tree, evalOut, seats string, words []byte, wordsSuffix func([]byte) string, criteria []Criterion, statement *item.SplitStatement, landings []item.SplitPartLanding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Split Closing Check\n\n## Header\nItem: %s; run: %s; seat: review; node: %s; target: %s (read-only)\n\n", parent, runID, item.SplitClosingNode, target)
	fmt.Fprintf(&b, "## Task\nAct as an independent read-only verifier judging the split parent's criteria against the trunk.\n")
	fmt.Fprintf(&b, "Write your JSON verdict to %s.\n", evalOut)
	fmt.Fprintf(&b, "The object must have evaluated_head=%q, evaluated_tree=%q, outcome (\"pass\" or \"fail\"), and verdicts covering every criterion below.\n", head, tree)
	fmt.Fprintf(&b, "Each entry has criterion_id, criterion_hash, verdict (done, not-done, blocked, defective), claim_class, reason, and evidence (non-empty single-token locator strings).\n")
	fmt.Fprintf(&b, "Each not-done entry must include a non-empty findings array (kind: gap|regression|unasked-change, case, paths).\n")
	fmt.Fprintf(&b, "A blocked entry also needs waiting_on (waiter: owner|design|plan|item:<id>).\n")
	fmt.Fprintf(&b, "Pass requires every criterion done.\n\n")
	fmt.Fprintf(&b, "## Scope and boundaries\nDo not commit, edit the worktree, modify git refs, or write the store. %s is your only deliverable.\n\n", evalOut)
	fmt.Fprintf(&b, "## Seats\nThese are seats tillr runs as nodes; a worker cannot start one.\n\n%s\n\n", seats)
	fmt.Fprintf(&b, "## Settled decisions\nThe parent item criteria are the bar; do not lower it.\n\n")
	fmt.Fprintf(&b, "## Facts carried\n\n### Parent Criteria\n")
	for _, c := range criteria {
		fmt.Fprintf(&b, "- %s (%s, hash=%s): %s\n", c.ID, c.ClaimClass, c.Hash, c.Text)
	}
	fmt.Fprintf(&b, "\n### Split Coverage Table\n")
	covRaw, _ := json.MarshalIndent(statement.Coverage, "", "  ")
	b.Write(covRaw)
	b.WriteString("\n\n### Split Parts and Landed Commits\n")
	for _, p := range landings {
		fmt.Fprintf(&b, "- %s (%s): required=%t state=%s commit=%s\n", p.Label, p.ItemID, p.Required, p.State, p.Commit)
	}
	fmt.Fprintf(&b, "\n## Method\nJudge every parent criterion against the trunk (evaluated_head=%s, evaluated_tree=%s) and the landed parts' commits.\n", head, tree)
	fmt.Fprintf(&b, "Pass requires every criterion done; each not-done entry must include non-empty findings.\n\n")
	fmt.Fprintf(&b, "## Verification commands\nRun the checks you need yourself against the trunk (%s). Record your own command and result as evidence in the verdict.\n\n", head)
	fmt.Fprintf(&b, "## Report shape\nWrite the verdict JSON to %s, then report RESULT: done.\n\n", evalOut)
	fmt.Fprintf(&b, "## Stop conditions\nDo not edit the repository, commit, or write the store. If a criterion cannot be settled without an owner decision, mark it blocked with waiting_on.waiter = \"owner\".\n")
	if len(words) > 0 && wordsSuffix != nil {
		b.WriteString(wordsSuffix(words))
	}
	return b.String()
}

func FormatExtensionBrief(parent, runID, target, outPath, seats string, words []byte, wordsSuffix func([]byte) string, criteria []Criterion, statement *item.SplitStatement, landings []item.SplitPartLanding, finding *FindingPayload, priorRefusal *RefusedPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Split Closing Extension\n\n## Header\nItem: %s; run: %s; seat: design; node: split-closing-extend; target: %s (read-only)\n\n", parent, runID, target)
	fmt.Fprintf(&b, "## Task\nThe closing check on split parent %s found missing work after all required parts landed.\n", parent)
	fmt.Fprintf(&b, "Write a SplitExtension JSON object to %s adding at least one new required part.\n\n", outPath)
	fmt.Fprintf(&b, "## Scope and boundaries\nDo not commit, edit the worktree, modify git refs, or write the store. %s is your only deliverable.\n\n", outPath)
	fmt.Fprintf(&b, "## Seats\nThese are seats tillr runs as nodes; a worker cannot start one.\n\n%s\n\n", seats)
	fmt.Fprintf(&b, "## Settled decisions\n- A split statement only grows: NEVER remove or modify any existing part.\n")
	fmt.Fprintf(&b, "- Add at least one new required part (required: true) with a unique label, title, and share.\n")
	fmt.Fprintf(&b, "- Include optional updated coverage if needed.\n\n")
	fmt.Fprintf(&b, "## Facts carried\n\n### Closing-Check Findings (trunk=%s, run=%s)\n", finding.TrunkSHA, finding.RunID)
	for _, f := range finding.Findings {
		fmt.Fprintf(&b, "- %s: %s\n", f.CriterionID, f.Reason)
	}
	if priorRefusal != nil && strings.TrimSpace(priorRefusal.Reason) != "" {
		fmt.Fprintf(&b, "\n### Prior Extension Refusal\nYour previous extension was refused for: %s\nFix this reason in the new output.\n", priorRefusal.Reason)
	}
	fmt.Fprintf(&b, "\n### Parent Criteria\n")
	for _, c := range criteria {
		fmt.Fprintf(&b, "- %s (%s, hash=%s): %s\n", c.ID, c.ClaimClass, c.Hash, c.Text)
	}
	fmt.Fprintf(&b, "\n### Existing Split Statement\n")
	stmtRaw, _ := json.MarshalIndent(statement, "", "  ")
	b.Write(stmtRaw)
	b.WriteString("\n\n### Landed Part Commits\n")
	for _, p := range landings {
		fmt.Fprintf(&b, "- %s (%s): required=%t state=%s commit=%s\n", p.Label, p.ItemID, p.Required, p.State, p.Commit)
	}
	fmt.Fprintf(&b, "\n## Method\nReview the closing-check findings against the parent's criteria, existing split statement, and landed part commits.\n")
	fmt.Fprintf(&b, "Write a SplitExtension JSON object matching this schema:\n```json\n{\n  \"parts\": [\n    {\n      \"label\": \"new-part-label\",\n      \"title\": \"Short title\",\n      \"share\": \"What this new part implements\",\n      \"leaves\": {},\n      \"criteria\": [\"Criterion text for this part\"],\n      \"follows\": [],\n      \"required\": true\n    }\n  ],\n  \"coverage\": {}\n}\n```\n\n")
	fmt.Fprintf(&b, "## Verification commands\nNone required; verify that your JSON output matches the SplitExtension schema and preserves all existing parts.\n\n")
	fmt.Fprintf(&b, "## Report shape\nWrite the SplitExtension JSON to %s, then report RESULT: done.\n\n", outPath)
	fmt.Fprintf(&b, "## Stop conditions\nDo not edit the repository, commit, or write the store.\n")
	if len(words) > 0 && wordsSuffix != nil {
		b.WriteString(wordsSuffix(words))
	}
	return b.String()
}

// CheckSplitJSONSurrogates walks escapes before any JSON decoding can discard
// their original spelling. The JSON decoder still checks all syntax.
func CheckSplitJSONSurrogates(raw []byte) error {
	codeUnit := func(b []byte) rune {
		if len(b) < 6 || b[0] != '\\' || b[1] != 'u' {
			return -1
		}
		u, err := strconv.ParseUint(string(b[2:6]), 16, 16)
		if err != nil {
			return -1
		}
		return rune(u)
	}
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		u := codeUnit(raw[i:])
		if !utf16.IsSurrogate(u) {
			i++ // Includes an escaped backslash: its following 'u' is literal.
			continue
		}
		v := codeUnit(raw[i+6:])
		if u >= 0xdc00 || v < 0xdc00 || v > 0xdfff {
			return fmt.Errorf("unpaired surrogate escape at byte %d", i+1)
		}
		i += 11 // Skip both six-byte escapes, including the low surrogate.
	}
	return nil
}

// UniqueJSON checks that no JSON object at any depth has duplicate keys.
func UniqueJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("expected object key")
				}
				if keys[name] {
					return fmt.Errorf("duplicate key %q", name)
				}
				keys[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected delimiter %s", delim)
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func FormatDefectiveBrief(parent, runID, target, outPath, seats string, words []byte, wordsSuffix func([]byte) string, criteria []Criterion, statement *item.SplitStatement, landings []item.SplitPartLanding, defectiveParts []item.DefectivePartFindings, priorRefusal *RefusedPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Split Defective Part Repair\n\n## Header\nItem: %s; run: %s; seat: design; node: split-defective-repair; target: %s (read-only)\n\n", parent, runID, target)
	fmt.Fprintf(&b, "## Task\nOne or more required parts of split parent %s were closed as defective by a verifier.\n", parent)
	fmt.Fprintf(&b, "Write either a replacement SplitExtension JSON object or an owner_decision JSON object to %s.\n\n", outPath)
	fmt.Fprintf(&b, "## Scope and boundaries\nDo not commit, edit the worktree, modify git refs, or write the store. %s is your only deliverable.\n\n", outPath)
	fmt.Fprintf(&b, "## Seats\nThese are seats tillr runs as nodes; a worker cannot start one.\n\n%s\n\n", seats)
	fmt.Fprintf(&b, "## Settled decisions\n- A split statement keeps closed parts in history (marked replaced_by) without deleting them.\n")
	fmt.Fprintf(&b, "- When replacing a defective required part, set \"replaces\" to the defective part label(s), add at least one new required part (required: true) with a unique label, title, and share, and update \"coverage\" so no parent criterion or clause still points to a replaced part.\n")
	fmt.Fprintf(&b, "- Only if the verifier's finding exposes a problem in the parent's request or requires dropping/changing what the owner asked for, output {\"owner_decision\": true, \"question\": \"...\"} instead.\n\n")
	fmt.Fprintf(&b, "## Facts carried\n\n### Defective Required Part Findings\n")
	for _, dp := range defectiveParts {
		fmt.Fprintf(&b, "- Part %s: close_reason=%q closed_by=%q\n", dp.PartID, dp.CloseReason, dp.ClosedBy)
		for _, v := range dp.Verdicts {
			fmt.Fprintf(&b, "  - %s (defective): %s\n", v.CriterionID, v.Reason)
			for _, f := range v.Findings {
				fmt.Fprintf(&b, "    - finding [%s]: %s (%s)\n", f.Kind, f.Case, strings.Join(f.Paths, ", "))
			}
		}
	}
	if priorRefusal != nil && strings.TrimSpace(priorRefusal.Reason) != "" {
		fmt.Fprintf(&b, "\n### Prior Extension Refusal\nYour previous extension was refused for: %s\nFix this reason in the new output.\n", priorRefusal.Reason)
	}
	fmt.Fprintf(&b, "\n### Parent Criteria\n")
	for _, c := range criteria {
		fmt.Fprintf(&b, "- %s (%s, hash=%s): %s\n", c.ID, c.ClaimClass, c.Hash, c.Text)
	}
	fmt.Fprintf(&b, "\n### Existing Split Statement\n")
	stmtRaw, _ := json.MarshalIndent(statement, "", "  ")
	b.Write(stmtRaw)
	b.WriteString("\n\n### Split Parts Status\n")
	for _, p := range landings {
		fmt.Fprintf(&b, "- %s (%s): required=%t state=%s commit=%s replaced_by=%v\n", p.Label, p.ItemID, p.Required, p.State, p.Commit, p.ReplacedBy)
	}
	fmt.Fprintf(&b, "\n## Method\nReview the verifier's defective finding against the parent's criteria and existing split statement.\n")
	fmt.Fprintf(&b, "Write a SplitExtension JSON object matching this schema:\n```json\n{\n  \"replaces\": [\"defective-part-label\"],\n  \"parts\": [\n    {\n      \"label\": \"corrected-part-label\",\n      \"title\": \"Short title\",\n      \"share\": \"What this corrected part implements\",\n      \"leaves\": {},\n      \"criteria\": [\"Criterion text for this part\"],\n      \"follows\": [],\n      \"required\": true\n    }\n  ],\n  \"coverage\": {}\n}\n```\nOr, if only the owner can decide:\n```json\n{\n  \"owner_decision\": true,\n  \"question\": \"Specific question for the owner\"\n}\n```\n\n")
	fmt.Fprintf(&b, "## Verification commands\nNone required; verify that your JSON output matches one of the schemas above.\n\n")
	fmt.Fprintf(&b, "## Report shape\nWrite the JSON output to %s, then report RESULT: done.\n\n", outPath)
	fmt.Fprintf(&b, "## Stop conditions\nDo not edit the repository, commit, or write the store.\n")
	if len(words) > 0 && wordsSuffix != nil {
		b.WriteString(wordsSuffix(words))
	}
	return b.String()
}

func checkExtensionJSONKeys(raw []byte) error {
	var shape struct {
		Replaces []json.RawMessage                       `json:"replaces"`
		Parts    []map[string]json.RawMessage            `json:"parts"`
		Coverage map[string][]map[string]json.RawMessage `json:"coverage"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	keys := func(fields map[string]json.RawMessage, allowed ...string) error {
		for key := range fields {
			found := false
			for _, a := range allowed {
				if key == a {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown field %q", key)
			}
		}
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	if err := keys(top, "replaces", "parts", "coverage"); err != nil {
		return err
	}
	for _, part := range shape.Parts {
		if err := keys(part, "label", "title", "share", "leaves", "criteria", "follows", "required", "takes", "paths", "shared_files", "inherited_findings"); err != nil {
			return err
		}
	}
	for _, entries := range shape.Coverage {
		for _, entry := range entries {
			if err := keys(entry, "clause", "part", "closing_check"); err != nil {
				return err
			}
		}
	}
	return nil
}

type ownerDecisionOutput struct {
	OwnerDecision bool   `json:"owner_decision"`
	Question      string `json:"question"`
}

func parseExtensionOrOwnerDecision(raw []byte) (item.SplitExtension, *ownerDecisionOutput, error) {
	if !utf8.Valid(raw) {
		return item.SplitExtension{}, nil, fmt.Errorf("invalid split extension: input must be UTF-8")
	}
	if err := CheckSplitJSONSurrogates(raw); err != nil {
		return item.SplitExtension{}, nil, fmt.Errorf("invalid split extension: %v", err)
	}
	if err := UniqueJSON(raw); err != nil {
		return item.SplitExtension{}, nil, fmt.Errorf("invalid split extension: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err == nil {
		if _, hasOD := top["owner_decision"]; hasOD {
			var od ownerDecisionOutput
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&od); err != nil {
				return item.SplitExtension{}, nil, fmt.Errorf("invalid owner_decision: %v", err)
			}
			if _, err := dec.Token(); err != io.EOF {
				return item.SplitExtension{}, nil, fmt.Errorf("invalid owner_decision: expected a single JSON object")
			}
			if !od.OwnerDecision {
				return item.SplitExtension{}, nil, fmt.Errorf("invalid owner_decision: owner_decision must be true")
			}
			if strings.TrimSpace(od.Question) == "" {
				return item.SplitExtension{}, nil, fmt.Errorf("invalid owner_decision: question must not be empty")
			}
			return item.SplitExtension{}, &od, nil
		}
	}
	ext, err := parseExtensionDirect(raw)
	if err != nil {
		return item.SplitExtension{}, nil, err
	}
	return ext, nil, nil
}

func parseExtension(raw []byte) (item.SplitExtension, error) {
	if !utf8.Valid(raw) {
		return item.SplitExtension{}, fmt.Errorf("invalid split extension: input must be UTF-8")
	}
	if err := CheckSplitJSONSurrogates(raw); err != nil {
		return item.SplitExtension{}, fmt.Errorf("invalid split extension: %v", err)
	}
	if err := UniqueJSON(raw); err != nil {
		return item.SplitExtension{}, fmt.Errorf("invalid split extension: %v", err)
	}
	return parseExtensionDirect(raw)
}

func parseExtensionDirect(raw []byte) (item.SplitExtension, error) {
	var ext item.SplitExtension
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ext); err != nil {
		return item.SplitExtension{}, fmt.Errorf("invalid split extension: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return item.SplitExtension{}, fmt.Errorf("invalid split extension: expected a single JSON object")
	}
	if err := checkExtensionJSONKeys(raw); err != nil {
		return item.SplitExtension{}, fmt.Errorf("invalid split extension: %v", err)
	}
	return ext, nil
}

func WriteAtomicJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func Advance(ctx context.Context, env Env, mainSHA string) (AdvanceResult, error) {
	if env.NonLanding {
		return AdvanceResult{}, nil
	}
	s, err := store.Open(ctx, env.Store, false)
	if err != nil {
		return AdvanceResult{}, err
	}
	defer s.Close()

	var parents []string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(
			`SELECT item_id FROM items WHERE state=? AND json_valid(waiting_on) AND json_extract(waiting_on, '$.source')=? ORDER BY item_id`,
			drainmodel.StateBlocked, item.SplitWaitSource,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			parents = append(parents, id)
		}
		return rows.Err()
	}); err != nil {
		return AdvanceResult{}, err
	}
	if len(parents) == 0 {
		return AdvanceResult{}, nil
	}

	var out AdvanceResult
	for _, parent := range parents {
		var ready bool
		var defectiveParts []item.DefectivePartFindings
		var st State
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			required, err := item.SplitRequiredParts(r, parent)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
			allLanded := true
			for _, partID := range required {
				var partState, closeReason string
				if err := r.QueryRow(`SELECT state, close_reason FROM items WHERE item_id=?`, partID).Scan(&partState, &closeReason); err != nil {
					if strings.Contains(err.Error(), "no such column: close_reason") {
						if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, partID).Scan(&partState); err != nil {
							return err
						}
					} else {
						return err
					}
				}
				if item.IsDefectiveClose(partState, closeReason) {
					findings, err := item.ReadDefectivePartFindings(r, partID)
					if err != nil {
						return err
					}
					defectiveParts = append(defectiveParts, findings)
				}
				if partState != drainmodel.StateLanded {
					allLanded = false
				}
			}
			if len(defectiveParts) == 0 && !allLanded {
				return nil
			}
			st, err = ReadState(r, parent)
			if err != nil {
				return err
			}
			if st.Held == nil {
				ready = true
			}
			return nil
		}); err != nil {
			return out, err
		}
		if !ready {
			continue
		}
		if st.Started != nil {
			res, state, err := env.CheckRun(ctx, st.Started.RunID)
			if err != nil {
				return out, err
			}
			if state == RunStateRunning {
				out.Pending = true
				continue
			}
			var settled bool
			if st.Started.Kind == RunKindCheck {
				settled, err = settleCheck(ctx, env, s, parent, *st.Started, res)
			} else {
				settled, err = settleExtend(ctx, env, s, parent, st, *st.Started, res)
			}
			if err != nil {
				return out, err
			}
			if env.ConsumeRun != nil {
				env.ConsumeRun(st.Started.RunID)
			}
			if settled {
				out.Worked = true
			}
			continue
		}
		if env.CanStart != nil && !env.CanStart(ctx) {
			continue
		}
		if len(defectiveParts) > 0 {
			w, err := startExtendWithDefective(ctx, env, s, parent, st, defectiveParts)
			if w {
				out.Worked = true
				out.Pending = true
			}
			if err != nil {
				return out, err
			}
			continue
		}
		if st.Finding == nil {
			w, err := startCheck(ctx, env, s, parent, mainSHA, st.StatementHash)
			if w {
				out.Worked = true
				out.Pending = true
			}
			if err != nil {
				return out, err
			}
		} else {
			w, err := startExtendWithDefective(ctx, env, s, parent, st, nil)
			if w {
				out.Worked = true
				out.Pending = true
			}
			if err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

func appendStarted(ctx context.Context, storePath, parent string, payload StartedPayload, at time.Time) error {
	ws, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer ws.Close()
	return ws.WriteTx(ctx, func(tx *store.Tx) error {
		return runhistory.AppendEffectOn(tx, StartedKind, "item", parent, payload, at)
	})
}

func startCheck(ctx context.Context, env Env, s *store.Store, parent, mainSHA, stmtHash string) (bool, error) {
	sel, ok := env.SelectSeats("review")
	if !ok || len(sel.Backends) == 0 {
		return false, nil
	}
	if mainSHA == "" {
		var err error
		mainSHA, err = env.RevParse(ctx, env.Root, env.TrunkRef)
		if err != nil {
			return false, err
		}
	}
	trunkTree, err := env.RevParse(ctx, env.Root, mainSHA+"^{tree}")
	if err != nil {
		return false, err
	}
	runID, err := nextRunID(ctx, s, env.Root, "closing", parent)
	if err != nil {
		return false, err
	}
	laneID, laneDir, durableDir := Dirs(env.Root, env.LaneRoot, env.LaneDir, runID)
	evalOut := filepath.Join(durableDir, "eval.output")

	var words []byte
	var criteria []Criterion
	var statement *item.SplitStatement
	var landings []item.SplitPartLanding
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var readErr error
		words, criteria, statement, landings, readErr = readInputs(r, parent)
		return readErr
	}); err != nil {
		return false, err
	}

	brief := FormatClosingBrief(parent, runID, env.Root, mainSHA, trunkTree, evalOut, sel.SeatLines, words, env.RequestWordsSuffix, criteria, statement, landings)
	if err := StageBriefFiles(env.Root, runID, laneDir, durableDir, brief, env.BraceLiteralCount(brief)); err != nil {
		return false, err
	}

	req := sel.Request(parent, laneID, runID, sel.Backends[0])
	req.Seat = "review"
	req.Workflow = item.SplitClosingWorkflow
	req.NodeID = item.SplitClosingNode
	req.Node = item.SplitClosingNode

	if err := appendStarted(ctx, env.Store, parent, StartedPayload{
		ItemID:        parent,
		RunID:         runID,
		Kind:          RunKindCheck,
		StatementHash: stmtHash,
		TrunkSHA:      mainSHA,
		TrunkTree:     trunkTree,
		LaneID:        laneID,
		OutputPath:    evalOut,
		Route:         req.Route(),
		Seat:          req.Seat,
		Workflow:      req.Workflow,
		NodeID:        req.NodeID,
		Backend:       req.Backend,
		Landings:      landings,
	}, env.Now().UTC()); err != nil {
		return false, err
	}
	if err := env.StartRun(ctx, req); err != nil {
		return false, err
	}
	return true, nil
}

func settleCheck(ctx context.Context, env Env, s *store.Store, parent string, started StartedPayload, res RunResult) (bool, error) {
	runID := started.RunID
	stmtHash := started.StatementHash
	mainSHA := started.TrunkSHA
	trunkTree := started.TrunkTree
	evalOut := started.OutputPath
	landings := started.Landings
	rec := res.Record
	runErr := res.Err

	env.CountSpend(rec)

	hold := func(reason, mover string) (bool, error) {
		if err := storelog.AppendJudgment(ctx, env.Store, HeldKind, "item", parent, HeldPayload{
			ItemID:        parent,
			RunID:         runID,
			StatementHash: stmtHash,
			Reason:        reason,
			Mover:         mover,
		}, env.Now().UTC()); err != nil {
			return false, err
		}
		return true, nil
	}

	if runErr != nil || rec.State == run.StateTimedOut || rec.EndCause == run.EndTimeLimit || (rec.ExitCode != nil && *rec.ExitCode != 0) {
		reason := fmt.Sprintf("closing check run %s failed", runID)
		if runErr != nil {
			reason = fmt.Sprintf("closing check run %s failed: %v", runID, runErr)
		} else if rec.ExitCode != nil {
			reason = fmt.Sprintf("closing check run %s exited with code %d", runID, *rec.ExitCode)
		}
		return hold(reason, "review")
	}

	rawEval, err := os.ReadFile(evalOut)
	if err != nil {
		return hold(fmt.Sprintf("closing check %s missing eval.output: %v", runID, err), "review")
	}

	var checked item.JudgeResult
	var judgeOpts item.JudgeOptions
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		binding, err := item.SplitClosingEvaluatorBinding(r, parent)
		if err != nil {
			return err
		}
		judgeOpts = item.JudgeOptions{
			EvaluatorBinding: binding,
			ItemID:           parent,
			Head:             mainSHA,
			Tree:             trunkTree,
			Actor:            "run=" + runID + ";route=" + started.Route,
			JudgedActor:      "split-parts:" + parent,
			At:               env.Now().UTC(),
		}
		checked, err = item.CheckJudge(ctx, r, rawEval, judgeOpts)
		return err
	}); err != nil {
		return hold(fmt.Sprintf("closing check %s invalid verdict: %v", runID, err), "review")
	}

	switch checked.Outcome {
	case "pass":
		recordPath := filepath.Join(drainmodel.RunDir(env.Root, runID), "closing-check.json")
		recordBody := Record{
			ItemID:    parent,
			TrunkSHA:  mainSHA,
			TrunkTree: trunkTree,
			RunID:     runID,
			Reason:    "split closing check passed",
			Landed:    false,
			Parts:     landings,
		}
		if err := WriteAtomicJSON(recordPath, recordBody); err != nil {
			return false, err
		}
		ws, err := store.Open(ctx, env.Store, true)
		if err != nil {
			return false, err
		}
		defer ws.Close()
		if err := ws.WriteTx(ctx, func(tx *store.Tx) error {
			var settled bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind='drain.graph-completed' AND object_id=?)`, runID).Scan(&settled); err != nil {
				return err
			}
			if settled {
				return nil
			}
			if _, err := item.RecordJudge(ctx, tx, rawEval, judgeOpts); err != nil {
				return err
			}
			return runhistory.AppendEffect(tx, "drain.graph-completed", runID, map[string]any{
				"item_id":   parent,
				"landed":    false,
				"reason":    "split closing check passed",
				"trunk_sha": mainSHA,
				"parts":     landings,
				"record":    recordPath,
			}, env.Now().UTC())
		}); err != nil {
			return false, err
		}
		if err := env.SetItemState(ctx, parent, runID, drainmodel.StateLanded); err != nil {
			return false, err
		}
		return true, nil

	case item.VerdictNotDone:
		var entries []FindingEntry
		var reasons []string
		for _, v := range checked.Output.Verdicts {
			if v.Verdict == item.VerdictNotDone {
				entries = append(entries, FindingEntry{
					CriterionID: v.CriterionID,
					Reason:      v.Reason,
					Findings:    v.Findings,
				})
				reasons = append(reasons, fmt.Sprintf("%s: %s", v.CriterionID, v.Reason))
			}
		}
		if err := storelog.AppendJudgment(ctx, env.Store, FindingKind, "item", parent, FindingPayload{
			ItemID:        parent,
			RunID:         runID,
			StatementHash: stmtHash,
			TrunkSHA:      mainSHA,
			Findings:      entries,
			Reasons:       reasons,
		}, env.Now().UTC()); err != nil {
			return false, err
		}
		return true, nil

	case item.VerdictDefective:
		var reasons []string
		for _, v := range checked.Output.Verdicts {
			if v.Verdict == item.VerdictDefective {
				reasons = append(reasons, fmt.Sprintf("%s: %s", v.CriterionID, v.Reason))
			}
		}
		return hold("closing check defective: "+strings.Join(reasons, "; "), "owner")

	case item.VerdictBlocked:
		mover := "review"
		var reasons []string
		for _, v := range checked.Output.Verdicts {
			if v.Verdict == item.VerdictBlocked {
				reasons = append(reasons, fmt.Sprintf("%s: %s", v.CriterionID, v.Reason))
				var w struct {
					Waiter string `json:"waiter"`
				}
				if len(v.WaitingOn) > 0 && json.Unmarshal(v.WaitingOn, &w) == nil && w.Waiter == "owner" {
					mover = "owner"
				}
			}
		}
		return hold("closing check blocked: "+strings.Join(reasons, "; "), mover)

	default:
		return hold(fmt.Sprintf("closing check %s unexpected outcome %q", runID, checked.Outcome), "review")
	}
}

func startExtendWithDefective(ctx context.Context, env Env, s *store.Store, parent string, st State, defectiveParts []item.DefectivePartFindings) (bool, error) {
	sel, ok := env.SelectSeats("design")
	if !ok || len(sel.Backends) == 0 {
		return false, nil
	}
	prefix := "closing-extend"
	nodeID := "split-closing-extend"
	kind := RunKindExtend
	if len(defectiveParts) > 0 {
		prefix = "defective-extend"
		nodeID = "split-defective-repair"
		kind = RunKindDefectiveExtend
	}
	runID, err := nextRunID(ctx, s, env.Root, prefix, parent)
	if err != nil {
		return false, err
	}
	laneID, laneDir, durableDir := Dirs(env.Root, env.LaneRoot, env.LaneDir, runID)
	extOut := filepath.Join(durableDir, "split-extension.json")

	var words []byte
	var criteria []Criterion
	var statement *item.SplitStatement
	var landings []item.SplitPartLanding
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var readErr error
		words, criteria, statement, landings, readErr = readInputs(r, parent)
		return readErr
	}); err != nil {
		return false, err
	}

	var brief string
	if len(defectiveParts) > 0 {
		brief = FormatDefectiveBrief(parent, runID, env.Root, extOut, sel.SeatLines, words, env.RequestWordsSuffix, criteria, statement, landings, defectiveParts, st.Refused)
	} else {
		brief = FormatExtensionBrief(parent, runID, env.Root, extOut, sel.SeatLines, words, env.RequestWordsSuffix, criteria, statement, landings, st.Finding, st.Refused)
	}
	if err := StageBriefFiles(env.Root, runID, laneDir, durableDir, brief, env.BraceLiteralCount(brief)); err != nil {
		return false, err
	}

	req := sel.Request(parent, laneID, runID, sel.Backends[0])
	req.Seat = "design"
	req.Workflow = item.SplitClosingWorkflow
	req.NodeID = nodeID
	req.Node = nodeID

	if err := appendStarted(ctx, env.Store, parent, StartedPayload{
		ItemID:         parent,
		RunID:          runID,
		Kind:           kind,
		StatementHash:  st.StatementHash,
		LaneID:         laneID,
		OutputPath:     extOut,
		Route:          req.Route(),
		Seat:           req.Seat,
		Workflow:       req.Workflow,
		NodeID:         req.NodeID,
		Backend:        req.Backend,
		DefectiveParts: defectiveParts,
	}, env.Now().UTC()); err != nil {
		return false, err
	}
	if err := env.StartRun(ctx, req); err != nil {
		return false, err
	}
	return true, nil
}

func settleExtend(ctx context.Context, env Env, s *store.Store, parent string, st State, started StartedPayload, res RunResult) (bool, error) {
	runID := started.RunID
	extOut := started.OutputPath
	defectiveParts := started.DefectiveParts
	rec := res.Record
	runErr := res.Err

	env.CountSpend(rec)

	hold := func(reason, mover string) (bool, error) {
		if err := storelog.AppendJudgment(ctx, env.Store, HeldKind, "item", parent, HeldPayload{
			ItemID:        parent,
			RunID:         runID,
			StatementHash: st.StatementHash,
			Reason:        reason,
			Mover:         mover,
		}, env.Now().UTC()); err != nil {
			return false, err
		}
		return true, nil
	}

	if runErr != nil || rec.State == run.StateTimedOut || rec.EndCause == run.EndTimeLimit || (rec.ExitCode != nil && *rec.ExitCode != 0) {
		reason := fmt.Sprintf("design extension run %s failed", runID)
		if runErr != nil {
			reason = fmt.Sprintf("design extension run %s failed: %v", runID, runErr)
		} else if rec.ExitCode != nil {
			reason = fmt.Sprintf("design extension run %s exited with code %d", runID, *rec.ExitCode)
		}
		return hold(reason, "design")
	}

	rawExt, err := os.ReadFile(extOut)
	if err != nil {
		return hold(fmt.Sprintf("design extension run %s missing split-extension.json: %v", runID, err), "design")
	}

	refuseOrHold := func(reason string) (bool, error) {
		if st.Refused != nil && st.Refused.Reason == reason {
			return hold(reason, "design")
		}
		if err := storelog.AppendJudgment(ctx, env.Store, RefusedKind, "item", parent, RefusedPayload{
			ItemID:        parent,
			RunID:         runID,
			StatementHash: st.StatementHash,
			Reason:        reason,
		}, env.Now().UTC()); err != nil {
			return false, err
		}
		return true, nil
	}

	ext, od, err := parseExtensionOrOwnerDecision(rawExt)
	if err != nil {
		return refuseOrHold(err.Error())
	}
	if od != nil {
		if len(defectiveParts) == 0 {
			return refuseOrHold("owner_decision is only valid when repairing a defective required part")
		}
		return hold(strings.TrimSpace(od.Question), "owner")
	}
	if len(defectiveParts) > 0 && len(ext.Replaces) == 0 {
		return refuseOrHold("defective-part repair must specify replaces naming the defective part label(s)")
	}

	ws, err := store.Open(ctx, env.Store, true)
	if err != nil {
		return false, err
	}
	defer ws.Close()
	if err := ws.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.ExtendSplit(ctx, tx, parent, ext, env.Now().UTC())
		return err
	}); err != nil {
		return refuseOrHold(err.Error())
	}
	return true, nil
}
