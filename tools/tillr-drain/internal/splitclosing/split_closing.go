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
	FindingKind = "drain.split-closing-finding"
	RefusedKind = "drain.split-closing-refused"
	HeldKind    = "drain.split-closing-held"

	briefFile            = "brief.md"
	braceLiteralsFile    = "brace-literals"
	endingCapabilityFile = "ending-capability"
)

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
	Held          *HeldPayload
	Finding       *FindingPayload
	Refused       *RefusedPayload
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
	RunRestartable     func(ctx context.Context, req run.Request) (run.Record, string, error)
	CountSpend         func(rec run.Record)
	IsRestartPending   func(err error) bool
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
		`SELECT kind, payload FROM events WHERE object_type='item' AND object_id=? AND kind IN (?,?,?) ORDER BY seq ASC`,
		parent, FindingKind, RefusedKind, HeldKind,
	)
	if err != nil {
		return State{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			return State{}, err
		}
		switch kind {
		case FindingKind:
			var p FindingPayload
			if err := json.Unmarshal(raw, &p); err == nil && p.StatementHash == stmtHash {
				cp := p
				st.Finding = &cp
			}
		case RefusedKind:
			var p RefusedPayload
			if err := json.Unmarshal(raw, &p); err == nil && p.StatementHash == stmtHash {
				cp := p
				st.Refused = &cp
			}
		case HeldKind:
			var p HeldPayload
			if err := json.Unmarshal(raw, &p); err == nil && p.StatementHash == stmtHash {
				cp := p
				st.Held = &cp
			}
		}
	}
	if err := rows.Err(); err != nil {
		return State{}, err
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
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE object_type='item' AND object_id=? AND kind IN (?,?,?)`, parent, FindingKind, RefusedKind, HeldKind).Scan(&evCount); err != nil {
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

func checkExtensionJSONKeys(raw []byte) error {
	var shape struct {
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
	if err := keys(top, "parts", "coverage"); err != nil {
		return err
	}
	for _, part := range shape.Parts {
		if err := keys(part, "label", "title", "share", "leaves", "criteria", "follows", "required"); err != nil {
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

func Advance(ctx context.Context, env Env, mainSHA string) (bool, error) {
	if env.NonLanding {
		return false, nil
	}
	s, err := store.Open(ctx, env.Store, false)
	if err != nil {
		return false, err
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
		return false, err
	}
	if len(parents) == 0 {
		return false, nil
	}

	worked := false
	for _, parent := range parents {
		var ready bool
		var st State
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			required, err := item.SplitRequiredParts(r, parent)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
			for _, partID := range required {
				var partState string
				if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, partID).Scan(&partState); err != nil {
					return err
				}
				if partState != drainmodel.StateLanded {
					return nil
				}
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
			return worked, err
		}
		if !ready {
			continue
		}
		if st.Finding == nil {
			w, err := runCheck(ctx, env, s, parent, mainSHA, st.StatementHash)
			if w {
				worked = true
			}
			if err != nil {
				return worked, err
			}
		} else {
			w, err := runExtend(ctx, env, s, parent, st)
			if w {
				worked = true
			}
			if err != nil {
				return worked, err
			}
		}
	}
	return worked, nil
}

func runCheck(ctx context.Context, env Env, s *store.Store, parent, mainSHA, stmtHash string) (bool, error) {
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

	rec, _, runErr := env.RunRestartable(ctx, req)
	if env.IsRestartPending(runErr) {
		return false, runErr
	}
	if ctx.Err() != nil && runErr != nil {
		return false, ctx.Err()
	}
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
			Actor:            "run=" + runID + ";route=" + req.Route(),
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

func runExtend(ctx context.Context, env Env, s *store.Store, parent string, st State) (bool, error) {
	sel, ok := env.SelectSeats("design")
	if !ok || len(sel.Backends) == 0 {
		return false, nil
	}
	runID, err := nextRunID(ctx, s, env.Root, "closing-extend", parent)
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

	brief := FormatExtensionBrief(parent, runID, env.Root, extOut, sel.SeatLines, words, env.RequestWordsSuffix, criteria, statement, landings, st.Finding, st.Refused)
	if err := StageBriefFiles(env.Root, runID, laneDir, durableDir, brief, env.BraceLiteralCount(brief)); err != nil {
		return false, err
	}

	req := sel.Request(parent, laneID, runID, sel.Backends[0])
	req.Seat = "design"
	req.Workflow = item.SplitClosingWorkflow
	req.NodeID = "split-closing-extend"
	req.Node = "split-closing-extend"

	rec, _, runErr := env.RunRestartable(ctx, req)
	if env.IsRestartPending(runErr) {
		return false, runErr
	}
	if ctx.Err() != nil && runErr != nil {
		return false, ctx.Err()
	}
	env.CountSpend(rec)

	hold := func(reason string) (bool, error) {
		if err := storelog.AppendJudgment(ctx, env.Store, HeldKind, "item", parent, HeldPayload{
			ItemID:        parent,
			RunID:         runID,
			StatementHash: st.StatementHash,
			Reason:        reason,
			Mover:         "design",
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
		return hold(reason)
	}

	rawExt, err := os.ReadFile(extOut)
	if err != nil {
		return hold(fmt.Sprintf("design extension run %s missing split-extension.json: %v", runID, err))
	}

	refuseOrHold := func(reason string) (bool, error) {
		if st.Refused != nil && st.Refused.Reason == reason {
			return hold(reason)
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

	ext, err := parseExtension(rawExt)
	if err != nil {
		return refuseOrHold(err.Error())
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
