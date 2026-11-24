package drain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// ReasonTriageNoBlock is a prerequisite of the document-fed Git landing workflow,
// not of the queue: an undeclared request must be shaped before it can be built.
const ReasonTriageNoBlock = "triage-no-block"

func documentPrerequisites(r *store.Reader, root string) (map[string]bool, map[string]string, map[string]string, error) {
	missing := map[string]bool{}
	rewrites := map[string]string{}
	owners := map[string]string{}
	if root == "" {
		return missing, rewrites, owners, nil
	}
	docs, err := docshare.ReadQueue(root)
	if err != nil {
		return nil, nil, nil, err
	}
	report, err := item.Sync(context.Background(), r, nil, item.SyncInput{Root: root, Documents: docs})
	if err != nil {
		return nil, nil, nil, err
	}
	if report.DocumentFed {
		for _, id := range report.UnsharedOpenIDs {
			// A generated item (IsGeneratedItem) is filed by the loop or produced by sync,
			// not requested by anyone: its brief is its criteria, and it has no block of its
			// own to declare it in. Triage would send it to the owner for a document it can
			// never have.
			if IsGeneratedItem(id) {
				continue
			}
			intake, err := readIdeaIntake(r, id)
			if err != nil {
				return nil, nil, nil, err
			}
			// Saved ideas deliberately have no block: their turn creates new items,
			// rather than triaging a declaration of the idea itself.
			if intake == nil {
				missing[id] = true
			}
		}
	}
	for id, notes := range item.ProseRewriteFindings(report) {
		var state string
		rewrite := "rewrite-" + id
		err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, rewrite).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		if state != StateLanded {
			rewrites[id] = rewrite
			for _, note := range notes {
				if strings.HasPrefix(note.Message, "criteria-count:") {
					owners[rewrite] = ReasonProseCriteriaCount + ": the original has more than eight criteria; preserving them cannot pass the guide. The owner must reconcile the original criteria before a rewrite can run."
				}
			}
		}
	}
	return missing, rewrites, owners, nil
}

func documentPrerequisitesFromInputs(root string, syncState item.SyncStoreState, intakes map[string]ideaIntakeResult, itemStates map[string]string) (map[string]bool, map[string]string, map[string]string, error) {
	missing := map[string]bool{}
	rewrites := map[string]string{}
	owners := map[string]string{}
	if root == "" {
		return missing, rewrites, owners, nil
	}
	docs, err := docshare.ReadQueue(root)
	if err != nil {
		return nil, nil, nil, err
	}
	fed, unsharedOpenIDs, proseFindings, err := item.CheckDocumentSharing(syncState, root, docs)
	if err != nil {
		return nil, nil, nil, err
	}
	if fed {
		for _, id := range unsharedOpenIDs {
			if IsGeneratedItem(id) {
				continue
			}
			res := intakes[id]
			if res.err != nil {
				return nil, nil, nil, res.err
			}
			if res.intake == nil {
				missing[id] = true
			}
		}
	}
	for id, notes := range proseFindings {
		rewrite := "rewrite-" + id
		state, exists := itemStates[rewrite]
		if !exists {
			continue
		}
		if state != StateLanded {
			rewrites[id] = rewrite
			for _, note := range notes {
				if strings.HasPrefix(note.Message, "criteria-count:") {
					owners[rewrite] = ReasonProseCriteriaCount + ": the original has more than eight criteria; preserving them cannot pass the guide. The owner must reconcile the original criteria before a rewrite can run."
				}
			}
		}
	}
	return missing, rewrites, owners, nil
}

// requestVersion reads only the request's three fields, never its execution state.
// The criteria order is stable; neither a rank edit nor a disposition grants a run.
func requestVersion(r *store.Reader, id string) (string, string, error) {
	fields, err := readStoredBlockFields(r, id)
	if err != nil {
		return "", "", err
	}
	return requestVersionFromStored(fields)
}

func requestVersionFromStored(fields storedBlockFields) (string, string, error) {
	raw, err := json.Marshal([]any{fields.title, fields.bodyRef, fields.criteria})
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), fields.bodyRef, nil
}

func triageDocument(ref string) string {
	file, _, _ := strings.Cut(ref, "#")
	clean := path.Clean(file)
	if file == "" || strings.Contains(file, "\\") || path.IsAbs(file) || clean == ".." || strings.HasPrefix(clean, "../") || !strings.HasPrefix(clean, "docs/") || !strings.HasSuffix(clean, ".md") {
		return ""
	}
	return clean
}

func (p *planner) triageDecision(r *store.Reader, row itemRow) (Decision, error) {
	var (
		version, ref string
		err          error
	)
	if p.preRead != nil {
		fields, ok := p.preRead.blockFields[row.ItemID]
		if !ok {
			return Decision{}, sql.ErrNoRows
		}
		version, ref, err = requestVersionFromStored(fields)
	} else {
		version, ref, err = requestVersion(r, row.ItemID)
	}
	if err != nil {
		return Decision{}, err
	}
	c := item.Conflict{ConflictID: "triage-" + row.ItemID + "-" + version, ItemID: row.ItemID, BodyRef: ref, Text: "Request has no document block; triage must declare it before it is built.", Status: "raised", WaitingOn: "design", BlockedCriteria: []string{}, TriageVersion: version}
	// Look through history, not just the latest conflict: changing A to B and back
	// to A must not spend a second run for the same request version.
	var raw []byte
	if p.preRead != nil {
		var ok bool
		if raw, ok = p.preRead.triageConflicts[triageConflictKey{itemID: row.ItemID, version: version}]; !ok {
			err = sql.ErrNoRows
		}
	} else {
		err = r.QueryRow(`SELECT payload FROM events WHERE kind='item.conflict' AND json_extract(payload,'$.item_id')=? AND json_extract(payload,'$.triage_version')=? ORDER BY seq DESC LIMIT 1`, row.ItemID, version).Scan(&raw)
	}
	if err == nil {
		if err = json.Unmarshal(raw, &c); err != nil {
			return Decision{}, err
		}
		if c.DesignRunID != "" && p.classes[c.DesignRunID] == AttemptInterrupted {
			// The claim stamps its run on the conflict before the run starts, so a stamped
			// run is not proof that triage ran. A run a loop stop interrupted is no attempt
			// (failover.go): the version keeps its one triage run, including after an owner
			// hold recorded from the stamp alone. A never-started claim is released before
			// planning (releaseUnlaunchedClaims).
			interrupted := c.DesignRunID
			c.Status, c.WaitingOn, c.DesignRunID, c.RunID = "raised", "design", "", ""
			c.Text = "Request has no document block; triage must declare it before it is built.\nTriage run " + interrupted + " was interrupted before it answered; an interrupted run is no attempt, so triage is dispatched again."
		} else if c.Status != "owner" && p.failedBackendFor(row.ItemID, c.DesignRunID) != "" {
			// A machinery ending did not perform triage. Keep the failed run's
			// identity so conflictDecision waits for, then reuses, that backend.
			c.Status, c.WaitingOn = "raised", "design"
		} else if c.DesignRunID != "" {
			c.Status, c.WaitingOn = "owner", "owner"
			if !strings.Contains(c.Text, "still has no document block") {
				c.Text += "\nRequest still has no document block after triage; the owner must close it or answer the conflict."
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Decision{}, err
	}
	if triageDocument(ref) == "" {
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text = "Request has no repository-relative Markdown document under docs/ in its body reference; sync reads only that document scope. The owner must supply one or close the item."
	}
	d := p.conflictDecision(row, c)
	d.BlockConflict = &c
	if c.WaitingOn == "design" {
		if d.Action == ActionDispatch || d.Reason == ReasonConflictDesign {
			d.Reason = ReasonTriageNoBlock
		}
		if d.RunRequest != nil {
			c.RunID = d.RunRequest.RunID
			d.BlockConflict = &c
		}
	}
	identifyDecision(&d)
	return d, nil
}

func triageTask(recorded stagedItem) string {
	return fmt.Sprintf(`Triage this stored request; do not build it yet.

Title: %s
Body reference: %s
Stored seat: %s; workflow: %s. Preserve this routing in the block unless shaping the request requires a different next step.
The criteria below are the stored request, not acceptance of its wording.

Produce exactly one of these three results:
1. Write a ready item block for %s in the referenced document. Keep the same id; you may tidy the wording and shape rough text into designable work in this run. Commit it for landing through the gate like any design change.
2. If the request is too thin to shape, declare that same item and write a numbered question to the owner in the same document, saying exactly what is missing. It will wait on question-open after landing.
3. Report that this request duplicates a named item or is already done, explaining the evidence. Commit nothing for this result; the report goes to an owner conflict.

Follow the prose guide at .agents/skills/tillr-prose-style/SKILL.md. Its ready-item shape requires an imperative heading of at most twelve words, a paragraph explaining why before the directive, valid allowed paths, item-id dependencies, one to eight nonempty criteria, and the current prose="2" mark. Read the guide before writing. The loop refuses every sync finding or warning on your block, including prose-unmarked and prose-shape. Do not implement the original request.`, recorded.Title, recorded.BodyRef, recorded.Seat, recorded.Workflow, recorded.ItemID)
}

// validateTriage reads the pinned candidate's tracked documents rather than the
// mutable lane tree. Use the same sync parser, including cross-document checks.
func (l *Loop) validateTriage(ctx context.Context, c Candidate) (string, error) {
	names, err := l.git(ctx, c.Worktree, "ls-tree", "-r", "-z", "--name-only", c.HeadSHA, "--", "docs")
	if err != nil {
		return "", err
	}
	docs := []item.SyncDocument{}
	file := triageDocument(c.BlockConflict.BodyRef)
	seen := false
	for _, name := range strings.Split(names, "\x00") {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		raw, err := l.git(ctx, c.Worktree, "show", c.HeadSHA+":"+name)
		if err != nil {
			return "", err
		}
		docs = append(docs, item.SyncDocument{Path: name, Content: []byte(raw)})
		if name == file {
			seen = item.DefinitionBlockExists(name, c.ItemID, []byte(raw))
		}
	}
	if !seen {
		return "triage-no-block: candidate does not declare the requested item in " + file, nil
	}
	r, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return "", err
	}
	defer r.Close()
	detail := ""
	err = r.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec("SAVEPOINT triage_admission"); err != nil {
			return err
		}
		// Run the real writer validation, including dependency cycles, then roll
		// every materialized row and event back before leaving this transaction.
		report, err := item.Sync(ctx, tx.Reader(), tx, item.SyncInput{Root: c.Worktree, Documents: docs, At: l.cfg.Now().UTC()})
		if err != nil {
			return err
		}
		for kind, notes := range [][]item.SyncNote{report.Findings, report.Warnings, report.Frozen} {
			for _, n := range notes {
				if n.ItemID == c.ItemID || (n.File == file && (n.ItemID == "" || kind != 1)) {
					detail += n.Rule + ": " + n.Message + "\n"
				}
			}
		}
		if _, err := tx.Exec("ROLLBACK TO SAVEPOINT triage_admission"); err != nil {
			return err
		}
		_, err = tx.Exec("RELEASE SAVEPOINT triage_admission")
		return err
	})
	return strings.TrimSpace(detail), err
}
