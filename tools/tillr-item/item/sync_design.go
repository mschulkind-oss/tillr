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

func planDesignMirror(ctx context.Context, r *store.Reader, in SyncInput, held map[string]storedState, finished func(string) bool) (designMirrorPlan, error) {
	var out designMirrorPlan
	if in.Partial && len(in.Roadmaps) == 0 {
		return out, nil
	}

	byPath := make(map[string]SyncDocument, len(in.Documents))
	var roadmaps []SyncDocument
	for _, doc := range in.Documents {
		byPath[doc.Path] = doc
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
		out.mirroredRoadmaps = append(out.mirroredRoadmaps, rm.Path)
	}

	routed := map[string]bool{}
	spineLinked := map[string]bool{}
	parkedLinked := map[string]bool{}
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
						if _, ok := byPath[target]; ok {
							hasDoc = true
							break
						}
					}
				}
				if !hasDoc {
					out.iceboxNoDoc++
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
				targetDoc, exists := byPath[target]
				if !exists {
					continue
				}
				if isPairedCompanionPlan(target, byPath) {
					continue
				}
				if !strings.HasPrefix(target, "docs/design/") && designStage(targetDoc.Content) == "" {
					continue
				}
				routed[target] = true
				if !inParked {
					if !spineLinked[target] {
						spineLinked[target] = true
						spine = append(spine, target)
					}
				} else {
					parkedLinked[target] = true
				}
			}
		})
		perRoadmapSpine = append(perRoadmapSpine, spine)
	}

	// Interleave spine-linked designs across mirrored roadmaps.
	var orderedSpineTargets []string
	for i := 0; ; i++ {
		any := false
		for _, spine := range perRoadmapSpine {
			if i < len(spine) {
				any = true
				orderedSpineTargets = append(orderedSpineTargets, spine[i])
			}
		}
		if !any {
			break
		}
	}

	routedList := make([]string, 0, len(routed))
	for target := range routed {
		routedList = append(routedList, target)
	}
	sort.Strings(routedList)

	// Check slug collisions across all routed eligible designs before any read/write.
	byID := make(map[string]string, len(routedList))
	for _, target := range routedList {
		id := designItemID(target)
		if prev, dup := byID[id]; dup && prev != target {
			return out, Rejectionf("slug-collision", id, "design documents %q and %q collide on item id %q", prev, target, id)
		}
		byID[id] = target
	}

	// Determine existing coverage by unfenced item blocks or other unfinished items.
	covered := make(map[string]bool, len(routedList))
	for _, target := range routedList {
		doc := byPath[target]
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
		if id != designItemID(target) {
			covered[target] = true
		}
	}

	var uncovered []string
	for _, target := range routedList {
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

	if len(uncovered) == 0 && !hasUnfinishedDesignItem {
		return out, nil
	}

	// Obtain Vantage index (fail-closed).
	var index vantage.VantageIndex
	if in.Index != nil {
		index = *in.Index
	} else if in.Root != "" {
		if ctx == nil {
			ctx = context.Background()
		}
		index = vantage.ReadVantageIndex(ctx, in.Root, in.QuestionsCmd)
	} else {
		return out, Externalf("index-unreadable: no repository root or planning index provided")
	}
	if _, err := index.StageRole(""); err != nil {
		return out, Externalf("index-unreadable: %v", err)
	}

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
		role, err := index.StageRole(target)
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
