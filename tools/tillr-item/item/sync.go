package item

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// SyncRecordKind is the record the sync face emits: what the documents declared and what the
// store did about it. Like SeedRecordKind it is registered as neither an event kind nor a
// materializer: every write sync makes is its own item.add or item.update, appended by Apply,
// and a second record of the batch would claim a transition the log did not make.
const SyncRecordKind = "item.sync"

// The item block grammar (docs/design/queue-persistence-ruling.md Section 1, D-RS1, and the
// seat amendment of 2026-11-15):
//
//	### <Title>
//	<!-- tillr: item id="<item_id>" paths="<prefix>,<prefix>" blocked_by="<id>,<id>" seat="<seat>" -->
//
//	- **c1:** <criterion text>
//
// A directive counts only when its opening `<!--` is the first non-whitespace on its line and
// the line is outside a fenced code block (D-TC1 in docs/design/tillr-convergence.md). Prose
// that writes the directive inside inline code never starts a line with it, so the forty-odd
// mentions in the two queue-persistence documents are not blocks.
var (
	directiveStart = docshare.Start
	attribute      = docshare.AttributePattern
	headingLine    = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)
	criterionLine  = regexp.MustCompile(`^[-*+]\s+\*\*(c[0-9]+):\*\*\s*(.*)$`)
	bulletTitle    = regexp.MustCompile(`^(\s*)[-*+]\s+\*\*(.+?)\*\*`)
	listMarker     = regexp.MustCompile(`^(?:[-*+]|[0-9]+[.)])\s`)
	itemIDShape    = regexp.MustCompile(`^item-[a-z0-9-]+$`)
	cloneIDShape   = regexp.MustCompile(`^(item-[a-z0-9-]+)-v[0-9]+$`)
	linkTarget     = regexp.MustCompile(`\]\(([^)\s]+)`)
)

// roadmapDocuments are the roadmaps whose `## Icebox` and `## Exploration` sections park a
// document: an item block in a parked document is synced as `not-started`, not `in-queue`.
var roadmapDocuments = []string{"docs/roadmap.md", "docs/pi/roadmap.md", "docs/tillr/roadmap.md"}

// parkedSections are the roadmap section headings that park the documents they link to. A
// heading counts when its text starts with one of these words, so `## Icebox 🧊` parks too.
var parkedSections = []string{"Icebox", "Exploration"}

// terminalStates are the item states no further work moves: an item in one of them is not
// open, so it is never counted as unshared residue.
var terminalStates = map[string]bool{
	"landed": true, "resolved-elsewhere": true, "closed": true, "abandoned": true, "deleted": true,
}

// SyncDocument is one tracked Markdown file: its repo-relative slash path and its bytes.
type SyncDocument = docshare.Document

// SyncInput is one sync run: the repository root the documents were read from (a path in a
// block that does not exist under it is a warning), the documents, and the clock.
type SyncInput struct {
	Root      string
	Documents []SyncDocument
	At        time.Time
}

// SyncNote is one finding, warning or frozen definition, located where an author has to look:
// the file, heading, and line. For design-without-item, File names the design while Roadmap
// names the file containing the linking heading and line; other notes locate the directive.
type SyncNote struct {
	File    string `json:"file"`
	Heading string `json:"heading"`
	Line    int    `json:"line"`
	ItemID  string `json:"item_id,omitempty"`
	Roadmap string `json:"roadmap,omitempty"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// SyncReport is item.sync's payload. The id lists are what this run wrote (or, on a dry run,
// would write); the counts beside them are the same lists' lengths, so a reader that wants
// only the summary does not count.
type SyncReport struct {
	Root      string `json:"root"`
	DryRun    bool   `json:"dry_run"`
	Documents int    `json:"documents"`
	Blocks    int    `json:"blocks"`

	Created   []string   `json:"created"`
	Updated   []string   `json:"updated"`
	Unchanged []string   `json:"unchanged"`
	Frozen    []SyncNote `json:"frozen"`

	CreatedCount   int `json:"created_count"`
	UpdatedCount   int `json:"updated_count"`
	UnchangedCount int `json:"unchanged_count"`
	FrozenCount    int `json:"frozen_count"`

	Warnings []SyncNote `json:"warnings"`
	Findings []SyncNote `json:"findings"`
	// SkippedFiles are the documents no item was written from because they carry a finding.
	SkippedFiles []string `json:"skipped_files"`

	// UnsharedOpen counts the store's non-terminal items that no document block declares:
	// private residue (docs/design/queue-persistence-ruling.md Section 4). The oldest one's
	// last write is named so the residue's age is visible, not only its size.
	DocumentFed        bool     `json:"document_fed"`
	UnsharedOpen       int      `json:"unshared_open"`
	UnsharedOpenIDs    []string `json:"unshared_open_ids"`
	UnsharedOpenOldest string   `json:"unshared_open_oldest,omitempty"`
	// StaleDocBlocks counts the document blocks whose item the store already holds as
	// `landed`: a block the landing should have deleted.
	StaleDocBlocks   int      `json:"stale_doc_blocks"`
	StaleDocBlockIDs []string `json:"stale_doc_block_ids"`
}

// syncBlock is one parsed directive with the item it declares.
type syncBlock struct {
	file    string
	heading string
	line    int
	payload itemPayload
	// Line ranges, with an exclusive end, retain the exact source bytes retirement removes.
	directiveEnd   int
	criterionLines []int
	// parked is whether a roadmap's Icebox or Exploration section links to the block's
	// document, which makes a new item `not-started` rather than `in-queue`.
	parked         bool
	proseParagraph bool
}

// ReadSyncDocuments answers the Markdown files git tracks under <root>/docs/, read from the
// working tree. Tracked is the rule because an untracked draft is not a shared document: a
// block in it would enter one developer's store and no one else's. A tracked file deleted
// from the working tree is skipped, since there is nothing to read.
func ReadSyncDocuments(root string) ([]SyncDocument, error) {
	docs, err := docshare.ReadTracked(root)
	if err != nil {
		var subdir docshare.SubdirectoryError
		if errors.As(err, &subdir) {
			return nil, Usagef("%s", err)
		}
		return nil, Externalf("%s", err)
	}
	return docs, nil
}

// syncPlan is what one sync decided before writing: per file, the items to write in write
// order, and the report as a dry run would print it.
type syncPlan struct {
	report SyncReport
	// writes are the files with something to write, in the order they may be written, and
	// each file's payloads in the order its items may be written.
	writes []fileWrites
}

type fileWrites struct {
	file     string
	heading  map[string]SyncNote // item id -> where its block is, for a refusal's location
	payloads []itemPayload
	created  map[string]bool
}

// Sync reads every item block in the documents and materializes the items it declares
// through Apply — the one record→row mapping — in the caller's transaction. A nil tx is a dry
// run: the same report, nothing written. A nil reader is a store that does not exist yet.
//
// What sync owns and what it never touches (D-TC2): it creates a declared item the store does
// not hold, `in-queue` or `not-started` when its document is parked; it updates the
// definition of an item it holds — title, fence, blockers, seat, criteria text, body_ref and
// design_doc — when no criterion of that item carries a recorded state; and it leaves the
// item's state, rank, runs and criterion states exactly as the store holds them. An item whose
// definition differs while a criterion carries a state is reported `definition-frozen` and
// left alone, because rewriting presented criteria would make a recorded verdict stop meaning
// what it said.
//
// A file with a finding writes nothing; every other file is written. Each file is applied
// under its own savepoint, so a refusal Apply makes for one file (an edge cycle, say) rolls
// back that file alone and becomes its finding.
func Sync(ctx context.Context, r *store.Reader, tx *store.Tx, in SyncInput) (SyncReport, error) {
	plan, err := planSync(r, in)
	if err != nil {
		return SyncReport{}, err
	}
	if tx == nil {
		plan.report.DryRun = true
		return finishReport(plan.report), nil
	}
	report := plan.report
	report.Created, report.Updated = []string{}, []string{}
	for _, fw := range plan.writes {
		if _, err := tx.Exec(`SAVEPOINT item_sync_file`); err != nil {
			return SyncReport{}, Internalf("opening the savepoint for %s: %v", fw.file, err)
		}
		var refused error
		var refusedID string
		for _, payload := range fw.payloads {
			env, err := envelopeOf(RecordKind, "item-"+payload.ItemID, in.At, payload)
			if err != nil {
				return SyncReport{}, err
			}
			if _, err := Apply(ctx, tx, env); err != nil {
				if ExitCode(err) == record.ExitInternal {
					return SyncReport{}, err
				}
				refused, refusedID = err, payload.ItemID
				break
			}
		}
		if refused != nil {
			if _, err := tx.Exec(`ROLLBACK TO SAVEPOINT item_sync_file`); err != nil {
				return SyncReport{}, Internalf("rolling back %s: %v", fw.file, err)
			}
			at := fw.heading[refusedID]
			rule := "apply-refused"
			if rej, ok := RejectionOf(refused); ok {
				rule = rej.Rule
			}
			at.Rule, at.Message = rule, refused.Error()
			report.Findings = append(report.Findings, at)
			report.SkippedFiles = append(report.SkippedFiles, fw.file)
		} else {
			for _, payload := range fw.payloads {
				if fw.created[payload.ItemID] {
					report.Created = append(report.Created, payload.ItemID)
				} else {
					report.Updated = append(report.Updated, payload.ItemID)
				}
			}
		}
		if _, err := tx.Exec(`RELEASE SAVEPOINT item_sync_file`); err != nil {
			return SyncReport{}, Internalf("releasing the savepoint for %s: %v", fw.file, err)
		}
	}
	if err := replaceDesignWithoutItems(tx, report.Warnings, in.At.UnixMilli()); err != nil {
		return SyncReport{}, err
	}
	return finishReport(report), nil
}

// finishReport sorts the lists a reader compares and fills the counts.
func finishReport(report SyncReport) SyncReport {
	sort.Strings(report.SkippedFiles)
	report.SkippedFiles = uniqueStrings(report.SkippedFiles)
	sort.SliceStable(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	report.CreatedCount = len(report.Created)
	report.UpdatedCount = len(report.Updated)
	report.UnchangedCount = len(report.Unchanged)
	report.FrozenCount = len(report.Frozen)
	return report
}

// planSync parses every document, validates every block, compares each with the store and
// decides what to write. It writes nothing.
func planSync(r *store.Reader, in SyncInput) (syncPlan, error) {
	report := SyncReport{
		Root: in.Root, Documents: len(in.Documents),
		Created: []string{}, Updated: []string{}, Unchanged: []string{}, Frozen: []SyncNote{},
		Warnings: []SyncNote{}, Findings: []SyncNote{}, SkippedFiles: []string{},
		UnsharedOpenIDs: []string{}, StaleDocBlockIDs: []string{},
	}
	parked := parkedDocuments(in.Documents)

	var blocks []syncBlock
	bad := map[string]bool{}
	for _, doc := range in.Documents {
		found, findings := parseItemBlocks(doc.Path, doc.Content)
		for i := range found {
			found[i].parked = parked[doc.Path]
			if ref := found[i].payload.Workflow; ref != "" {
				rule, message := "", ""
				if !WorkflowRefValid(ref) {
					rule, message = "invalid-workflow", "workflow must have the form <name>@v<N>"
				} else if info, err := os.Stat(filepath.Join(in.Root, "config", "tillr", "workflows", ref+".json")); err != nil || !info.Mode().IsRegular() {
					rule, message = "workflow-missing", "workflow file config/tillr/workflows/"+ref+".json does not exist"
				}
				if rule != "" {
					findings = append(findings, SyncNote{File: doc.Path, Heading: found[i].heading, Line: found[i].line, ItemID: found[i].payload.ItemID, Rule: rule, Message: message})
				}
			}
			report.Warnings = append(report.Warnings, proseWarnings(found[i])...)
		}
		blocks = append(blocks, found...)
		for _, f := range findings {
			bad[f.File] = true
		}
		report.Findings = append(report.Findings, findings...)
	}
	report.Blocks = len(blocks)

	// The fence's paths may name what a slice creates, so a path that is not on disk is a
	// warning, never a finding.
	if in.Root != "" {
		for _, b := range blocks {
			for _, p := range b.payload.AllowedPaths {
				if _, _, _, checkout := ForkCheckoutPath(p); checkout {
					continue // staging materializes the dispatched lane's checkout
				}
				if _, err := os.Stat(filepath.Join(in.Root, filepath.FromSlash(p))); err != nil {
					report.Warnings = append(report.Warnings, note(b, "path-missing",
						fmt.Sprintf("paths names %q, which does not exist under the repository root; the slice may create it", p)))
				}
			}
		}
	}

	// One id, one block, across every document: two blocks with one id are two definitions
	// of one item, and neither file can be trusted to be the one meant.
	byID := map[string][]syncBlock{}
	for _, b := range blocks {
		if b.payload.ItemID != "" {
			byID[b.payload.ItemID] = append(byID[b.payload.ItemID], b)
		}
	}
	for id, same := range byID {
		if len(same) < 2 {
			continue
		}
		places := make([]string, len(same))
		for i, b := range same {
			places[i] = fmt.Sprintf("%s:%d", b.file, b.line)
		}
		sort.Strings(places)
		for _, b := range same {
			report.Findings = append(report.Findings, note(b, "duplicate-id",
				fmt.Sprintf("item %q is declared by more than one block (%s)", id, strings.Join(places, ", "))))
			bad[b.file] = true
		}
	}

	held := map[string]storedState{}
	if r != nil {
		// tillr-store init creates the core schema, not the item materialization.
		// Until a writing item operation creates it, this reader holds no items.
		// Check the schema rather than suppressing query errors on an existing table.
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'items')`).Scan(&exists); err != nil {
			return syncPlan{}, Internalf("checking the stored item schema: %v", err)
		}
		if exists {
			var err error
			if held, err = readStoredStates(r); err != nil {
				return syncPlan{}, err
			}
		} else {
			r = nil
		}
	}

	stages, err := designStageRoles(in.Root)
	if err != nil {
		return syncPlan{}, err
	}
	report.Warnings = append(report.Warnings, designWithoutItems(in.Documents, held, stages)...)

	// Refuse only new copies of an unfinished stored item. Existing version-suffixed
	// ids keep their definitions and history; sync does not retroactively rename them.
	for _, b := range blocks {
		id := b.payload.ItemID
		if _, exists := held[id]; exists {
			continue
		}
		match := cloneIDShape.FindStringSubmatch(id)
		if match == nil {
			continue
		}
		original := match[1]
		if st, exists := held[original]; exists && !terminalStates[st.state] {
			report.Findings = append(report.Findings, note(b, "clone-of-held-item",
				fmt.Sprintf("item %q copies held item %q in state %q; retry the original under its own id with tillr drain retry --item %s (see tillr drain retry --help)", id, original, st.state, original)))
			bad[b.file] = true
		}
	}

	// A blocker must be something: a block some document declares, or an item the store
	// already holds.
	for _, b := range blocks {
		for _, blocker := range b.payload.BlockedBy {
			if _, declared := byID[blocker]; declared {
				continue
			}
			if _, stored := held[blocker]; stored {
				continue
			}
			report.Findings = append(report.Findings, note(b, "unknown-blocker",
				fmt.Sprintf("blocked_by names %q, which no document block declares and the store does not hold", blocker)))
			bad[b.file] = true
		}
	}
	// Class weakening is a finding even when text is frozen, including in dry runs.
	if r != nil {
		for _, b := range blocks {
			if bad[b.file] {
				continue
			}
			stored, found, err := storedItem(r, b.payload.ItemID)
			if err != nil {
				return syncPlan{}, err
			}
			if found {
				if err := checkClaimMoves(b.payload.ItemID, stored.Criteria, b.payload.Criteria); err != nil {
					rejection, _ := RejectionOf(err)
					report.Findings = append(report.Findings, note(b, rejection.Rule, err.Error()))
					bad[b.file] = true
				}
			}
		}
	}
	// A blocker declared only in a file that writes nothing will not exist when this block's
	// edge is written. That makes this file a finding too, and the rule is applied until no
	// file changes, since one skipped file can strand another.
	for changed := true; changed; {
		changed = false
		for _, b := range blocks {
			if bad[b.file] {
				continue
			}
			for _, blocker := range b.payload.BlockedBy {
				if _, stored := held[blocker]; stored {
					continue
				}
				usable := false
				for _, decl := range byID[blocker] {
					if !bad[decl.file] {
						usable = true
					}
				}
				if usable {
					continue
				}
				report.Findings = append(report.Findings, note(b, "blocker-unavailable",
					fmt.Sprintf("blocked_by names %q, whose only block is in a document with a finding, and the store does not hold it", blocker)))
				bad[b.file] = true
				changed = true
				break
			}
		}
	}

	states := make(map[string]string, len(held))
	for id, st := range held {
		states[id] = st.state
	}
	fed, missing := docshare.Sharing(in.Documents, states)
	report.DocumentFed = fed
	unshared := make(map[string]bool, len(missing))
	for _, id := range missing {
		unshared[id] = true
	}

	// The counters read the store as it is: sync never moves an item's state, so a block's
	// item is `landed` after this run exactly when it was before it.
	ids := make([]string, 0, len(held))
	for id := range held {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var oldest int64
	for _, id := range ids {
		st := held[id]
		if !unshared[id] {
			continue
		}
		report.UnsharedOpenIDs = append(report.UnsharedOpenIDs, id)
		if oldest == 0 || (st.updatedAt > 0 && st.updatedAt < oldest) {
			oldest = st.updatedAt
		}
	}
	report.UnsharedOpen = len(report.UnsharedOpenIDs)
	if oldest > 0 {
		report.UnsharedOpenOldest = time.UnixMilli(oldest).UTC().Format(time.RFC3339)
	}
	for _, b := range blocks {
		if st, ok := held[b.payload.ItemID]; ok && st.state == "landed" {
			report.StaleDocBlockIDs = append(report.StaleDocBlockIDs, b.payload.ItemID)
		}
	}
	report.StaleDocBlocks = len(report.StaleDocBlockIDs)

	// Classify every block of a file with no finding.
	perFile := map[string]*fileWrites{}
	var fileOrder []string
	for _, b := range blocks {
		if bad[b.file] {
			continue
		}
		// A landed block is stale document residue, not a definition to apply.
		// Skipping just this block lets its file's other items sync normally.
		if st, ok := held[b.payload.ItemID]; ok && st.state == "landed" {
			continue
		}
		desired := b.payload
		var stored itemPayload
		found := false
		if r != nil {
			var err error
			if stored, found, err = storedItem(r, desired.ItemID); err != nil {
				return syncPlan{}, err
			}
		}
		fw := perFile[b.file]
		if fw == nil {
			fw = &fileWrites{file: b.file, heading: map[string]SyncNote{}, created: map[string]bool{}}
			perFile[b.file] = fw
			fileOrder = append(fileOrder, b.file)
		}
		fw.heading[desired.ItemID] = note(b, "", "")
		if !found {
			desired.State = "in-queue"
			if b.parked {
				desired.State = "not-started"
			}
			fw.payloads = append(fw.payloads, desired)
			fw.created[desired.ItemID] = true
			report.Created = append(report.Created, desired.ItemID)
			continue
		}
		field := definitionDiff(desired, stored)
		if field == "" {
			report.Unchanged = append(report.Unchanged, desired.ItemID)
			continue
		}
		// Class-only hardening does not rewrite what the judge read.
		withoutClaims := desired
		withoutClaims.Criteria = append([]criterion(nil), desired.Criteria...)
		for i := range withoutClaims.Criteria {
			for _, held := range stored.Criteria {
				if held.CriterionID == withoutClaims.Criteria[i].CriterionID {
					withoutClaims.Criteria[i].ClaimClass = held.ClaimClass
				}
			}
		}
		if judged := judgedCriterion(stored); judged != "" && definitionDiff(withoutClaims, stored) != "" {
			report.Frozen = append(report.Frozen, note(b, "definition-frozen",
				fmt.Sprintf("the block's %s differs from the store's, and criterion %q already carries the state %q, so the stored definition stands: a presented criterion is not rewritten", field, judged, criterionState(stored, judged))))
			continue
		}
		if terminalState(stored.State) {
			report.Frozen = append(report.Frozen, note(b, "state-terminal",
				fmt.Sprintf("item %q is %q; its stored definition stands", stored.ItemID, stored.State)))
			continue
		}
		// The definition is the document's; everything the store learned is the store's.
		desired.State = stored.State
		desired.Rank = stored.Rank
		fw.payloads = append(fw.payloads, desired)
		report.Updated = append(report.Updated, desired.ItemID)
	}
	for file := range bad {
		report.SkippedFiles = append(report.SkippedFiles, file)
	}

	writes, err := orderFileWrites(perFile, fileOrder)
	if err != nil {
		return syncPlan{}, err
	}
	return syncPlan{report: report, writes: writes}, nil
}

// orderFileWrites answers the files with writes in an order where a file is written after
// the files holding the blockers its items name, and each file's items in an order where a
// blocker is written before the item it blocks. Files that block each other both ways are
// written in path order, and Apply's own edge rule then refuses the one that cannot go first,
// which becomes that file's finding.
func orderFileWrites(perFile map[string]*fileWrites, order []string) ([]fileWrites, error) {
	owner := map[string]string{}
	for _, file := range order {
		for _, p := range perFile[file].payloads {
			owner[p.ItemID] = file
		}
	}
	sort.Strings(order)
	done := map[string]bool{}
	var out []fileWrites
	for len(done) < len(order) {
		pick := ""
		for _, file := range order {
			if done[file] {
				continue
			}
			ready := true
			for _, p := range perFile[file].payloads {
				for _, blocker := range p.BlockedBy {
					if o, ok := owner[blocker]; ok && o != file && !done[o] {
						ready = false
					}
				}
			}
			if ready {
				pick = file
				break
			}
		}
		if pick == "" {
			// The remaining files block each other: path order, and Apply decides.
			for _, file := range order {
				if !done[file] {
					pick = file
					break
				}
			}
		}
		done[pick] = true
		fw := *perFile[pick]
		if len(fw.payloads) == 0 {
			continue
		}
		ordered, err := orderSeed(fw.payloads)
		if err != nil {
			// A cycle inside one file is that file's to fix; Apply would refuse it edge by
			// edge, so the payloads go in document order and the savepoint carries the refusal.
			ordered = fw.payloads
		}
		fw.payloads = ordered
		out = append(out, fw)
	}
	return out, nil
}

// storedState is the part of a stored item the counters read.
type storedState struct {
	state     string
	designDoc string
	updatedAt int64
}

func readStoredStates(r *store.Reader) (map[string]storedState, error) {
	rows, err := r.Query(`SELECT item_id, state, COALESCE(updated_at, 0), COALESCE(design_doc, '') FROM items`)
	if err != nil {
		return nil, Internalf("reading the stored items: %v", err)
	}
	defer rows.Close()
	held := map[string]storedState{}
	for rows.Next() {
		var id string
		var st storedState
		if err := rows.Scan(&id, &st.state, &st.updatedAt, &st.designDoc); err != nil {
			return nil, Internalf("reading a stored item: %v", err)
		}
		held[id] = st
	}
	if err := rows.Err(); err != nil {
		return nil, Internalf("reading the stored items: %v", err)
	}
	return held, nil
}

// definitionDiff names the first field of the item's definition where the block and the
// store disagree, or "" when they agree. The definition is everything sync derives from the
// document; the item's state, its rank and its criteria's states are not part of it.
func definitionDiff(block, stored itemPayload) string {
	switch {
	case block.Title != stored.Title:
		return "title"
	case block.BodyRef != stored.BodyRef:
		return "body_ref"
	case block.DesignDoc != stored.DesignDoc:
		return "design_doc"
	case block.Seat != stored.Seat:
		return "seat"
	case block.JudgeMode != stored.JudgeMode:
		return "judge"
	case block.Workflow != stored.Workflow:
		return "workflow"
	case block.Prose != stored.Prose:
		return "prose"
	case strings.Join(block.AllowedPaths, "\x00") != strings.Join(stored.AllowedPaths, "\x00"):
		return "paths"
	case strings.Join(block.BlockedBy, "\x00") != strings.Join(stored.BlockedBy, "\x00"):
		return "blocked_by"
	case len(block.Criteria) != len(stored.Criteria):
		return "criteria"
	}
	for i, c := range block.Criteria {
		if c.CriterionID != stored.Criteria[i].CriterionID || c.Text != stored.Criteria[i].Text || c.ClaimClass != stored.Criteria[i].ClaimClass {
			return "criterion " + c.CriterionID
		}
	}
	return ""
}

// judgedCriterion answers the first stored criterion carrying a state, or "".
func judgedCriterion(stored itemPayload) string {
	for _, c := range stored.Criteria {
		if c.State != "" {
			return c.CriterionID
		}
	}
	return ""
}

func criterionState(stored itemPayload, id string) string {
	for _, c := range stored.Criteria {
		if c.CriterionID == id {
			return c.State
		}
	}
	return ""
}

// note locates a finding, warning or frozen report at its block.
func note(b syncBlock, rule, message string) SyncNote {
	return SyncNote{File: b.file, Heading: b.heading, Line: b.line, ItemID: b.payload.ItemID, Rule: rule, Message: message}
}

// parkedDocuments answers the documents a roadmap's `## Icebox` or `## Exploration` section
// links to, as repo-relative slash paths. Only the roadmaps among the documents are read, so
// the answer is a function of the same tracked files the blocks came from.
func parkedDocuments(docs []SyncDocument) map[string]bool {
	parked := map[string]bool{}
	byPath := map[string][]byte{}
	for _, d := range docs {
		byPath[d.Path] = d.Content
	}
	for _, roadmap := range roadmapDocuments {
		content, ok := byPath[roadmap]
		if !ok {
			continue
		}
		inParked := false
		fence := fenceState{}
		for _, line := range splitLines(content) {
			if fence.step(line) {
				continue
			}
			if strings.HasPrefix(line, "## ") {
				title := strings.TrimSpace(strings.TrimPrefix(line, "## "))
				inParked = false
				for _, s := range parkedSections {
					if strings.HasPrefix(title, s) {
						inParked = true
					}
				}
				continue
			}
			if strings.HasPrefix(line, "# ") {
				inParked = false
				continue
			}
			if !inParked {
				continue
			}
			for _, m := range linkTarget.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				target, _, _ = strings.Cut(target, "#")
				if target == "" {
					continue
				}
				parked[path.Clean(path.Join(path.Dir(roadmap), target))] = true
			}
		}
	}
	return parked
}

// fenceState tracks fenced code blocks (``` and ~~~) line by line. step answers whether the
// line is fence content or a fence delimiter, which is every line no directive, heading or
// criterion may come from.
type fenceState struct{ docshare.Fence }

func (f *fenceState) step(line string) bool { return f.Step(line) }
func splitLines(content []byte) []string {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	return strings.Split(text, "\n")
}

// parseItemBlocks answers every item block in one document and the findings its blocks
// carry. It reads no store and no disk: the file's bytes are the whole input.
func parseItemBlocks(file string, content []byte) ([]syncBlock, []SyncNote) {
	lines := splitLines(content)
	fenced := make([]bool, len(lines))
	fence := fenceState{}
	for i, line := range lines {
		fenced[i] = fence.step(line)
	}
	isHeading := func(i int) (string, bool) {
		if fenced[i] {
			return "", false
		}
		m := headingLine.FindStringSubmatch(lines[i])
		if m == nil {
			return "", false
		}
		return strings.TrimSpace(strings.TrimRight(m[2], "#")), true
	}
	isDirective := func(i int) bool {
		return !fenced[i] && directiveStart.MatchString(strings.TrimLeft(lines[i], " \t"))
	}

	var blocks []syncBlock
	var findings []SyncNote
	heading := ""
	sectionStart := 0
	for i := 0; i < len(lines); i++ {
		if h, ok := isHeading(i); ok {
			heading = h
			sectionStart = i + 1
			continue
		}
		if !isDirective(i) {
			continue
		}
		b := syncBlock{file: file, heading: heading, line: i + 1,
			proseParagraph: hasProseParagraph(lines, fenced, sectionStart, i)}
		bad := func(rule, format string, args ...any) {
			findings = append(findings, note(b, rule, fmt.Sprintf(format, args...)))
		}

		// The directive runs to its `-->`, which may be lines below.
		var text strings.Builder
		end := -1
		for j := i; j < len(lines); j++ {
			text.WriteString(lines[j])
			text.WriteString("\n")
			if strings.Contains(lines[j], "-->") {
				end = j
				break
			}
		}
		if end < 0 {
			bad("malformed-directive", "the directive opened here is never closed with -->")
			break
		}
		b.directiveEnd = end + 1
		body := text.String()
		body = body[strings.Index(body, "<!--")+len("<!--"):]
		body = body[:strings.Index(body, "-->")]
		body = strings.TrimSpace(body)
		body = strings.TrimSpace(strings.TrimPrefix(body, "tillr:"))
		body = strings.TrimPrefix(body, "item")

		attrs, pending := docshare.Attributes(body)

		id := strings.TrimSpace(attrs["id"])
		b.payload.ItemID = id
		b.payload.Prose = attrs["prose"]
		b.payload.Workflow = attrs["workflow"]
		if judge, named := attrs["judge"]; named {
			if judge != "agent" {
				bad("invalid-judge", "judge %q is not agent: omit the attribute for mechanical retirement", judge)
			}
			b.payload.JudgeMode = judge
		}
		for _, f := range pending {
			bad(f[0], "%s", f[1])
		}
		switch {
		case id == "":
			bad("missing-id", "the directive names no id")
		case !itemIDShape.MatchString(id):
			bad("invalid-id", "id %q does not match ^item-[a-z0-9-]+$", id)
		}

		paths := splitList(attrs["paths"])
		if len(paths) == 0 {
			bad("missing-paths", "the directive names no paths: an item with no fence cannot be staged")
		}
		for _, p := range paths {
			if reason := badFencePath(p); reason != "" {
				bad("invalid-path", "paths carries %q: %s", p, reason)
			}
		}
		b.payload.AllowedPaths = paths

		blockers := splitList(attrs["blocked_by"])
		for _, blocker := range blockers {
			if blocker == id && id != "" {
				bad("invalid-blocker", "the item is blocked by itself")
			} else if !itemIDShape.MatchString(blocker) {
				bad("invalid-blocker", "blocked_by carries %q, which is not an item id", blocker)
			}
		}
		sort.Strings(blockers)
		b.payload.BlockedBy = blockers

		if seat, named := attrs["seat"]; named {
			if !KnownSeat(seat) {
				bad("invalid-seat", "seat %q is not one of the item model's seats: %s", seat, SeatList())
			}
			b.payload.Seat = seat
		}

		// The title is the enclosing roadmap bullet's bold lead when the directive sits
		// indented beneath one, and the nearest heading otherwise.
		title := heading
		if indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " \t")); indent > 0 {
			for k := i - 1; k >= 0; k-- {
				if _, ok := isHeading(k); ok {
					break
				}
				if fenced[k] {
					continue
				}
				if m := bulletTitle.FindStringSubmatch(lines[k]); m != nil && len(m[1]) < indent {
					title = strings.TrimSpace(m[2])
					break
				}
			}
		}
		title = strings.TrimSpace(title)
		if title == "" {
			bad("missing-title", "the block has no heading above it and no enclosing bullet, so the item has no title")
		}
		b.payload.Title = title
		b.payload.DesignDoc = file
		b.payload.BodyRef = file
		if heading != "" {
			b.payload.BodyRef = file + "#" + headingSlug(heading)
		}

		// Criteria run from the directive's end to the next heading or directive.
		var criteria []criterion
		seen := map[string]bool{}
		current := -1
		k := end + 1
		for ; k < len(lines); k++ {
			if _, ok := isHeading(k); ok {
				break
			}
			if isDirective(k) {
				break
			}
			if fenced[k] {
				current = -1
				continue
			}
			trimmed := strings.TrimSpace(lines[k])
			if m := criterionLine.FindStringSubmatch(trimmed); m != nil {
				b.criterionLines = append(b.criterionLines, k)
				if seen[m[1]] {
					bad("duplicate-criterion", "criterion %q appears twice", m[1])
					current = -1
					continue
				}
				seen[m[1]] = true
				criteria = append(criteria, criterion{CriterionID: m[1], Text: strings.TrimSpace(m[2])})
				current = len(criteria) - 1
				continue
			}
			if trimmed == "" || listMarker.MatchString(trimmed) || strings.HasPrefix(trimmed, "<!--") {
				current = -1
				continue
			}
			if current >= 0 {
				b.criterionLines = append(b.criterionLines, k)
				criteria[current].Text = strings.TrimSpace(criteria[current].Text + " " + trimmed)
			}
		}
		if !seen["c1"] {
			bad("missing-criterion", "the block carries no `- **c1:**` criterion: an item without one has nothing a verdict can judge")
		}
		claimIDs := map[string]bool{}
		for _, entry := range splitList(attrs["claims"]) {
			criterionID, class, ok := strings.Cut(entry, ":")
			criterionID, class = strings.TrimSpace(criterionID), strings.TrimSpace(class)
			if !ok || !claimClasses[class] {
				bad("invalid-claim-class", "claims carries %q: name a criterion and a known claim class", entry)
				continue
			}
			if !seen[criterionID] {
				bad("claim-criterion-missing", "claims names criterion %q, which this block does not carry", criterionID)
				continue
			}
			if claimIDs[criterionID] {
				bad("duplicate-claim", "claims names criterion %q twice", criterionID)
				continue
			}
			claimIDs[criterionID] = true
			for i := range criteria {
				if criteria[i].CriterionID == criterionID {
					criteria[i].ClaimClass = class
				}
			}
		}
		if err := checkCriteria(id, criteria); err != nil {
			rule := "invalid-criteria"
			if rejection, ok := RejectionOf(err); ok {
				rule = rejection.Rule
			}
			bad(rule, "%s", err)
		}
		for _, c := range criteria {
			if c.Text == "" {
				bad("empty-criterion", "criterion %q has no text", c.CriterionID)
			}
		}
		sort.Slice(criteria, func(x, y int) bool { return criteria[x].CriterionID < criteria[y].CriterionID })
		b.payload.Criteria = criteria
		blocks = append(blocks, b)
		// Resume after the directive; the criteria scan does not consume the next heading.
		i = end
	}
	return blocks, findings
}

// badFencePath answers why a fence path cannot be stored, or "".
func badFencePath(p string) string {
	switch {
	case strings.ContainsAny(p, "*?["):
		return "a fence is spelled as path prefixes, never as a pattern"
	case p == "." || p == "./" || p == "/" || p == "..":
		return "it names the whole repository instead of a fence"
	case strings.HasPrefix(p, "/"):
		return "a fence path is repo-relative, not absolute"
	case strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || strings.Contains(p, "/../"):
		return "a fence path is a plain repo-relative prefix, not a traversal"
	}
	return ""
}

// splitList is a comma-separated attribute's values, trimmed, with empty entries dropped so a
// trailing comma is not a path.
func splitList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstWord(s string) string {
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// headingSlug is the GitHub-style anchor of a heading: lowercased, letters, digits, `-` and
// `_` kept, spaces turned into hyphens, everything else dropped.
func headingSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func uniqueStrings(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// SyncEnvelope is the item.sync record for one report.
func SyncEnvelope(report SyncReport, at time.Time) (record.Envelope, error) {
	return envelopeOf(SyncRecordKind, "sync", at, report)
}

// SyncText is the report as a person reads it: the summary, the two residue counters, then
// one line per warning, frozen definition and finding.
func SyncText(report SyncReport, at time.Time) string {
	var b strings.Builder
	label := "sync"
	if report.DryRun {
		label = "sync (dry run, nothing written)"
	}
	fmt.Fprintf(&b, "%s: %d block(s) in %d tracked document(s): created %d, unchanged %d, updated %d, frozen %d\n",
		label, report.Blocks, report.Documents, report.CreatedCount, report.UnchangedCount, report.UpdatedCount, report.FrozenCount)
	oldest := ""
	if report.UnsharedOpenOldest != "" {
		if t, err := time.Parse(time.RFC3339, report.UnsharedOpenOldest); err == nil {
			oldest = fmt.Sprintf(" (oldest %s)", at.Sub(t).Round(time.Minute))
		}
	}
	fmt.Fprintf(&b, "unshared_open: %d%s\n", report.UnsharedOpen, oldest)
	fmt.Fprintf(&b, "stale_doc_blocks: %d\n", report.StaleDocBlocks)
	for _, id := range report.Created {
		fmt.Fprintf(&b, "created: %s\n", id)
	}
	for _, id := range report.Updated {
		fmt.Fprintf(&b, "updated: %s\n", id)
	}
	for _, id := range report.StaleDocBlockIDs {
		fmt.Fprintf(&b, "stale: %s is landed in this store; delete its block\n", id)
	}
	for _, n := range report.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", noteText(n))
	}
	for _, n := range report.Frozen {
		fmt.Fprintf(&b, "frozen: %s\n", noteText(n))
	}
	for _, n := range report.Findings {
		fmt.Fprintf(&b, "finding: %s\n", noteText(n))
	}
	if len(report.SkippedFiles) > 0 {
		fmt.Fprintf(&b, "no item was written from: %s\n", strings.Join(report.SkippedFiles, ", "))
	}
	return b.String()
}

func noteText(n SyncNote) string {
	where := fmt.Sprintf("%s:%d", n.File, n.Line)
	if n.Heading != "" {
		where += fmt.Sprintf(" (%s)", n.Heading)
	}
	id := ""
	if n.ItemID != "" {
		id = " " + n.ItemID
	}
	return fmt.Sprintf("%s%s: %s: %s", where, id, n.Rule, n.Message)
}
