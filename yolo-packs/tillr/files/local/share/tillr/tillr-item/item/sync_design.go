package item

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

const designItemPrefix = "item-design-"

type designMirrorEntry struct {
	id         string
	target     string
	parkedOnly bool
	spinePos   int // order across interleaved roadmaps; -1 when parked-only
}

type designMirrorTransition struct {
	id          string
	toState     string
	receipt     MoveReceipt
	waitingOn   *WaitingOn
	isPark      bool
	isUnpark    bool
	isCreate    bool
	payload     itemPayload
	applyUpdate bool
}

type designMirrorPlan struct {
	entries          []designMirrorEntry
	spineOrder       []string // item IDs in interleaved roadmap order
	iceboxNoDoc      int
	creates          []designMirrorTransition
	updates          []designMirrorTransition
	closes           []designMirrorTransition
	resolves         []designMirrorTransition
	unchanged        []string
	parkedIDs        []string
	unparkedIDs      []string
	mirroredRoadmaps []string
	index            *vantage.VantageIndex
	companion        companionResult
}

func designItemID(docPath string) string {
	slug := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(docPath)), "docs/")
	slug = strings.TrimSuffix(slug, ".md")
	slug = strings.Trim(reconcileSlugRun.ReplaceAllString(strings.ToLower(slug), "-"), "-")
	return designItemPrefix + slug
}

func isDesignItemID(id string) bool {
	return strings.HasPrefix(id, designItemPrefix)
}

func designTitle(docPath string, content []byte) string {
	lines := splitLines(content)
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				break
			}
			if strings.HasPrefix(lines[i], "title:") {
				val := strings.TrimSpace(strings.TrimPrefix(lines[i], "title:"))
				if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
					val = val[1 : len(val)-1]
				}
				if val != "" {
					return val
				}
			}
		}
	}
	fence := fenceState{}
	for _, line := range lines {
		if fence.step(line) {
			continue
		}
		if h := headingLine.FindStringSubmatch(line); h != nil && len(h[1]) == 1 {
			if title := strings.TrimSpace(strings.TrimRight(h[2], "#")); title != "" {
				return title
			}
		}
	}
	base := strings.TrimSuffix(path.Base(docPath), ".md")
	return strings.ReplaceAll(base, "-", " ")
}

func isIceboxHeading(title string) bool {
	return strings.HasPrefix(strings.TrimSpace(title), "Icebox")
}

func isPairedCompanionPlan(target string, byPath map[string]SyncDocument) bool {
	if !strings.HasSuffix(target, "-plan.md") {
		return false
	}
	parent := strings.TrimSuffix(target, "-plan.md") + ".md"
	_, exists := byPath[parent]
	return exists
}

type routedDesigns struct {
	byPath              map[string]SyncDocument
	mirroredRoadmaps    []string
	routed              map[string]bool
	routedList          []string
	spineLinked         map[string]bool
	parkedLinked        map[string]bool
	orderedSpineTargets []string
	iceboxNoDoc         int
}

func collectRoutedDesigns(in SyncInput) routedDesigns {
	rd := routedDesigns{
		byPath:       make(map[string]SyncDocument, len(in.Documents)),
		routed:       map[string]bool{},
		spineLinked:  map[string]bool{},
		parkedLinked: map[string]bool{},
	}
	var roadmaps []SyncDocument
	for _, doc := range in.Documents {
		rd.byPath[doc.Path] = doc
		if isRoadmap(doc.Path, in.Roadmaps) {
			roadmaps = append(roadmaps, doc)
		}
	}
	sort.SliceStable(roadmaps, func(i, j int) bool {
		if len(in.Roadmaps) > 0 {
			pos := func(p string) int {
				for idx, sel := range in.Roadmaps {
					if sel == p {
						return idx
					}
				}
				return len(in.Roadmaps)
			}
			if a, b := pos(roadmaps[i].Path), pos(roadmaps[j].Path); a != b {
				return a < b
			}
		}
		return roadmaps[i].Path < roadmaps[j].Path
	})
	for _, rm := range roadmaps {
		rd.mirroredRoadmaps = append(rd.mirroredRoadmaps, rm.Path)
	}

	var perRoadmapSpine [][]string
	for _, rm := range roadmaps {
		var spine []string
		heading := ""
		roadmapSections(rm, func(line int, text string, inParked, inExcluded bool) {
			if h := headingLine.FindStringSubmatch(text); h != nil {
				heading = strings.TrimSpace(strings.TrimRight(h[2], "#"))
			}
			if inExcluded {
				return
			}
			matches := linkTarget.FindAllStringSubmatch(text, -1)
			if inParked && isIceboxHeading(heading) && listMarker.MatchString(strings.TrimSpace(text)) {
				hasDoc := false
				for _, m := range matches {
					target := roadmapLink(rm.Path, m[1])
					if target != "" {
						if _, ok := rd.byPath[target]; ok {
							hasDoc = true
							break
						}
					}
				}
				if !hasDoc {
					rd.iceboxNoDoc++
				}
			}
			for _, m := range matches {
				target := roadmapLink(rm.Path, m[1])
				if target == "" || !strings.HasPrefix(target, "docs/") {
					continue
				}
				if isRoadmap(target) || isRoadmap(target, in.Roadmaps) {
					continue
				}
				targetDoc, exists := rd.byPath[target]
				if !exists {
					continue
				}
				if isPairedCompanionPlan(target, rd.byPath) {
					continue
				}
				// Unstaged Markdown outside docs/design/ is routed too, so a design needs no
				// frontmatter vocabulary (D-PSH3); an unstaged reference page is not a design.
				if designStage(targetDoc.Content) == "" && strings.HasPrefix(target, "docs/reference/") {
					continue
				}
				rd.routed[target] = true
				if !inParked {
					if !rd.spineLinked[target] {
						rd.spineLinked[target] = true
						spine = append(spine, target)
					}
				} else {
					rd.parkedLinked[target] = true
				}
			}
		})
		perRoadmapSpine = append(perRoadmapSpine, spine)
	}

	// Interleave spine-linked designs across mirrored roadmaps.
	for i := 0; ; i++ {
		any := false
		for _, spine := range perRoadmapSpine {
			if i < len(spine) {
				any = true
				rd.orderedSpineTargets = append(rd.orderedSpineTargets, spine[i])
			}
		}
		if !any {
			break
		}
	}

	rd.routedList = make([]string, 0, len(rd.routed))
	for target := range rd.routed {
		rd.routedList = append(rd.routedList, target)
	}
	sort.Strings(rd.routedList)
	return rd
}

// isCompanionPiece answers whether a stored item was imported from the companion plan of
// the design at target.
func isCompanionPiece(st storedState, target string) bool {
	ref, _, _ := strings.Cut(st.bodyRef, "#")
	return ref != "" && ref == companionPlanPath(target)
}

// companionCandidates lists the companion plans of the routed designs, in path order.
func companionCandidates(rd routedDesigns) []string {
	var plans []string
	for _, target := range rd.routedList {
		plan := companionPlanPath(target)
		if _, ok := rd.byPath[plan]; ok {
			plans = append(plans, plan)
		}
	}
	return plans
}

func designIndexNeeded(rd routedDesigns, held map[string]storedState, finished func(string) bool) (map[string]bool, []string, bool, error) {
	// Check slug collisions across all routed eligible designs before any read/write.
	byID := make(map[string]string, len(rd.routedList))
	for _, target := range rd.routedList {
		id := designItemID(target)
		if prev, dup := byID[id]; dup && prev != target {
			return nil, nil, false, Rejectionf("slug-collision", id, "design documents %q and %q collide on item id %q", prev, target, id)
		}
		byID[id] = target
	}

	// Determine existing coverage by unfenced item blocks or other unfinished items.
	covered := make(map[string]bool, len(rd.routedList))
	for _, target := range rd.routedList {
		doc := rd.byPath[target]
		if blocks, _ := parseItemBlocks(doc.Path, doc.Content); len(blocks) > 0 {
			covered[target] = true
		}
	}
	for id, st := range held {
		if finished(st.state) || st.designDoc == "" {
			continue
		}
		target, _, _ := strings.Cut(st.designDoc, "#")
		target = path.Clean(target)
		// A piece of the design's companion plan does not cover it: the design item
		// stays, and rests on its questions while the pieces build (D-PSH8).
		if id != designItemID(target) && !isCompanionPiece(st, target) {
			covered[target] = true
		}
	}

	var uncovered []string
	for _, target := range rd.routedList {
		if !covered[target] {
			uncovered = append(uncovered, target)
		}
	}

	hasUnfinishedDesignItem := false
	for id, st := range held {
		if isDesignItemID(id) && !finished(st.state) {
			hasUnfinishedDesignItem = true
			break
		}
	}

	return covered, uncovered, hasUnfinishedDesignItem, nil
}

// NeedsDesignIndex reports whether Sync will read the external Vantage index
// for in against the store state visible to r. Callers use this in a short
// ReadTx before opening WriteTx so vantage-check never runs while holding
// SQLite's write lock (or a long-lived read snapshot).
func NeedsDesignIndex(r *store.Reader, in SyncInput) (bool, error) {
	if in.Index != nil || in.Root == "" {
		return false, nil
	}
	if len(in.Roadmaps) > 0 {
		in.Partial = true
	}
	if in.Partial && len(in.Roadmaps) == 0 {
		return false, nil
	}
	if _, err := designStageRoles(in.Root); err != nil {
		return false, nil
	}
	d, err := itemDefinition(r)
	if err != nil {
		return false, err
	}
	finished := func(state string) bool { return d.IsFinished(state) || frozenStates[state] }
	held := map[string]storedState{}
	if r != nil {
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'items')`).Scan(&exists); err != nil {
			return false, Internalf("checking the stored item schema: %v", err)
		}
		if exists {
			if held, err = readStoredStates(r); err != nil {
				return false, err
			}
		}
	}
	rd := collectRoutedDesigns(in)
	_, uncovered, hasUnfinishedDesignItem, err := designIndexNeeded(rd, held, finished)
	if err != nil {
		return false, nil
	}
	return len(uncovered) > 0 || hasUnfinishedDesignItem || len(companionCandidates(rd)) > 0, nil
}

// readDesignIndex answers the planning index sync reads, failing closed.
func readDesignIndex(ctx context.Context, in SyncInput) (vantage.VantageIndex, error) {
	var index vantage.VantageIndex
	if in.Index != nil {
		index = *in.Index
	} else if in.Root != "" {
		if ctx == nil {
			ctx = context.Background()
		}
		index = vantage.ReadVantageIndex(ctx, in.Root, in.QuestionsCmd)
	} else {
		return index, Externalf("index-unreadable: no repository root or planning index provided")
	}
	if _, err := index.StageRole(""); err != nil {
		return index, Externalf("index-unreadable: %v", err)
	}
	return index, nil
}

// designRole is the stage role sync treats a routed design as holding. A design with no
// frontmatter stage is finished when its companion plan is BUILT and stamped with the
// design's current git blob sha; changing the design makes it outstanding again (D-PSH3).
func designRole(index vantage.VantageIndex, byPath map[string]SyncDocument, target string) (string, error) {
	role, err := index.StageRole(target)
	if err != nil || role != "" {
		return role, err
	}
	doc := byPath[target]
	if designStage(doc.Content) != "" {
		return role, nil
	}
	plan, ok := byPath[companionPlanPath(target)]
	if !ok {
		return role, nil
	}
	planRole, err := index.StageRole(plan.Path)
	if err != nil {
		return "", err
	}
	if planRole == vantage.StageRoleBuilt && planStampedAt(plan.Content) == gitBlobSHA(doc.Content) {
		return vantage.StageRoleBuilt, nil
	}
	return role, nil
}

func planDesignMirror(ctx context.Context, r *store.Reader, in SyncInput, held map[string]storedState, finished func(string) bool) (designMirrorPlan, error) {
	var out designMirrorPlan
	if in.Partial && len(in.Roadmaps) == 0 {
		return out, nil
	}

	rd := collectRoutedDesigns(in)
	byPath := rd.byPath
	routed := rd.routed
	spineLinked := rd.spineLinked
	parkedLinked := rd.parkedLinked
	orderedSpineTargets := rd.orderedSpineTargets
	out.mirroredRoadmaps = rd.mirroredRoadmaps
	out.iceboxNoDoc = rd.iceboxNoDoc

	covered, uncovered, hasUnfinishedDesignItem, err := designIndexNeeded(rd, held, finished)
	if err != nil {
		return out, err
	}

	if len(uncovered) == 0 && !hasUnfinishedDesignItem {
		return out, nil
	}

	index, err := readDesignIndex(ctx, in)
	if err != nil {
		return out, err
	}
	out.index = &index

	d, err := itemDefinition(r)
	if err != nil {
		return out, err
	}

	// Close unfinished item-design-* rows whose design_doc is no longer routed.
	var unroutedIDs []string
	for id, st := range held {
		if !isDesignItemID(id) || finished(st.state) {
			continue
		}
		target, _, _ := strings.Cut(st.designDoc, "#")
		target = path.Clean(target)
		if !routed[target] {
			unroutedIDs = append(unroutedIDs, id)
		}
	}
	sort.Strings(unroutedIDs)
	for _, id := range unroutedIDs {
		out.closes = append(out.closes, designMirrorTransition{
			id:      id,
			toState: d.Closed,
			receipt: MoveReceipt{
				By:     "tillr-item/sync",
				Reason: "design no longer routed by mirrored roadmap",
			},
		})
	}

	spinePosByTarget := make(map[string]int, len(orderedSpineTargets))
	for i, target := range orderedSpineTargets {
		spinePosByTarget[target] = i
	}

	for _, target := range orderedSpineTargets {
		if covered[target] {
			continue
		}
		out.spineOrder = append(out.spineOrder, designItemID(target))
	}

	for _, target := range uncovered {
		id := designItemID(target)
		role, err := designRole(index, byPath, target)
		if err != nil {
			return out, Externalf("index-unreadable: %v", err)
		}
		st, exists := held[id]
		if role == vantage.StageRoleBuilt || role == vantage.StageRoleDone {
			if exists && !finished(st.state) {
				out.resolves = append(out.resolves, designMirrorTransition{
					id:      id,
					toState: "resolved-elsewhere",
					receipt: MoveReceipt{
						ResolvesVia: "doc:" + target,
						By:          "tillr-item/sync",
						Reason:      "design stage reached " + role,
					},
				})
			}
			continue
		}

		// Outstanding: role is "", "open", or "ready".
		if exists && finished(st.state) {
			// Reopening finished item-design-* rows is Slice 3.
			continue
		}

		unanswered, err := index.DesignUnansweredQuestions(target)
		if err != nil {
			return out, Externalf("index-unreadable: %v", err)
		}
		seat := "design"
		if role == vantage.StageRoleReady && len(unanswered) == 0 {
			seat = "plan"
		}
		parkedOnly := parkedLinked[target] && !spineLinked[target]
		desiredState := d.Ready
		if parkedOnly {
			desiredState = d.Blocked
		}
		spinePos := -1
		if pos, ok := spinePosByTarget[target]; ok && !parkedOnly {
			spinePos = pos
		}
		out.entries = append(out.entries, designMirrorEntry{
			id:         id,
			target:     target,
			parkedOnly: parkedOnly,
			spinePos:   spinePos,
		})

		if !exists {
			payload := itemPayload{
				ItemID:    id,
				Title:     designTitle(target, byPath[target].Content),
				BodyRef:   target,
				DesignDoc: target,
				Seat:      seat,
				State:     desiredState,
				Criteria: []criterion{
					{
						CriterionID: "c1",
						Text:        fmt.Sprintf("Advance or settle %s per its design stage", target),
						ClaimClass:  ClaimVerification,
					},
				},
			}
			var wait *WaitingOn
			if parkedOnly {
				wait = &WaitingOn{Waiter: "owner", Reason: "icebox"}
			}
			out.creates = append(out.creates, designMirrorTransition{
				id:          id,
				toState:     desiredState,
				waitingOn:   wait,
				isCreate:    true,
				applyUpdate: true,
				payload:     payload,
			})
			continue
		}

		// Existing unfinished item-design-* row.
		stored, found, err := storedItem(r, id)
		if err != nil {
			return out, err
		}
		if !found {
			continue
		}
		judged := judgedCriterion(stored)
		waiting, _, err := ReadWaitingOn(r, id)
		if err != nil {
			return out, err
		}
		seatChanged := judged == "" && stored.Seat != seat
		park := stored.State == d.Ready && parkedOnly
		unpark := stored.State == d.Blocked && waiting != nil && waiting.Reason == "icebox" && !parkedOnly
		if !seatChanged && !park && !unpark {
			out.unchanged = append(out.unchanged, id)
			continue
		}

		desired := stored
		if seatChanged {
			desired.Seat = seat
		}
		var wait *WaitingOn
		if park {
			desired.State = d.Blocked
			desired.Rank = ""
			wait = &WaitingOn{Waiter: "owner", Reason: "icebox"}
			out.parkedIDs = append(out.parkedIDs, id)
		}
		if unpark {
			desired.State = d.Ready
			desired.Rank = ""
			out.unparkedIDs = append(out.unparkedIDs, id)
		}
		out.updates = append(out.updates, designMirrorTransition{
			id:          id,
			toState:     desired.State,
			waitingOn:   wait,
			isPark:      park,
			isUnpark:    unpark,
			applyUpdate: true,
			payload:     desired,
		})
	}

	return out, nil
}
