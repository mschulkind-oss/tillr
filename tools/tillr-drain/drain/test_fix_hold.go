package drain

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// TestFixWaitPrefix prefixes the WaitingOn.Reason when a candidate's gate failed
// only on a test that an open item names as the test it fixes.
const TestFixWaitPrefix = "test-fix-open: "

var (
	unittestFailRe   = regexp.MustCompile(`^(?:FAIL|ERROR):\s+(\S+)\s+\([^)]+\)\s*$`)
	pytestFailRe     = regexp.MustCompile(`^FAILED\s+(\S+?)(?:::(\S+))?(?:\s+-\s+.*)?$`)
	goAffectedLineRe = regexp.MustCompile(`^  \S+:\s+(.+)$`)
	goBuildFailRe    = regexp.MustCompile(`^FAIL\t\S+\s+\[(?:build|setup) failed\]`)
)

func testFixHeldWait(wait item.WaitingOn) bool {
	dep, ok := strings.CutPrefix(wait.Waiter, "item:")
	return ok && dep != "" && strings.HasPrefix(wait.Reason, TestFixWaitPrefix)
}

func candidateHeldWait(wait item.WaitingOn) bool {
	return trunkAcquittedWait(wait) || testFixHeldWait(wait)
}

// holdForOpenTestFix holds a candidate whose gate failed only on a single test
// that an open item names as the test it fixes. It records no refusal, blocks
// the candidate on that item, and sets candidate.trunkAcquitted so the loop
// keeps the candidate's commit and releases its lane for re-gating once the
// fixing item lands.
func (l *Loop) holdForOpenTestFix(ctx context.Context, candidate *Candidate, gate GateOutcome, evidence string) (bool, error) {
	testNames := singleFailingTestNames(l.cfg.Root, *candidate, gate)
	if len(testNames) == 0 {
		return false, nil
	}
	fixerID, matchedTest, err := l.findOpenTestFixer(ctx, candidate.ItemID, testNames)
	if err != nil || fixerID == "" {
		return false, err
	}
	reason := TestFixWaitPrefix + matchedTest + " (fixed by " + fixerID + ")"
	if err := l.cfg.Steps.ItemState(ctx, candidate.ItemID, candidate.RunID, StateBlocked, item.WaitingOn{
		Waiter: "item:" + fixerID,
		Reason: reason,
	}); err != nil {
		return false, err
	}
	l.reportf("loop: %s held blocked by %s: gate failed only on %s, which %s fixes (trunk evidence: %s)\n",
		candidate.ItemID, fixerID, matchedTest, fixerID, evidence)
	candidate.trunkAcquitted = true
	return true, nil
}

// singleFailingTestNames extracts the single failing test from a gate failure's
// check logs. It returns nil if any failing check has no log or an unnamed/non-test
// failure, or if more than one distinct top-level test failed. For a single
// failing subtest TestFoo/sub, it returns ["TestFoo/sub", "TestFoo"] so an item
// naming either matches; for multiple subtests under TestFoo, it returns ["TestFoo"].
func singleFailingTestNames(root string, candidate Candidate, gate GateOutcome) []string {
	checkLogs := map[string]string{}
	for id, ref := range gate.CheckLogs {
		if id != "" && ref != "" {
			checkLogs[id] = ref
		}
	}
	var verdictChecks []EndingCheck
	if root != "" && candidate.RunID != "" {
		verdictChecks = gateFailingChecks(RunDir(root, candidate.RunID), candidate.RunID)
		for _, vc := range verdictChecks {
			if vc.CheckID != "" && vc.LogRef != "" && checkLogs[vc.CheckID] == "" {
				checkLogs[vc.CheckID] = vc.LogRef
			}
		}
	}
	checkIDs := append([]string(nil), gate.FailingChecks...)
	if len(checkIDs) == 0 {
		for id := range checkLogs {
			checkIDs = append(checkIDs, id)
		}
		sort.Strings(checkIDs)
	}
	if len(checkIDs) == 0 {
		return nil
	}
	seenTests := map[string]bool{}
	var tests []string
	for _, id := range checkIDs {
		ref := checkLogs[id]
		if ref == "" {
			return nil
		}
		found, ok := failingTestsFromLog(ref)
		if !ok || len(found) == 0 {
			return nil
		}
		for _, name := range found {
			if !seenTests[name] {
				seenTests[name] = true
				tests = append(tests, name)
			}
		}
	}
	return collapseSingleTest(tests)
}

func collapseSingleTest(tests []string) []string {
	if len(tests) == 0 {
		return nil
	}
	roots := map[string]bool{}
	leaves := map[string]bool{}
	for _, name := range tests {
		root := testRootName(name)
		roots[root] = true
		if strings.Contains(name, "/") {
			leaves[name] = true
		}
	}
	if len(roots) != 1 {
		return nil
	}
	var root string
	for r := range roots {
		root = r
	}
	if len(leaves) == 1 {
		for leaf := range leaves {
			return []string{leaf, root}
		}
	}
	return []string{root}
}

func testRootName(name string) string {
	if idx := strings.IndexByte(name, '/'); idx >= 0 {
		return name[:idx]
	}
	return name
}

// failingTestsFromLog extracts failing test names from a check's log (or its
// whole-log sibling when logRef is a .triage.log). Returns (names, false) if
// the log shows a non-test or unnamed failure.
func failingTestsFromLog(logRef string) ([]string, bool) {
	if triage := readTriageBlock(logRef); len(triage) > 0 {
		seen := map[string]bool{}
		var names []string
		for _, fc := range triage {
			if len(fc.Cases) == 0 {
				return nil, false
			}
			for _, c := range fc.Cases {
				name := strings.TrimSpace(c.Name)
				if !isTestName(name) {
					return nil, false
				}
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
		return names, len(names) > 0
	}

	whole := strings.TrimSuffix(logRef, ".triage.log")
	if whole != logRef {
		whole += ".log"
	}
	f, err := os.Open(whole)
	if err != nil && whole != logRef {
		f, err = os.Open(logRef)
	}
	if err != nil {
		return nil, false
	}
	defer f.Close()

	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if isTestName(name) && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	inGoAffectedSummary := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), triageScanMax)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			inGoAffectedSummary = false
			continue
		}
		if goBuildFailRe.MatchString(trimmed) {
			return nil, false
		}
		if trimmed == "go-affected: failing packages and tests:" {
			inGoAffectedSummary = true
			continue
		}
		if inGoAffectedSummary {
			if m := goAffectedLineRe.FindStringSubmatch(line); m != nil {
				for _, part := range strings.Split(m[1], ",") {
					add(part)
				}
				continue
			}
			inGoAffectedSummary = false
		}
		if _, shardNames := parseShardFailureLine(trimmed); len(shardNames) > 0 {
			for _, name := range shardNames {
				add(name)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "--- FAIL:"))
			if fields := strings.Fields(rest); len(fields) > 0 {
				add(fields[0])
			}
			continue
		}
		if m := unittestFailRe.FindStringSubmatch(trimmed); m != nil {
			add(m[1])
			continue
		}
		if m := pytestFailRe.FindStringSubmatch(trimmed); m != nil {
			if m[2] != "" {
				add(m[2])
			} else {
				add(m[1])
			}
		}
	}
	if scanner.Err() != nil {
		return nil, false
	}
	return names, len(names) > 0
}

func isTestName(name string) bool {
	if len(name) < 2 || strings.ContainsAny(name, " \t\r\n") {
		return false
	}
	return true
}

// findOpenTestFixer searches open items in queue order for one that names any
// of testNames as the test it fixes, skipping candidateID and any item that
// already depends on or waits on candidateID.
func (l *Loop) findOpenTestFixer(ctx context.Context, candidateID string, testNames []string) (string, string, error) {
	if len(testNames) == 0 || l.cfg.Store == "" {
		return "", "", nil
	}
	var foundID, foundTest string
	err := l.readStore(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		rows, err := r.Query(`SELECT item_id, title, state, COALESCE(prose, '') FROM items ORDER BY rank, item_id`)
		if err != nil {
			return err
		}
		type openRow struct {
			id, title, state, prose string
		}
		var open []openRow
		for rows.Next() {
			var row openRow
			if err := rows.Scan(&row.id, &row.title, &row.state, &row.prose); err != nil {
				rows.Close()
				return err
			}
			if row.id == candidateID {
				continue
			}
			if row.state == "not-started" || finishedState(declared.Parsed, row.state) || closedState(declared.Parsed, row.state) {
				continue
			}
			open = append(open, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, row := range open {
			criteria, err := readItemCriteriaTexts(r, row.id)
			if err != nil {
				return err
			}
			words, err := item.RequestWords(r, row.id)
			if err != nil {
				return err
			}
			for _, testName := range testNames {
				if !itemFixesTest(row.title, criteria, row.prose, string(words), testName) {
					continue
				}
				cycle, err := wouldCreateWaitCycle(r, candidateID, row.id)
				if err != nil {
					return err
				}
				if cycle {
					continue
				}
				foundID, foundTest = row.id, testName
				return nil
			}
		}
		return nil
	})
	return foundID, foundTest, err
}

func readItemCriteriaTexts(r *store.Reader, itemID string) ([]string, error) {
	rows, err := r.Query(`SELECT text FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, rows.Err()
}

// wouldCreateWaitCycle reports whether targetID already depends on or waits on
// candidateID (directly or transitively), so blocking candidateID on targetID
// would form a cycle.
func wouldCreateWaitCycle(r *store.Reader, candidateID, targetID string) (bool, error) {
	visited := map[string]bool{targetID: true}
	queue := []string{targetID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		wait, _, err := item.ReadWaitingOn(r, cur)
		if err != nil {
			return false, err
		}
		if wait != nil {
			if dep, ok := strings.CutPrefix(wait.Waiter, "item:"); ok && dep != "" {
				if dep == candidateID {
					return true, nil
				}
				if !visited[dep] {
					visited[dep] = true
					queue = append(queue, dep)
				}
			}
		}
		rows, err := r.Query(`SELECT blocked_by FROM item_edges WHERE item_id = ?`, cur)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				rows.Close()
				return false, err
			}
			if dep == candidateID {
				rows.Close()
				return true, nil
			}
			if !visited[dep] {
				visited[dep] = true
				queue = append(queue, dep)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
	}
	return false, nil
}

// itemFixesTest reports whether an open item names testName as the test it fixes.
func itemFixesTest(title string, criteria []string, prose, requestWords, testName string) bool {
	if !isTestName(testName) {
		return false
	}
	inTitle := containsTestIdentifier(title, testName)
	inProse := containsTestIdentifier(prose, testName)
	inWords := containsTestIdentifier(requestWords, testName)
	inCriteria := false
	for _, c := range criteria {
		if containsTestIdentifier(c, testName) {
			inCriteria = true
			if hasFixIntent(c) {
				return true
			}
		}
	}
	if !inTitle && !inProse && !inWords && !inCriteria {
		return false
	}
	if inTitle && hasFixIntent(title) {
		return true
	}
	if isTestFixTitle(title) && (inProse || inWords) {
		return true
	}
	if inProse && textDirectlyFixesTest(prose, testName) {
		return true
	}
	if inWords && textDirectlyFixesTest(requestWords, testName) {
		return true
	}
	return false
}

func containsTestIdentifier(text, testName string) bool {
	if text == "" || testName == "" {
		return false
	}
	start := 0
	for {
		idx := strings.Index(text[start:], testName)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(testName)
		beforeOK := pos == 0 || !isIdentByte(text[pos-1])
		afterOK := end == len(text) || !isIdentByte(text[end])
		if beforeOK && afterOK {
			return true
		}
		start = pos + 1
	}
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

var fixIntentWords = map[string]bool{
	"fix":            true,
	"fixes":          true,
	"fixed":          true,
	"fixing":         true,
	"repair":         true,
	"repairs":        true,
	"repaired":       true,
	"repairing":      true,
	"pass":           true,
	"passes":         true,
	"passing":        true,
	"flaky":          true,
	"flake":          true,
	"deflake":        true,
	"unflake":        true,
	"stabilize":      true,
	"stabilise":      true,
	"reliable":       true,
	"reliably":       true,
	"resolve":        true,
	"resolves":       true,
	"resolved":       true,
	"race":           true,
	"load-sensitive": true,
	"intermittent":   true,
	"intermittently": true,
}

var directFixVerbs = map[string]bool{
	"fix":       true,
	"fixes":     true,
	"fixing":    true,
	"repair":    true,
	"repairs":   true,
	"stabilize": true,
	"stabilise": true,
	"deflake":   true,
	"unflake":   true,
	"resolve":   true,
	"resolves":  true,
}

var testFixTitlePrefixes = []string{
	"fix ",
	"make ",
	"repair ",
	"stabilize ",
	"stabilise ",
	"deflake ",
	"unflake ",
	"resolve ",
	"stop ",
	"prevent ",
	"eliminate ",
	"remove ",
	"keep ",
}

func splitWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_')
	})
}

func hasFixIntent(text string) bool {
	for _, w := range splitWords(text) {
		if fixIntentWords[w] {
			return true
		}
	}
	return false
}

func isTestFixTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	if !hasFixIntent(lower) {
		return false
	}
	hasPrefix := false
	for _, p := range testFixTitlePrefixes {
		if strings.HasPrefix(lower, p) {
			hasPrefix = true
			break
		}
	}
	if !hasPrefix {
		return false
	}
	for _, w := range splitWords(lower) {
		if w == "test" || w == "tests" || w == "flaky" || w == "flake" {
			return true
		}
	}
	return false
}

func textDirectlyFixesTest(text, testName string) bool {
	for _, line := range strings.Split(text, "\n") {
		if !containsTestIdentifier(line, testName) {
			continue
		}
		for _, sentence := range strings.Split(line, ". ") {
			if !containsTestIdentifier(sentence, testName) {
				continue
			}
			if sentenceDirectlyFixesTest(sentence, testName) {
				return true
			}
		}
	}
	return false
}

func sentenceDirectlyFixesTest(sentence, testName string) bool {
	lower := strings.ToLower(sentence)
	if strings.Contains(lower, "do not") || strings.Contains(lower, "don't") || strings.Contains(lower, "not fix") {
		return false
	}
	idx := strings.Index(sentence, testName)
	if idx < 0 {
		return false
	}
	beforeWords := splitWords(sentence[:idx])
	afterWords := splitWords(sentence[idx+len(testName):])

	// Pattern 1: "<fix-verb> [up to 3 words] <testName>"
	start := len(beforeWords) - 4
	if start < 0 {
		start = 0
	}
	for _, w := range beforeWords[start:] {
		if directFixVerbs[w] {
			return true
		}
	}

	// Pattern 2: "make ... <testName> ... pass/passes"
	hasMakeBefore := false
	for _, w := range beforeWords[start:] {
		if w == "make" {
			hasMakeBefore = true
			break
		}
	}
	limit := 4
	if len(afterWords) < limit {
		limit = len(afterWords)
	}
	for i, w := range afterWords[:limit] {
		if w == "passes" {
			return true
		}
		if w == "pass" {
			if hasMakeBefore {
				return true
			}
			if i > 0 {
				switch afterWords[i-1] {
				case "to", "must", "should", "will", "can", "does":
					return true
				}
			}
		}
		if hasMakeBefore && (w == "passing" || w == "reliable" || w == "reliably") {
			return true
		}
	}
	return false
}

// landedBlockerSHA answers the trunk_after commit of a landed blocker item when
// recorded in landings, or "" if none is recorded.
func landedBlockerSHA(r *store.Reader, blockerID string) (string, error) {
	var sha string
	err := r.QueryRow(`SELECT COALESCE(trunk_after, '') FROM landings WHERE item_id = ? AND result = 'landed' ORDER BY rowid DESC LIMIT 1`, blockerID).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sha, err
}
