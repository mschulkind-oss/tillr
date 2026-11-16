package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seedOut is the item.seed record a seed run emits, decoded: the entries it wrote, the
// entries the store already held, and (for a dry run) what it would have written.
type seedOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		From     string      `json:"from"`
		DryRun   bool        `json:"dry_run"`
		Written  []string    `json:"written"`
		Ranked   []string    `json:"ranked"`
		Existing []string    `json:"existing"`
		Items    []itemInput `json:"items"`
	} `json:"payload"`
}

// seedDocument writes one seed document to scratch and answers its path. The document
// carries the provenance keys the real seed file carries, so a case that adds a key adds
// only the one it is testing.
func seedDocument(t *testing.T, entries ...any) string {
	t.Helper()
	return seedDocumentOf(t, map[string]any{
		"kind":      "tillr.seed",
		"version":   "0.1.0",
		"generated": "2026-11-12",
		"source":    "docs/archive/board-2026-11-12/board-content.json",
		"triage":    "docs/design/tillr-seed-triage.md",
		"note":      "fixture",
		"items":     entries,
	})
}

// seedDocumentOf writes the document as given, so a case can change the top level.
func seedDocumentOf(t *testing.T, doc map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("building the seed document failed: %v", err)
	}
	path := filepath.Join(scratch(t, "seed-doc"), "seed-items.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the seed document failed: %v", err)
	}
	return path
}

// seed drives the seed face against one document.
func seed(t *testing.T, db, from string, extra ...string) (int, []byte) {
	t.Helper()
	return run(t, append([]string{"seed", "--store", db, "--from", from, "--now", "2026-11-12T00:00:00Z"}, extra...)...)
}

// seedBoth drives the seed face and answers both streams, so a case can read the
// diagnostic a refusal prints on stderr.
func seedBoth(t *testing.T, db, from string, extra ...string) (int, []byte, []byte) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"seed", "--store", db, "--from", from, "--now", "2026-11-12T00:00:00Z"}, extra...)...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), stdout.Bytes(), stderr.Bytes()
	}
	if err != nil {
		t.Fatalf("running the seed face failed: %v", err)
	}
	return 0, stdout.Bytes(), stderr.Bytes()
}

func decodeSeed(t *testing.T, out []byte) seedOut {
	t.Helper()
	var got seedOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the seed face did not emit a record: %v\n%s", err, out)
	}
	return got
}

// A valid document writes every item and every criterion, and an entry blocked by another
// entry of the same document is written after it: the blocker is listed second here, so a
// loader that wrote in file order would fail the edge rule.
//
// The negative control is the event count: three item.add events, one per entry, and the
// criteria read back through show rather than from the file the case wrote.
func TestSeedWritesEveryItemAndCriterion(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-writes"), "board.sqlite3")
	first := base("item-seed-01", "the first item")
	first.Criteria = []criterion{
		{CriterionID: "c1", Text: "the first criterion", State: ""},
		{CriterionID: "c2", Text: "the second criterion", State: ""},
	}
	second := base("item-seed-02", "the second item")
	second.BlockedBy = []string{"item-seed-01"}
	from := seedDocument(t, first, second)

	code, out := seed(t, db, from)
	if code != 0 {
		t.Fatalf("seed exited %d: %s", code, out)
	}
	got := decodeSeed(t, out)
	if got.Kind != "item.seed" {
		t.Fatalf("seed emitted kind %q, want item.seed", got.Kind)
	}
	if strings.Join(got.Payload.Written, ",") != "item-seed-01,item-seed-02" {
		t.Fatalf("seed wrote %v, want both entries in document order", got.Payload.Written)
	}
	if len(got.Payload.Existing) != 0 {
		t.Fatalf("seed reported %v as already present on an empty store", got.Payload.Existing)
	}
	if events := eventsOfKind(t, db, "item.add"); events != 2 {
		t.Fatalf("the seed appended %d item.add events, want 2", events)
	}

	code, out = run(t, "show", "--store", db, "--item", "item-seed-01")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	shown := decode(t, out)
	if len(shown.Payload.Criteria) != 2 {
		t.Fatalf("the stored item carries %d criteria, want 2", len(shown.Payload.Criteria))
	}
	if shown.Payload.Criteria[1].CriterionID != "c2" || shown.Payload.Criteria[1].Text != "the second criterion" {
		t.Fatalf("the stored criterion is %q / %q", shown.Payload.Criteria[1].CriterionID, shown.Payload.Criteria[1].Text)
	}

	code, out = run(t, "show", "--store", db, "--item", "item-seed-02")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	if blocked := decode(t, out).Payload.BlockedBy; len(blocked) != 1 || blocked[0] != "item-seed-01" {
		t.Fatalf("the stored edge is %v, want [item-seed-01]", blocked)
	}
}

// Seeding one file twice is a no-op that reports what it found, not a second write. The
// negative control is the event count: a loader that rewrote the items would append two
// more item.update events and the count below would move.
func TestSeedIsANoOpTheSecondTime(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-noop"), "board.sqlite3")
	from := seedDocument(t,
		base("item-seed-01", "the first item"),
		base("item-seed-02", "the second item"),
	)
	if code, out := seed(t, db, from); code != 0 {
		t.Fatalf("the first seed exited %d: %s", code, out)
	}

	code, out := seed(t, db, from)
	if code != 0 {
		t.Fatalf("the second seed exited %d: %s", code, out)
	}
	got := decodeSeed(t, out)
	if len(got.Payload.Written) != 0 {
		t.Fatalf("the second seed wrote %v, want nothing", got.Payload.Written)
	}
	if strings.Join(got.Payload.Existing, ",") != "item-seed-01,item-seed-02" {
		t.Fatalf("the second seed reported %v as existing", got.Payload.Existing)
	}
	if len(got.Payload.Items) != 0 {
		t.Fatalf("the second seed reported %d items written, want none", len(got.Payload.Items))
	}
	if updates := eventsOfKind(t, db, "item.update"); updates != 0 {
		t.Fatalf("the second seed appended %d item.update events, want 0", updates)
	}
	if adds := eventsOfKind(t, db, "item.add"); adds != 2 {
		t.Fatalf("the two runs appended %d item.add events, want 2", adds)
	}
}

// The seed owns the queue's order: a document whose entries are identical except for
// their rank rearranges the queue and reports them as ranked rather than as existing,
// and re-running it is stable.
//
// The negative control is the last block: the same rank change carried by a document that
// also changes the title is refused, and the stored rank does not move — so the rerank
// path cannot smuggle a definition change past the conflict rule.
func TestSeedRearrangesTheQueueByRank(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-rerank"), "board.sqlite3")
	first := base("item-seed-01", "the first item")
	first.Rank = "a"
	second := base("item-seed-02", "the second item")
	second.Rank = "b"
	if code, out := seed(t, db, seedDocument(t, first, second)); code != 0 {
		t.Fatalf("the first seed exited %d: %s", code, out)
	}

	first.Rank, second.Rank = "b", "a"
	code, out := seed(t, db, seedDocument(t, first, second))
	if code != 0 {
		t.Fatalf("the re-ranking seed exited %d: %s", code, out)
	}
	got := decodeSeed(t, out)
	if len(got.Payload.Written) != 0 {
		t.Fatalf("the re-ranking seed wrote %v, want nothing", got.Payload.Written)
	}
	if strings.Join(got.Payload.Ranked, ",") != "item-seed-01,item-seed-02" {
		t.Fatalf("the re-ranking seed reported %v as ranked", got.Payload.Ranked)
	}
	if len(got.Payload.Existing) != 0 {
		t.Fatalf("the re-ranking seed reported %v as existing", got.Payload.Existing)
	}
	if rank := shownRank(t, db, "item-seed-01"); rank != "b" {
		t.Fatalf("item-seed-01 reads rank %q, want the seed's \"b\"", rank)
	}
	if rank := shownRank(t, db, "item-seed-02"); rank != "a" {
		t.Fatalf("item-seed-02 reads rank %q, want the seed's \"a\"", rank)
	}

	first.Rank = "z"
	first.Title = "the changed title"
	code, out, stderr := seedBoth(t, db, seedDocument(t, first, second))
	if code != 1 {
		t.Fatalf("a redefinition carrying a rank exited %d, want 1: %s / %s", code, out, stderr)
	}
	if rank := shownRank(t, db, "item-seed-01"); rank != "b" {
		t.Fatalf("the refused seed moved the rank to %q", rank)
	}
}

// secondFrozenText is the second criterion's frozen text, so the re-seed case pins every
// criterion's state rather than only the first.
const secondFrozenText = "the second criterion"

// A re-seed reports the store's runtime state, not the file's. The seed owns an item's
// definition, its intake state at first insert and its rank; the store owns everything
// learned since, and an item's own state and its criteria's states are the two facts a
// verdict and the drain move. So an item that has landed, with criteria a verdict has
// retired, is re-seeded from a file that still says `in-queue` with empty criterion
// states and a new rank: the definition matches, the rank is the seed's to move, and the
// state and criterion states stay the store's.
//
// The negative control is the unfixed loader: the same seed is refused with
// `seed-conflict` on the item's state (and then on the criterion's), which is exactly the
// refusal this case exists to remove. The last block pins the other half of the pair: a
// fix that deleted the two comparisons but still reapplied the file's entry would leave
// the item at `in-queue` with empty criterion states, so the re-seed would exit 0 and the
// state assertions below would fail.
func TestSeedReranksWithoutResettingTheStoresRuntimeState(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-rerank-state"), "board.sqlite3")
	entry := base("item-seed-01", "the first item")
	entry.State = "in-queue"
	entry.Rank = "a"
	entry.Criteria = []criterion{
		{CriterionID: "c1", Text: frozenText, State: ""},
		{CriterionID: "c2", Text: secondFrozenText, State: ""},
	}
	if code, out := seed(t, db, seedDocument(t, entry)); code != 0 {
		t.Fatalf("the first seed exited %d: %s", code, out)
	}

	// The store learns: the item lands and a verdict retires each criterion. Both are
	// the store's writers (SetState and RecordVerdict), not the seed's, and both
	// criteria are judged so the case pins every criterion's state rather than the first.
	if code, out := run(t, "state", "--store", db, "--item", "item-seed-01", "--state", "landed", "--now", issuedAt); code != 0 {
		t.Fatalf("moving the item to landed exited %d: %s", code, out)
	}
	for _, judged := range []struct{ id, text string }{{"c1", frozenText}, {"c2", secondFrozenText}} {
		verdict := mechanicalVerdict()
		verdict.ItemID = "item-seed-01"
		verdict.CriterionID = judged.id
		verdict.CriterionHash = hashOf(judged.text)
		if code, out := recordVerdict(t, db, verdict); code != 0 {
			t.Fatalf("recording the verdict on %s exited %d: %s", judged.id, code, out)
		}
	}

	// The file says nothing about what was learned: the item is still `in-queue` there,
	// its criteria carry no state, and only the rank moved.
	entry.Rank = "b"
	code, out, stderr := seedBoth(t, db, seedDocument(t, entry))
	if code != 0 {
		t.Fatalf("the re-seed exited %d, want 0: %s / %s", code, out, stderr)
	}
	got := decodeSeed(t, out)
	if strings.Join(got.Payload.Ranked, ",") != "item-seed-01" {
		t.Fatalf("the re-seed reported %v as ranked, want item-seed-01", got.Payload.Ranked)
	}
	if len(got.Payload.Written) != 0 || len(got.Payload.Existing) != 0 {
		t.Fatalf("the re-seed wrote %v and found %v existing, want neither", got.Payload.Written, got.Payload.Existing)
	}

	shown := showItem(t, db, "item-seed-01")
	if shown.Payload.State != "landed" {
		t.Fatalf("the re-seed reset the item's state to %q, want the store's landed", shown.Payload.State)
	}
	if len(shown.Payload.Criteria) != 2 {
		t.Fatalf("the stored item carries %d criteria, want 2", len(shown.Payload.Criteria))
	}
	for _, c := range shown.Payload.Criteria {
		if c.State != "done" {
			t.Fatalf("the re-seed reset criterion %s's state to %q, want the store's done", c.CriterionID, c.State)
		}
	}
	if rank := shownRank(t, db, "item-seed-01"); rank != "b" {
		t.Fatalf("the re-seed left the rank at %q, want the seed's \"b\"", rank)
	}
}

// shownRank reads one item's stored rank through the show face, so a case reads the row
// rather than the report the seed made about it.
func shownRank(t *testing.T, db, id string) string {
	t.Helper()
	code, out := run(t, "show", "--store", db, "--item", id)
	if code != 0 {
		t.Fatalf("show %s exited %d: %s", id, code, out)
	}
	var record struct {
		Payload struct {
			Rank string `json:"rank"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("show %s did not emit a record: %v", id, err)
	}
	return record.Payload.Rank
}

// An entry whose definition differs from the stored item is refused, with the id named,
// and nothing is written: the first entry of the document is identical and would have
// been written first if the loader wrote as it validated.
//
// The negative control is the stored title and the event count: an overwrite would leave
// the second item reading "the changed title" and would append an item.update event.
func TestSeedRefusesAConflictingRedefinitionAndWritesNothing(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-conflict"), "board.sqlite3")
	if code, out := seed(t, db, seedDocument(t,
		base("item-seed-01", "the first item"),
		base("item-seed-02", "the second item"),
	)); code != 0 {
		t.Fatalf("the first seed exited %d: %s", code, out)
	}

	changed := base("item-seed-02", "the changed title")
	from := seedDocument(t, base("item-seed-01", "the first item"), changed)
	code, out, stderr := seedBoth(t, db, from)
	if code != 1 {
		t.Fatalf("a conflicting seed exited %d, want 1: %s / %s", code, out, stderr)
	}
	got := decode(t, out)
	if got.Kind != "item.rejection" || got.Payload.Rule != "seed-conflict" || got.Payload.ItemID != "item-seed-02" {
		t.Fatalf("the refusal is %q / rule %q / item %q", got.Kind, got.Payload.Rule, got.Payload.ItemID)
	}
	if !strings.Contains(got.Payload.Reason, "item-seed-02") || !strings.Contains(got.Payload.Reason, "title") {
		t.Fatalf("the refusal does not name the id and the field that differs: %q", got.Payload.Reason)
	}
	if updates := eventsOfKind(t, db, "item.update"); updates != 0 {
		t.Fatalf("the refused seed appended %d item.update events, want 0", updates)
	}
	code, out = run(t, "show", "--store", db, "--item", "item-seed-02")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	if shown := decode(t, out); shown.Payload.Title != "the second item" {
		t.Fatalf("the refused seed left the title at %q", shown.Payload.Title)
	}
}

// One bad entry aborts the whole document: the first two entries are valid and the third
// carries no criterion, and afterwards none of the three is in the store.
//
// The negative control is the not-found exit on the first entry: a loader that wrote the
// entries it had validated would have stored it.
func TestOneBadEntryAbortsTheWholeSeed(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-abort"), "board.sqlite3")
	bad := base("item-seed-03", "the third item")
	bad.Criteria = nil
	from := seedDocument(t,
		base("item-seed-01", "the first item"),
		base("item-seed-02", "the second item"),
		bad,
	)
	code, out, stderr := seedBoth(t, db, from)
	if code != 2 {
		t.Fatalf("a seed with one bad entry exited %d, want 2: %s / %s", code, out, stderr)
	}
	if len(out) != 0 {
		t.Fatalf("a refused seed emitted a record: %s", out)
	}
	if !strings.Contains(string(stderr), "entry 3") || !strings.Contains(string(stderr), "item-seed-03") {
		t.Fatalf("the refusal does not name the offending entry: %s", stderr)
	}
	for _, id := range []string{"item-seed-01", "item-seed-02", "item-seed-03"} {
		if code, showOut := run(t, "show", "--store", db, "--item", id); code != 1 {
			t.Fatalf("a refused seed left %s in the store (exit %d): %s", id, code, showOut)
		}
	}
	if adds := eventsOfKind(t, db, "item.add"); adds != 0 {
		t.Fatalf("a refused seed appended %d item.add events, want 0", adds)
	}
}

// A dry run validates and reports what it would write and touches nothing: the store path
// does not exist afterwards, and a second dry run against the same missing store still
// reports every entry.
//
// The negative control is the missing file: a dry run that created the store to inspect it
// would leave a database where the case asserts there is none.
func TestSeedDryRunWritesNothingAndStillValidates(t *testing.T) {
	dir := scratch(t, "seed-dry")
	db := filepath.Join(dir, "board.sqlite3")
	from := seedDocument(t,
		base("item-seed-01", "the first item"),
		base("item-seed-02", "the second item"),
	)

	code, out := seed(t, db, from, "--dry-run")
	if code != 0 {
		t.Fatalf("a valid dry run exited %d: %s", code, out)
	}
	got := decodeSeed(t, out)
	if !got.Payload.DryRun {
		t.Fatal("the dry run's report does not say it was a dry run")
	}
	if len(got.Payload.Written) != 0 {
		t.Fatalf("the dry run reported %v written", got.Payload.Written)
	}
	if len(got.Payload.Items) != 2 {
		t.Fatalf("the dry run reported %d items it would write, want 2", len(got.Payload.Items))
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("the dry run touched the store path: %v", err)
	}

	// The same dry run against a store that exists and already holds the items reports
	// them as existing rather than as work, so the report answers the real run's question.
	if code, out := seed(t, db, from); code != 0 {
		t.Fatalf("the real seed exited %d: %s", code, out)
	}
	code, out = seed(t, db, from, "--dry-run")
	if code != 0 {
		t.Fatalf("the dry run over a seeded store exited %d: %s", code, out)
	}
	got = decodeSeed(t, out)
	if len(got.Payload.Items) != 0 || len(got.Payload.Existing) != 2 {
		t.Fatalf("the dry run would write %d and found %d existing, want 0 and 2",
			len(got.Payload.Items), len(got.Payload.Existing))
	}

	// A dry run still validates: a bad entry exits nonzero and writes nothing.
	bad := seedDocumentOf(t, map[string]any{"kind": "tillr.seed", "items": []any{map[string]any{}}})
	if code, out := seed(t, filepath.Join(scratch(t, "seed-dry-bad"), "board.sqlite3"), bad, "--dry-run"); code == 0 {
		t.Fatalf("a dry run over a bad document exited 0: %s", out)
	}
}

// A seed accepting the item's fence: `allowed_paths` is a column on the item record (the
// gap the triage recorded), so a file that states it stores it and `show` reads it back.
//
// The negative control is show's output: a loader that parsed the field and dropped it on
// the floor would exit 0 with an item that stages no fence, and the second half of the case
// pins that a wildcard never reaches the store at all.
func TestSeedStoresTheItemsAllowedPaths(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-fence"), "board.sqlite3")
	entry := map[string]any{
		"item_id":       "item-seed-01",
		"title":         "the first item",
		"body_ref":      "briefs/item-seed-01.md",
		"state":         "not-started",
		"design_doc":    "",
		"blocked_by":    []string{},
		"allowed_paths": []string{"tools/tillr-item", "tools/tillr-run"},
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion", "state": ""},
		},
	}
	from := seedDocument(t, entry)

	code, out, stderr := seedBoth(t, db, from)
	if code != 0 {
		t.Fatalf("a seed carrying allowed_paths exited %d, want 0: %s / %s", code, out, stderr)
	}
	code, showOut := run(t, "show", "--store", db, "--item", "item-seed-01")
	if code != 0 {
		t.Fatalf("showing the seeded item exited %d: %s", code, showOut)
	}
	if !strings.Contains(string(showOut), `"allowed_paths":["tools/tillr-item","tools/tillr-run"]`) {
		t.Fatalf("show does not carry the seeded allowed_paths: %s", showOut)
	}

	// A wildcard is refused at store time rather than stored and trusted later.
	wildcard := map[string]any{
		"item_id":       "item-seed-02",
		"title":         "a wildcard fence",
		"body_ref":      "briefs/item-seed-02.md",
		"state":         "not-started",
		"design_doc":    "",
		"blocked_by":    []string{},
		"allowed_paths": []string{"*"},
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion", "state": ""},
		},
	}
	code, out, stderr = seedBoth(t, db, seedDocument(t, wildcard))
	if code == 0 {
		t.Fatalf("a seed carrying the wildcard fence exited 0: %s", out)
	}
	// The refusal is a rejection record (exit 1), so it names the rule and the reason on
	// stdout: a caller reading only the exit code cannot tell a wildcard from any other no.
	if !strings.Contains(string(out), "fence-wildcard") || !strings.Contains(string(out), "allowed_paths") {
		t.Fatalf("the wildcard refusal does not name the rule and the field: %s / %s", out, stderr)
	}
	if code, showOut := run(t, "show", "--store", db, "--item", "item-seed-02"); code != 1 {
		t.Fatalf("a refused wildcard seed left the item in the store (exit %d): %s", code, showOut)
	}

	// The document's shape is still closed, one level down: an unknown key on a criterion is refused too.
	criterionEntry := base("item-seed-01", "the first item")
	broken := map[string]any{
		"item_id":    criterionEntry.ItemID,
		"title":      criterionEntry.Title,
		"body_ref":   criterionEntry.BodyRef,
		"state":      criterionEntry.State,
		"blocked_by": []string{},
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion", "state": "", "criterion_hash": "x"},
		},
	}
	if code, out, stderr := seedBoth(t, db, seedDocument(t, broken)); code != 2 {
		t.Fatalf("a criterion with an unknown key exited %d, want 2: %s / %s", code, out, stderr)
	}
}

// A seed accepting the item's rank: `rank` is a column on the item record, so a file that
// states it stores it and `show` reads it back. The rank is the sparse lexical token the
// queue orders by — a string over item's RankAlphabet, here `am` between `a` and `b` — and
// an entry that states none stores an unranked item rather than being refused, which is
// what every other case in this file already exercises.
//
// The negative controls are the two halves of the field's contract: show's output, so a
// loader that parsed the rank and dropped it on the floor fails here instead of exiting 0
// with an unranked item; and a token outside the alphabet, refused by name with the whole
// document unwritten — a rank the store cannot order would reorder the queue silently, and
// a document refused half way through would leave the entries before the bad one stored.
func TestSeedStoresTheItemsRank(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-rank"), "board.sqlite3")
	entry := map[string]any{
		"item_id":    "item-seed-01",
		"title":      "the first item",
		"body_ref":   "briefs/item-seed-01.md",
		"state":      "not-started",
		"design_doc": "",
		"blocked_by": []string{},
		"rank":       "am",
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion", "state": ""},
		},
	}
	code, out, stderr := seedBoth(t, db, seedDocument(t, entry))
	if code != 0 {
		t.Fatalf("a seed carrying a rank exited %d, want 0: %s / %s", code, out, stderr)
	}
	code, showOut := run(t, "show", "--store", db, "--item", "item-seed-01")
	if code != 0 {
		t.Fatalf("showing the seeded item exited %d: %s", code, showOut)
	}
	if !strings.Contains(string(showOut), `"rank":"am"`) {
		t.Fatalf("show does not carry the seeded rank: %s", showOut)
	}

	// A rank outside the alphabet is refused by the one mapping every write goes through
	// (Apply), naming the rule, and the whole document goes with it: the bad entry is the
	// second one, and the first — whose rank is valid — is not stored either, because the
	// seed's writes are one transaction.
	offAlphabet := map[string]any{
		"item_id":    "item-seed-02",
		"title":      "a rank the queue cannot order",
		"body_ref":   "briefs/item-seed-02.md",
		"state":      "not-started",
		"design_doc": "",
		"blocked_by": []string{},
		"rank":       "A",
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion", "state": ""},
		},
	}
	third := map[string]any{
		"item_id":    "item-seed-03",
		"title":      "an entry before the bad one",
		"body_ref":   "briefs/item-seed-03.md",
		"state":      "not-started",
		"design_doc": "",
		"blocked_by": []string{},
		"rank":       "b",
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion", "state": ""},
		},
	}
	code, out, stderr = seedBoth(t, db, seedDocument(t, third, offAlphabet))
	if code != 1 {
		t.Fatalf("a seed carrying an off-alphabet rank exited %d, want 1: %s / %s", code, out, stderr)
	}
	if !strings.Contains(string(out), "rank-alphabet") {
		t.Fatalf("the rank refusal does not name the rule: %s / %s", out, stderr)
	}
	for _, id := range []string{"item-seed-03", "item-seed-02"} {
		if code, showOut := run(t, "show", "--store", db, "--item", id); code != 1 {
			t.Fatalf("a refused rank seed left %s in the store (exit %d): %s", id, code, showOut)
		}
	}
}

// A document whose entries block each other can be written in no order, so it is refused
// by name before any row is written.
func TestSeedRefusesACycleBetweenItsOwnEntries(t *testing.T) {
	db := filepath.Join(scratch(t, "seed-cycle"), "board.sqlite3")
	first := base("item-seed-01", "the first item")
	first.BlockedBy = []string{"item-seed-02"}
	second := base("item-seed-02", "the second item")
	second.BlockedBy = []string{"item-seed-01"}
	from := seedDocument(t, first, second)

	code, out, stderr := seedBoth(t, db, from)
	if code != 1 {
		t.Fatalf("a cyclic seed exited %d, want 1: %s / %s", code, out, stderr)
	}
	if got := decode(t, out); got.Payload.Rule != "edge-cycle" {
		t.Fatalf("the refusal names rule %q, want edge-cycle", got.Payload.Rule)
	}
	if adds := eventsOfKind(t, db, "item.add"); adds != 0 {
		t.Fatalf("a cyclic seed appended %d item.add events, want 0", adds)
	}
}
