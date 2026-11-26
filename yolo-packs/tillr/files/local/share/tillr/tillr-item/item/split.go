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

const SplitWaitSource = "split"

func splitKey(parent, label string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(parent+"\x00"+label)))
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
		byLabel[p.Label], ids[p.Label] = p, "item-part-"+splitKey(parent, p.Label)
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
			required = append(required, "item-part-"+splitKey(parent, p.Label))
		}
	}
	slices.Sort(required)
	return required, nil
}

func checkSplitMove(d definition.Definition, id string, wait *WaitingOn, to string) error {
	if wait != nil && wait.Source == SplitWaitSource && to != d.Blocked && !d.IsFinished(to) {
		return Rejectionf("split-parent-held", id, "split parent %q cannot return to the build queue", id)
	}
	return nil
}

func init() {
	if err := store.RegisterDDL("item-splits", `CREATE TABLE IF NOT EXISTS item_splits (
		parent_id TEXT PRIMARY KEY REFERENCES items(item_id), statement BLOB NOT NULL
	)`); err != nil {
		panic(err)
	}
}
