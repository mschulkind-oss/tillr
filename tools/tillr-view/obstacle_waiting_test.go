package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

func buildFreshDrainBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tillr-drain")
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = filepath.Join("..", "tillr-drain")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building tillr-drain: %v\n%s", err, raw)
	}
	return path
}

func TestObstacleHoldsShowRealReasonAndMoverInWaitingAndAgreeWithPlan(t *testing.T) {
	ctx := context.Background()
	storePath := filepath.Join(t.TempDir(), "obstacle-view.sqlite3")
	st, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	execSQL := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("seeding %q: %v", stmt, err)
		}
	}
	execSQL(string(schema))
	execSQL(`ALTER TABLE items ADD COLUMN rank TEXT NOT NULL DEFAULT ''`)
	execSQL(`ALTER TABLE items ADD COLUMN seat TEXT NOT NULL DEFAULT ''`)
	execSQL(`ALTER TABLE items ADD COLUMN waiting_on TEXT NOT NULL DEFAULT ''`)
	execSQL(`ALTER TABLE items ADD COLUMN underlying_state TEXT NOT NULL DEFAULT ''`)
	execSQL(`CREATE TABLE retries (
		retry_id          TEXT PRIMARY KEY,
		item_id           TEXT NOT NULL,
		supersedes_run_id TEXT NOT NULL,
		requested_by      TEXT NOT NULL,
		reason            TEXT NOT NULL,
		requested_at      INTEGER NOT NULL
	)`)

	type spec struct {
		itemID     string
		runID      string
		kind       string
		fields     map[string]string
		checks     []string
		wantReason string
		wantClass  string
		wantWhere  string
		wantWhySub []string
		wantMover  string
	}
	specs := []spec{
		{
			itemID:     "item-request-owner",
			runID:      "run-owner-1",
			kind:       "owner",
			fields:     map[string]string{"QUESTION": "Should unknown flags exit 2 or 64?"},
			wantReason: "obstacle-owner",
			wantClass:  "needs you",
			wantWhere:  "queue",
			wantWhySub: []string{"obstacle-owner:", "Should unknown flags exit 2 or 64?"},
			wantMover:  "owner; answer the question",
		},
		{
			itemID:     "item-request-trunk",
			runID:      "run-trunk-1",
			kind:       "trunk",
			fields:     map[string]string{"CHECKS": "go-tool/unit, tillr-docs", "BASE": "0123456"},
			checks:     []string{"go-tool/unit", "tillr-docs"},
			wantReason: "obstacle-trunk",
			wantClass:  "waits on work",
			wantWhere:  "trunk",
			wantWhySub: []string{"obstacle-trunk:", "go-tool/unit, tillr-docs"},
			wantMover:  "drain; returns when checks go-tool/unit, tillr-docs are green",
		},
		{
			itemID:     "item-request-tooling",
			runID:      "run-tooling-1",
			kind:       "tooling",
			fields:     map[string]string{"COMMAND": "go test ./...", "EVIDENCE": "exec: gcc: executable file not found"},
			wantReason: "obstacle-tooling",
			wantClass:  "needs you",
			wantWhere:  "queue",
			wantWhySub: []string{"obstacle-tooling:", "go test ./...: exec: gcc: executable file not found"},
			wantMover:  "operator; exec: gcc: executable file not found",
		},
		{
			itemID:     "item-request-seat",
			runID:      "run-seat-1",
			kind:       "seat",
			fields:     map[string]string{"TRIED": "two parser rewrites", "MISSING": "context-sensitive grammar proof"},
			wantReason: "obstacle-seat",
			wantClass:  "needs you",
			wantWhere:  "queue",
			wantWhySub: []string{"obstacle-seat:", "tried: two parser rewrites; missing: context-sensitive grammar proof"},
			wantMover:  "operator; tried: two parser rewrites; missing: context-sensitive grammar proof",
		},
		{
			itemID:     "item-request-definition",
			runID:      "run-def-1",
			kind:       "definition",
			fields:     map[string]string{"CRITERIA": "request", "EVIDENCE": "named command was retired"},
			wantReason: "obstacle-definition",
			wantClass:  "needs you",
			wantWhere:  "queue",
			wantWhySub: []string{"obstacle-definition:", "named command was retired"},
			wantMover:  "operator; named command was retired",
		},
	}

	zero := 0
	for i, sp := range specs {
		fence := `["tools/item-` + sp.kind + `/"]`
		state := "in-queue"
		waitingJSON := ""
		underlying := ""
		if sp.kind == "owner" {
			state = "blocked"
			waitingJSON = `{"waiter":"owner","reason":"Should unknown flags exit 2 or 64?"}`
			underlying = "in-queue"
		}
		execSQL(`INSERT INTO items (item_id, title, body_ref, state, waiting_on, underlying_state, design_doc, updated_at, allowed_paths)
			VALUES (?, ?, '', ?, ?, ?, '', ?, ?)`,
			sp.itemID, "request "+sp.kind, state, waitingJSON, underlying, 1000+i, fence)
		execSQL(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			VALUES (?, 'item.record', 'item', ?, ?)`, 1000+i, sp.itemID, `{"state":"in-queue","allowed_paths":["tools/item-`+sp.kind+`/"]}`)
		execSQL(`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, artifact_refs, event_seq)
			VALUES (?, ?, ?, 'pi', 'exited', ?, ?, ?, '[]', 0)`,
			sp.runID, sp.itemID, "lane-"+sp.kind, &zero, 1100+i, 1200+i)
		execSQL(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			VALUES (?, 'run.allocate', 'run', ?, ?)`,
			1050+i, sp.runID, `{"item_id":"`+sp.itemID+`","run_id":"`+sp.runID+`"}`)

		hold := obstacles.NewHold(sp.itemID, sp.runID, sp.kind, sp.fields, sp.checks, "0123456", []string{"tools/item-" + sp.kind + "/"})
		rawHold, err := json.Marshal(hold)
		if err != nil {
			t.Fatal(err)
		}
		if sp.kind == "owner" {
			execSQL(`INSERT INTO events (ts, kind, object_type, object_id, payload)
				VALUES (?, 'item.update', 'item', ?, ?)`,
				1300+i, sp.itemID, `{"state":"blocked","waiting_on":{"waiter":"owner","reason":"Should unknown flags exit 2 or 64?"}}`)
		}
		execSQL(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			VALUES (?, ?, 'item', ?, ?)`,
			1400+i, obstacles.HeldKind, sp.itemID, string(rawHold))
		execSQL(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			VALUES (?, 'drain.decision', 'item', ?, ?)`,
			1500+i, sp.itemID, `{"item_id":"`+sp.itemID+`","action":"hold","reason":"`+sp.wantReason+`"}`)
	}

	drainBin := buildFreshDrainBinary(t)
	origDrainBin := sharedDrainBin
	sharedDrainBin = drainBin
	defer func() { sharedDrainBin = origDrainBin }()

	// 1. Dispatchable count in tillr-view agrees with tillr-drain plan across all 6 obstacle holds.
	wantHolds := make(map[string]string, len(specs))
	for _, sp := range specs {
		wantHolds[sp.itemID] = sp.wantReason
	}
	assertAgreesWithPlan(t, storePath, "all five obstacle holds active", wantHolds, 0)

	checkRows := func(mode string, rows []waitingRow) {
		t.Helper()
		byItem := make(map[string]waitingRow, len(rows))
		for i := range rows {
			normalizeWaitingMover(&rows[i])
			byItem[rows[i].ItemID] = rows[i]
		}
		for _, sp := range specs {
			row, ok := byItem[sp.itemID]
			if !ok {
				t.Fatalf("%s: missing waiting row for %s in %+v", mode, sp.itemID, rows)
			}
			if row.Class != sp.wantClass || !strings.HasPrefix(row.Where, sp.wantWhere) {
				t.Fatalf("%s %s: class=%q where=%q, want %q/%q", mode, sp.itemID, row.Class, row.Where, sp.wantClass, sp.wantWhere)
			}
			for _, bad := range []string{"# run report", "no document exists", "triage"} {
				if strings.Contains(row.Why, bad) || strings.Contains(row.Mover, bad) {
					t.Fatalf("%s %s leaked %q: why=%q mover=%q", mode, sp.itemID, bad, row.Why, row.Mover)
				}
			}
			for _, sub := range sp.wantWhySub {
				if !strings.Contains(row.Why, sub) {
					t.Fatalf("%s %s: why=%q lacks %q", mode, sp.itemID, row.Why, sub)
				}
			}
			if !strings.HasPrefix(row.Mover, sp.wantMover) {
				t.Fatalf("%s %s: mover=%q, want prefix %q", mode, sp.itemID, row.Mover, sp.wantMover)
			}
		}
		var out bytes.Buffer
		if err := printWaiting(&out, rows); err != nil {
			t.Fatal(err)
		}
		wantSummary := "summary: owner-waiting 1; next [drain=1, design seat=0, operator=3]"
		if !strings.Contains(out.String(), wantSummary) {
			t.Fatalf("%s summary mismatch:\ngot:\n%s\nwant substring: %s", mode, out.String(), wantSummary)
		}
	}

	// 2. Live `tillr view waiting` via `tillr-drain why`.
	rootDir := t.TempDir()
	_, liveRows, err := askPlanner(ctx, drainBin, buildStamp{}, storePath, rootDir, "", "", []string{"/bin/true"}, nil)
	if err != nil {
		t.Fatalf("askPlanner failed: %v", err)
	}
	checkRows("live", liveRows)

	// 3. Stored `tillr view waiting --history` via readWaiting.
	histRows, err := readWaiting(ctx, storePath, &vantage.VantageQuestions{Root: rootDir, Command: []string{"/bin/true"}, Context: ctx})
	if err != nil {
		t.Fatalf("readWaiting failed: %v", err)
	}
	checkRows("history", histRows)

	// 4. ProjectItemState shows owner's QUESTION on the blocked item and ready on non-owner holds.
	rs, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		domain, err := readStoreDomain(r)
		if err != nil {
			return err
		}
		for _, it := range domain.items {
			proj := ProjectItemState(it, nil, nil, nil, "")
			if it.ItemID == "item-request-owner" {
				if proj.State != "blocked" || proj.Underlying != "owner: Should unknown flags exit 2 or 64?" || len(proj.WaitingOn) != 1 || proj.WaitingOn[0] != "owner" {
					t.Fatalf("owner item projection = %+v", proj)
				}
			} else if proj.State != "in_queue" || len(proj.WaitingOn) != 0 {
				t.Fatalf("non-owner item %s projection = %+v, want in_queue with no waiter", it.ItemID, proj)
			}
		}
		return nil
	}); err != nil {
		rs.Close()
		t.Fatal(err)
	}
	rs.Close()

	// 5. Clearing owner blocked state releases the hold in plan and view.
	execSQL(`UPDATE items SET state='in-queue', waiting_on='', underlying_state='' WHERE item_id='item-request-owner'`)
	execSQL(`INSERT INTO events (ts, kind, object_type, object_id, payload)
		VALUES (2000, 'item.update', 'item', 'item-request-owner', '{"state":"in-queue","allowed_paths":["tools/item-owner/"]}')`)
	delete(wantHolds, "item-request-owner")
	assertAgreesWithPlan(t, storePath, "after owner unblock", wantHolds, 1)
}
