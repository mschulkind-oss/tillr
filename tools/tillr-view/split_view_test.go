package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestSplitViewAPIsAndSurfaces(t *testing.T) {
	h := newServeHarness(t)
	ctx := context.Background()

	parentID := "item-seed-01"
	partAID := splitPartID(parentID, "a")
	partBID := splitPartID(parentID, "b")
	nestedPartID := splitPartID(partBID, "b1")
	nestedPart2ID := splitPartID(partBID, "b2")

	parentSplit := splitStatement{
		Parts: []splitPart{
			{Label: "a", Title: "Part A", Share: "Deliver A", Leaves: map[string]string{"b": "Deliver B"}, Required: boolPtr(true)},
			{Label: "b", Title: "Part B", Share: "Deliver B", Leaves: map[string]string{"a": "Deliver A"}, Follows: []string{"a"}, Required: boolPtr(true)},
		},
		Coverage: map[string][]splitCoverage{
			"c5": {
				{Clause: "A works;", Part: "a"},
				{Clause: "B works.", Part: "b"},
			},
			"c6": {{ClosingCheck: true}},
		},
	}
	nestedSplit := splitStatement{
		Parts: []splitPart{
			{Label: "b1", Title: "Part B1", Share: "Deliver B1", Required: boolPtr(true)},
			{Label: "b2", Title: "Part B2", Share: "Deliver B2", Follows: []string{"b1"}, Required: boolPtr(true)},
		},
		Coverage: map[string][]splitCoverage{
			"c5": {{Part: "b1"}},
		},
	}
	parentJSON, err := json.Marshal(parentSplit)
	if err != nil {
		t.Fatal(err)
	}
	nestedJSON, err := json.Marshal(nestedSplit)
	if err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`CREATE TABLE IF NOT EXISTS item_splits (parent_id TEXT PRIMARY KEY, statement BLOB NOT NULL)`, nil},
		{`CREATE TABLE IF NOT EXISTS request_words (item_id TEXT PRIMARY KEY, words BLOB NOT NULL)`, nil},
		{`CREATE TABLE IF NOT EXISTS landings (landing_id TEXT PRIMARY KEY, item_id TEXT, run_id TEXT, verdict_id TEXT, result TEXT, reason TEXT, base_sha TEXT, head_sha TEXT, trunk_before TEXT, trunk_after TEXT, gated_tree TEXT, landed_tree TEXT, conflict_paths TEXT, gate_skipped_reason TEXT, event_seq INT)`, nil},
		{`INSERT OR REPLACE INTO items (item_id, title, state, updated_at) VALUES (?, 'Parent Ask', 'blocked', 1900)`, []any{parentID}},
		{`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, 'Part A', 'landed', 2000)`, []any{partAID}},
		{`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, 'Part B', 'blocked', 2100)`, []any{partBID}},
		{`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, 'Part B1', 'in-queue', 2200)`, []any{nestedPartID}},
		{`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, 'Part B2', 'in-queue', 2300)`, []any{nestedPart2ID}},
		{`INSERT INTO request_words (item_id, words) VALUES (?, ?)`, []any{parentID, []byte("Owner's original words.")}},
		{`INSERT INTO item_criteria (item_id, criterion_id, text, state) VALUES (?, 'c5', 'A works; B works.', 'not-done')`, []any{parentID}},
		{`INSERT INTO item_criteria (item_id, criterion_id, text, state) VALUES (?, 'c6', 'Together they work.', 'not-done')`, []any{parentID}},
		{`INSERT INTO item_splits (parent_id, statement) VALUES (?, ?)`, []any{parentID, parentJSON}},
		{`INSERT INTO item_splits (parent_id, statement) VALUES (?, ?)`, []any{partBID, nestedJSON}},
		{`INSERT INTO landings (landing_id, item_id, run_id, verdict_id, result, reason, base_sha, head_sha, trunk_before, trunk_after, gated_tree, landed_tree, conflict_paths, gate_skipped_reason, event_seq) VALUES ('land-a', ?, 'run-a', 'ver-a', 'landed', '', 'base', 'head', 'tb', 'sha-landed-a', 'gt', 'lt', '[]', '', 99)`, []any{partAID}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}

	// 1. Item page API for parent.
	resp, err := h.http.Client().Get(h.http.URL + "/api/v1/items/" + parentID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("parent itemPayload status=%d body=%s", resp.StatusCode, body)
	}
	var parentEnv struct {
		Data itemData `json:"data"`
	}
	if err := json.Unmarshal(body, &parentEnv); err != nil {
		t.Fatal(err)
	}
	if parentEnv.Data.Split == nil || parentEnv.Data.Split.Parent == nil {
		t.Fatalf("missing split parent detail: %+v", parentEnv.Data.Split)
	}
	sp := parentEnv.Data.Split.Parent
	if sp.OwnerWords != "Owner's original words." || sp.ProgressSummary != "1 of 2 required parts landed" || sp.TotalParts != 2 {
		t.Fatalf("unexpected parent detail: %+v", sp)
	}
	if sp.Parts[0].LandedCommit != "sha-landed-a" || sp.Parts[0].LandingID != "land-a" {
		t.Fatalf("part A missing landed commit: %+v", sp.Parts[0])
	}
	if len(sp.Coverage) != 2 || sp.Coverage[0].CriterionID != "c5" || len(sp.Coverage[0].Entries) != 2 || !sp.Coverage[1].Entries[0].ClosingCheck {
		t.Fatalf("unexpected coverage: %+v", sp.Coverage)
	}

	// 2. Item page API for nested split item (both part and parent).
	respB, err := h.http.Client().Get(h.http.URL + "/api/v1/items/" + partBID)
	if err != nil {
		t.Fatal(err)
	}
	defer respB.Body.Close()
	bodyB, err := io.ReadAll(respB.Body)
	if err != nil {
		t.Fatal(err)
	}
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("part B itemPayload status=%d body=%s", respB.StatusCode, bodyB)
	}
	var partBEnv struct {
		Data itemData `json:"data"`
	}
	if err := json.Unmarshal(bodyB, &partBEnv); err != nil {
		t.Fatal(err)
	}
	if partBEnv.Data.Split == nil || partBEnv.Data.Split.Part == nil || partBEnv.Data.Split.Parent == nil {
		t.Fatalf("nested split item missing part or parent detail: %+v", partBEnv.Data.Split)
	}
	pb := partBEnv.Data.Split.Part
	if pb.ParentID != parentID || pb.ParentTitle != "Parent Ask" || pb.Label != "b" || pb.Share != "Deliver B" {
		t.Fatalf("unexpected part B detail: %+v", pb)
	}
	if len(pb.Follows) != 1 || pb.Follows[0].Label != "a" || pb.Follows[0].State != "landed" {
		t.Fatalf("unexpected follows: %+v", pb.Follows)
	}
	if len(pb.Leaves) != 1 || pb.Leaves[0].SiblingLabel != "a" || pb.Leaves[0].Work != "Deliver A" {
		t.Fatalf("unexpected leaves: %+v", pb.Leaves)
	}

	// 3. Landing history reports part A under Parent Ask.
	rs, err := store.OpenRead(ctx, h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		landings, err := readLandingHistory(r)
		if err != nil {
			return err
		}
		foundPartLanding := false
		for _, l := range landings {
			if l.ItemID == partAID {
				foundPartLanding = true
				if l.ParentID != parentID || l.ParentTitle != "Parent Ask" || l.PartLabel != "a" {
					t.Fatalf("part landing missing parent metadata: %+v", l)
				}
			}
		}
		if !foundPartLanding {
			t.Fatal("missing part A landing in history")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 4. Snapshot counts do not count unlanded split parent as landed, and include split_progress/split_part.
	snapResp, err := h.http.Client().Get(h.http.URL + "/api/v1/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer snapResp.Body.Close()
	snapBody, err := io.ReadAll(snapResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if snapResp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", snapResp.StatusCode, snapBody)
	}
	var snapEnv struct {
		Data snapshotData `json:"data"`
	}
	if err := json.Unmarshal(snapBody, &snapEnv); err != nil {
		t.Fatal(err)
	}
	if snapEnv.Data.Counts.ByState["landed"] != 1 {
		t.Fatalf("expected only 1 landed item (part A, not parent), got %+v", snapEnv.Data.Counts.ByState)
	}
	snapItems := map[string]itemView{}
	for _, it := range snapEnv.Data.Items {
		snapItems[it.ItemID] = it
	}
	if snapItems[parentID].SplitProgress != "1 of 2 required parts landed" {
		t.Fatalf("parent SplitProgress = %q", snapItems[parentID].SplitProgress)
	}
	if snapItems[partAID].SplitPart == nil || snapItems[partAID].SplitPart.ParentTitle != "Parent Ask" || snapItems[partAID].SplitPart.PartLabel != "a" {
		t.Fatalf("part A SplitPart = %+v", snapItems[partAID].SplitPart)
	}

	// 5. printWaiting labels part rows with parent title and part label.
	var buf bytes.Buffer
	if err := printWaiting(&buf, []waitingRow{
		{ItemID: partBID, Class: "needs you", Where: "queue", Why: "waiting", Mover: "owner", Since: "now", SplitPart: &whySplitPart{ParentID: parentID, ParentTitle: "Parent Ask", PartLabel: "b", Required: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), partBID+" (Parent Ask · part b)") {
		t.Fatalf("printWaiting output missing parent label: %s", buf.String())
	}

	// 6. decorateWhyPassSplits decorates parent and part rows for whyFromPass.
	rows := []whyPassRow{
		{waitingRow: waitingRow{ItemID: parentID, Class: "waits on work", Why: "waiting", Mover: partBID}, State: "blocked", Reason: "blocked"},
		{waitingRow: waitingRow{ItemID: partBID, Class: "waits on work", Why: "waiting", Mover: nestedPartID}, State: "blocked", Reason: "blocked"},
		{waitingRow: waitingRow{ItemID: nestedPartID, Class: "ready", Why: "ready", Mover: "drain"}, State: "in-queue", Reason: "ready"},
		{waitingRow: waitingRow{ItemID: nestedPart2ID, Class: "waits on work", Why: "waiting", Mover: nestedPartID}, State: "in-queue", Reason: "blocked-by"},
	}
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		return decorateWhyPassSplits(r, rows)
	}); err != nil {
		t.Fatalf("decorateWhyPassSplits: %v", err)
	}
	byID := map[string]whyPassRow{}
	for _, r := range rows {
		byID[r.ItemID] = r
	}
	if byID[parentID].SplitParent == nil || !strings.HasPrefix(byID[parentID].Why, "split into 2 parts:") {
		t.Fatalf("whyFromPass parent row: %+v", byID[parentID])
	}
	if byID[nestedPartID].SplitPart == nil || byID[nestedPartID].SplitPart.ParentID != partBID || byID[nestedPartID].SplitPart.PartLabel != "b1" {
		t.Fatalf("whyFromPass part row: %+v", byID[nestedPartID])
	}
}

func TestItemSplitDonorAndCarvingFields(t *testing.T) {
	h := newServeHarness(t)
	ctx := context.Background()

	parentID := "item-seed-01"
	wholeID := splitPartID(parentID, "whole-p")
	namedID := splitPartID(parentID, "named-p")

	st := splitStatement{
		Donor: &splitDonorView{
			RunID:   "run-donor-view",
			Ref:     "refs/tillr/runs/run-donor-view",
			SHA:     "donor-sha-view",
			BaseSHA: "base-sha-view",
		},
		Parts: []splitPart{
			{
				Label:    "whole-p",
				Title:    "Whole Part",
				Share:    "Deliver A",
				Takes:    "whole",
				Required: boolPtr(true),
			},
			{
				Label:             "named-p",
				Title:             "Named Part",
				Share:             "Deliver B",
				Takes:             "named",
				Paths:             []string{"tools/b/b.go"},
				SharedFiles:       []string{"tools/shared/s.go"},
				InheritedFindings: []string{"c5 [regression] (B failed): tools/b/b.go"},
				Required:          boolPtr(true),
			},
		},
		Coverage: map[string][]splitCoverage{
			"c5": {{Clause: "A works;", Part: "whole-p"}, {Clause: "B works.", Part: "named-p"}},
		},
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS item_splits (parent_id TEXT PRIMARY KEY, statement BLOB NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,updated_at) VALUES (?,?,?,?)`, wholeID, "Whole Part", "in-queue", "2026-11-27T09:00:00Z"); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,updated_at) VALUES (?,?,?,?)`, namedID, "Named Part", "in-queue", "2026-11-27T09:00:00Z"); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_splits(parent_id, statement) VALUES (?,?)`, parentID, raw)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	rs, err := store.OpenRead(ctx, h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		parentView, err := readItemSplitView(r, parentID)
		if err != nil || parentView == nil || parentView.Parent == nil {
			t.Fatalf("parentView = %+v, %v", parentView, err)
		}
		if parentView.Parent.Donor == nil || parentView.Parent.Donor.SHA != "donor-sha-view" || parentView.Parent.Donor.BaseSHA != "base-sha-view" {
			t.Fatalf("parent Donor = %+v", parentView.Parent.Donor)
		}
		if parentView.Parent.Parts[0].Takes != "whole" || parentView.Parent.Parts[1].Takes != "named" || len(parentView.Parent.Parts[1].Paths) != 1 {
			t.Fatalf("parent Parts = %+v", parentView.Parent.Parts)
		}

		partView, err := readItemSplitView(r, namedID)
		if err != nil || partView == nil || partView.Part == nil {
			t.Fatalf("partView = %+v, %v", partView, err)
		}
		if partView.Part.Donor == nil || partView.Part.Donor.SHA != "donor-sha-view" {
			t.Fatalf("part Donor = %+v", partView.Part.Donor)
		}
		if partView.Part.Takes != "named" || len(partView.Part.Paths) != 1 || len(partView.Part.SharedFiles) != 1 || len(partView.Part.InheritedFindings) != 1 {
			t.Fatalf("part fields = %+v", partView.Part)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestSplitViewDefectiveRequiredPartAndWaiting(t *testing.T) {
	ctx := context.Background()
	h := newServeHarness(t)

	parentID := "item-parent-defective"
	partAID := splitPartID(parentID, "a")
	partAV2ID := splitPartID(parentID, "a-v2")
	partBID := splitPartID(parentID, "b")

	stmt := splitStatement{
		Parts: []splitPart{
			{Label: "a", Title: "Part A", Share: "Do A", Required: boolPtr(true)},
			{Label: "b", Title: "Part B", Share: "Do B", Follows: []string{"a"}, Required: boolPtr(true)},
		},
	}
	rawStmt, err := json.Marshal(stmt)
	if err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(ctx, h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS item_splits (parent_id TEXT PRIMARY KEY, statement BLOB NOT NULL)`); err != nil {
			return err
		}
		_, _ = tx.Exec(`ALTER TABLE items ADD COLUMN close_reason TEXT NOT NULL DEFAULT ''`)
		for _, it := range []struct {
			id, title, state, closeReason string
		}{
			{parentID, "Parent Ask", "blocked", ""},
			{partAID, "Part A", "closed", "defective: part words forbid test change"},
			{partBID, "Part B", "in-queue", ""},
		} {
			if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,close_reason,updated_at) VALUES (?,?,?,?,?)`,
				it.id, it.title, it.state, it.closeReason, int64(1795770000000)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by) VALUES (?,?)`, partBID, partAID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_splits(parent_id, statement) VALUES (?,?)`, parentID, rawStmt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	rs, err := store.OpenRead(ctx, h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		// 1. Parent row via decorateWhyPassSplits -> waits on work / design (not owner).
		rows := []whyPassRow{
			{waitingRow: waitingRow{ItemID: parentID, Class: "waits on work", Why: "waiting", Mover: partAID}, State: "blocked", Reason: "blocked"},
		}
		if err := decorateWhyPassSplits(r, rows); err != nil {
			return err
		}
		if rows[0].Class != "waits on work" || rows[0].Mover != "design" {
			t.Fatalf("parent row = %+v, want waits on work / design", rows[0])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 2. Dependent sibling via readWaiting -> waits on work / design (not operator).
	wRows, err := readWaiting(ctx, h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sibRow *waitingRow
	for i := range wRows {
		if wRows[i].ItemID == partBID {
			sibRow = &wRows[i]
			break
		}
	}
	if sibRow == nil || sibRow.Class != "waits on work" || !strings.Contains(sibRow.Mover, "design") || strings.Contains(sibRow.Mover, "operator") {
		t.Fatalf("sibling waiting row = %+v, want waits on work / design", sibRow)
	}

	// 3. After replacement: replaced part is excluded from active required counts,
	// and a non-defective closed required part still names owner.
	stmtReplaced := splitStatement{
		Parts: []splitPart{
			{Label: "a", Title: "Part A", Share: "Do A", Required: boolPtr(true), ReplacedBy: []string{"a-v2"}},
			{Label: "a-v2", Title: "Part A v2", Share: "Do A v2", Required: boolPtr(true)},
			{Label: "b", Title: "Part B", Share: "Do B", Follows: []string{"a-v2"}, Required: boolPtr(true)},
		},
	}
	rawReplaced, _ := json.Marshal(stmtReplaced)
	s, err = store.Open(ctx, h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,close_reason,updated_at) VALUES (?,?,?,?,?)`,
			partAV2ID, "Part A v2", "landed", "", int64(1795770300000)); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET state='closed', close_reason='obsolete' WHERE item_id=?`, partBID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE item_splits SET statement=? WHERE parent_id=?`, rawReplaced, parentID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	rs2, err := store.OpenRead(ctx, h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer rs2.Close()
	if err := rs2.ReadTx(ctx, func(r *store.Reader) error {
		progress, _, err := splitSnapshotDecorations(r)
		if err != nil {
			return err
		}
		if progress[parentID] != "1 of 2 required parts landed" {
			t.Fatalf("progress = %q, want 1 of 2 required parts landed", progress[parentID])
		}
		rows := []whyPassRow{
			{waitingRow: waitingRow{ItemID: parentID, Class: "waits on work", Why: "waiting", Mover: partBID}, State: "blocked", Reason: "blocked"},
		}
		if err := decorateWhyPassSplits(r, rows); err != nil {
			return err
		}
		if rows[0].Class != "needs you" || !strings.HasPrefix(rows[0].Mover, "owner; required part did not land: "+partBID) {
			t.Fatalf("non-defective closed part row = %+v, want owner; required part did not land", rows[0])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingAndWhyPassShowHeldSplitClosingReasonAndActionableMover(t *testing.T) {
	h := newServeHarness(t)
	ctx := context.Background()

	parentID := "item-seed-06"
	partAID := splitPartID(parentID, "a")
	st := splitStatement{
		Parts: []splitPart{
			{Label: "a", Title: "Part A", Share: "Do A", Required: boolPtr(true)},
		},
	}
	rawStmt, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	stmtHash := fmt.Sprintf("%x", sha256.Sum256(rawStmt))
	holdReason := "closing check closing-1 invalid verdict: malformed JSON"
	holdPayload, _ := json.Marshal(map[string]any{
		"item_id":        parentID,
		"run_id":         "closing-1",
		"statement_hash": stmtHash,
		"reason":         holdReason,
		"mover":          "operator",
	})
	waitingJSON, _ := json.Marshal(map[string]any{
		"waiter": "owner",
		"source": "split",
		"reason": "split parent; check its criteria after the required parts land",
	})

	s, err := store.Open(ctx, h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS item_splits (parent_id TEXT PRIMARY KEY, statement BLOB NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO item_splits(parent_id, statement) VALUES (?, ?)`, parentID, rawStmt); err != nil {
			return err
		}
		if _, err := tx.Exec(`ALTER TABLE items ADD COLUMN waiting_on TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET state='blocked', waiting_on=? WHERE item_id=?`, string(waitingJSON), parentID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO items(item_id,title,state,updated_at) VALUES (?,?,?,?)`, partAID, "Part A", "landed", int64(1795770300000)); err != nil {
			return err
		}
		_, err := tx.Exec(
			`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?,?,?,?,?)`,
			int64(1795770400000), "drain.split-closing-held", "item", parentID, string(holdPayload),
		)
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	// 1. waiting view shows the real held reason and actionable mover.
	waitingRows, err := readWaiting(ctx, h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found *waitingRow
	for i := range waitingRows {
		if waitingRows[i].ItemID == parentID {
			found = &waitingRows[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("parent %s missing from waiting rows: %+v", parentID, waitingRows)
	}
	if found.Class != "needs you" || !strings.HasPrefix(found.Mover, "operator;") || !strings.Contains(found.Mover, holdReason) {
		t.Fatalf("waiting row mover/class = %+v, want needs you / operator with real reason", *found)
	}
	if !strings.Contains(found.Why, holdReason) || strings.Contains(found.Why, "split parent; check its criteria") {
		t.Fatalf("waiting row why = %q, want real reason", found.Why)
	}

	rs, err := store.OpenRead(ctx, h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		// 2. whyFromPass decoration also shows the real held reason and actionable mover.
		passRows := []whyPassRow{
			{waitingRow: waitingRow{ItemID: parentID, Class: "waits on work", Why: "waiting", Mover: "review"}, State: "blocked", Reason: "blocked"},
		}
		if err := decorateWhyPassSplits(r, passRows); err != nil {
			return err
		}
		if passRows[0].Class != "needs you" || !strings.HasPrefix(passRows[0].Mover, "operator;") || !strings.Contains(passRows[0].Mover, holdReason) {
			t.Fatalf("whyPass row mover/class = %+v, want needs you / operator with real reason", passRows[0])
		}
		if !strings.Contains(passRows[0].Why, holdReason) || strings.Contains(passRows[0].Why, "check the parent's criteria by hand") {
			t.Fatalf("whyPass row why = %q, want real reason", passRows[0].Why)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
