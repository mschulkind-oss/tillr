package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const splitExample = `{
  "parts": [
    {"label":"second", "title":"Second part", "share":"Deliver B", "leaves":{"first":"Deliver A"}, "criteria":["B works"], "follows":["first"], "required":true},
    {"label":"first", "title":"First part", "share":"Deliver A", "leaves":{"second":"Deliver B"}, "criteria":["A works"], "required":true},
    {"label":"extra", "title":"Optional part", "share":"Give an example", "required":false}
  ],
  "coverage": {"c5":[{"clause":"A works；", "part":"first"},{"clause":"B works.", "part":"second"}], "c6":[{"closing_check":true}]}
}`

const splitParentWords = "  Owner's words\n\nKeep this whitespace.\n"

func splitFixture(t *testing.T) (refusalLoopFixture, *Loop, string) {
	t.Helper()
	return splitFixtureWithCriterion(t, "A works； B works.")
}

func splitFixtureWithCriterion(t *testing.T, criterion string) (refusalLoopFixture, *Loop, string) {
	t.Helper()
	f, l := passFixture(t, "queue-before", "queue-after")
	workflow := filepath.Join(f.root, "config/tillr/workflows/planned-build@v1.json")
	if err := os.MkdirAll(filepath.Dir(workflow), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflow, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	var parent string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := item.Place(context.Background(), tx, "queue-before", item.Destination{To: "first"}, l.cfg.Now(), "operator", "test"); err != nil {
			return err
		}
		env, err := item.FileRequest(context.Background(), tx, item.RequestFiling{
			Title: "Parent request", Words: []byte(splitParentWords), Criteria: []string{criterion, "Together they work"},
			Ordinary: true, At: l.cfg.Now(), Destination: item.Destination{Before: "queue-after"},
		})
		if err != nil {
			return err
		}
		parent = env.ID
		_, err = item.SetDeps(context.Background(), tx, "queue-after", []string{parent}, nil, l.cfg.Now())
		return err
	})
	return f, l, parent
}

func TestFileSplitFilesPartsAndKeepsParentHeld(t *testing.T) {
	f, l, parent := splitFixture(t)
	ctx := context.Background()
	file := func() (map[string]string, error) {
		return FileSplit(ctx, f.root, f.storePath, parent, strings.NewReader(splitExample), l.cfg.Now())
	}
	parts, err := file()
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("filed parts: %v", parts)
	}
	head := eventCount(t, f.storePath)
	if replay, err := file(); err != nil || !reflect.DeepEqual(parts, replay) || eventCount(t, f.storePath) != head {
		t.Fatalf("replay: %v, %v (want %v without writes)", replay, err, parts)
	}
	if _, err := FileSplit(ctx, f.root, f.storePath, parent, strings.NewReader(strings.Replace(splitExample, "Deliver B", "Different work", 1)), l.cfg.Now()); err == nil {
		t.Fatal("replaced a filed statement")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		r := tx.Reader()
		edges, err := readEdges(r)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(edges[parts["second"]], []itemEdge{{Target: parts["first"], Kind: item.EdgeKindBlockedBy}}) || len(edges[parts["first"]]) != 0 || len(edges[parts["extra"]]) != 0 || len(edges[parent]) != 2 || !reflect.DeepEqual(edges["queue-after"], []itemEdge{{Target: parent, Kind: item.EdgeKindBlockedBy}}) {
			t.Fatalf("edges: %v", edges)
		}
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		var order []string
		for _, row := range rows {
			switch row.ItemID {
			case parts["first"], parts["second"], parts["extra"]:
				if row.Express || row.Seat != "build" || row.State != "in-queue" {
					t.Errorf("part: %+v", row)
				}
			case parent:
				if row.State != "blocked" {
					t.Errorf("parent state: %s", row.State)
				}
			}
			order = append(order, row.ItemID)
		}
		want := []string{"queue-before", parts["first"], parts["second"], parts["extra"], parent, "queue-after"}
		if !reflect.DeepEqual(order, want) {
			t.Errorf("queue: %v, want %v", order, want)
		}
		words, err := item.RequestWords(r, parts["second"])
		if err != nil || !strings.HasPrefix(string(words), splitParentWords) || !strings.Contains(string(words), "Deliver B") || !strings.Contains(string(words), "first: Deliver A") {
			t.Errorf("words: %q (%v)", words, err)
		}
		var criteria, unchanged int
		if err := r.QueryRow(`SELECT count(*) FROM item_criteria WHERE item_id=?`, parts["second"]).Scan(&criteria); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM item_criteria WHERE item_id=? AND (criterion_id, text) IN (('c5','A works； B works.'),('c6','Together they work'))`, parent).Scan(&unchanged); err != nil {
			return err
		}
		if criteria != 5 || unchanged != 2 {
			t.Errorf("part has %d criteria; %d parent criteria unchanged", criteria, unchanged)
		}
		return nil
	})
	checkWhy := func(operator bool) {
		t.Helper()
		s, err := store.OpenRead(ctx, f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			a, err := WhyForItem(r, Pass{Now: l.cfg.Now().Format(time.RFC3339)}, nil, nil, parent)
			if err != nil {
				return err
			}
			for _, row := range a.Rows {
				if row.ItemID != parent {
					continue
				}
				if row.State != StateBlocked || row.Decision.Action != ActionHold || (row.Mover == "operator; check the parent's criteria by hand") != operator {
					t.Fatalf("parent why (operator=%v): %+v", operator, row)
				}
				return nil
			}
			return errors.New("missing parent why row")
		}); err != nil {
			t.Fatal(err)
		}
	}
	checkWhy(false)
	for _, label := range []string{"first", "second"} {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			// Simulate the state written by the ordinary lander after settlement.
			_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, parts[label])
			return err
		})
		checkWhy(label == "second")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, move := range []struct {
			state  string
			waiter []item.WaitingOn
		}{
			{state: "in-queue"},
			{state: "blocked", waiter: []item.WaitingOn{{Waiter: "item:" + parts["first"], Reason: "replace the wait"}}},
		} {
			_, err := item.SetState(ctx, tx, parent, move.state, l.cfg.Now(), move.waiter...)
			if e, ok := item.RejectionOf(err); !ok || e.Rule != "split-parent-held" {
				t.Fatalf("split parent could change its hold: %v", err)
			}
		}
		env, err := item.ReadStored(tx.Reader(), parent, f.root)
		if err != nil {
			return err
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return err
		}
		payload["state"] = json.RawMessage(`"in-queue"`)
		env.Payload, err = json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = item.Apply(ctx, tx, env)
		if e, ok := item.RejectionOf(err); !ok || e.Rule != "split-parent-held" {
			t.Fatalf("record could put split parent back into the build queue: %v", err)
		}
		return nil
	})
}

func TestFileSplitAcceptsUnspacedSentencesAndPreservesUnicode(t *testing.T) {
	f, l, parent := splitFixtureWithCriterion(t, "保存する。表示する。")
	body := strings.NewReplacer("A works；", "保存する。", "B works.", "表示する。",
		"Give an example", `Example \uD83D\uDE00 � \\uD800`).Replace(splitExample)
	parts, err := FileSplit(context.Background(), f.root, f.storePath, parent, strings.NewReader(body), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		words, err := item.RequestWords(tx.Reader(), parts["extra"])
		if err == nil && !strings.Contains(string(words), `Example 😀 � \uD800`) {
			t.Errorf("share did not preserve Unicode and the literal escape: %q", words)
		}
		return err
	})
}

func TestFileSplitUnspacedSemicolon(t *testing.T) {
	for _, sep := range []string{";", "；"} {
		t.Run(sep, func(t *testing.T) {
			f, l, parent := splitFixtureWithCriterion(t, "A works"+sep+"B works.")
			body := strings.Replace(splitExample, "A works；", "A works"+sep, 1)
			parts, err := FileSplit(context.Background(), f.root, f.storePath, parent, strings.NewReader(body), l.cfg.Now())
			if err != nil || len(parts) != 3 {
				t.Fatalf("split at an exact clause boundary: %v, %v", parts, err)
			}
		})
	}
}

func TestFileSplitRefusesDecimalFragments(t *testing.T) {
	f, l, parent := splitFixtureWithCriterion(t, "The value is 3.14.")
	body := strings.NewReplacer("A works；", "The value is 3.", "B works.", "14.").Replace(splitExample)
	before := eventCount(t, f.storePath)
	parts, err := FileSplit(context.Background(), f.root, f.storePath, parent, strings.NewReader(body), l.cfg.Now())
	if parts != nil || err == nil || !strings.Contains(err.Error(), "coverage for c5") || eventCount(t, f.storePath) != before {
		t.Fatalf("split inside a decimal: %v, %v", parts, err)
	}
}

func TestFileSplitKeepsOwnerApprovalHold(t *testing.T) {
	f, l, parent := splitFixture(t)
	ctx := context.Background()
	wait := item.WaitingOn{Waiter: "owner", Reason: "Do not start any implementation until the owner authorizes it", Source: "state"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, parent, StateBlocked, l.cfg.Now(), wait)
		return err
	})
	before := eventCount(t, f.storePath)
	file := func() (map[string]string, error) {
		return FileSplit(ctx, f.root, f.storePath, parent, strings.NewReader(splitExample), l.cfg.Now())
	}
	if ids, err := file(); ids != nil || err == nil || !strings.Contains(err.Error(), wait.Reason) || !strings.Contains(err.Error(), "authorize the work or keep it held") {
		t.Fatalf("expected an owner decision with the original reason, got %v, %v", ids, err)
	}
	if eventCount(t, f.storePath) != before {
		t.Fatal("refused split appended events")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		got, underlying, err := item.ReadWaitingOn(tx.Reader(), parent)
		if err != nil || got == nil || *got != wait || underlying != StateInQueue {
			t.Fatalf("refused split changed the wait: %v, %s, %v", got, underlying, err)
		}
		var items, splits int
		if err := tx.QueryRow(`SELECT (SELECT count(*) FROM items), (SELECT count(*) FROM item_splits)`).Scan(&items, &splits); err != nil {
			return err
		}
		if items != 3 || splits != 0 {
			t.Fatalf("refused split filed work: %d items, %d split receipts", items, splits)
		}
		// Simulate the owner clearing the approval hold, then retry the same statement.
		_, err = item.SetState(ctx, tx, parent, StateInQueue, l.cfg.Now())
		return err
	})
	if ids, err := file(); err != nil || len(ids) != 3 {
		t.Fatalf("split after approval: %v, %v", ids, err)
	}
}

func TestFileSplitRefusesOutstandingParentHolds(t *testing.T) {
	for _, name := range []string{"named item wait", "plan wait", "owner-only criterion", "published owner question"} {
		t.Run(name, func(t *testing.T) {
			f, l, parent := splitFixture(t)
			ctx := context.Background()
			var reasons []string
			fixtureWrite(t, f, func(tx *store.Tx) error {
				switch name {
				case "owner-only criterion":
					question := "May implementation proceed without a migration?"
					reasons = []string{"c5", question, "record the ruling or keep the work held"}
					_, err := tx.Exec(`UPDATE item_criteria SET text=?, claim_class='owner-only' WHERE item_id=? AND criterion_id='c5'`, question, parent)
					return err
				case "published owner question":
					reasons = []string{"docs/decision.md#question", "record the ruling or keep the work held"}
					return item.RecordConflict(tx, item.Conflict{ConflictID: "split-question", ItemID: parent, RunID: "prior-run",
						Text: "May implementation proceed?", WaitingOn: "owner", Status: "question",
						QuestionRef: reasons[0], QuestionHead: "published-head", QuestionRunID: "question-run"}, l.cfg.Now())
				default:
					waiter, reason := "item:queue-before", "Wait for the prerequisite to land before starting any work"
					if name == "plan wait" {
						waiter, reason = "plan", "Wait for the plan to settle before starting work"
					}
					reasons = []string{waiter, reason}
					_, err := item.SetState(ctx, tx, parent, StateBlocked, l.cfg.Now(), item.WaitingOn{Waiter: waiter, Reason: reason})
					return err
				}
			})
			body := splitExample
			if name == "owner-only criterion" {
				// Valid whole-criterion coverage isolates the existing hold.
				body = strings.Replace(body, `{"clause":"A works；", "part":"first"},{"clause":"B works.", "part":"second"}`, `{"part":"first"}`, 1)
			}
			before := eventCount(t, f.storePath)
			ids, err := FileSplit(ctx, f.root, f.storePath, parent, strings.NewReader(body), l.cfg.Now())
			after := eventCount(t, f.storePath)
			if ids != nil || err == nil || before != after {
				t.Fatalf("filing must not bypass the hold: %v, %v (events %d -> %d)", ids, err, before, after)
			}
			for _, reason := range reasons {
				if !strings.Contains(err.Error(), reason) {
					t.Errorf("refusal should include %q: %v", reason, err)
				}
			}
		})
	}
}

func TestSplitParentRequiresLandingForManualCheck(t *testing.T) {
	f, l, parent := splitFixture(t)
	ctx := context.Background()
	parts, err := FileSplit(ctx, f.root, f.storePath, parent, strings.NewReader(splitExample), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, parts["first"]); err != nil {
			return err
		}
		_, err := item.SetStateWithReceipt(ctx, tx, parts["second"], StateResolvedElsewhere, l.cfg.Now(), item.MoveReceipt{
			By: "operator", Reason: "covered by the first part", ResolvesVia: "item:" + parts["first"],
		})
		return err
	})
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		a, err := WhyForItem(r, Pass{Now: l.cfg.Now().Format(time.RFC3339)}, nil, nil, parent)
		if err != nil {
			return err
		}
		for _, row := range a.Rows {
			if row.ItemID == parent {
				if row.State != StateBlocked || row.Decision.Action != ActionHold ||
					!reflect.DeepEqual(row.WaitingOn, []string{parts["second"]}) ||
					strings.Contains(row.Why+row.Mover, "check the parent's criteria by hand") {
					t.Fatalf("resolved elsewhere is not a required part's landing: %+v", row)
				}
				return nil
			}
		}
		return errors.New("missing parent why row")
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileSplitRefusesWholeStatement(t *testing.T) {
	for _, tc := range []struct{ name, from, to, reason string }{
		{"unknown field", `"required":false`, `"required":false,"donor":"whole"`, "unknown field"},
		{"duplicate label", `"label":"extra"`, `"label":"first"`, "duplicate"},
		{"missing required choice", `, "required":false`, ``, "required"},
		{"one part", splitExample, `{"parts":[{"label":"alone","title":"One","share":"Everything","required":true}]}`, "at least two"},
		{"duplicate field", `"required":false`, `"required":false,"required":true`, "duplicate key"},
		{"case alias field", `"required":false`, `"required":false,"Required":true`, "unknown field"},
		{"wrong field case", `"part":"first"`, `"Part":"first"`, "unknown field"},
		{"unknown sibling", `"second":"Deliver B"`, `"missing":"Deliver B"`, "sibling"},
		{"unknown predecessor", `"follows":["first"]`, `"follows":["missing"]`, "follows"},
		{"cycle", `"required":false`, `"required":false,"follows":["second"]`, "cycle"},
		{"unknown coverage part", `"part":"first"`, `"part":"missing"`, "coverage"},
		{"unowned clause", `,{"clause":"B works.", "part":"second"}`, ``, "coverage"},
		{"midword fragments", `"A works；", "part":"first"},{"clause":"B works."`, `"A wor", "part":"first"},{"clause":"ks； B works."`, "coverage"},
		{"midclause fragments", `"A works；", "part":"first"},{"clause":"B works."`, `"A", "part":"first"},{"clause":"works； B works."`, "coverage"},
		{"invalid UTF-8", "Give an example", "malformed \xff", "UTF-8"},
		{"unpaired high surrogate", "Give an example", `Malformed \uD800 without low surrogate`, "unpaired surrogate"},
		{"unpaired low surrogate", "Give an example", `Malformed \udc00 without high surrogate`, "unpaired surrogate"},
		{"high surrogate followed by BMP", "Give an example", `Malformed \udbff\uFFFD`, "unpaired surrogate"},
		{"missing criterion", `, "c6":[{"closing_check":true}]`, ``, "c6"},
		{"all optional parts", splitExample, strings.NewReplacer(`"required":true`, `"required":false`, `{"clause":"A works；", "part":"first"},{"clause":"B works.", "part":"second"}`, `{"closing_check":true}`).Replace(splitExample), "at least one required part"},
		{"optional part owns clause", `"part":"second"`, `"part":"extra"`, "optional part"},
		{"optional part owns criterion", `{"clause":"A works；", "part":"first"},{"clause":"B works.", "part":"second"}`, `{"part":"extra"}`, "optional part"},
		{"late write failure", ``, ``, "injected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, parent := splitFixture(t)
			body := strings.Replace(splitExample, tc.from, tc.to, 1)
			if tc.name == "cycle" {
				body = strings.Replace(body, `"criteria":["A works"]`, `"criteria":["A works"],"follows":["extra"]`, 1)
			}
			if tc.name == "invalid UTF-8" {
				// Isolate decoding from clause-boundary validation.
				body = strings.Replace(body, `{"clause":"A works；", "part":"first"},{"clause":"B works.", "part":"second"}`, `{"part":"first"}`, 1)
			}
			if tc.name == "late write failure" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`CREATE TRIGGER reject_split_block BEFORE UPDATE OF state ON items WHEN NEW.state='blocked' BEGIN SELECT RAISE(ABORT,'injected'); END`)
					return err
				})
			}
			before := eventCount(t, f.storePath)
			if ids, err := FileSplit(context.Background(), f.root, f.storePath, parent, strings.NewReader(body), l.cfg.Now()); ids != nil || err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("wanted %q refusal with no IDs, got %v, %v", tc.reason, ids, err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var items, words, edges, splits int
				for table, out := range map[string]*int{"items": &items, "request_words": &words, "item_edges": &edges, "item_splits": &splits} {
					if err := tx.QueryRow(`SELECT count(*) FROM ` + table).Scan(out); err != nil {
						return err
					}
				}
				if items != 3 || words != 1 || edges != 1 || splits != 0 {
					return errors.New("refusal changed items, words, edges or statements")
				}
				var state string
				if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, parent).Scan(&state); err != nil {
					return err
				}
				if state != StateInQueue {
					t.Errorf("refused split moved parent to %s", state)
				}
				return nil
			})
			if eventCount(t, f.storePath) != before {
				t.Fatal("refused split appended events")
			}
		})
	}
}

func TestWhyReportsSplitParentAndParts(t *testing.T) {
	f, l, parent := splitFixture(t)
	ctx := context.Background()
	parts, err := FileSplit(ctx, f.root, f.storePath, parent, strings.NewReader(splitExample), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Mark "first" landed, "extra" held on owner, and leave "second" queued.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, parts["first"]); err != nil {
			return err
		}
		_, err := item.SetState(ctx, tx, parts["extra"], StateBlocked, l.cfg.Now(), item.WaitingOn{Waiter: "owner", Reason: "needs clarification", Source: "state"})
		return err
	})

	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pol := &Policy{
		Backends: map[string]Backend{"pi": {Adapter: "fixture"}},
		Seats: map[string]string{
			"build": "pi", "plan": "pi", "review": "pi", "scout": "pi",
			"test-run": "pi", "verify": "pi", "design": "pi", "edit": "pi",
			"tricky": "pi", "test-write": "pi", "docs": "pi", "classify": "pi",
		},
		ReadOnlySeats: []string{"scout", "test-run", "verify", "review", "classify"},
	}
	noLanes := []string{}
	readWhy := func() map[string]WhyRow {
		t.Helper()
		byID := map[string]WhyRow{}
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			a, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), Root: f.root, AvailableLanes: &noLanes}, pol, nil)
			if err != nil {
				return err
			}
			for _, row := range a.Rows {
				byID[row.ItemID] = row
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return byID
	}

	byID := readWhy()
	// Non-split item has no SplitParent / SplitPart.
	if qb := byID["queue-before"]; qb.SplitParent != nil || qb.SplitPart != nil {
		t.Fatalf("non-split item got split fields: %+v", qb)
	}

	// Part row carries SplitPart with parent title and part label.
	sec := byID[parts["second"]]
	if sec.SplitPart == nil || sec.SplitPart.ParentID != parent || sec.SplitPart.ParentTitle != "Parent request" || sec.SplitPart.PartLabel != "second" || !sec.SplitPart.Required {
		t.Fatalf("second part SplitPart = %+v", sec.SplitPart)
	}
	ext := byID[parts["extra"]]
	if ext.SplitPart == nil || ext.SplitPart.Required {
		t.Fatalf("extra part SplitPart = %+v", ext.SplitPart)
	}

	// Parent row summarizes counts and surfaces held part reason & mover.
	prow := byID[parent]
	if prow.SplitParent == nil {
		t.Fatalf("parent missing SplitParent: %+v", prow)
	}
	if prow.SplitParent.TotalParts != 3 || prow.SplitParent.RequiredParts != 2 || prow.SplitParent.RequiredLanded != 1 || prow.SplitParent.Landed != 1 || prow.SplitParent.Held != 1 || prow.SplitParent.Queued != 1 {
		t.Fatalf("unexpected SplitParent counts: %+v", prow.SplitParent)
	}
	if prow.SplitParent.ProgressSummary != "1 of 2 required parts landed" {
		t.Fatalf("ProgressSummary = %q", prow.SplitParent.ProgressSummary)
	}
	if !strings.HasPrefix(prow.Why, "split into 3 parts: 1 landed, 1 held, 1 queued") {
		t.Fatalf("parent Why = %q", prow.Why)
	}
	if !strings.Contains(prow.Why, "held: extra (") || !strings.Contains(prow.Why, "mover:") {
		t.Fatalf("parent Why missing held part detail: %q", prow.Why)
	}
	if len(prow.SplitParent.HeldParts) != 1 || prow.SplitParent.HeldParts[0].Label != "extra" {
		t.Fatalf("HeldParts = %+v", prow.SplitParent.HeldParts)
	}

	// Closing a required part surfaces owner hold on the parent.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id=?`, parts["second"])
		return err
	})
	byID = readWhy()
	prow = byID[parent]
	if prow.Class != "needs you" || !strings.Contains(prow.Mover, "owner; required part did not land: "+parts["second"]) {
		t.Fatalf("closed required part parent: class=%q mover=%q why=%q", prow.Class, prow.Mover, prow.Why)
	}
	if prow.SplitParent.Closed != 1 {
		t.Fatalf("expected Closed=1, got %+v", prow.SplitParent)
	}

	// Landing all required parts surfaces operator hand-check with split count prefix.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, parts["second"])
		return err
	})
	byID = readWhy()
	prow = byID[parent]
	if prow.Class != "needs you" || prow.Mover != "operator; check the parent's criteria by hand" {
		t.Fatalf("all-required-landed parent: class=%q mover=%q", prow.Class, prow.Mover)
	}
	if !strings.Contains(prow.Why, "split into 3 parts: 2 landed, 1 held; check the parent's criteria by hand") {
		t.Fatalf("all-required-landed Why = %q", prow.Why)
	}
	if prow.SplitParent.ProgressSummary != "2 of 2 required parts landed" {
		t.Fatalf("ProgressSummary = %q", prow.SplitParent.ProgressSummary)
	}
}
