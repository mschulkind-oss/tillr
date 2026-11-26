package drain

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func insertOpenTestFixItem(t *testing.T, s *store.Store, id, title, state, words string) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO items(item_id, title, state, rank, updated_at) VALUES(?, ?, ?, 'a', 1000)`,
			id, title, state,
		); err != nil {
			return err
		}
		if words != "" {
			if _, err := tx.Exec(
				`INSERT INTO request_words(item_id, words_hash, words, input_hash) VALUES(?, ?, ?, ?)`,
				id, "hash-"+id, []byte(words), "input-"+id,
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func readLinkedFailures(t *testing.T, s *store.Store) []struct {
	ObjectType string
	ObjectID   string
	Payload    trunkFailureLinked
} {
	t.Helper()
	var out []struct {
		ObjectType string
		ObjectID   string
		Payload    trunkFailureLinked
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT COALESCE(object_type, ''), COALESCE(object_id, ''), payload FROM events WHERE kind = ? ORDER BY seq`, TrunkFailureLinkedKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var objType, objID, raw string
			if err := rows.Scan(&objType, &objID, &raw); err != nil {
				return err
			}
			var p trunkFailureLinked
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				return err
			}
			out = append(out, struct {
				ObjectType string
				ObjectID   string
				Payload    trunkFailureLinked
			}{ObjectType: objType, ObjectID: objID, Payload: p})
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func readFixerItems(t *testing.T, s *store.Store) []string {
	t.Helper()
	var ids []string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id FROM items WHERE item_id LIKE 'fixer-main-%' ORDER BY item_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestHeavyProofLinksMatchingOpenItemInsteadOfFilingFixer(t *testing.T) {
	const goCheckID = goTestPrefix + "tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestOneAppliedPassSharesItsGenerationAcrossRequests"

	t.Run("matching open item by Go test name in request words files no fixer and records link", func(t *testing.T) {
		ctx := context.Background()
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		tip := heavyCommit(t, root, "tip")
		s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			return recordFixtureLanding(tx, "fixture", "fixture-run", tip, 0)
		}); err != nil {
			t.Fatal(err)
		}
		insertOpenTestFixItem(t, s,
			"item-request-2eb7c49739",
			"Make the one-pass-one-generation drain test pass under load, at its cause",
			StateInQueue,
			"TestOneAppliedPassSharesItsGenerationAcrossRequests fails under load in trigger_test.go; fix the race at its cause.\n",
		)
		// Failing at both base and tip means no single red commit can be established.
		steps := heavyTestSteps{judge: func(_ context.Context, _ string, only string) ([]HeavyCheck, error) {
			return []HeavyCheck{{CheckID: goCheckID, Result: "fail", Detail: "one pass allocated multiple generations"}}, nil
		}}
		log := &strings.Builder{}
		l := &Loop{
			gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
			cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: log},
		}
		if err := l.startHeavy(ctx, tip); err != nil {
			t.Fatal(err)
		}
		l.heavyWG.Wait()

		if fixers := readFixerItems(t, s); len(fixers) != 0 {
			t.Fatalf("fixers=%v, want none", fixers)
		}
		links := readLinkedFailures(t, s)
		if len(links) != 1 {
			t.Fatalf("links=%+v, want 1", links)
		}
		got := links[0]
		if got.ObjectType != "item" || got.ObjectID != "item-request-2eb7c49739" ||
			got.Payload.ItemID != "item-request-2eb7c49739" ||
			got.Payload.RedSHA != tip ||
			got.Payload.CheckID != goCheckID ||
			got.Payload.MatchedName != "TestOneAppliedPassSharesItsGenerationAcrossRequests" {
			t.Fatalf("unexpected link record: %+v", got)
		}
		if !strings.Contains(log.String(), "linked to open item item-request-2eb7c49739") {
			t.Fatalf("expected link report in log, got %q", log.String())
		}
	})

	t.Run("matching open item by check id in title files no fixer and records link", func(t *testing.T) {
		ctx := context.Background()
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		tip := heavyCommit(t, root, "tip")
		s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			return recordFixtureLanding(tx, "fixture", "fixture-run", tip, 0)
		}); err != nil {
			t.Fatal(err)
		}
		insertOpenTestFixItem(t, s, "item-request-lint", "Fix lint-check on main", StateBlocked, "Repair lint-check.\n")
		steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
			return []HeavyCheck{{CheckID: "lint-check", Result: "fail", Detail: "lint failed"}}, nil
		}}
		l := &Loop{
			gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
			cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard},
		}
		if err := l.startHeavy(ctx, tip); err != nil {
			t.Fatal(err)
		}
		l.heavyWG.Wait()

		if fixers := readFixerItems(t, s); len(fixers) != 0 {
			t.Fatalf("fixers=%v, want none", fixers)
		}
		links := readLinkedFailures(t, s)
		if len(links) != 1 || links[0].ObjectID != "item-request-lint" || links[0].Payload.MatchedName != "lint-check" {
			t.Fatalf("links=%+v, want 1 link to item-request-lint", links)
		}
	})

	t.Run("no matching open item still files fixer", func(t *testing.T) {
		ctx := context.Background()
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		tip := heavyCommit(t, root, "tip")
		s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			return recordFixtureLanding(tx, "fixture", "fixture-run", tip, 0)
		}); err != nil {
			t.Fatal(err)
		}
		// A closed item that fixes the test must not suppress a new fixer.
		insertOpenTestFixItem(t, s, "item-request-closed", "Fix TestOneAppliedPassSharesItsGenerationAcrossRequests", "closed", "")
		// An open item that only mentions the test incidentally in an incident report must not match.
		insertOpenTestFixItem(t, s, "item-request-incidental", "Document drain alerts", StateInQueue,
			"What happened: the proof failed TestOneAppliedPassSharesItsGenerationAcrossRequests.\n")

		steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
			return []HeavyCheck{{CheckID: goCheckID, Result: "fail", Detail: "one pass allocated multiple generations"}}, nil
		}}
		l := &Loop{
			gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
			cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard},
		}
		if err := l.startHeavy(ctx, tip); err != nil {
			t.Fatal(err)
		}
		l.heavyWG.Wait()

		fixers := readFixerItems(t, s)
		if len(fixers) != 1 || fixers[0] != TrunkFixerID(tip) {
			t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(tip))
		}
		if links := readLinkedFailures(t, s); len(links) != 0 {
			t.Fatalf("links=%+v, want none", links)
		}
	})
}

func TestHeavyProofLinksMatchingCheckAndFilesFixerForUnmatchedCheck(t *testing.T) {
	const goCheckID = goTestPrefix + "tools/tillr-drain:github.com/mschulkind-oss/tillr-drain/drain:TestOneAppliedPassSharesItsGenerationAcrossRequests"

	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "tip")
	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "fixture", "fixture-run", tip, 0)
	}); err != nil {
		t.Fatal(err)
	}
	insertOpenTestFixItem(t, s,
		"item-request-2eb7c49739",
		"Make the one-pass-one-generation drain test pass under load, at its cause",
		StateInQueue,
		"TestOneAppliedPassSharesItsGenerationAcrossRequests fails under load.\n",
	)

	steps := heavyTestSteps{judge: func(_ context.Context, _ string, only string) ([]HeavyCheck, error) {
		switch only {
		case goCheckID:
			return []HeavyCheck{{CheckID: goCheckID, Result: "fail", Detail: "flake"}}, nil
		case "unit-other":
			return []HeavyCheck{{CheckID: "unit-other", Result: "fail", Detail: "broken"}}, nil
		default:
			return []HeavyCheck{
				{CheckID: goCheckID, Result: "fail", Detail: "flake"},
				{CheckID: "unit-other", Result: "fail", Detail: "broken"},
			}, nil
		}
	}}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard},
	}
	if err := l.startHeavy(ctx, tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	links := readLinkedFailures(t, s)
	if len(links) != 1 || links[0].ObjectID != "item-request-2eb7c49739" || links[0].Payload.CheckID != goCheckID {
		t.Fatalf("links=%+v, want 1 link for %s", links, goCheckID)
	}
	fixers := readFixerItems(t, s)
	if len(fixers) != 1 || fixers[0] != TrunkFixerID(tip) {
		t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(tip))
	}
	var criteria string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT text FROM item_criteria WHERE item_id = ? AND criterion_id = 'trunk-green'`, TrunkFixerID(tip)).Scan(&criteria)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(criteria, "unit-other") || strings.Contains(criteria, "TestOneAppliedPassSharesItsGenerationAcrossRequests") {
		t.Fatalf("fixer criteria=%q, want only unit-other", criteria)
	}
}

func TestHeavyProofSingleRedCommitStillFilesFixer(t *testing.T) {
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "bad")
	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "fixture", "fixture-run", red, 0)
	}); err != nil {
		t.Fatal(err)
	}
	insertOpenTestFixItem(t, s, "item-request-unit", "Fix unit on main", StateInQueue, "Fix unit.\n")

	steps := heavyTestSteps{judge: func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
		value, err := os.ReadFile(filepath.Join(dir, "value"))
		result := "pass"
		if string(value) != "good" {
			result = "fail"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}}}, err
	}}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard},
	}
	if err := l.startHeavy(ctx, red); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	fixers := readFixerItems(t, s)
	if len(fixers) != 1 || fixers[0] != TrunkFixerID(red) {
		t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(red))
	}
	if links := readLinkedFailures(t, s); len(links) != 0 {
		t.Fatalf("links=%+v, want none", links)
	}
}

func TestHeavyProofCoalescesTwoChecksWithSameFailingTestAndFailureLine(t *testing.T) {
	const (
		testName    = "TestTreeDeclarationReadingGoModuleReadsInRepoDependencies"
		failureLine = "tree_checks_test.go:456: go-test-tillr-cli.json: missing Go dependencies in reads"
	)
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	first := heavyCommit(t, root, "first")
	second := heavyCommit(t, root, "second")

	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := recordFixtureLanding(tx, "item-1", "run-1", first, 0); err != nil {
			return err
		}
		return recordFixtureLanding(tx, "item-2", "run-2", second, 0)
	}); err != nil {
		t.Fatal(err)
	}

	logDir := scratchUnderDurable(t)
	logGate := filepath.Join(logDir, "go-test-gate.log")
	logTree := filepath.Join(logDir, "tree-checks-test.log")
	body := "--- FAIL: " + testName + " (0.01s)\n    " + failureLine + "\nFAIL\n"
	if err := os.WriteFile(logGate, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logTree, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// go-test-gate turns red at first; tree-checks-test turns red at second.
	steps := heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		value, err := os.ReadFile(filepath.Join(dir, "value"))
		if err != nil {
			return nil, err
		}
		v := string(value)
		gateRes, treeRes := "pass", "pass"
		if v == "first" || v == "second" {
			gateRes = "fail"
		}
		if v == "second" {
			treeRes = "fail"
		}
		switch only {
		case "go-test-gate":
			return []HeavyCheck{{CheckID: "go-test-gate", Result: gateRes, Reads: []string{"value"}, LogPath: logGate}}, nil
		case "tree-checks-test":
			return []HeavyCheck{{CheckID: "tree-checks-test", Result: treeRes, Reads: []string{"value"}, LogPath: logTree}}, nil
		default:
			return []HeavyCheck{
				{CheckID: "go-test-gate", Result: gateRes, Reads: []string{"value"}, LogPath: logGate},
				{CheckID: "tree-checks-test", Result: treeRes, Reads: []string{"value"}, LogPath: logTree},
			}, nil
		}
	}}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard},
	}
	if err := l.startHeavy(ctx, second); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	fixers := readFixerItems(t, s)
	if len(fixers) != 1 || fixers[0] != TrunkFixerID(first) {
		t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(first))
	}
	var criteria string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT text FROM item_criteria WHERE item_id = ? AND criterion_id = 'trunk-green'`, fixers[0]).Scan(&criteria)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(criteria, "go-test-gate") || !strings.Contains(criteria, "tree-checks-test") {
		t.Fatalf("fixer criteria=%q, want both go-test-gate and tree-checks-test", criteria)
	}
}

func TestHeavyProofAttributedChecksLinkWhenFirstOpenItemFixesFailingTest(t *testing.T) {
	const (
		testName    = "TestTreeDeclarationReadingGoModuleReadsInRepoDependencies"
		failureLine = "tree_checks_test.go:456: go-test-tillr-cli.json: missing Go dependencies in reads"
	)
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	first := heavyCommit(t, root, "first")
	second := heavyCommit(t, root, "second")

	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := recordFixtureLanding(tx, "item-1", "run-1", first, 0); err != nil {
			return err
		}
		return recordFixtureLanding(tx, "item-2", "run-2", second, 0)
	}); err != nil {
		t.Fatal(err)
	}
	insertOpenTestFixItem(t, s,
		"item-request-tree-deps",
		"Fix "+testName,
		StateInProgress,
		"Fix "+testName+" failing with missing Go dependencies in reads.\n",
	)

	logDir := scratchUnderDurable(t)
	logGate := filepath.Join(logDir, "go-test-gate.log")
	logTree := filepath.Join(logDir, "tree-checks-test.log")
	body := "--- FAIL: " + testName + " (0.01s)\n    " + failureLine + "\nFAIL\n"
	if err := os.WriteFile(logGate, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logTree, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	steps := heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		value, err := os.ReadFile(filepath.Join(dir, "value"))
		if err != nil {
			return nil, err
		}
		v := string(value)
		gateRes, treeRes := "pass", "pass"
		if v == "first" || v == "second" {
			gateRes = "fail"
		}
		if v == "second" {
			treeRes = "fail"
		}
		switch only {
		case "go-test-gate":
			return []HeavyCheck{{CheckID: "go-test-gate", Result: gateRes, Reads: []string{"value"}, LogPath: logGate}}, nil
		case "tree-checks-test":
			return []HeavyCheck{{CheckID: "tree-checks-test", Result: treeRes, Reads: []string{"value"}, LogPath: logTree}}, nil
		default:
			return []HeavyCheck{
				{CheckID: "go-test-gate", Result: gateRes, Reads: []string{"value"}, LogPath: logGate},
				{CheckID: "tree-checks-test", Result: treeRes, Reads: []string{"value"}, LogPath: logTree},
			}, nil
		}
	}}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard},
	}
	if err := l.startHeavy(ctx, second); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	if fixers := readFixerItems(t, s); len(fixers) != 0 {
		t.Fatalf("fixers=%v, want none", fixers)
	}
	links := readLinkedFailures(t, s)
	if len(links) != 2 {
		t.Fatalf("links=%+v, want 2 links (go-test-gate and tree-checks-test)", links)
	}
	for _, link := range links {
		if link.ObjectID != "item-request-tree-deps" || link.Payload.MatchedName != testName {
			t.Fatalf("unexpected link=%+v", link)
		}
	}
}

func TestHeavyProofAttributedChecksFileFixerWhenMatchingItemNotFirstInQueue(t *testing.T) {
	const (
		testName    = "TestTreeDeclarationReadingGoModuleReadsInRepoDependencies"
		failureLine = "tree_checks_test.go:456: go-test-tillr-cli.json: missing Go dependencies in reads"
	)
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "red")

	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "item-1", "run-1", red, 0)
	}); err != nil {
		t.Fatal(err)
	}
	// Put an unrelated item first in queue (rank 'a') and the matching fix item second (rank 'b').
	insertOpenTestFixItem(t, s, "item-request-first", "Unrelated work", StateInQueue, "Unrelated.\n")
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id, title, state, rank, updated_at) VALUES(?, ?, ?, 'b', 1000)`,
			"item-request-tree-deps", "Fix "+testName, StateInQueue); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	logGate := filepath.Join(scratchUnderDurable(t), "go-test-gate.log")
	if err := os.WriteFile(logGate, []byte("--- FAIL: "+testName+" (0.01s)\n    "+failureLine+"\nFAIL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := heavyTestSteps{judge: func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
		value, err := os.ReadFile(filepath.Join(dir, "value"))
		res := "pass"
		if string(value) != "good" {
			res = "fail"
		}
		return []HeavyCheck{{CheckID: "go-test-gate", Result: res, Reads: []string{"value"}, LogPath: logGate}}, err
	}}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard},
	}
	if err := l.startHeavy(ctx, red); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	if fixers := readFixerItems(t, s); len(fixers) != 1 || fixers[0] != TrunkFixerID(red) {
		t.Fatalf("fixers=%v, want [%s]", fixers, TrunkFixerID(red))
	}
	if links := readLinkedFailures(t, s); len(links) != 0 {
		t.Fatalf("links=%+v, want none", links)
	}
}
