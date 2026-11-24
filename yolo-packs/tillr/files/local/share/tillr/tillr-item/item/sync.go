// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package item

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// SyncRecordKind is the record the sync face emits: what the documents declared and what the
// store did about it. It is registered as neither an event kind nor a
// materializer: every write sync makes is its own item.add or item.update, appended by Apply,
// and a second record of the batch would claim a transition the log did not make.
const SyncRecordKind = "item.sync"

// The item block grammar (docs/design/queue-persistence-ruling.md Section 1, D-RS1, and the
// seat and source-placement amendments):
//
//	### <Title>
//	<!-- tillr: item id="<item_id>" paths="<prefix>,<prefix>" blocked_by="<id>,<id>" seat="<seat>" from="<id>" -->
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

// parkedSections are the roadmap section headings that park the documents they link to. A
// heading counts when its text starts with one of these words, so `## Icebox 🧊` parks too.
var parkedSections = []string{"Icebox", "Exploration"}

// SyncDocument is one tracked Markdown file: its repo-relative slash path and its bytes.
type SyncDocument = docshare.Document

// SyncInput is one sync run: the repository root the documents were read from (a path in a
// block that does not exist under it is a warning), the documents, and the clock.
type SyncInput struct {
	// Partial marks documents as a subset; it must not replace the last whole sync observation.
	Partial   bool
	Roadmaps  []string // selected roadmaps, in flag order; nil for every document
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

// SyncSkippedBlock is one unfenced item directive outside the selected roadmap scope.
type SyncSkippedBlock struct {
	File   string `json:"file"`
	ItemID string `json:"item_id"`
}

// SyncReport is item.sync's payload. The id lists are what this run wrote (or, on a dry run,
// would write); the counts beside them are the same lists' lengths, so a reader that wants
// only the summary does not count.
type SyncReport struct {
	Partial   bool   `json:"-"`
	Scope     any    `json:"scope"` // "all-documents" or selected roadmap paths
	Root      string `json:"root"`
	DryRun    bool   `json:"dry_run"`
	Documents int    `json:"documents"`
	Blocks    int    `json:"blocks"`

	Placed        []SyncPlacement    `json:"placed"`
	Created       []string           `json:"created"`
	Updated       []string           `json:"updated"`
	Unchanged     []string           `json:"unchanged"`
	Parked        []string           `json:"parked"`
	Unparked      []string           `json:"unparked"`
	Frozen        []SyncNote         `json:"frozen"`
	SkippedBlocks []SyncSkippedBlock `json:"skipped_blocks"`

	CreatedCount   int `json:"created_count"`
	UpdatedCount   int `json:"updated_count"`
	UnchangedCount int `json:"unchanged_count"`
	FrozenCount    int `json:"frozen_count"`
	SkippedCount   int `json:"skipped_count"`

	Warnings []SyncNote `json:"warnings"`
	Findings []SyncNote `json:"findings"`
	// SkippedFiles are the documents no item was written from because they carry a finding.
	SkippedFiles []string `json:"skipped_files"`

	// UnsharedOpen counts the store's non-terminal items that no document block declares and
	// that are not store-declared (an item carrying workflow_args, which its record declares):
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
	file       string
	heading    string
	line       int
	payload    itemPayload
	attributes map[string]string
	// Line ranges, with an exclusive end, retain the exact source bytes retirement removes.
	directiveEnd   int
	criterionLines []int
	// parked is whether a roadmap's Icebox or Exploration section links to the block's
	// document, or contains the block itself; queued items become unranked not-started.
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
	allowed     map[string]bool // nil for whole sync; declared in-scope ids for partial syncs
	allowedDocs map[string]bool // nil for whole sync; in-scope document paths for partial syncs
	report      SyncReport
	// writes are the files with something to write, in the order they may be written, and
	// each file's payloads in the order its items may be written.
	writes []fileWrites
	// placements retain block order independently of dependency-ordered writes.
	placements []sourcePlacement
}

type sourcePlacement struct{ id, from string }

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
// item's runs and criterion states exactly as the store holds them. Roadmap parking
// moves only in-queue to unranked not-started; a whole sync unparks not-started items
// to in-queue at the end of the stored ranks. Other states are preserved. The from
// mark places new items only; a crowded gap may respace keys while preserving the
// other items' relative order. An item whose
// definition differs while a criterion carries a state is reported `definition-frozen` and
// its definition is left alone, because rewriting presented criteria would make a recorded
// verdict stop meaning what it said. Routing still applies to that stored definition.
//
// A file with a finding writes nothing; every other file is written. Each file is applied
// under its own savepoint, so a refusal Apply makes for one file (an edge cycle, say) rolls
// back that file alone and becomes its finding.
func Sync(ctx context.Context, r *store.Reader, tx *store.Tx, in SyncInput) (SyncReport, error) {
	if len(in.Roadmaps) > 0 {
		in.Partial = true
	}
	plan, err := planSync(r, in)
	if err != nil {
		return SyncReport{}, err
	}
	if tx == nil {
		plan.report.DryRun = true
		plan.report.Placed, err = previewPlacements(r, plan)
		if err != nil {
			return SyncReport{}, err
		}
		return finishReport(plan.report), nil
	}
	report := plan.report
	report.Created, report.Updated = []string{}, []string{}
	report.Parked, report.Unparked = []string{}, []string{}
	created := map[string]bool{}
	parking := map[string]bool{}
	for _, id := range plan.report.Parked {
		parking[id] = true
	}
	checkProtected, err := protectUnreadRanks(tx.Reader(), plan.allowed)
	if err != nil {
		return SyncReport{}, err
	}
	keyed, err := keyMissingOrder(ctx, tx, in.At, parking, plan.allowed)
	if err != nil {
		return SyncReport{}, err
	}
	var last string
	if err := tx.Reader().QueryRow(`SELECT COALESCE(MAX(rank),'') FROM items`).Scan(&last); err != nil {
		return SyncReport{}, err
	}
	appendIDs := []string{}
	needsKey := map[string]bool{}
	for _, fw := range plan.writes {
		for _, payload := range fw.payloads {
			needsKey[payload.ItemID] = payload.Rank == "" && InOrder(payload.State)
		}
	}
	for _, p := range plan.placements {
		if needsKey[p.id] {
			appendIDs = append(appendIDs, p.id)
		}
	}
	keys := appendKeys(last, len(appendIDs))
	appendRank := map[string]string{}
	for i, id := range appendIDs {
		appendRank[id] = keys[i]
	}
	for _, fw := range plan.writes {
		if _, err := tx.Exec(`SAVEPOINT item_sync_file`); err != nil {
			return SyncReport{}, Internalf("opening the savepoint for %s: %v", fw.file, err)
		}
		var refused error
		var refusedID string
		for _, payload := range fw.payloads {
			if payload.Rank == "" && InOrder(payload.State) {
				// Planning precedes the missing-key writes. Preserve a key supplied above.
				if !fw.created[payload.ItemID] && !containsID(plan.report.Unparked, payload.ItemID) {
					if err := tx.Reader().QueryRow(`SELECT rank FROM items WHERE item_id=?`, payload.ItemID).Scan(&payload.Rank); err != nil {
						return SyncReport{}, err
					}
				}
				if payload.Rank == "" {
					payload.Rank = appendRank[payload.ItemID]
				}
			}
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
					created[payload.ItemID] = true
				} else {
					report.Updated = append(report.Updated, payload.ItemID)
					if parking[payload.ItemID] {
						report.Parked = append(report.Parked, payload.ItemID)
					}
					if containsID(plan.report.Unparked, payload.ItemID) {
						report.Unparked = append(report.Unparked, payload.ItemID)
					}
				}
			}
		}
		if _, err := tx.Exec(`RELEASE SAVEPOINT item_sync_file`); err != nil {
			return SyncReport{}, Internalf("releasing the savepoint for %s: %v", fw.file, err)
		}
	}
	// Follow source marks after all successful imports have appended.
	// A source imported later in block order must not move away from its children.
	places, err := OrderPlaces(tx.Reader())
	if err != nil {
		return SyncReport{}, err
	}
	for _, p := range plan.placements {
		if created[p.id] && places[p.id] != 0 && p.from != "" {
			if err := placeFrom(tx, p.id, p.from, in.At); err != nil {
				return SyncReport{}, err
			}
		}
	}
	for _, id := range keyed {
		if !containsID(report.Created, id) && !containsID(report.Updated, id) {
			report.Updated = append(report.Updated, id)
		}
		for i, v := range report.Unchanged {
			if v == id {
				report.Unchanged = append(report.Unchanged[:i], report.Unchanged[i+1:]...)
				break
			}
		}
	}
	placedIDs := map[string]string{}
	for _, id := range keyed {
		placedIDs[id] = ""
	}
	for _, id := range appendIDs {
		if containsID(report.Created, id) || containsID(report.Updated, id) {
			placedIDs[id] = ""
		}
	}
	for _, p := range plan.placements {
		if !created[p.id] || places[p.id] == 0 || p.from == "" || p.from == p.id {
			continue
		}
		var sourceKey string
		if err := tx.Reader().QueryRow(`SELECT COALESCE((SELECT rank FROM items WHERE item_id=?),'')`, p.from).Scan(&sourceKey); err != nil {
			return SyncReport{}, err
		}
		if sourceKey != "" {
			placedIDs[p.id] = p.from
		}
	}
	finalPlaces, err := OrderPlaces(tx.Reader())
	if err != nil {
		return SyncReport{}, err
	}
	report.Placed = placementReport(finalPlaces, placedIDs)
	if err := checkProtected(tx.Reader()); err != nil {
		return SyncReport{}, err
	}
	if err := replaceDesignWithoutItems(tx, report.Warnings, in.At.UnixMilli(), plan.allowedDocs); err != nil {
		return SyncReport{}, err
	}
	if !in.Partial {
		if err := replaceSyncResidue(tx, report, in.At); err != nil {
			return SyncReport{}, Internalf("saving sync residue: %v", err)
		}
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
	if report.SkippedBlocks == nil {
		report.SkippedBlocks = []SyncSkippedBlock{}
	}
	report.CreatedCount = len(report.Created)
	report.UpdatedCount = len(report.Updated)
	report.UnchangedCount = len(report.Unchanged)
	report.FrozenCount = len(report.Frozen)
	report.SkippedCount = len(report.SkippedBlocks)
	return report
}

// planSync parses every document, validates every block, compares each with the store and
// decides what to write. It writes nothing.
func planSync(r *store.Reader, in SyncInput) (syncPlan, error) {
	scoped := len(in.Roadmaps) > 0
	if scoped {
		in.Partial = true
	}
	d, err := itemDefinition(r)
	if err != nil {
		return syncPlan{}, err
	}
	finished := func(state string) bool { return d.IsFinished(state) || frozenStates[state] }
	report := SyncReport{
		Root: in.Root, Documents: len(in.Documents), Partial: in.Partial, Scope: "all-documents",
		Created: []string{}, Updated: []string{}, Unchanged: []string{}, Frozen: []SyncNote{},
		Parked: []string{}, Unparked: []string{}, SkippedBlocks: []SyncSkippedBlock{},
		Warnings: []SyncNote{}, Findings: []SyncNote{}, SkippedFiles: []string{},
		UnsharedOpenIDs: []string{}, StaleDocBlockIDs: []string{},
	}
	if scoped {
		report.Scope = append([]string(nil), in.Roadmaps...)
	}
	parked := parkedDocuments(in.Documents, in.Roadmaps)
	parkedLines := map[string]map[int]bool{}
	skippedRoadmapLines := map[string]map[int]bool{}
	var inScopeDocs map[string]bool
	if scoped {
		inScopeDocs = map[string]bool{}
		byPath := make(map[string]SyncDocument, len(in.Documents))
		for _, doc := range in.Documents {
			byPath[doc.Path] = doc
		}
		for _, doc := range in.Documents {
			if !isRoadmap(doc.Path, in.Roadmaps) {
				continue
			}
			inScopeDocs[doc.Path] = true
			skippedLines := map[int]bool{}
			roadmapSections(doc, func(line int, text string, inParked, inExcluded bool) {
				if inParked || inExcluded {
					skippedLines[line] = true
					return
				}
				for _, m := range linkTarget.FindAllStringSubmatch(text, -1) {
					target := roadmapLink(doc.Path, m[1])
					if target == "" || isRoadmap(target) || isRoadmap(target, in.Roadmaps) || parked[target] {
						continue
					}
					if _, exists := byPath[target]; exists {
						inScopeDocs[target] = true
					}
				}
			})
			skippedRoadmapLines[doc.Path] = skippedLines
		}
	} else {
		for _, doc := range in.Documents {
			if isRoadmap(doc.Path) {
				parkedLines[doc.Path] = map[int]bool{}
				roadmapLines(doc, func(line int, _ string, inParked bool) { parkedLines[doc.Path][line] = inParked })
			}
		}
	}

	var blocks []syncBlock
	var rawSkipped []skippedDirective
	inScopeDocuments := in.Documents
	if scoped {
		inScopeDocuments = make([]SyncDocument, 0, len(inScopeDocs))
	}
	bad := map[string]bool{}
	for _, doc := range in.Documents {
		if scoped && !inScopeDocs[doc.Path] {
			rawSkipped = append(rawSkipped, scanSkippedDirectives(doc.Path, doc.Content, nil)...)
			continue
		}
		var skipLine func(int) bool
		if scoped {
			inScopeDocuments = append(inScopeDocuments, doc)
			if lines := skippedRoadmapLines[doc.Path]; len(lines) > 0 {
				skipLine = func(line int) bool { return lines[line] }
				rawSkipped = append(rawSkipped, scanSkippedDirectives(doc.Path, doc.Content, skipLine)...)
			}
		}
		found, findings := parseItemBlocksScoped(doc.Path, doc.Content, skipLine)
		for i := range found {
			if !scoped {
				found[i].parked = parked[doc.Path] || parkedLines[doc.Path][found[i].line]
			}
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
	if scoped {
		sort.Slice(rawSkipped, func(i, j int) bool {
			if rawSkipped[i].file != rawSkipped[j].file {
				return rawSkipped[i].file < rawSkipped[j].file
			}
			return rawSkipped[i].line < rawSkipped[j].line
		})
		report.SkippedBlocks = make([]SyncSkippedBlock, len(rawSkipped))
		for i, s := range rawSkipped {
			report.SkippedBlocks[i] = SyncSkippedBlock{File: s.file, ItemID: s.itemID}
		}
		report.SkippedCount = len(report.SkippedBlocks)
		report.Documents = len(inScopeDocuments)
	}
	blocks = roadmapBlocks(inScopeDocuments, blocks, in.Roadmaps)
	report.Blocks = len(blocks)

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
	report.Warnings = append(report.Warnings, designWithoutItems(inScopeDocuments, held, stages, in.Roadmaps)...)

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
		// A legacy in-progress row still represents a held run for clone protection,
		// even though sync treats frozen legacy rows as finished document residue.
		if st, exists := held[original]; exists && (!finished(st.state) || st.state == "in-progress") {
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

	if !in.Partial {
		states := make(map[string]string, len(held))
		for id, st := range held {
			states[id] = st.state
		}
		fed, missing := docshare.SharingWithFinished(in.Documents, states, finished)
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
		oldestSet := false
		for _, id := range ids {
			st := held[id]
			if !unshared[id] || ProseRewriteOriginal(id) != "" || st.storeDeclared {
				continue
			}
			report.UnsharedOpenIDs = append(report.UnsharedOpenIDs, id)
			if !oldestSet || st.updatedAt < oldest {
				oldest, oldestSet = st.updatedAt, true
			}
		}
		report.UnsharedOpen = len(report.UnsharedOpenIDs)
		if oldestSet {
			report.UnsharedOpenOldest = time.UnixMilli(oldest).UTC().Format(time.RFC3339Nano)
		}
		// Only Git-landing promises to remove a landed item's block. Another use may
		// declare that spelling as an unfinished state with ordinary document updates.
		staleLanding := func(state string) bool {
			return d.Name == definition.GitLandingName && state == "landed" && d.IsFinished(state)
		}
		for _, b := range blocks {
			if st, ok := held[b.payload.ItemID]; ok && staleLanding(st.state) {
				report.StaleDocBlockIDs = append(report.StaleDocBlockIDs, b.payload.ItemID)
			}
		}
		report.StaleDocBlocks = len(report.StaleDocBlockIDs)
	}
	staleLanding := func(state string) bool {
		return d.Name == definition.GitLandingName && state == "landed" && d.IsFinished(state)
	}

	// Classify every block of a file with no finding.
	perFile := map[string]*fileWrites{}
	var fileOrder []string
	var placements []sourcePlacement
	for _, b := range blocks {
		if bad[b.file] {
			continue
		}
		// A landed block is stale document residue, not a definition to apply.
		// Skipping just this block lets its file's other items sync normally.
		if st, ok := held[b.payload.ItemID]; ok && staleLanding(st.state) {
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
			desired.State = d.Ready
			if b.parked && d.HasState("not-started") {
				desired.State = "not-started"
			}
			fw.payloads = append(fw.payloads, desired)
			fw.created[desired.ItemID] = true
			placements = append(placements, sourcePlacement{desired.ItemID, b.attributes["from"]})
			report.Created = append(report.Created, desired.ItemID)
			continue
		}
		// Legacy lists are inert history. Sync neither compares the declaration's
		// retired paths attribute nor clears the previously stored value.
		desired.AllowedPaths = stored.AllowedPaths
		// Only a whole sync can observe that a roadmap no longer parks this document.
		unpark := !in.Partial && stored.State == "not-started" && !b.parked
		park := stored.State == "in-queue" && b.parked && d.HasState("not-started")
		field := definitionDiff(desired, stored)
		if field == "" && !unpark && !park {
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
			if !park && !unpark {
				continue
			}
			// Routing is independent of the frozen definition. Preserve what
			// the judge read while applying only the state/rank transition.
			desired = stored
		}
		if finished(stored.State) && stored.State != d.Closed {
			report.Frozen = append(report.Frozen, note(b, "state-terminal",
				fmt.Sprintf("item %q is %q; its stored definition stands", stored.ItemID, stored.State)))
			continue
		}
		// The definition is the document's; everything the store learned is the store's.
		desired.State = stored.State
		desired.Rank = stored.Rank
		if park {
			desired.State, desired.Rank = "not-started", ""
			report.Parked = append(report.Parked, desired.ItemID)
		}
		if unpark {
			desired.State, desired.Rank = d.Ready, ""
			report.Unparked = append(report.Unparked, desired.ItemID)
		}
		fw.payloads = append(fw.payloads, desired)
		if desired.Rank == "" && InOrder(desired.State) {
			placements = append(placements, sourcePlacement{desired.ItemID, ""})
		}
		report.Updated = append(report.Updated, desired.ItemID)
	}
	for file := range bad {
		report.SkippedFiles = append(report.SkippedFiles, file)
	}

	writes, err := orderFileWrites(perFile, fileOrder)
	if err != nil {
		return syncPlan{}, err
	}
	if in.Root != "" {
		config, err := settings.Read(in.Root)
		if err != nil {
			return syncPlan{}, err
		}
		if config.Prose.Rewrite {
			rewrites := proseRewriteWrites(&report, blocks, held, d.Ready)
			writes = append(writes, rewrites...)
			for _, fw := range rewrites {
				for _, p := range fw.payloads {
					placements = append(placements, sourcePlacement{p.ItemID, ""})
				}
			}
		}
	}
	var allowed, allowedDocs map[string]bool
	if scoped {
		allowed = map[string]bool{}
		for _, b := range blocks {
			if b.payload.ItemID != "" {
				allowed[b.payload.ItemID] = true
			}
		}
		allowedDocs = inScopeDocs
	} else if in.Partial {
		allowed = docshare.Declared(in.Documents)
		allowedDocs = allowedDocuments(in)
	}
	return syncPlan{report: report, writes: writes, placements: placements, allowed: allowed, allowedDocs: allowedDocs}, nil
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
		ordered, err := orderItems(fw.payloads)
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
	// storeDeclared is an item whose record is its declaration, so it is never unshared:
	// one carrying its workflow's arguments, which no document block can carry, or a filed
	// request, whose words the store holds (request.go).
	storeDeclared bool
}

func readStoredStates(r *store.Reader) (map[string]storedState, error) {
	var hasArgs bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('items') WHERE name = 'workflow_args')`).Scan(&hasArgs); err != nil {
		return nil, Internalf("checking workflow arguments: %v", err)
	}
	argsColumn := "0"
	if hasArgs {
		argsColumn = "COALESCE(workflow_args, '') != ''"
	}
	var hasRequests bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'request_words')`).Scan(&hasRequests); err != nil {
		return nil, Internalf("checking request words: %v", err)
	}
	if hasRequests {
		argsColumn = "(" + argsColumn + " OR item_id IN (SELECT item_id FROM request_words))"
	}
	rows, err := r.Query(`SELECT item_id, state, COALESCE(updated_at, 0), COALESCE(design_doc, ''), ` + argsColumn + ` FROM items`)
	if err != nil {
		return nil, Internalf("reading the stored items: %v", err)
	}
	defer rows.Close()
	held := map[string]storedState{}
	for rows.Next() {
		var id string
		var st storedState
		if err := rows.Scan(&id, &st.state, &st.updatedAt, &st.designDoc, &st.storeDeclared); err != nil {
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
	case block.Express != stored.Express:
		return "express"
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
func parkedDocuments(docs []SyncDocument, selected ...[]string) map[string]bool {
	parked := map[string]bool{}
	byPath := map[string][]byte{}
	for _, d := range docs {
		byPath[d.Path] = d.Content
	}
	for roadmap, content := range byPath {
		if !isRoadmap(roadmap, selected...) {
			continue
		}
		roadmapLines(SyncDocument{Path: roadmap, Content: content}, func(_ int, line string, inParked bool) {
			if !inParked {
				return
			}
			for _, m := range linkTarget.FindAllStringSubmatch(line, -1) {
				if target := roadmapLink(roadmap, m[1]); target != "" {
					parked[target] = true
				}
			}
		})
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

type skippedDirective struct {
	file   string
	line   int
	itemID string
}

func scanSkippedDirectives(file string, content []byte, skipLine func(int) bool) []skippedDirective {
	lines := splitLines(content)
	fenced := make([]bool, len(lines))
	fence := fenceState{}
	for i, line := range lines {
		fenced[i] = fence.step(line)
	}
	var out []skippedDirective
	for i := 0; i < len(lines); i++ {
		if fenced[i] || !directiveStart.MatchString(strings.TrimLeft(lines[i], " \t")) {
			continue
		}
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
		id := ""
		if end >= 0 {
			body := text.String()
			body = body[strings.Index(body, "<!--")+len("<!--"):]
			body = body[:strings.Index(body, "-->")]
			body = strings.TrimSpace(body)
			body = strings.TrimSpace(strings.TrimPrefix(body, "tillr:"))
			body = strings.TrimPrefix(body, "item")
			attrs, _ := docshare.Attributes(body)
			id = strings.TrimSpace(attrs["id"])
		}
		if skipLine == nil || skipLine(i+1) {
			out = append(out, skippedDirective{file: file, line: i + 1, itemID: id})
		}
		if end < 0 {
			break
		}
		i = end
	}
	return out
}

// parseItemBlocks answers every item block in one document and the findings its blocks
// carry. It reads no store and no disk: the file's bytes are the whole input.
func parseItemBlocks(file string, content []byte) ([]syncBlock, []SyncNote) {
	return parseItemBlocksScoped(file, content, nil)
}

func parseItemBlocksScoped(file string, content []byte, skipLine func(int) bool) ([]syncBlock, []SyncNote) {
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
		if skipLine != nil && skipLine(i+1) {
			end := -1
			for j := i; j < len(lines); j++ {
				if strings.Contains(lines[j], "-->") {
					end = j
					break
				}
			}
			if end < 0 {
				break
			}
			i = end
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
		b.attributes = attrs

		id := strings.TrimSpace(attrs["id"])
		b.payload.ItemID = id
		b.payload.Prose = attrs["prose"]
		b.payload.Workflow = attrs["workflow"]
		if express, named := attrs["express"]; named {
			if express != "true" && express != "false" {
				bad("invalid-express", "express must be true or false")
			}
			b.payload.Express = express == "true"
		}
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

		if from, named := attrs["from"]; named && !itemIDShape.MatchString(from) {
			bad("invalid-from", "from %q does not match ^item-[a-z0-9-]+$", from)
		}

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
			if !ValidSeatName(seat) {
				bad("invalid-seat", invalidSeatName, seat)
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
	skippedCount := report.SkippedCount
	if skippedCount == 0 && len(report.SkippedBlocks) > 0 {
		skippedCount = len(report.SkippedBlocks)
	}
	skippedSummary := ""
	if skippedCount > 0 {
		skippedSummary = fmt.Sprintf(", skipped %d", skippedCount)
	}
	fmt.Fprintf(&b, "%s: %d block(s) in %d tracked document(s): created %d, unchanged %d, updated %d, frozen %d%s\n",
		label, report.Blocks, report.Documents, report.CreatedCount, report.UnchangedCount, report.UpdatedCount, report.FrozenCount, skippedSummary)
	oldest := ""
	if report.UnsharedOpenOldest != "" {
		if t, err := time.Parse(time.RFC3339, report.UnsharedOpenOldest); err == nil {
			oldest = fmt.Sprintf(" (oldest %s)", at.Sub(t).Round(time.Minute))
		}
	}
	if report.Partial {
		b.WriteString("unshared_open: not computed; needs a sync of every document\n")
		b.WriteString("stale_doc_blocks: not computed; needs a sync of every document\n")
	} else {
		fmt.Fprintf(&b, "unshared_open: %d%s\n", report.UnsharedOpen, oldest)
		fmt.Fprintf(&b, "stale_doc_blocks: %d\n", report.StaleDocBlocks)
	}
	for _, id := range report.Created {
		fmt.Fprintf(&b, "created: %s\n", id)
	}
	for _, id := range report.Updated {
		fmt.Fprintf(&b, "updated: %s\n", id)
	}
	for _, id := range report.Parked {
		fmt.Fprintf(&b, "parked: %s\n", id)
	}
	for _, id := range report.Unparked {
		fmt.Fprintf(&b, "unparked: %s\n", id)
	}
	for _, p := range report.Placed {
		fmt.Fprintf(&b, "placed: %s at %d", p.ItemID, p.Place)
		if p.From != "" {
			fmt.Fprintf(&b, " from %s", p.From)
		}
		b.WriteByte('\n')
	}
	for _, sb := range report.SkippedBlocks {
		id := sb.ItemID
		if id == "" {
			id = "(missing id)"
		}
		fmt.Fprintf(&b, "skipped: %s %s\n", sb.File, id)
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
