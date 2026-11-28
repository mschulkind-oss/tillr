package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type splitDonorView struct {
	RunID   string `json:"run_id,omitempty"`
	Ref     string `json:"ref,omitempty"`
	SHA     string `json:"sha"`
	BaseSHA string `json:"base_sha,omitempty"`
}

type splitStatement struct {
	Donor    *splitDonorView            `json:"donor,omitempty"`
	Parts    []splitPart                `json:"parts"`
	Coverage map[string][]splitCoverage `json:"coverage"`
}

type splitPart struct {
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

type splitCoverage struct {
	Clause       string `json:"clause,omitempty"`
	Part         string `json:"part,omitempty"`
	ClosingCheck bool   `json:"closing_check,omitempty"`
}

type splitPartMembership struct {
	ParentID string
	Label    string
	Part     splitPart
}

func splitPartID(parent, label string) string {
	return fmt.Sprintf("item-part-%x", sha256.Sum256([]byte(parent+"\x00"+label)))
}

func readAllSplits(r *store.Reader) (map[string]splitStatement, error) {
	out := map[string]splitStatement{}
	rows, err := r.Query(`SELECT parent_id, statement FROM item_splits`)
	if isMissingTable(err) {
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
		var st splitStatement
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, err
		}
		out[parentID] = st
	}
	return out, rows.Err()
}

func readSplitsForItems(r *store.Reader, itemIDs []string) (map[string]splitStatement, error) {
	out := map[string]splitStatement{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		batch := itemIDs[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(`SELECT parent_id, statement FROM item_splits WHERE parent_id IN (`+placeholders+`)`, args...)
		if isMissingTable(err) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var parentID string
			var raw []byte
			if err := rows.Scan(&parentID, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var st splitStatement
			if err := json.Unmarshal(raw, &st); err != nil {
				rows.Close()
				return nil, err
			}
			out[parentID] = st
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	partIdx := splitPartIndex(out)
	for _, id := range itemIDs {
		if strings.HasPrefix(id, "item-part-") {
			if _, ok := partIdx[id]; !ok {
				return readAllSplits(r)
			}
		}
	}
	return out, nil
}

func splitPartIndex(splits map[string]splitStatement) map[string]splitPartMembership {
	out := make(map[string]splitPartMembership)
	for parentID, st := range splits {
		for _, p := range st.Parts {
			out[splitPartID(parentID, p.Label)] = splitPartMembership{
				ParentID: parentID,
				Label:    p.Label,
				Part:     p,
			}
		}
	}
	return out
}

func readSplitRequestWords(r *store.Reader, id string) (string, error) {
	var raw []byte
	err := r.QueryRow(`SELECT words FROM request_words WHERE item_id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || isMissingTable(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

type whySplitHeldPart struct {
	ItemID string `json:"item_id"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
	Mover  string `json:"mover"`
}

type whySplitParent struct {
	TotalParts      int                `json:"total_parts"`
	RequiredParts   int                `json:"required_parts"`
	RequiredLanded  int                `json:"required_landed"`
	Landed          int                `json:"landed"`
	Working         int                `json:"working"`
	Held            int                `json:"held"`
	Queued          int                `json:"queued"`
	Closed          int                `json:"closed"`
	ProgressSummary string             `json:"progress_summary"`
	HeldParts       []whySplitHeldPart `json:"held_parts,omitempty"`
}

type whySplitPart struct {
	ParentID    string `json:"parent_id"`
	ParentTitle string `json:"parent_title"`
	PartLabel   string `json:"part_label"`
	Required    bool   `json:"required"`
}

type itemSplitView struct {
	Parent *splitParentDetailView `json:"parent,omitempty"`
	Part   *splitPartDetailView   `json:"part,omitempty"`
}

type splitParentDetailView struct {
	OwnerWords      string                 `json:"owner_words"`
	Donor           *splitDonorView        `json:"donor,omitempty"`
	TotalParts      int                    `json:"total_parts"`
	RequiredParts   int                    `json:"required_parts"`
	RequiredLanded  int                    `json:"required_landed"`
	ProgressSummary string                 `json:"progress_summary"`
	Parts           []splitPartStatusView  `json:"parts"`
	Coverage        []splitCoverageRowView `json:"coverage"`
}

type splitPartStatusView struct {
	Label             string            `json:"label"`
	ItemID            string            `json:"item_id"`
	Title             string            `json:"title"`
	Share             string            `json:"share"`
	Leaves            map[string]string `json:"leaves,omitempty"`
	Follows           []string          `json:"follows,omitempty"`
	Required          bool              `json:"required"`
	Takes             string            `json:"takes,omitempty"`
	Paths             []string          `json:"paths,omitempty"`
	SharedFiles       []string          `json:"shared_files,omitempty"`
	InheritedFindings []string          `json:"inherited_findings,omitempty"`
	State             string            `json:"state"`
	LandedCommit      string            `json:"landed_commit,omitempty"`
	LandingID         string            `json:"landing_id,omitempty"`
}

type splitCoverageRowView struct {
	CriterionID   string                   `json:"criterion_id"`
	CriterionText string                   `json:"criterion_text"`
	Entries       []splitCoverageEntryView `json:"entries"`
}

type splitCoverageEntryView struct {
	Clause       string `json:"clause,omitempty"`
	PartLabel    string `json:"part_label,omitempty"`
	PartItemID   string `json:"part_item_id,omitempty"`
	ClosingCheck bool   `json:"closing_check,omitempty"`
}

type splitPartDetailView struct {
	ParentID          string            `json:"parent_id"`
	ParentTitle       string            `json:"parent_title"`
	Donor             *splitDonorView   `json:"donor,omitempty"`
	Label             string            `json:"label"`
	Title             string            `json:"title"`
	Share             string            `json:"share"`
	Required          bool              `json:"required"`
	Takes             string            `json:"takes,omitempty"`
	Paths             []string          `json:"paths,omitempty"`
	SharedFiles       []string          `json:"shared_files,omitempty"`
	InheritedFindings []string          `json:"inherited_findings,omitempty"`
	Follows           []splitSiblingRef `json:"follows,omitempty"`
	Leaves            []splitLeafView   `json:"leaves,omitempty"`
}

type splitSiblingRef struct {
	Label  string `json:"label"`
	ItemID string `json:"item_id"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

type splitLeafView struct {
	SiblingLabel  string `json:"sibling_label"`
	SiblingItemID string `json:"sibling_item_id"`
	SiblingTitle  string `json:"sibling_title"`
	SiblingState  string `json:"sibling_state"`
	Work          string `json:"work"`
}

type splitItemMeta struct {
	Title       string
	State       string
	CloseReason string
}

func isDefectiveClose(state, closeReason string) bool {
	return state == "closed" && strings.HasPrefix(closeReason, "defective:")
}

type splitClosingHold struct {
	Mover  string `json:"mover"`
	Reason string `json:"reason"`
}

type splitClosingState struct {
	Held          *splitClosingHold
	RefusedReason string
}

func readSplitClosingStates(r *store.Reader) (map[string]splitClosingState, error) {
	out := map[string]splitClosingState{}
	hashes := map[string]string{}
	srows, err := r.Query(`SELECT parent_id, statement FROM item_splits`)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var parentID string
		var raw []byte
		if err := srows.Scan(&parentID, &raw); err != nil {
			srows.Close()
			return nil, err
		}
		hashes[parentID] = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	if err := srows.Err(); err != nil {
		srows.Close()
		return nil, err
	}
	srows.Close()
	if len(hashes) == 0 {
		return out, nil
	}
	erows, err := r.Query(`SELECT object_id, kind, payload FROM events WHERE object_type='item' AND kind IN ('drain.split-closing-held','drain.split-closing-refused') ORDER BY seq ASC`)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer erows.Close()
	for erows.Next() {
		var parentID, kind string
		var raw []byte
		if err := erows.Scan(&parentID, &kind, &raw); err != nil {
			return nil, err
		}
		wantHash, ok := hashes[parentID]
		if !ok {
			continue
		}
		var payload struct {
			StatementHash string `json:"statement_hash"`
			Reason        string `json:"reason"`
			Mover         string `json:"mover"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil || payload.StatementHash != wantHash {
			continue
		}
		st := out[parentID]
		switch kind {
		case "drain.split-closing-held":
			st.Held = &splitClosingHold{Mover: payload.Mover, Reason: payload.Reason}
		case "drain.split-closing-refused":
			st.RefusedReason = payload.Reason
		}
		out[parentID] = st
	}
	return out, erows.Err()
}

func readSplitClosingStatesFor(r *store.Reader, parentIDs []string) (map[string]splitClosingState, error) {
	out := map[string]splitClosingState{}
	if len(parentIDs) == 0 {
		return out, nil
	}
	hashes := map[string]string{}
	const chunk = 400
	for start := 0; start < len(parentIDs); start += chunk {
		end := start + chunk
		if end > len(parentIDs) {
			end = len(parentIDs)
		}
		batch := parentIDs[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		srows, err := r.Query(`SELECT parent_id, statement FROM item_splits WHERE parent_id IN (`+placeholders+`)`, args...)
		if isMissingTable(err) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var parentID string
			var raw []byte
			if err := srows.Scan(&parentID, &raw); err != nil {
				srows.Close()
				return nil, err
			}
			hashes[parentID] = fmt.Sprintf("%x", sha256.Sum256(raw))
		}
		if err := srows.Err(); err != nil {
			srows.Close()
			return nil, err
		}
		srows.Close()
	}
	if len(hashes) == 0 {
		return out, nil
	}
	events, err := r.EventsForObjects(0, parentIDs, "drain.split-closing-held", "drain.split-closing-refused")
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.ObjectType != "item" || e.ObjectID == nil {
			continue
		}
		parentID := *e.ObjectID
		wantHash, ok := hashes[parentID]
		if !ok {
			continue
		}
		var payload struct {
			StatementHash string `json:"statement_hash"`
			Reason        string `json:"reason"`
			Mover         string `json:"mover"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.StatementHash != wantHash {
			continue
		}
		st := out[parentID]
		switch e.Kind {
		case "drain.split-closing-held":
			st.Held = &splitClosingHold{Mover: payload.Mover, Reason: payload.Reason}
		case "drain.split-closing-refused":
			st.RefusedReason = payload.Reason
		}
		out[parentID] = st
	}
	return out, nil
}

func readSplitItemMeta(r *store.Reader) (map[string]splitItemMeta, error) {
	out := map[string]splitItemMeta{}
	rows, err := r.Query(`SELECT item_id, title, state, close_reason FROM items`)
	if err != nil && strings.Contains(err.Error(), "no such column: close_reason") {
		rows, err = r.Query(`SELECT item_id, title, state FROM items`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, state string
			if err := rows.Scan(&id, &title, &state); err != nil {
				return nil, err
			}
			out[id] = splitItemMeta{Title: title, State: state}
		}
		return out, rows.Err()
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, state, closeReason string
		if err := rows.Scan(&id, &title, &state, &closeReason); err != nil {
			return nil, err
		}
		out[id] = splitItemMeta{Title: title, State: state, CloseReason: closeReason}
	}
	return out, rows.Err()
}

func readSplitItemMetaFor(r *store.Reader, itemIDs []string) (map[string]splitItemMeta, error) {
	out := map[string]splitItemMeta{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		batch := itemIDs[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(`SELECT item_id, title, state, close_reason FROM items WHERE item_id IN (`+placeholders+`)`, args...)
		if err != nil && strings.Contains(err.Error(), "no such column: close_reason") {
			rows, err = r.Query(`SELECT item_id, title, state FROM items WHERE item_id IN (`+placeholders+`)`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id, title, state string
				if err := rows.Scan(&id, &title, &state); err != nil {
					rows.Close()
					return nil, err
				}
				out[id] = splitItemMeta{Title: title, State: state}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
			continue
		}
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, title, state, closeReason string
			if err := rows.Scan(&id, &title, &state, &closeReason); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = splitItemMeta{Title: title, State: state, CloseReason: closeReason}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func readLandedCommits(r *store.Reader) (map[string]struct{ landingID, sha string }, error) {
	out := map[string]struct{ landingID, sha string }{}
	rows, err := r.Query(`SELECT item_id, landing_id, trunk_after FROM landings WHERE result = 'landed' ORDER BY event_seq DESC, landing_id DESC`)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, landingID, sha string
		if err := rows.Scan(&itemID, &landingID, &sha); err != nil {
			return nil, err
		}
		if _, exists := out[itemID]; !exists {
			out[itemID] = struct{ landingID, sha string }{landingID: landingID, sha: sha}
		}
	}
	return out, rows.Err()
}

func readItemCriteriaTexts(r *store.Reader, itemID string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := r.Query(`SELECT criterion_id, text FROM item_criteria WHERE item_id = ?`, itemID)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, text string
		if err := rows.Scan(&cid, &text); err != nil {
			return nil, err
		}
		out[cid] = text
	}
	return out, rows.Err()
}

func readItemSplitView(r *store.Reader, id string) (*itemSplitView, error) {
	splits, err := readAllSplits(r)
	if err != nil {
		return nil, err
	}
	if len(splits) == 0 {
		return nil, nil
	}
	partIndex := splitPartIndex(splits)
	parentSt, isParent := splits[id]
	partMem, isPart := partIndex[id]
	if !isParent && !isPart {
		return nil, nil
	}
	meta, err := readSplitItemMeta(r)
	if err != nil {
		return nil, err
	}
	view := &itemSplitView{}

	if isParent {
		words, err := readSplitRequestWords(r, id)
		if err != nil {
			return nil, err
		}
		landed, err := readLandedCommits(r)
		if err != nil {
			return nil, err
		}
		critTexts, err := readItemCriteriaTexts(r, id)
		if err != nil {
			return nil, err
		}
		pv := &splitParentDetailView{
			OwnerWords: string(words),
			Donor:      parentSt.Donor,
			TotalParts: len(parentSt.Parts),
			Parts:      make([]splitPartStatusView, 0, len(parentSt.Parts)),
			Coverage:   make([]splitCoverageRowView, 0, len(parentSt.Coverage)),
		}
		partIDByLabel := make(map[string]string, len(parentSt.Parts))
		for _, p := range parentSt.Parts {
			pid := splitPartID(id, p.Label)
			partIDByLabel[p.Label] = pid
			req := p.Required != nil && *p.Required
			m := meta[pid]
			if req {
				pv.RequiredParts++
				if m.State == "landed" {
					pv.RequiredLanded++
				}
			}
			l := landed[pid]
			pv.Parts = append(pv.Parts, splitPartStatusView{
				Label:             p.Label,
				ItemID:            pid,
				Title:             p.Title,
				Share:             p.Share,
				Leaves:            p.Leaves,
				Follows:           slices.Clone(p.Follows),
				Required:          req,
				Takes:             p.Takes,
				Paths:             slices.Clone(p.Paths),
				SharedFiles:       slices.Clone(p.SharedFiles),
				InheritedFindings: slices.Clone(p.InheritedFindings),
				State:             m.State,
				LandedCommit:      l.sha,
				LandingID:         l.landingID,
			})
		}
		pv.ProgressSummary = fmt.Sprintf("%d of %d required parts landed", pv.RequiredLanded, pv.RequiredParts)

		cids := make([]string, 0, len(parentSt.Coverage))
		for cid := range parentSt.Coverage {
			cids = append(cids, cid)
		}
		sort.Strings(cids)
		for _, cid := range cids {
			entries := make([]splitCoverageEntryView, 0, len(parentSt.Coverage[cid]))
			for _, e := range parentSt.Coverage[cid] {
				entries = append(entries, splitCoverageEntryView{
					Clause:       e.Clause,
					PartLabel:    e.Part,
					PartItemID:   partIDByLabel[e.Part],
					ClosingCheck: e.ClosingCheck,
				})
			}
			pv.Coverage = append(pv.Coverage, splitCoverageRowView{
				CriterionID:   cid,
				CriterionText: critTexts[cid],
				Entries:       entries,
			})
		}
		view.Parent = pv
	}

	if isPart {
		parentMeta := meta[partMem.ParentID]
		parentSplit := splits[partMem.ParentID]
		siblingTitleByLabel := make(map[string]string, len(parentSplit.Parts))
		for _, p := range parentSplit.Parts {
			siblingTitleByLabel[p.Label] = p.Title
		}
		req := partMem.Part.Required != nil && *partMem.Part.Required
		pv := &splitPartDetailView{
			ParentID:          partMem.ParentID,
			ParentTitle:       parentMeta.Title,
			Donor:             parentSplit.Donor,
			Label:             partMem.Label,
			Title:             partMem.Part.Title,
			Share:             partMem.Part.Share,
			Required:          req,
			Takes:             partMem.Part.Takes,
			Paths:             slices.Clone(partMem.Part.Paths),
			SharedFiles:       slices.Clone(partMem.Part.SharedFiles),
			InheritedFindings: slices.Clone(partMem.Part.InheritedFindings),
		}
		for _, fLabel := range partMem.Part.Follows {
			sibID := splitPartID(partMem.ParentID, fLabel)
			sm := meta[sibID]
			title := sm.Title
			if title == "" {
				title = siblingTitleByLabel[fLabel]
			}
			pv.Follows = append(pv.Follows, splitSiblingRef{
				Label:  fLabel,
				ItemID: sibID,
				Title:  title,
				State:  sm.State,
			})
		}
		leafLabels := make([]string, 0, len(partMem.Part.Leaves))
		for label := range partMem.Part.Leaves {
			leafLabels = append(leafLabels, label)
		}
		sort.Strings(leafLabels)
		for _, label := range leafLabels {
			sibID := splitPartID(partMem.ParentID, label)
			sm := meta[sibID]
			title := sm.Title
			if title == "" {
				title = siblingTitleByLabel[label]
			}
			pv.Leaves = append(pv.Leaves, splitLeafView{
				SiblingLabel:  label,
				SiblingItemID: sibID,
				SiblingTitle:  title,
				SiblingState:  sm.State,
				Work:          partMem.Part.Leaves[label],
			})
		}
		view.Part = pv
	}

	return view, nil
}

func formatSplitCounts(total, landed, working, held, queued, closed int) string {
	var parts []string
	if landed > 0 {
		parts = append(parts, fmt.Sprintf("%d landed", landed))
	}
	if working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", working))
	}
	if held > 0 {
		parts = append(parts, fmt.Sprintf("%d held", held))
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", queued))
	}
	if closed > 0 {
		parts = append(parts, fmt.Sprintf("%d closed", closed))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("split into %d parts", total)
	}
	return fmt.Sprintf("split into %d parts: %s", total, strings.Join(parts, ", "))
}

func decorateWhyPassSplits(r *store.Reader, rows []whyPassRow) error {
	if len(rows) == 0 {
		return nil
	}
	splits, err := readAllSplits(r)
	if err != nil {
		return err
	}
	if len(splits) == 0 {
		return nil
	}
	meta, err := readSplitItemMeta(r)
	if err != nil {
		return err
	}
	closingStates, err := readSplitClosingStates(r)
	if err != nil {
		return err
	}
	declared, err := definition.ForReader(r)
	if err != nil {
		return err
	}
	def := declared.Parsed
	partIndex := splitPartIndex(splits)
	rowByID := make(map[string]*whyPassRow, len(rows))
	for i := range rows {
		row := &rows[i]
		rowByID[row.ItemID] = row
		if m, ok := partIndex[row.ItemID]; ok {
			req := m.Part.Required != nil && *m.Part.Required
			row.SplitPart = &whySplitPart{
				ParentID:    m.ParentID,
				ParentTitle: meta[m.ParentID].Title,
				PartLabel:   m.Label,
				Required:    req,
			}
		}
	}
	for i := range rows {
		row := &rows[i]
		st, ok := splits[row.ItemID]
		if !ok {
			continue
		}
		sp := &whySplitParent{}
		var closedRequired []string
		var defectiveRequired []string
		var unlandedRequired []string
		for _, p := range st.Parts {
			if len(p.ReplacedBy) > 0 {
				continue
			}
			sp.TotalParts++
			partID := splitPartID(row.ItemID, p.Label)
			req := p.Required != nil && *p.Required
			if req {
				sp.RequiredParts++
			}
			partState := meta[partID].State
			switch {
			case partState == "landed":
				sp.Landed++
				if req {
					sp.RequiredLanded++
				}
			case def.IsFinished(partState) || partState == def.Closed || partState == "closed":
				sp.Closed++
				if req {
					if isDefectiveClose(partState, meta[partID].CloseReason) {
						defectiveRequired = append(defectiveRequired, partID)
					} else {
						closedRequired = append(closedRequired, partID)
					}
					unlandedRequired = append(unlandedRequired, partID)
				}
			default:
				if req {
					unlandedRequired = append(unlandedRequired, partID)
				}
				pr := rowByID[partID]
				if pr != nil && (pr.Position == "running" || pr.Class == "moving") {
					sp.Working++
				} else if pr != nil && (pr.Class == "needs you" || pr.Class == "alarm" || pr.Class == "needs a cause") {
					sp.Held++
					reason := pr.Reason
					if reason == "" {
						reason = pr.Why
					}
					sp.HeldParts = append(sp.HeldParts, whySplitHeldPart{
						ItemID: partID,
						Label:  p.Label,
						Reason: reason,
						Mover:  pr.Mover,
					})
				} else {
					sp.Queued++
				}
			}
		}
		sp.ProgressSummary = fmt.Sprintf("%d of %d required parts landed", sp.RequiredLanded, sp.RequiredParts)
		row.SplitParent = sp
		if def.IsFinished(row.State) || row.State == def.Closed || row.State == "closed" {
			continue
		}
		summary := formatSplitCounts(sp.TotalParts, sp.Landed, sp.Working, sp.Held, sp.Queued, sp.Closed)
		switch {
		case len(closedRequired) > 0:
			id := closedRequired[0]
			row.Class = "needs you"
			row.Mover = "owner; required part did not land: " + id
			row.Why = summary + "; required part did not land: " + id + " (" + meta[id].State + ")"
		case len(defectiveRequired) > 0:
			id := defectiveRequired[0]
			cs := closingStates[row.ItemID]
			if cs.Held != nil {
				if cs.Held.Mover == "owner" {
					row.Class = "needs you"
					row.Mover = "owner; " + cs.Held.Reason
				} else {
					row.Class = "waits on work"
					row.Mover = cs.Held.Mover
				}
				row.Why = summary + "; " + cs.Held.Reason
			} else {
				row.Class = "waits on work"
				row.Mover = "design"
				row.Why = summary + "; required part closed as defective: " + id + "; waiting on design seat"
				if cs.RefusedReason != "" {
					row.Why += "; prior extension refused: " + cs.RefusedReason
				}
			}
		case sp.RequiredLanded == sp.RequiredParts:
			row.Class = "needs you"
			row.Mover = "operator; check the parent's criteria by hand"
			row.Why = summary + "; check the parent's criteria by hand"
		case len(sp.HeldParts) > 0:
			heldDetails := make([]string, 0, len(sp.HeldParts))
			heldMovers := make([]string, 0, len(sp.HeldParts))
			for _, hp := range sp.HeldParts {
				heldDetails = append(heldDetails, fmt.Sprintf("%s (%s; mover: %s)", hp.Label, hp.Reason, hp.Mover))
				if hp.Mover != "" {
					heldMovers = append(heldMovers, hp.Label+": "+hp.Mover)
				}
			}
			row.Why = summary + "; held: " + strings.Join(heldDetails, ", ")
			if len(heldMovers) > 0 {
				row.Mover = strings.Join(heldMovers, "; ")
			} else if len(unlandedRequired) > 0 {
				row.Mover = strings.Join(unlandedRequired, ", ")
			}
		default:
			row.Why = summary
			if len(unlandedRequired) > 0 {
				row.Mover = strings.Join(unlandedRequired, ", ")
			}
		}
	}
	return nil
}

func splitSnapshotDecorations(r *store.Reader) (map[string]string, map[string]*whySplitPart, error) {
	splits, err := readAllSplits(r)
	if err != nil {
		return nil, nil, err
	}
	if len(splits) == 0 {
		return nil, nil, nil
	}
	meta, err := readSplitItemMeta(r)
	if err != nil {
		return nil, nil, err
	}
	progress := make(map[string]string, len(splits))
	for parentID, st := range splits {
		if meta[parentID].State == "landed" {
			continue
		}
		reqTotal, reqLanded := 0, 0
		for _, p := range st.Parts {
			if len(p.ReplacedBy) > 0 {
				continue
			}
			if p.Required != nil && *p.Required {
				reqTotal++
				if meta[splitPartID(parentID, p.Label)].State == "landed" {
					reqLanded++
				}
			}
		}
		progress[parentID] = fmt.Sprintf("%d of %d required parts landed", reqLanded, reqTotal)
	}
	partIndex := splitPartIndex(splits)
	parts := make(map[string]*whySplitPart, len(partIndex))
	for pid, m := range partIndex {
		req := m.Part.Required != nil && *m.Part.Required
		parts[pid] = &whySplitPart{
			ParentID:    m.ParentID,
			ParentTitle: meta[m.ParentID].Title,
			PartLabel:   m.Label,
			Required:    req,
		}
	}
	return progress, parts, nil
}
