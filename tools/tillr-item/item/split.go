package item

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	SplitTakesNothing = "nothing"
	SplitTakesWhole   = "whole"
	SplitTakesNamed   = "named"
)

// SplitDonor identifies the refused candidate from which split parts take work.
type SplitDonor struct {
	RunID   string `json:"run_id,omitempty"`
	Ref     string `json:"ref,omitempty"`
	SHA     string `json:"sha"`
	BaseSHA string `json:"base_sha,omitempty"`
}

// SplitStatement is the design seat's division of a request.
type SplitStatement struct {
	Donor    *SplitDonor                `json:"donor,omitempty"`
	Parts    []SplitPart                `json:"parts"`
	Coverage map[string][]SplitCoverage `json:"coverage"`
}

type SplitPart struct {
	Label             string            `json:"label"`
	Title             string            `json:"title"`
	Share             string            `json:"share"`
	Leaves            map[string]string `json:"leaves,omitempty"`
	Criteria          []string          `json:"criteria,omitempty"`
	Follows           []string          `json:"follows,omitempty"`
	Required          *bool             `json:"required"`
	Takes             string            `json:"takes,omitempty"`
	Paths             []string          `json:"paths,omitempty"`
	SharedFiles       []string          `json:"shared_files,omitempty"`
	InheritedFindings []string          `json:"inherited_findings,omitempty"`
	ReplacedBy        []string          `json:"replaced_by,omitempty"`
}

// An empty Clause names the whole criterion. Otherwise quoted clauses exhaust
// the stored text in order and meet at explicit punctuation or line boundaries.
type SplitCoverage struct {
	Clause       string `json:"clause,omitempty"`
	Part         string `json:"part,omitempty"`
	ClosingCheck bool   `json:"closing_check,omitempty"`
}

const (
	SplitWaitSource      = "split"
	SplitGapKind         = "item.split-gap"
	SplitClosingWorkflow = "split-closing-check"
	SplitClosingNode     = "closing-check"
)

type SplitPartLanding struct {
	Label      string   `json:"label"`
	ItemID     string   `json:"item_id"`
	Required   bool     `json:"required"`
	State      string   `json:"state"`
	Commit     string   `json:"commit,omitempty"`
	ReplacedBy []string `json:"replaced_by,omitempty"`
}

type SplitExtension struct {
	Replaces []string                   `json:"replaces,omitempty"`
	Parts    []SplitPart                `json:"parts"`
	Coverage map[string][]SplitCoverage `json:"coverage,omitempty"`
}

// IsDefectiveClose reports whether an item state and close_reason represent a
// verifier-issued defective closure.
func IsDefectiveClose(state, closeReason string) bool {
	return state == "closed" && strings.HasPrefix(closeReason, "defective:")
}

type DefectiveVerdict struct {
	CriterionID string          `json:"criterion_id"`
	Reason      string          `json:"reason"`
	Findings    VerdictFindings `json:"findings,omitempty"`
}

type DefectivePartFindings struct {
	PartID      string             `json:"part_id"`
	CloseReason string             `json:"close_reason"`
	ClosedBy    string             `json:"closed_by"`
	Verdicts    []DefectiveVerdict `json:"verdicts,omitempty"`
}

// ReadDefectivePartFindings reads the close_reason, closed_by, and any active
// defective criterion verdicts recorded for partID.
func ReadDefectivePartFindings(r *store.Reader, partID string) (DefectivePartFindings, error) {
	var out DefectivePartFindings
	out.PartID = partID
	if err := r.QueryRow(`SELECT close_reason, closed_by FROM items WHERE item_id=?`, partID).Scan(&out.CloseReason, &out.ClosedBy); err != nil {
		return DefectivePartFindings{}, err
	}
	rows, err := r.Query(
		`SELECT kind, payload FROM events WHERE object_type='item' AND object_id=? AND kind IN (?,?) ORDER BY seq ASC`,
		partID, VerdictRecordKind, VerdictsWithdrawnKind,
	)
	if err != nil {
		return DefectivePartFindings{}, err
	}
	defer rows.Close()
	latest := map[string]verdictPayload{}
	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			return DefectivePartFindings{}, err
		}
		switch kind {
		case VerdictsWithdrawnKind:
			clear(latest)
		case VerdictRecordKind:
			var v verdictPayload
			if err := json.Unmarshal(raw, &v); err != nil {
				return DefectivePartFindings{}, err
			}
			latest[v.CriterionID] = v
		}
	}
	if err := rows.Err(); err != nil {
		return DefectivePartFindings{}, err
	}
	ids := make([]string, 0, len(latest))
	for id, v := range latest {
		if v.Verdict == VerdictDefective {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		v := latest[id]
		out.Verdicts = append(out.Verdicts, DefectiveVerdict{
			CriterionID: v.CriterionID,
			Reason:      v.Reason,
			Findings:    v.Findings,
		})
	}
	return out, nil
}

// SplitSibling describes a sibling part in a filed split statement.
type SplitSibling struct {
	Label    string `json:"label"`
	ItemID   string `json:"item_id"`
	Title    string `json:"title"`
	Share    string `json:"share"`
	Required bool   `json:"required"`
	State    string `json:"state"`
}

// SplitPartInfo describes a split part's parent, donor, own part definition, and sibling parts.
type SplitPartInfo struct {
	ParentID string                  `json:"parent_id"`
	Donor    *SplitDonor             `json:"donor,omitempty"`
	Part     SplitPart               `json:"part"`
	Siblings map[string]SplitSibling `json:"siblings"`
}

// SplitPartContext returns split context when itemID is a filed split part, or nil, nil otherwise.
func SplitPartContext(r *store.Reader, itemID string) (*SplitPartInfo, error) {
	if !strings.HasPrefix(itemID, "item-part-") {
		return nil, nil
	}
	rows, err := r.Query(`SELECT parent_id, statement FROM item_splits ORDER BY parent_id`)
	if err != nil {
		return nil, err
	}
	type splitRow struct {
		parentID string
		raw      []byte
	}
	var splits []splitRow
	for rows.Next() {
		var row splitRow
		if err := rows.Scan(&row.parentID, &row.raw); err != nil {
			rows.Close()
			return nil, err
		}
		splits = append(splits, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for _, row := range splits {
		var statement SplitStatement
		if err := json.Unmarshal(row.raw, &statement); err != nil {
			return nil, err
		}
		for _, p := range statement.Parts {
			if "item-part-"+splitKey(row.parentID, p.Label) != itemID {
				continue
			}
			siblings := make(map[string]SplitSibling, len(statement.Parts)-1)
			for _, sib := range statement.Parts {
				if sib.Label == p.Label {
					continue
				}
				sibID := "item-part-" + splitKey(row.parentID, sib.Label)
				var state string
				err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, sibID).Scan(&state)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				siblings[sib.Label] = SplitSibling{
					Label:    sib.Label,
					ItemID:   sibID,
					Title:    sib.Title,
					Share:    sib.Share,
					Required: sib.Required != nil && *sib.Required,
					State:    state,
				}
			}
			return &SplitPartInfo{
				ParentID: row.parentID,
				Donor:    statement.Donor,
				Part:     p,
				Siblings: siblings,
			}, nil
		}
	}
	return nil, nil
}

// SiblingGap is a gap in another case of the parent's request reported by a part's verifier.
type SiblingGap struct {
	Sibling  string   `json:"sibling"`
	Case     string   `json:"case"`
	Paths    []string `json:"paths"`
	Evidence []string `json:"evidence"`
}

// SiblingGaps is the validated list of sibling gaps in a judge output.
type SiblingGaps []SiblingGap

func (g *SiblingGaps) UnmarshalJSON(raw []byte) error {
	var entries []SiblingGap
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return fmt.Errorf("reading sibling_gaps: %w", err)
	}
	if len(entries) == 0 {
		*g = nil
		return nil
	}
	for i, entry := range entries {
		if strings.TrimSpace(entry.Sibling) == "" {
			return fmt.Errorf("sibling_gap %d: sibling must not be blank", i+1)
		}
		if strings.TrimSpace(entry.Case) == "" {
			return fmt.Errorf("sibling_gap %d: case must not be blank", i+1)
		}
		if len(entry.Paths) == 0 {
			return fmt.Errorf("sibling_gap %d: at least one path is required", i+1)
		}
		for _, locator := range entry.Paths {
			if !validFindingLocator(locator) {
				return fmt.Errorf("sibling_gap %d: %q is not a file:<repo-relative-path>[:line] locator", i+1, locator)
			}
		}
		if len(entry.Evidence) == 0 {
			return fmt.Errorf("sibling_gap %d: at least one evidence locator is required", i+1)
		}
		for _, ev := range entry.Evidence {
			if strings.TrimSpace(ev) == "" || len(strings.Fields(ev)) != 1 {
				return fmt.Errorf("sibling_gap %d: evidence must contain non-empty locators, not prose", i+1)
			}
		}
	}
	*g = entries
	return nil
}

// RecordedSplitGap is a stored sibling gap recorded against a split parent and sibling part.
type RecordedSplitGap struct {
	GapID         string   `json:"gap_id"`
	ParentID      string   `json:"parent_id"`
	FromPartID    string   `json:"from_part_id"`
	FromPartLabel string   `json:"from_part_label"`
	SiblingID     string   `json:"sibling_id"`
	SiblingLabel  string   `json:"sibling_label"`
	Case          string   `json:"case"`
	Paths         []string `json:"paths"`
	Evidence      []string `json:"evidence"`
	EvaluatedHead string   `json:"evaluated_head"`
	Actor         string   `json:"actor"`
	RecordedAt    int64    `json:"recorded_at"`
}

// RecordSplitGaps stores sibling gaps idempotently against the split parent and named siblings.
func RecordSplitGaps(ctx context.Context, tx *store.Tx, fromPartID string, gaps []SiblingGap, head, actor string, at time.Time) error {
	if len(gaps) == 0 {
		return nil
	}
	info, err := SplitPartContext(tx.Reader(), fromPartID)
	if err != nil {
		return err
	}
	if info == nil {
		return Rejectionf("sibling-gaps-not-part", fromPartID, "item %q is not a split part", fromPartID)
	}
	recordedAt := at.UTC().Unix()
	for _, g := range gaps {
		if g.Sibling == info.Part.Label {
			return Rejectionf("sibling-gap-self", fromPartID, "sibling gap cannot target the part itself (%q)", g.Sibling)
		}
		sib, ok := info.Siblings[g.Sibling]
		if !ok {
			return Rejectionf("sibling-gap-unknown", fromPartID, "unknown sibling %q for split parent %s", g.Sibling, info.ParentID)
		}
		pathsJSON, err := json.Marshal(g.Paths)
		if err != nil {
			return err
		}
		evidenceJSON, err := json.Marshal(g.Evidence)
		if err != nil {
			return err
		}
		gapID := fmt.Sprintf("gap-%x", sha256.Sum256([]byte(
			info.ParentID+"\x00"+fromPartID+"\x00"+sib.ItemID+"\x00"+head+"\x00"+g.Case+"\x00"+string(pathsJSON),
		)))
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO item_split_gaps(gap_id, parent_id, from_part_id, from_part_label, sibling_id, sibling_label, case_text, paths, evidence, evaluated_head, actor, recorded_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			gapID, info.ParentID, fromPartID, info.Part.Label, sib.ItemID, sib.Label, g.Case, string(pathsJSON), string(evidenceJSON), head, actor, recordedAt,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		rec := RecordedSplitGap{
			GapID:         gapID,
			ParentID:      info.ParentID,
			FromPartID:    fromPartID,
			FromPartLabel: info.Part.Label,
			SiblingID:     sib.ItemID,
			SiblingLabel:  sib.Label,
			Case:          g.Case,
			Paths:         append([]string(nil), g.Paths...),
			Evidence:      append([]string(nil), g.Evidence...),
			EvaluatedHead: head,
			Actor:         actor,
			RecordedAt:    recordedAt,
		}
		body, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		parentID := info.ParentID
		if _, err := tx.Append(event.Event{Kind: SplitGapKind, ObjectType: "item", ObjectID: &parentID, Payload: body, At: at}); err != nil {
			return err
		}
	}
	return nil
}

// SplitGapsForParent returns all recorded sibling gaps for a split parent, ordered by recorded_at, gap_id.
func SplitGapsForParent(r *store.Reader, parentID string) ([]RecordedSplitGap, error) {
	return querySplitGaps(r, `WHERE parent_id=?`, parentID)
}

// SplitGapsForSibling returns all recorded sibling gaps assigned to a sibling part, ordered by recorded_at, gap_id.
func SplitGapsForSibling(r *store.Reader, siblingID string) ([]RecordedSplitGap, error) {
	return querySplitGaps(r, `WHERE sibling_id=?`, siblingID)
}

func querySplitGaps(r *store.Reader, where, id string) ([]RecordedSplitGap, error) {
	rows, err := r.Query(`SELECT gap_id, parent_id, from_part_id, from_part_label, sibling_id, sibling_label, case_text, paths, evidence, evaluated_head, actor, recorded_at FROM item_split_gaps `+where+` ORDER BY recorded_at, gap_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordedSplitGap
	for rows.Next() {
		var rec RecordedSplitGap
		var pathsRaw, evidenceRaw string
		if err := rows.Scan(&rec.GapID, &rec.ParentID, &rec.FromPartID, &rec.FromPartLabel, &rec.SiblingID, &rec.SiblingLabel, &rec.Case, &pathsRaw, &evidenceRaw, &rec.EvaluatedHead, &rec.Actor, &rec.RecordedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(pathsRaw), &rec.Paths); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidenceRaw), &rec.Evidence); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func splitKey(parent, label string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(parent+"\x00"+label)))
}

// SplitPartID returns the deterministic item ID for a part of parent.
func SplitPartID(parent, label string) string {
	return "item-part-" + splitKey(parent, label)
}

func validSplitRepoPath(p string) bool {
	if strings.TrimSpace(p) != p || p == "" || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") || path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	return true
}

func splitOrder(parent string, parts []SplitPart, donor *SplitDonor) ([]SplitPart, map[string]string, error) {
	return validateSplitParts(parent, parts, donor, 2)
}

func validateSplitParts(parent string, parts []SplitPart, donor *SplitDonor, minParts int) ([]SplitPart, map[string]string, error) {
	if len(parts) < minParts {
		return nil, nil, Rejectionf("split-parts", parent, "a split needs at least two parts")
	}
	byLabel := map[string]SplitPart{}
	ids := map[string]string{}
	hasRequired := false
	for _, p := range parts {
		if p.Label == "" || strings.ContainsFunc(p.Label, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
		}) {
			return nil, nil, Rejectionf("split-label", parent, "invalid part label %q: use letters, digits, hyphens or underscores", p.Label)
		}
		if ids[p.Label] != "" {
			return nil, nil, Rejectionf("split-label", parent, "duplicate part label %q", p.Label)
		}
		if strings.TrimSpace(p.Share) == "" || p.Required == nil {
			return nil, nil, Rejectionf("split-part", parent, "part %q needs its share and an explicit required choice", p.Label)
		}
		switch p.Takes {
		case "", SplitTakesNothing:
			if len(p.Paths) > 0 || len(p.SharedFiles) > 0 {
				return nil, nil, Rejectionf("split-part-takes", parent, "part %q takes nothing and must not list paths or shared_files", p.Label)
			}
		case SplitTakesWhole:
			if donor == nil || strings.TrimSpace(donor.SHA) == "" || strings.TrimSpace(donor.BaseSHA) == "" {
				return nil, nil, Rejectionf("split-donor", parent, "part %q takes the donor whole, but the statement has no donor sha and base_sha", p.Label)
			}
			if len(p.Paths) > 0 || len(p.SharedFiles) > 0 {
				return nil, nil, Rejectionf("split-part-takes", parent, "part %q takes the donor whole and must not list paths or shared_files", p.Label)
			}
		case SplitTakesNamed:
			if donor == nil || strings.TrimSpace(donor.SHA) == "" {
				return nil, nil, Rejectionf("split-donor", parent, "part %q takes named work, but the statement has no donor sha", p.Label)
			}
			if len(p.Paths) == 0 {
				return nil, nil, Rejectionf("split-part-takes", parent, "part %q takes named work and must list at least one path", p.Label)
			}
			for _, repoPath := range p.Paths {
				if !validSplitRepoPath(repoPath) {
					return nil, nil, Rejectionf("split-part-paths", parent, "part %q has invalid repo-relative path %q", p.Label, repoPath)
				}
			}
			for _, shared := range p.SharedFiles {
				if !validSplitRepoPath(shared) {
					return nil, nil, Rejectionf("split-part-paths", parent, "part %q has invalid repo-relative shared_files path %q", p.Label, shared)
				}
			}
		default:
			return nil, nil, Rejectionf("split-part-takes", parent, "part %q has invalid takes %q (want nothing, whole, or named)", p.Label, p.Takes)
		}
		for _, finding := range p.InheritedFindings {
			if strings.TrimSpace(finding) == "" {
				return nil, nil, Rejectionf("split-part-findings", parent, "part %q has a blank inherited_findings entry", p.Label)
			}
		}
		if *p.Required {
			hasRequired = true
		}
		byLabel[p.Label], ids[p.Label] = p, SplitPartID(parent, p.Label)
	}
	if !hasRequired {
		return nil, nil, Rejectionf("split-parts", parent, "a split needs at least one required part")
	}
	order := make([]SplitPart, 0, len(parts))
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(SplitPart) error
	visit = func(p SplitPart) error {
		if done[p.Label] {
			return nil
		}
		if visiting[p.Label] {
			return Rejectionf("split-cycle", parent, "cycle in part order at %q", p.Label)
		}
		visiting[p.Label] = true
		for sibling, work := range p.Leaves {
			if ids[sibling] == "" || sibling == p.Label || strings.TrimSpace(work) == "" {
				return Rejectionf("split-sibling", parent, "part %q must leave named work to another sibling, got %q", p.Label, sibling)
			}
		}
		seen := map[string]bool{}
		for _, label := range p.Follows {
			if ids[label] == "" || seen[label] {
				return Rejectionf("split-follows", parent, "part %q follows an unknown or repeated part %q", p.Label, label)
			}
			seen[label] = true
			if err := visit(byLabel[label]); err != nil {
				return err
			}
		}
		done[p.Label] = true
		order = append(order, p)
		return nil
	}
	for _, p := range parts {
		if err := visit(p); err != nil {
			return nil, nil, err
		}
	}
	return order, ids, nil
}

func checkSplitCoverage(parent string, criteria []criterion, coverage map[string][]SplitCoverage, ids map[string]string, requiredParts map[string]bool) error {
	owned := map[string]bool{}
	for _, c := range criteria {
		standard := false
		for i, text := range requestCriteria {
			if c.CriterionID == fmt.Sprintf("c%d", i+1) && c.Text == text {
				standard = true
				break
			}
		}
		if standard {
			continue
		}
		owned[c.CriterionID] = true
		bad := func() error {
			return Rejectionf("split-coverage", parent, "coverage for %s must name one owner for the whole criterion or quote and assign every clause in order", c.CriterionID)
		}
		entries := coverage[c.CriterionID]
		left := strings.TrimSpace(c.Text)
		for _, entry := range entries {
			if entry.ClosingCheck {
				if len(entries) != 1 || entry.Part != "" || entry.Clause != "" {
					return bad()
				}
			} else if ids[entry.Part] == "" {
				return bad()
			} else if !requiredParts[entry.Part] {
				return Rejectionf("split-coverage", parent, "coverage for %s names optional part %q; every part that owns a parent criterion or clause must be required", c.CriterionID, entry.Part)
			}
			if len(entries) == 1 && entry.Clause == "" {
				left = ""
				break
			}
			clause := strings.TrimSpace(entry.Clause)
			rest, ok := strings.CutPrefix(left, clause)
			if !ok || clause == "" {
				return bad()
			}
			left = strings.TrimSpace(rest)
			if len(entries) > 1 && strings.IndexFunc(clause, func(r rune) bool {
				return unicode.IsLetter(r) || unicode.IsNumber(r)
			}) == -1 {
				return bad()
			}
			if left != "" {
				gap := rest[:len(rest)-len(strings.TrimLeftFunc(rest, unicode.IsSpace))]
				end, _ := utf8.DecodeLastRuneInString(clause)
				// Semicolons and unspaced sentence terminators such as 。
				// mark a boundary on their own; a decimal dot does not.
				punctuated := gap != "" && unicode.Is(unicode.Terminal_Punctuation, end) ||
					strings.ContainsRune(";؛﹔；", end) ||
					end > unicode.MaxASCII && unicode.Is(unicode.Sentence_Terminal, end)
				if !punctuated && !strings.ContainsAny(gap, "\r\n") {
					return bad()
				}
			}
		}
		if len(entries) == 0 || left != "" {
			return bad()
		}
	}
	for id := range coverage {
		if !owned[id] {
			return Rejectionf("split-coverage", parent, "coverage names %s, which is not a criterion of the parent's own", id)
		}
	}
	return nil
}

func splitPartWords(words []byte, p SplitPart) []byte {
	var b bytes.Buffer
	b.Write(words)
	fmt.Fprintf(&b, "\n\nPart %s: %s\n\nThis part is responsible only for this share: %s\n", p.Label, p.Title, p.Share)
	labels := make([]string, 0, len(p.Leaves))
	for label := range p.Leaves {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	for _, label := range labels {
		fmt.Fprintf(&b, "Leaves to %s: %s\n", label, p.Leaves[label])
	}
	return b.Bytes()
}

// A design answer can replace a design wait, but must not route work around
// another mover or an outstanding owner ruling. Check live rows in the same
// transaction as the filing, so a newly placed hold cannot race it.
func checkSplitParentHolds(r *store.Reader, p itemPayload, blockedState string, allowSplitWait bool) error {
	parent := p.ItemID
	if wait := p.WaitingOn; p.State == blockedState && wait != nil {
		if allowSplitWait && wait.Source == SplitWaitSource {
			// Extending an active split parent is allowed; owner holds and conflicts are still checked below.
		} else {
			switch wait.Waiter {
			case "owner":
				return Rejectionf("split-owner-hold", parent, "parent %q is held for the owner: %s; ask the owner to authorize the work or keep it held", parent, wait.Reason)
			case "design":
				// The statement is the design seat's answer.
			default:
				return Rejectionf("split-parent-wait", parent, "parent %q still waits on %s: %s; settle the wait before filing a split", parent, wait.Waiter, wait.Reason)
			}
		}
	}
	for _, c := range p.Criteria {
		if c.ClaimClass == "owner-only" && c.State != "done" {
			return Rejectionf("split-owner-hold", parent, "parent %q has an unresolved owner-only criterion %s: %s; ask the owner to record the ruling or keep the work held", parent, c.CriterionID, c.Text)
		}
	}
	// A published owner question can hold a queued parent without waiting_on.
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'item' AND object_id = ? ORDER BY seq DESC LIMIT 1`, ConflictKind, parent).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var c Conflict
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	if c.WaitingOn == "owner" && c.Status != "answered" && c.Status != "closed" {
		reason := c.wait().Reason
		if c.QuestionRef != "" {
			reason += " (" + c.QuestionRef + ")"
		}
		return Rejectionf("split-owner-hold", parent, "parent %q still waits for an owner ruling: %s; ask the owner to record the ruling or keep the work held", parent, reason)
	}
	return nil
}

// FileSplit writes a statement in the caller's transaction. On any error the caller
// must roll back. The parent receipt makes exact replays no-ops even after settlement.
func FileSplit(ctx context.Context, tx *store.Tx, parent string, statement SplitStatement, at time.Time) (map[string]string, error) {
	if statement.Donor == nil {
		var runID, ref, sha, baseSHA string
		err := tx.QueryRow(
			`SELECT run_id, donor_ref, donor_sha, base_sha FROM split_calls WHERE item_id=? AND status IN (?,?) ORDER BY call_id DESC LIMIT 1`,
			parent, SplitCallStatusPending, SplitCallStatusSplit,
		).Scan(&runID, &ref, &sha, &baseSHA)
		if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingSplitsTable(err) {
			return nil, err
		}
		if err == nil && sha != "" {
			statement.Donor = &SplitDonor{
				RunID:   runID,
				Ref:     ref,
				SHA:     sha,
				BaseSHA: baseSHA,
			}
		}
	}
	order, ids, err := splitOrder(parent, statement.Parts, statement.Donor)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(statement)
	if err != nil {
		return nil, err
	}
	var saved []byte
	err = tx.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&saved)
	if err == nil {
		if !bytes.Equal(saved, raw) {
			return nil, Rejectionf("split-differs", parent, "parent %q already has a different filed split", parent)
		}
		return ids, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	p, _, err := readStoredItem(tx.Reader(), parent)
	if err != nil {
		return nil, err
	}
	d, err := itemDefinition(tx.Reader())
	if err != nil {
		return nil, err
	}
	if d.Name != definition.GitLandingName || (p.State != d.Ready && p.State != d.Blocked) {
		return nil, Rejectionf("split-state", parent, "only a queued or blocked Git-landing request may be split")
	}
	if err := checkSplitParentHolds(tx.Reader(), p, d.Blocked, false); err != nil {
		return nil, err
	}
	words, err := RequestWords(tx.Reader(), parent)
	if err != nil {
		return nil, err
	}
	if words == nil {
		return nil, Rejectionf("split-parent", parent, "parent %q has no saved request words", parent)
	}
	requiredParts := map[string]bool{}
	for _, part := range order {
		if *part.Required {
			requiredParts[part.Label] = true
		}
	}
	if err := checkSplitCoverage(parent, p.Criteria, statement.Coverage, ids, requiredParts); err != nil {
		return nil, err
	}
	required := []string{}
	for _, part := range order {
		if *part.Required {
			required = append(required, ids[part.Label])
		}
		// Parts cannot bypass dependencies that were already on the parent.
		f := RequestFiling{Words: splitPartWords(words, part), Title: part.Title, Criteria: part.Criteria,
			Ordinary: !p.Express, BlockedBy: slices.Clone(p.BlockedBy), AfterItems: slices.Clone(p.After), Destination: Destination{Before: parent},
			At: at, By: "design", Via: "split"}
		for _, label := range part.Follows {
			f.BlockedBy = append(f.BlockedBy, ids[label])
		}
		if err := normalizeRequest(&f); err != nil {
			return nil, Rejectionf("split-part", parent, "part %q: %v", part.Label, err)
		}
		id := ids[part.Label]
		exists, err := itemExists(ctx, tx.Reader(), id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, Rejectionf("split-part-exists", parent, "part %q would reuse existing item %s", part.Label, id)
		}
		if _, err := insertRequest(ctx, tx, f, id, "split:"+splitKey(parent, part.Label), requestInputHash(f)); err != nil {
			return nil, err
		}
	}
	if _, err := SetDeps(ctx, tx, parent, required, nil, at); err != nil {
		return nil, err
	}
	if _, err := SetState(ctx, tx, parent, d.Blocked, at, WaitingOn{Waiter: "owner", Source: SplitWaitSource, Reason: "split parent; check its criteria after the required parts land"}); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO item_splits(parent_id, statement) VALUES (?,?)`, parent, raw); err != nil {
		return nil, err
	}
	if err := markSplitCallSplit(ctx, tx, parent, "", at); err != nil {
		return nil, err
	}
	return ids, nil
}

// ReadSplit reads the filed split statement and returns its deterministic label->itemID map.
func ReadSplit(r *store.Reader, parent string) (*SplitStatement, map[string]string, error) {
	var raw []byte
	if err := r.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&raw); err != nil {
		return nil, nil, err
	}
	var statement SplitStatement
	if err := json.Unmarshal(raw, &statement); err != nil {
		return nil, nil, err
	}
	ids := make(map[string]string, len(statement.Parts))
	for _, p := range statement.Parts {
		ids[p.Label] = "item-part-" + splitKey(parent, p.Label)
	}
	return &statement, ids, nil
}

// ReadSplitPartLandings returns each part's current state and latest unreverted landed commit SHA.
func ReadSplitPartLandings(r *store.Reader, parent string) ([]SplitPartLanding, error) {
	statement, ids, err := ReadSplit(r, parent)
	if err != nil {
		return nil, err
	}
	out := make([]SplitPartLanding, 0, len(statement.Parts))
	for _, p := range statement.Parts {
		itemID := ids[p.Label]
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, itemID).Scan(&state); err != nil {
			return nil, err
		}
		landing, err := CurrentLanding(r, itemID)
		if err != nil {
			return nil, err
		}
		commit := ""
		if landing != nil {
			commit = landing.TrunkAfter
		}
		out = append(out, SplitPartLanding{
			Label:      p.Label,
			ItemID:     itemID,
			Required:   p.Required != nil && *p.Required,
			State:      state,
			Commit:     commit,
			ReplacedBy: slices.Clone(p.ReplacedBy),
		})
	}
	return out, nil
}

// SplitClosingEvaluatorBinding returns the evaluator binding for a split parent's closing check.
func SplitClosingEvaluatorBinding(r *store.Reader, parent string) (EvaluatorBinding, error) {
	var raw []byte
	err := r.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return EvaluatorBinding{}, Rejectionf("evaluator-selection", parent, "split parent %q has no filed split statement", parent)
	}
	if err != nil {
		return EvaluatorBinding{}, Internalf("reading split statement: %v", err)
	}
	return EvaluatorBinding{
		Workflow:      SplitClosingWorkflow,
		EvaluatorNode: SplitClosingNode,
		GraphSHA256:   CriterionHash(string(raw)),
	}, nil
}

// ExtendSplit appends new parts to an existing split statement in the caller's transaction.
// On any error the caller must roll back. Exact replays are idempotent no-ops.
func ExtendSplit(ctx context.Context, tx *store.Tx, parent string, ext SplitExtension, at time.Time) (map[string]string, error) {
	if len(ext.Parts) < 1 {
		return nil, Rejectionf("split-parts", parent, "a split extension needs at least one part")
	}
	var raw []byte
	err := tx.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Rejectionf("split-not-found", parent, "parent %q has no filed split", parent)
	}
	if err != nil {
		return nil, err
	}
	var existing SplitStatement
	if err := json.Unmarshal(raw, &existing); err != nil {
		return nil, err
	}
	existingByLabel := make(map[string]SplitPart, len(existing.Parts))
	for _, ep := range existing.Parts {
		existingByLabel[ep.Label] = ep
	}

	seenNew := make(map[string]bool, len(ext.Parts))
	newIDs := make(map[string]string, len(ext.Parts))
	existingMatches := 0
	for _, p := range ext.Parts {
		if p.Label == "" || strings.ContainsFunc(p.Label, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
		}) {
			return nil, Rejectionf("split-label", parent, "invalid part label %q: use letters, digits, hyphens or underscores", p.Label)
		}
		if seenNew[p.Label] {
			return nil, Rejectionf("split-label", parent, "duplicate part label %q", p.Label)
		}
		seenNew[p.Label] = true
		newIDs[p.Label] = "item-part-" + splitKey(parent, p.Label)
		if ep, ok := existingByLabel[p.Label]; ok {
			existingMatches++
			if !reflect.DeepEqual(p, ep) {
				return nil, Rejectionf("split-differs", parent, "part %q already exists in parent %q with different fields", p.Label, parent)
			}
		}
	}
	if existingMatches == len(ext.Parts) {
		if len(ext.Coverage) > 0 && !reflect.DeepEqual(ext.Coverage, existing.Coverage) {
			return nil, Rejectionf("split-differs", parent, "parent %q already has a different coverage map", parent)
		}
		var newRequiredLabels []string
		for _, p := range ext.Parts {
			if p.Required != nil && *p.Required {
				newRequiredLabels = append(newRequiredLabels, p.Label)
			}
		}
		for _, repLabel := range ext.Replaces {
			ep, ok := existingByLabel[repLabel]
			if !ok || !slices.Equal(ep.ReplacedBy, newRequiredLabels) {
				return nil, Rejectionf("split-differs", parent, "parent %q already has a different replacement state for %q", parent, repLabel)
			}
		}
		return newIDs, nil
	}
	if existingMatches > 0 {
		for _, p := range ext.Parts {
			if _, ok := existingByLabel[p.Label]; ok {
				return nil, Rejectionf("split-label", parent, "part %q already exists in parent %q", p.Label, parent)
			}
		}
	}

	hasRequired := false
	var newRequiredLabels []string
	var newRequiredIDs []string
	for _, p := range ext.Parts {
		if strings.TrimSpace(p.Share) == "" || p.Required == nil {
			return nil, Rejectionf("split-part", parent, "part %q needs its share and an explicit required choice", p.Label)
		}
		if *p.Required {
			hasRequired = true
			newRequiredLabels = append(newRequiredLabels, p.Label)
			newRequiredIDs = append(newRequiredIDs, newIDs[p.Label])
		}
	}
	if !hasRequired {
		return nil, Rejectionf("split-parts", parent, "a split extension needs at least one required part")
	}

	replacesSet := make(map[string]bool, len(ext.Replaces))
	var replacedIDs []string
	for _, repLabel := range ext.Replaces {
		if replacesSet[repLabel] {
			return nil, Rejectionf("split-replaces", parent, "duplicate replaced part %q", repLabel)
		}
		replacesSet[repLabel] = true
		ep, ok := existingByLabel[repLabel]
		if !ok {
			return nil, Rejectionf("split-replaces", parent, "replaced part %q does not exist in parent %q", repLabel, parent)
		}
		if ep.Required == nil || !*ep.Required {
			return nil, Rejectionf("split-replaces", parent, "replaced part %q is not a required part", repLabel)
		}
		if len(ep.ReplacedBy) > 0 {
			return nil, Rejectionf("split-replaces", parent, "replaced part %q has already been replaced", repLabel)
		}
		repID := SplitPartID(parent, repLabel)
		var repState, closeReason string
		if err := tx.QueryRow(`SELECT state, close_reason FROM items WHERE item_id=?`, repID).Scan(&repState, &closeReason); err != nil {
			return nil, err
		}
		if !IsDefectiveClose(repState, closeReason) {
			return nil, Rejectionf("split-replaces", parent, "replaced part %q is %s (%q), not closed as defective", repLabel, repState, closeReason)
		}
		replacedIDs = append(replacedIDs, repID)
	}
	for _, p := range ext.Parts {
		for _, fl := range p.Follows {
			if replacesSet[fl] {
				return nil, Rejectionf("split-follows", parent, "replacement part %q cannot follow replaced part %q", p.Label, fl)
			}
		}
	}

	d, err := itemDefinition(tx.Reader())
	if err != nil {
		return nil, err
	}

	combinedParts := make([]SplitPart, 0, len(existing.Parts)+len(ext.Parts))
	for _, ep := range existing.Parts {
		cp := ep
		if replacesSet[cp.Label] {
			cp.ReplacedBy = slices.Clone(newRequiredLabels)
		} else if len(replacesSet) > 0 && len(cp.ReplacedBy) == 0 {
			sibID := SplitPartID(parent, cp.Label)
			sibPayload, _, err := readStoredItem(tx.Reader(), sibID)
			if err != nil {
				return nil, err
			}
			if !d.IsFinished(sibPayload.State) {
				var updatedFollows []string
				seenFollow := make(map[string]bool, len(cp.Follows)+len(newRequiredLabels))
				for _, fl := range cp.Follows {
					if replacesSet[fl] {
						for _, nl := range newRequiredLabels {
							if !seenFollow[nl] {
								seenFollow[nl] = true
								updatedFollows = append(updatedFollows, nl)
							}
						}
					} else if !seenFollow[fl] {
						seenFollow[fl] = true
						updatedFollows = append(updatedFollows, fl)
					}
				}
				cp.Follows = updatedFollows
			}
		}
		combinedParts = append(combinedParts, cp)
	}
	combinedParts = append(combinedParts, ext.Parts...)
	order, ids, err := validateSplitParts(parent, combinedParts, existing.Donor, 2)
	if err != nil {
		return nil, err
	}

	p, _, err := readStoredItem(tx.Reader(), parent)
	if err != nil {
		return nil, err
	}
	if d.Name != definition.GitLandingName || p.State != d.Blocked || p.WaitingOn == nil || p.WaitingOn.Source != SplitWaitSource {
		return nil, Rejectionf("split-state", parent, "only a blocked split parent waiting on %q may be extended", SplitWaitSource)
	}
	if err := checkSplitParentHolds(tx.Reader(), p, d.Blocked, true); err != nil {
		return nil, err
	}
	words, err := RequestWords(tx.Reader(), parent)
	if err != nil {
		return nil, err
	}
	if words == nil {
		return nil, Rejectionf("split-parent", parent, "parent %q has no saved request words", parent)
	}

	coverage := existing.Coverage
	if len(ext.Coverage) > 0 {
		coverage = ext.Coverage
	}
	if len(ext.Coverage) > 0 || len(replacesSet) > 0 {
		requiredParts := map[string]bool{}
		for _, part := range order {
			if *part.Required && len(part.ReplacedBy) == 0 {
				requiredParts[part.Label] = true
			}
		}
		if err := checkSplitCoverage(parent, p.Criteria, coverage, ids, requiredParts); err != nil {
			return nil, err
		}
	}

	allRequired := []string{}
	for _, part := range order {
		if *part.Required && len(part.ReplacedBy) == 0 {
			allRequired = append(allRequired, ids[part.Label])
		}
		if _, isExisting := existingByLabel[part.Label]; isExisting {
			continue
		}
		f := RequestFiling{
			Words:       splitPartWords(words, part),
			Title:       part.Title,
			Criteria:    part.Criteria,
			Ordinary:    !p.Express,
			Destination: Destination{Before: parent},
			At:          at,
			By:          "design",
			Via:         "split",
		}
		for _, label := range part.Follows {
			f.BlockedBy = append(f.BlockedBy, ids[label])
		}
		if err := normalizeRequest(&f); err != nil {
			return nil, Rejectionf("split-part", parent, "part %q: %v", part.Label, err)
		}
		id := ids[part.Label]
		exists, err := itemExists(ctx, tx.Reader(), id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, Rejectionf("split-part-exists", parent, "part %q would reuse existing item %s", part.Label, id)
		}
		if _, err := insertRequest(ctx, tx, f, id, "split:"+splitKey(parent, part.Label), requestInputHash(f)); err != nil {
			return nil, err
		}
	}
	if len(replacesSet) > 0 {
		for _, ep := range existing.Parts {
			if replacesSet[ep.Label] || len(ep.ReplacedBy) > 0 {
				continue
			}
			sibID := SplitPartID(parent, ep.Label)
			sibPayload, _, err := readStoredItem(tx.Reader(), sibID)
			if err != nil {
				return nil, err
			}
			if d.IsFinished(sibPayload.State) {
				continue
			}
			var removeSib []string
			for _, repID := range replacedIDs {
				if slices.Contains(sibPayload.BlockedBy, repID) {
					removeSib = append(removeSib, repID)
				}
			}
			if len(removeSib) > 0 {
				if _, err := SetDeps(ctx, tx, sibID, newRequiredIDs, removeSib, at); err != nil {
					return nil, err
				}
			}
		}
		if _, err := SetDeps(ctx, tx, parent, newRequiredIDs, replacedIDs, at); err != nil {
			return nil, err
		}
	} else {
		if _, err := SetDeps(ctx, tx, parent, allRequired, nil, at); err != nil {
			return nil, err
		}
	}
	grown := SplitStatement{
		Donor:    existing.Donor,
		Parts:    combinedParts,
		Coverage: coverage,
	}
	grownRaw, err := json.Marshal(grown)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE item_splits SET statement=? WHERE parent_id=?`, grownRaw, parent); err != nil {
		return nil, err
	}
	return newIDs, nil
}

// SplitRequiredParts reads the filed requirements, not a mutable queue edge.
func SplitRequiredParts(r *store.Reader, parent string) ([]string, error) {
	var raw []byte
	if err := r.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&raw); err != nil {
		return nil, err
	}
	var statement SplitStatement
	if err := json.Unmarshal(raw, &statement); err != nil {
		return nil, err
	}
	var required []string
	for _, p := range statement.Parts {
		if p.Required != nil && *p.Required && len(p.ReplacedBy) == 0 {
			required = append(required, SplitPartID(parent, p.Label))
		}
	}
	slices.Sort(required)
	return required, nil
}

// SplitPartMembership identifies the parent split and part metadata for a part item.
type SplitPartMembership struct {
	ParentID string
	Label    string
	Part     SplitPart
}

func isMissingSplitsTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// ReadSplitStatement reads the filed SplitStatement for parent. It returns
// (nil, false, nil) when no row exists or the item_splits table is absent.
func ReadSplitStatement(r *store.Reader, parent string) (*SplitStatement, bool, error) {
	var raw []byte
	err := r.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || isMissingSplitsTable(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var st SplitStatement
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, false, err
	}
	return &st, true, nil
}

// ReadAllSplits reads all filed split statements keyed by parent_id.
// It returns an empty map when the item_splits table is absent.
func ReadAllSplits(r *store.Reader) (map[string]SplitStatement, error) {
	out := map[string]SplitStatement{}
	rows, err := r.Query(`SELECT parent_id, statement FROM item_splits`)
	if isMissingSplitsTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var parentID string
		var raw []byte
		if err := rows.Scan(&parentID, &raw); err != nil {
			return nil, err
		}
		var st SplitStatement
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, err
		}
		out[parentID] = st
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SplitPartIndex maps each part's item ID to its parent membership.
func SplitPartIndex(splits map[string]SplitStatement) map[string]SplitPartMembership {
	out := make(map[string]SplitPartMembership)
	for parentID, st := range splits {
		for _, p := range st.Parts {
			out[SplitPartID(parentID, p.Label)] = SplitPartMembership{
				ParentID: parentID,
				Label:    p.Label,
				Part:     p,
			}
		}
	}
	return out
}

func checkSplitMove(d definition.Definition, id string, wait *WaitingOn, to string) error {
	if wait != nil && wait.Source == SplitWaitSource && to != d.Blocked && !d.IsFinished(to) {
		return Rejectionf("split-parent-held", id, "split parent %q cannot return to the build queue", id)
	}
	return nil
}

const (
	SplitCallKind   = "item.split-call"
	SplitAnswerKind = "item.split-answer"

	SplitCallerVerifier = "verifier"

	SplitCallStatusPending = "pending"
	SplitCallStatusSplit   = "split"
	SplitCallStatusNoSplit = "no-split"

	SplitSignalS1 = "S1"
)

type SplitSignal struct {
	Signal         string `json:"signal"`
	Files          int64  `json:"files,omitempty"`
	AddedLines     int64  `json:"added_lines,omitempty"`
	FileLimit      int64  `json:"file_limit,omitempty"`
	AddedLineLimit int64  `json:"added_line_limit,omitempty"`
}

type SplitNoSplit struct {
	Reason  string        `json:"reason"`
	Signals []SplitSignal `json:"signals,omitempty"`
}

type SplitCall struct {
	CallID              int64         `json:"call_id,omitempty"`
	Caller              string        `json:"caller"`
	Timestamp           time.Time     `json:"timestamp"`
	ItemID              string        `json:"item_id"`
	RunID               string        `json:"run_id"`
	DonorRef            string        `json:"donor_ref,omitempty"`
	DonorSHA            string        `json:"donor_sha,omitempty"`
	BaseSHA             string        `json:"base_sha,omitempty"`
	Signals             []SplitSignal `json:"signals"`
	Status              string        `json:"status"`
	DesignRunID         string        `json:"design_run_id,omitempty"`
	NoSplit             *SplitNoSplit `json:"no_split,omitempty"`
	LastRejectionReason string        `json:"last_rejection_reason,omitempty"`
	AnsweredAt          *time.Time    `json:"answered_at,omitempty"`
}

type SplitDonorRef struct {
	ItemID   string
	RunID    string
	DonorRef string
	Status   string
}

func RecordSplitCall(ctx context.Context, tx *store.Tx, call SplitCall) (SplitCall, error) {
	if call.ItemID == "" {
		return SplitCall{}, Rejectionf("split-call", "", "split call needs an item_id")
	}
	if call.Caller == "" {
		return SplitCall{}, Rejectionf("split-call", call.ItemID, "split call needs a caller")
	}
	if len(call.Signals) == 0 {
		return SplitCall{}, Rejectionf("split-call", call.ItemID, "split call needs at least one measured signal")
	}
	if call.Timestamp.IsZero() {
		call.Timestamp = time.Now().UTC()
	} else {
		call.Timestamp = call.Timestamp.UTC()
	}
	existing, err := PendingSplitCall(tx.Reader(), call.ItemID)
	if err != nil {
		return SplitCall{}, err
	}
	if existing != nil {
		for _, sig := range call.Signals {
			if !slices.Contains(existing.Signals, sig) {
				existing.Signals = append(existing.Signals, sig)
			}
		}
		rawSignals, err := json.Marshal(existing.Signals)
		if err != nil {
			return SplitCall{}, err
		}
		if _, err := tx.Exec(`UPDATE split_calls SET signals=? WHERE call_id=?`, rawSignals, existing.CallID); err != nil {
			return SplitCall{}, err
		}
		body, err := json.Marshal(existing)
		if err != nil {
			return SplitCall{}, err
		}
		if _, err := tx.Append(event.Event{Kind: SplitCallKind, ObjectType: "item", ObjectID: &existing.ItemID, Payload: body, At: call.Timestamp}); err != nil {
			return SplitCall{}, err
		}
		return *existing, nil
	}
	call.Status = SplitCallStatusPending
	rawSignals, err := json.Marshal(call.Signals)
	if err != nil {
		return SplitCall{}, err
	}
	res, err := tx.Exec(
		`INSERT INTO split_calls(item_id, caller, run_id, donor_ref, donor_sha, base_sha, signals, status, design_run_id, no_split_reason, last_rejection_reason, created_at, answered_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		call.ItemID, call.Caller, call.RunID, call.DonorRef, call.DonorSHA, call.BaseSHA,
		rawSignals, call.Status, call.DesignRunID, "", call.LastRejectionReason,
		call.Timestamp.Format(time.RFC3339Nano), "",
	)
	if err != nil {
		return SplitCall{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SplitCall{}, err
	}
	call.CallID = id
	body, err := json.Marshal(call)
	if err != nil {
		return SplitCall{}, err
	}
	if _, err := tx.Append(event.Event{Kind: SplitCallKind, ObjectType: "item", ObjectID: &call.ItemID, Payload: body, At: call.Timestamp}); err != nil {
		return SplitCall{}, err
	}
	return call, nil
}

func scanSplitCall(scan func(dest ...any) error) (SplitCall, error) {
	var c SplitCall
	var rawSignals []byte
	var noSplitReason, createdAt, answeredAt string
	if err := scan(
		&c.CallID, &c.ItemID, &c.Caller, &c.RunID, &c.DonorRef, &c.DonorSHA, &c.BaseSHA,
		&rawSignals, &c.Status, &c.DesignRunID, &noSplitReason, &c.LastRejectionReason,
		&createdAt, &answeredAt,
	); err != nil {
		return SplitCall{}, err
	}
	if err := json.Unmarshal(rawSignals, &c.Signals); err != nil {
		return SplitCall{}, err
	}
	if createdAt != "" {
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return SplitCall{}, err
		}
		c.Timestamp = t
	}
	if answeredAt != "" {
		t, err := time.Parse(time.RFC3339Nano, answeredAt)
		if err != nil {
			return SplitCall{}, err
		}
		c.AnsweredAt = &t
	}
	if c.Status == SplitCallStatusNoSplit || noSplitReason != "" {
		c.NoSplit = &SplitNoSplit{Reason: noSplitReason, Signals: slices.Clone(c.Signals)}
	}
	return c, nil
}

func PendingSplitCall(r *store.Reader, itemID string) (*SplitCall, error) {
	row := r.QueryRow(
		`SELECT call_id, item_id, caller, run_id, donor_ref, donor_sha, base_sha, signals, status, design_run_id, no_split_reason, last_rejection_reason, created_at, answered_at
		 FROM split_calls WHERE item_id=? AND status=? ORDER BY call_id DESC LIMIT 1`,
		itemID, SplitCallStatusPending,
	)
	c, err := scanSplitCall(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func ReadSplitCalls(r *store.Reader, itemID string) ([]SplitCall, error) {
	rows, err := r.Query(
		`SELECT call_id, item_id, caller, run_id, donor_ref, donor_sha, base_sha, signals, status, design_run_id, no_split_reason, last_rejection_reason, created_at, answered_at
		 FROM split_calls WHERE item_id=? ORDER BY call_id ASC`,
		itemID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var calls []SplitCall
	for rows.Next() {
		c, err := scanSplitCall(rows.Scan)
		if err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}

func AnswerSplitNoSplit(ctx context.Context, tx *store.Tx, itemID, designRunID, reason string, at time.Time) (SplitCall, error) {
	if strings.TrimSpace(reason) == "" {
		return SplitCall{}, Rejectionf("split-no-split", itemID, "no-split answer requires a reason")
	}
	pending, err := PendingSplitCall(tx.Reader(), itemID)
	if err != nil {
		return SplitCall{}, err
	}
	if pending == nil {
		return SplitCall{}, Rejectionf("split-no-call", itemID, "item %q has no pending split call", itemID)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	answered := at.Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`UPDATE split_calls SET status=?, design_run_id=?, no_split_reason=?, last_rejection_reason='', answered_at=? WHERE call_id=?`,
		SplitCallStatusNoSplit, designRunID, reason, answered, pending.CallID,
	); err != nil {
		return SplitCall{}, err
	}
	pending.Status = SplitCallStatusNoSplit
	pending.DesignRunID = designRunID
	pending.LastRejectionReason = ""
	pending.AnsweredAt = &at
	pending.NoSplit = &SplitNoSplit{Reason: reason, Signals: slices.Clone(pending.Signals)}
	body, err := json.Marshal(pending)
	if err != nil {
		return SplitCall{}, err
	}
	if _, err := tx.Append(event.Event{Kind: SplitAnswerKind, ObjectType: "item", ObjectID: &pending.ItemID, Payload: body, At: at}); err != nil {
		return SplitCall{}, err
	}
	return *pending, nil
}

func markSplitCallSplit(ctx context.Context, tx *store.Tx, itemID, designRunID string, at time.Time) error {
	pending, err := PendingSplitCall(tx.Reader(), itemID)
	if err != nil || pending == nil {
		return err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	if _, err := tx.Exec(
		`UPDATE split_calls SET status=?, design_run_id=?, last_rejection_reason='', answered_at=? WHERE call_id=?`,
		SplitCallStatusSplit, designRunID, at.Format(time.RFC3339Nano), pending.CallID,
	); err != nil {
		return err
	}
	pending.Status = SplitCallStatusSplit
	pending.DesignRunID = designRunID
	pending.LastRejectionReason = ""
	pending.AnsweredAt = &at
	body, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: SplitAnswerKind, ObjectType: "item", ObjectID: &pending.ItemID, Payload: body, At: at})
	return err
}

func RecordSplitRejection(ctx context.Context, tx *store.Tx, itemID, designRunID, reason string) error {
	pending, err := PendingSplitCall(tx.Reader(), itemID)
	if err != nil || pending == nil {
		return err
	}
	if designRunID == "" {
		designRunID = pending.DesignRunID
	}
	_, err = tx.Exec(
		`UPDATE split_calls SET last_rejection_reason=?, design_run_id=? WHERE call_id=?`,
		reason, designRunID, pending.CallID,
	)
	return err
}

func S1AnsweredNoSplit(r *store.Reader, itemID string, sig SplitSignal) (bool, error) {
	rows, err := r.Query(
		`SELECT signals FROM split_calls WHERE item_id=? AND status=?`,
		itemID, SplitCallStatusNoSplit,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		var signals []SplitSignal
		if err := json.Unmarshal(raw, &signals); err != nil {
			return false, err
		}
		for _, prev := range signals {
			if prev.Signal == SplitSignalS1 && prev.Files >= sig.Files && prev.AddedLines >= sig.AddedLines {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

func ReadSplitDonorRefs(r *store.Reader) ([]SplitDonorRef, error) {
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='split_calls')`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	rows, err := r.Query(
		`SELECT item_id, run_id, donor_ref, status FROM split_calls WHERE status IN (?,?)`,
		SplitCallStatusPending, SplitCallStatusSplit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []SplitDonorRef
	for rows.Next() {
		var ref SplitDonorRef
		if err := rows.Scan(&ref.ItemID, &ref.RunID, &ref.DonorRef, &ref.Status); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func init() {
	for _, kind := range []string{SplitCallKind, SplitAnswerKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
	if err := store.RegisterDDL("item-splits", `CREATE TABLE IF NOT EXISTS item_splits (
		parent_id TEXT PRIMARY KEY REFERENCES items(item_id), statement BLOB NOT NULL
	)`); err != nil {
		panic(err)
	}
	if err := store.RegisterDDL("item-split-gaps", `CREATE TABLE IF NOT EXISTS item_split_gaps (
		gap_id TEXT PRIMARY KEY,
		parent_id TEXT NOT NULL REFERENCES items(item_id),
		from_part_id TEXT NOT NULL REFERENCES items(item_id),
		from_part_label TEXT NOT NULL,
		sibling_id TEXT NOT NULL REFERENCES items(item_id),
		sibling_label TEXT NOT NULL,
		case_text TEXT NOT NULL,
		paths TEXT NOT NULL,
		evidence TEXT NOT NULL,
		evaluated_head TEXT NOT NULL,
		actor TEXT NOT NULL,
		recorded_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS item_split_gaps_parent ON item_split_gaps(parent_id, recorded_at, gap_id);
	CREATE INDEX IF NOT EXISTS item_split_gaps_sibling ON item_split_gaps(sibling_id, recorded_at, gap_id);`); err != nil {
		panic(err)
	}
	if err := event.Register(SplitGapKind, Utility); err != nil {
		panic(err)
	}
	if err := store.RegisterDDL("item-split-calls", `CREATE TABLE IF NOT EXISTS split_calls (
		call_id INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id TEXT NOT NULL REFERENCES items(item_id),
		caller TEXT NOT NULL,
		run_id TEXT NOT NULL,
		donor_ref TEXT NOT NULL DEFAULT '',
		donor_sha TEXT NOT NULL DEFAULT '',
		base_sha TEXT NOT NULL DEFAULT '',
		signals BLOB NOT NULL,
		status TEXT NOT NULL,
		design_run_id TEXT NOT NULL DEFAULT '',
		no_split_reason TEXT NOT NULL DEFAULT '',
		last_rejection_reason TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		answered_at TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS split_calls_item_status_idx ON split_calls(item_id, status);
	CREATE INDEX IF NOT EXISTS split_calls_donor_idx ON split_calls(donor_ref) WHERE donor_ref != '';`); err != nil {
		panic(err)
	}
}
