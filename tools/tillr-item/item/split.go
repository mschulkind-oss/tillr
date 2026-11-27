package item

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// SplitStatement is the design seat's division of a request before donor work is
// available. Parts take nothing; donor and verdict fields are not part of this format.
type SplitStatement struct {
	Parts    []SplitPart                `json:"parts"`
	Coverage map[string][]SplitCoverage `json:"coverage"`
}

type SplitPart struct {
	Label    string            `json:"label"`
	Title    string            `json:"title"`
	Share    string            `json:"share"`
	Leaves   map[string]string `json:"leaves,omitempty"`
	Criteria []string          `json:"criteria,omitempty"`
	Follows  []string          `json:"follows,omitempty"`
	Required *bool             `json:"required"`
}

// An empty Clause names the whole criterion. Otherwise quoted clauses exhaust
// the stored text in order and meet at explicit punctuation or line boundaries.
type SplitCoverage struct {
	Clause       string `json:"clause,omitempty"`
	Part         string `json:"part,omitempty"`
	ClosingCheck bool   `json:"closing_check,omitempty"`
}

const (
	SplitWaitSource = "split"
	SplitGapKind    = "item.split-gap"
)

// SplitSibling describes a sibling part in a filed split statement.
type SplitSibling struct {
	Label    string `json:"label"`
	ItemID   string `json:"item_id"`
	Title    string `json:"title"`
	Share    string `json:"share"`
	Required bool   `json:"required"`
	State    string `json:"state"`
}

// SplitPartInfo describes a split part's parent, own part definition, and sibling parts.
type SplitPartInfo struct {
	ParentID string                  `json:"parent_id"`
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

func splitOrder(parent string, parts []SplitPart) ([]SplitPart, map[string]string, error) {
	if len(parts) < 2 {
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
func checkSplitParentHolds(r *store.Reader, p itemPayload, blockedState string) error {
	parent := p.ItemID
	if wait := p.WaitingOn; p.State == blockedState && wait != nil {
		switch wait.Waiter {
		case "owner":
			return Rejectionf("split-owner-hold", parent, "parent %q is held for the owner: %s; ask the owner to authorize the work or keep it held", parent, wait.Reason)
		case "design":
			// The statement is the design seat's answer.
		default:
			return Rejectionf("split-parent-wait", parent, "parent %q still waits on %s: %s; settle the wait before filing a split", parent, wait.Waiter, wait.Reason)
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
	order, ids, err := splitOrder(parent, statement.Parts)
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
	if err := checkSplitParentHolds(tx.Reader(), p, d.Blocked); err != nil {
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
		if p.Required != nil && *p.Required {
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
