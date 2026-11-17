package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonConflictDesign   = "conflict-design"
	ReasonConflictOwner    = "conflict-owner"
	ReasonConflictAnswered = "conflict-answered"
)

// conflictDecision gives the design seat exactly one dispatch for a raised report.
// The pending-work record takes over after claim. A quota refusal raises the
// conflict again; other unanswered terminal attempts keep their disposition.
func (p *planner) conflictDecision(row itemRow, c item.Conflict) Decision {
	d := Decision{ItemID: row.ItemID, Seat: c.WaitingOn, ConflictID: c.ConflictID, Attempt: len(p.priors[row.ItemID]) + 1, From: "held", Action: ActionHold, Reason: ReasonConflictDesign}
	switch {
	case c.WaitingOn == "owner":
		d.Reason = ReasonConflictOwner
		// The design run that handed the conflict over is named beside it, so a view
		// says which run ended without an answer (design_unanswered.go).
		d.DeferredBy = c.DesignRunID
	case c.Status != "raised":
	case p.policy == nil:
		d.Reason = ReasonNoPolicy
	case len(p.policy.seatList("design")) == 0:
		d.Reason = ReasonNoPolicy
	case c.BodyRef == "":
		d.Reason = ReasonConflictOwner
	case p.fixerOpen != "":
		d.Reason = ReasonTrunkBroken
		d.DeferredBy = p.fixerOpen
	default:
		// Unlike ordinary failover, a single design backend must not spill
		// onto the build preference. Only this seat's declared backends qualify.
		backend := ""
		for _, name := range p.policy.seatList("design") {
			if !p.shut[name] {
				backend = name
				break
			}
		}
		if backend == "" {
			d.Reason = ReasonBackendWindow
			break
		}
		if p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches {
			d.Reason = ReasonNoLane
			break
		}
		if lane, ok := p.freeLane(); ok {
			p.assigned[lane] = true
			p.dispatched++
			d.Action, d.From, d.To, d.LaneID = ActionDispatch, "queued", "running", lane
			d.Seat = "design"
			d.BlockConflict = &c
			d.RunRequest = p.request(row.ItemID, lane, backend)
		} else {
			d.Reason = ReasonNoLane
		}
	}
	identifyDecision(&d)
	return d
}

func (l *Loop) saveBlockConflict(ctx context.Context, c item.Conflict) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// Recording a conflict moves the item to blocked, which item refuses for a
		// terminal item. Such an item gets no conflict: its run is settled unjudged.
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "blocked-report"); err != nil {
			return err
		}
		return item.RecordConflict(tx, c, l.cfg.Now().UTC())
	})
}

func (l *Loop) reportedBlock(ctx context.Context, d Decision, log string) error {
	// Settlement supplies this run's pinned extracted report, or its legacy
	// plain-text log. Never read the reusable lane's report from here.
	text := strings.TrimSpace(log)
	if d.BlockConflict != nil {
		c := *d.BlockConflict
		c.Text += "\n\nDesign run " + d.RunRequest.RunID + ":\n" + text
		if c.TriageVersion != "" {
			c.DesignRunID = d.RunRequest.RunID
		}
		c.Status, c.WaitingOn = "owner", "owner"
		return l.saveBlockConflict(ctx, c)
	}
	c := item.Conflict{ConflictID: "conflict-" + d.RunRequest.RunID, ItemID: d.ItemID, RunID: d.RunRequest.RunID, Text: text, Status: "raised", WaitingOn: "design", BlockedCriteria: []string{}}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		if err := tx.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id = ?`, d.ItemID).Scan(&c.BodyRef); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT criterion_id FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`, d.ItemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`).MatchString(text) {
				c.BlockedCriteria = append(c.BlockedCriteria, id)
			}
		}
		return rows.Err()
	})
	r.Close()
	if err != nil {
		return err
	}
	// A report that names no criterion is not permission to guess which one it
	// meant. Keep that absence explicit alongside the full report for the designer.
	if len(c.BlockedCriteria) == 0 {
		c.BlockedCriteria = []string{"not stated in report"}
	}
	if c.BodyRef == "" || IsTrunkFixer(d.ItemID) {
		// A generated repair has no design decision to dispatch. In particular a
		// fence refusal goes straight to the owner with the needed paths in the
		// pinned report, rather than waiting behind its own broken-trunk hold.
		c.WaitingOn, c.Status = "owner", "owner"
	}
	l.reportf("loop: %s conflict %s waits on %s: %s\n", c.ItemID, c.ConflictID, c.WaitingOn, c.Text)
	return l.saveBlockConflict(ctx, c)
}

func (l *Loop) blockReference(ctx context.Context, id string) (string, error) {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer r.Close()
	var ref string
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		return tx.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id = ?`, id).Scan(&ref)
	})
	return ref, err
}

// definitionChanged compares the pinned blobs, not the lane's mutable filesystem.
// Unrelated sections of the same document remain outside the protected block.
func (l *Loop) definitionChanged(ctx context.Context, c Candidate, ref string) (bool, error) {
	file, _, _ := strings.Cut(ref, "#")
	if file == "" {
		return false, nil
	}
	if filepath.IsAbs(file) || strings.HasPrefix(filepath.Clean(file), "..") {
		return false, Usagef("item block %s is not repository-relative", ref)
	}
	bodies, raws := []string{}, []string{}
	for _, sha := range []string{c.BaseSHA, c.HeadSHA} {
		// A deleted/new blob is an explicit input; other git failures are errors.
		files, err := l.git(ctx, c.Worktree, "ls-tree", "--name-only", sha, "--", file)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(files) == "" {
			bodies, raws = append(bodies, "document missing"), append(raws, "")
			continue
		}
		raw, err := l.git(ctx, c.Worktree, "show", sha+":"+file)
		if err != nil {
			return false, err
		}
		bodies, raws = append(bodies, item.DefinitionBodyContent(ref, c.ItemID, []byte(raw))), append(raws, raw)
	}
	if bodies[0] == bodies[1] {
		return false, nil
	}
	// A conflict-fix run of a candidate the loop retired may carry that retirement: the
	// prepared head it re-applies holds the loop's own commit removing this block. That
	// removal is the loop's, not the worker's, so the head is compared against exactly what
	// the loop's retirement makes of this base — never excused as any edit to the block.
	if raws[0] != "" && raws[1] != "" && isConflictFixRun(c.RunID) {
		retired, err := l.retiredOriginal(ctx, strings.TrimSuffix(c.RunID, ConflictFixSuffix))
		if err != nil {
			return false, err
		}
		if retired {
			if text, found := item.RetireItemBlock(raws[0], c.ItemID); found && item.DefinitionBodyContent(ref, c.ItemID, []byte(text)) == bodies[1] {
				return false, nil
			}
		}
	}
	return true, nil
}

func (l *Loop) answerBlock(ctx context.Context, candidate Candidate) error {
	c := *candidate.BlockConflict
	file, _, _ := strings.Cut(c.BodyRef, "#")
	raw, err := l.git(ctx, l.cfg.Root, "show", TrunkRef+":"+file)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// An answered conflict returns its item to the queue; a terminal item stays put.
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "conflict-answer"); err != nil {
			return err
		}
		report, err := item.Sync(ctx, tx.Reader(), tx, item.SyncInput{Root: l.cfg.Root, Documents: []item.SyncDocument{{Path: file, Content: []byte(raw)}}, At: l.cfg.Now().UTC()})
		if err != nil {
			return err
		}
		if len(report.Findings) != 0 || len(report.Frozen) != 0 {
			return fmt.Errorf("landed conflict answer %s could not sync its definition: %s", c.ConflictID, mustJSON(report))
		}
		found := false
		for _, ids := range [][]string{report.Created, report.Updated, report.Unchanged} {
			for _, id := range ids {
				if id == c.ItemID {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("landed conflict answer %s does not declare item %s in %s", c.ConflictID, c.ItemID, file)
		}
		c.Status, c.WaitingOn, c.AnswerHead = "answered", "build", candidate.HeadSHA
		return item.RecordConflict(tx, c, l.cfg.Now().UTC())
	})
}

func mustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
