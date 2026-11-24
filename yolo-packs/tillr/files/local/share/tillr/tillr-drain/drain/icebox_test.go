package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func planDecisions(t *testing.T, storePath string, tick Pass, policy *Policy, questions QuestionSource) map[string]Decision {
	t.Helper()
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out := map[string]Decision{}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		envs, err := Plan(r, tick, policy, questions)
		if err != nil {
			return err
		}
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			out[d.ItemID] = d
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPlanHoldsInQueueItemWhoseWordsNameIceboxDesignDoc(t *testing.T) {
	f, l := passFixture(t, "item-ready")
	if err := os.MkdirAll(filepath.Join(f.root, "docs/tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scouted-design@v1.json"), "{\"name\":\"scouted-design\",\"version\":1,\"stages\":[{\"id\":\"design\",\"seat\":\"design\",\"access\":\"write\",\"write_scope\":[\"docs/\"]}]}\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/tillr-autoqa.md"), "# AutoQA\n\nIceboxed design.\n")
	writeForkFile(t, filepath.Join(f.root, "docs/tillr/roadmap.md"), strings.Join([]string{
		"# tillr Roadmap",
		"",
		"## 1. Active work",
		"",
		"- **Ready feature**",
		"",
		"## Icebox",
		"",
		"- **AutoQA, tillr's exploratory testing of itself** ([design](../design/tillr-autoqa.md)), iceboxed 2026-11-17.",
		"",
	}, "\n"))
	runGit(t, f.root, "add", "docs", "config")
	runGit(t, f.root, "commit", "-qm", "add roadmap icebox and autoqa design")

	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
	questions := &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}}
	l.questions = questions

	var iceboxItemID string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat='build', rank='z' WHERE item_id='item-ready'`); err != nil {
			return err
		}
		env, err := item.FileRequest(context.Background(), tx, item.RequestFiling{
			Words:       []byte("Refiled from item-design-autoqa-slices (originally defined in docs/design/tillr-autoqa.md, section \"Add a build order for AutoQA\")\n\nWrite an ordered slice list.\n"),
			Title:       "Add a build order for AutoQA",
			Seat:        "design",
			Workflow:    "scouted-design@v1",
			Paths:       []string{"docs/design/tillr-autoqa.md"},
			Destination: item.Destination{To: "first"},
			At:          time.Date(2026, 11, 23, 17, 19, 0, 0, time.UTC),
			By:          "owner",
			Via:         "cli",
		})
		iceboxItemID = env.ID
		return err
	})

	lanes := []string{"lane-1"}
	tick := Pass{Now: "2026-11-23T17:19:00Z", AvailableLanes: &lanes, StorePath: f.storePath}
	decisions := planDecisions(t, f.storePath, tick, l.cfg.Policy, questions)

	const wantReason = `icebox: "AutoQA, tillr's exploratory testing of itself" in docs/tillr/roadmap.md`
	got := decisions[iceboxItemID]
	if got.Action != ActionHold || got.Reason != wantReason {
		t.Fatalf("icebox item decision = %+v, want Action=%q Reason=%q", got, ActionHold, wantReason)
	}
	if got.RunRequest != nil || got.LaneID != "" {
		t.Fatalf("held icebox item still allocated execution resources: %+v", got)
	}
	// The single offered lane is not spent on the held Icebox item, so the next ready item dispatches.
	ready := decisions["item-ready"]
	if ready.Action != ActionDispatch || ready.LaneID != "lane-1" {
		t.Fatalf("next ready item decision = %+v, want dispatch on lane-1", ready)
	}

	// Verify the item remains in-queue (never closed automatically) and Why surfaces the Icebox reason.
	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, iceboxItemID).Scan(&state); err != nil {
			return err
		}
		if state != StateInQueue {
			t.Fatalf("item state = %q, want %q", state, StateInQueue)
		}
		whyAns, err := WhyForItem(r, tick, l.cfg.Policy, questions, iceboxItemID)
		if err != nil {
			return err
		}
		var matched *WhyRow
		for i := range whyAns.Rows {
			if whyAns.Rows[i].ItemID == iceboxItemID {
				matched = &whyAns.Rows[i]
				break
			}
		}
		if matched == nil || matched.Reason != wantReason || !strings.Contains(matched.Why, "AutoQA, tillr's exploratory testing of itself") {
			t.Fatalf("WhyForItem = %+v, want row for %s with reason %q", whyAns.Rows, iceboxItemID, wantReason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Verify startView shadow proposals also hold the Icebox item and offer the next ready item.
	snap, err := readProposedStarts(context.Background(), f.storePath, l.cfg.Now(), l.cfg.Policy, l.startViewPass(), questions, startSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if hold, ok := snap.holds[iceboxItemID]; !ok || hold != wantReason {
		t.Fatalf("startView hold for %s = %q (ok=%v), want %q", iceboxItemID, hold, ok, wantReason)
	}
	if entry := snap.entries[iceboxItemID]; entry.decision.Action != ActionHold || entry.decision.Reason != wantReason || entry.decision.RunRequest != nil {
		t.Fatalf("startView entry for icebox item %s = %+v, want ActionHold %q", iceboxItemID, entry, wantReason)
	}
	if entry := snap.entries["item-ready"]; entry.decision.Action != ActionDispatch {
		t.Fatalf("startView entry for item-ready = %+v, want ActionDispatch", entry)
	}
}

func TestPlanHoldsInQueueItemWhoseDesignDocSitsInRoadmapIcebox(t *testing.T) {
	f, l := passFixture(t, "item-icebox-doc", "item-not-started")
	if err := os.MkdirAll(filepath.Join(f.root, "docs/tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/design/tillr-goal-monitor.md"), "# Goal Monitor\n\nParked design.\n")
	writeForkFile(t, filepath.Join(f.root, "docs/tillr/roadmap.md"), strings.Join([]string{
		"# Roadmap",
		"",
		"## Icebox",
		"",
		"- **The goal monitor** ([design](../design/tillr-goal-monitor.md)), iceboxed 2026-11-17.",
		"",
	}, "\n"))
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "add goal monitor to icebox")

	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
	l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='in-queue', seat='design', design_doc='docs/design/tillr-goal-monitor.md', body_ref='' WHERE item_id='item-icebox-doc'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='not-started', seat='design', design_doc='docs/design/tillr-goal-monitor.md', body_ref='' WHERE item_id='item-not-started'`)
		return err
	})

	const wantReason = `icebox: "The goal monitor" in docs/tillr/roadmap.md`
	got := planFor(t, l, "item-icebox-doc")
	if got.Action != ActionHold || got.Reason != wantReason {
		t.Fatalf("item-icebox-doc decision = %+v, want Action=%q Reason=%q", got, ActionHold, wantReason)
	}
	notStarted := planFor(t, l, "item-not-started")
	if notStarted.Action != ActionHold || notStarted.Reason != ReasonNotReady {
		t.Fatalf("item-not-started decision = %+v, want %q", notStarted, ReasonNotReady)
	}
}

func TestPlanIceboxIgnoresOtherSectionsFragmentLinksAndActiveSpineCrossReferences(t *testing.T) {
	f, l := passFixture(t)
	for _, dir := range []string{"docs/pi", "docs/design"} {
		if err := os.MkdirAll(filepath.Join(f.root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, doc := range []string{
		"docs/design/live-config.md",
		"docs/design/review-comments.md",
		"docs/design/exploratory.md",
		"docs/design/auto-mode-classifier.md",
	} {
		writeForkFile(t, filepath.Join(f.root, doc), "# Doc\n")
	}
	writeForkFile(t, filepath.Join(f.root, "docs/pi/roadmap.md"), strings.Join([]string{
		"# Pi Roadmap",
		"",
		"## 1. Active",
		"",
		"[Live drain capacity](../design/live-config.md) — DECIDED.",
		"",
		"## Exploration",
		"",
		"- **Exploratory idea** ([design](../design/exploratory.md))",
		"",
		"## Icebox 🧊",
		"",
		"- **Multi-backend spillover** — out of scope for [live drain capacity](../design/live-config.md); see [OQ-RC4](../design/review-comments.md#OQ-RC4).",
		"",
		"| # | Question | Why parked |",
		"|---|---|---|",
		"| **I3** | [What judges the auto-mode gate?](../design/auto-mode-classifier.md) — decided to stay on session model | No user pain |",
		"",
		"## Notes After Icebox",
		"",
		"- **Post-icebox section** ([design](../design/exploratory.md))",
		"",
	}, "\n"))
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "add roadmap sections")

	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	questions := &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}}

	fileReq := func(words string) string {
		return fileFixtureRequestWith(t, f, words, "build", item.SingleWorkerWorkflow, []string{"docs/"}, false)
	}
	activeID := fileReq("Implement slice 1 of docs/design/live-config.md.\n")
	fragmentID := fileReq("Implement docs/design/review-comments.md.\n")
	exploreID := fileReq("Implement docs/design/exploratory.md.\n")
	tableIceboxID := fileReq("Implement docs/design/auto-mode-classifier.md.\n")

	lanes := []string{"lane-1", "lane-2", "lane-3", "lane-4"}
	decisions := planDecisions(t, f.storePath, Pass{Now: "2026-11-23T18:00:00Z", AvailableLanes: &lanes, StorePath: f.storePath}, l.cfg.Policy, questions)

	for name, id := range map[string]string{
		"active-spine-doc": activeID,
		"fragment-only":    fragmentID,
		"exploration-doc":  exploreID,
	} {
		if d := decisions[id]; d.Action != ActionDispatch {
			t.Errorf("%s (%s) = %+v, want dispatch", name, id, d)
		}
	}
	const wantTableReason = `icebox: "I3: What judges the auto-mode gate?" in docs/pi/roadmap.md`
	if d := decisions[tableIceboxID]; d.Action != ActionHold || d.Reason != wantTableReason {
		t.Fatalf("table icebox item = %+v, want Action=%q Reason=%q", d, ActionHold, wantTableReason)
	}
}

func TestPlanIceboxRespectsConfiguredSyncRoadmaps(t *testing.T) {
	f, l := passFixture(t)
	for _, dir := range []string{"docs/tillr", "docs/design"} {
		if err := os.MkdirAll(filepath.Join(f.root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeForkFile(t, filepath.Join(f.root, ".tillr.toml"), "[sync]\nroadmaps = [\"docs/tillr/roadmap.md\"]\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/status-bar-plan.md"), "# Status bar\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/tillr-autoqa.md"), "# AutoQA\n")
	writeForkFile(t, filepath.Join(f.root, "docs/roadmap.md"), "# Root Roadmap\n\n## Icebox\n\n- **The Ironbar bar** — [plan](design/status-bar-plan.md)\n")
	writeForkFile(t, filepath.Join(f.root, "docs/tillr/roadmap.md"), "# tillr Roadmap\n\n## Icebox\n\n- **AutoQA** — [design](../design/tillr-autoqa.md)\n")
	runGit(t, f.root, "add", ".tillr.toml", "docs")
	runGit(t, f.root, "commit", "-qm", "scope roadmaps to tillr")

	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	questions := &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}}

	ironbarID := fileFixtureRequestWith(t, f, "Finish docs/design/status-bar-plan.md.\n", "build", item.SingleWorkerWorkflow, []string{"docs/"}, false)
	autoqaID := fileFixtureRequestWith(t, f, "Finish docs/design/tillr-autoqa.md.\n", "build", item.SingleWorkerWorkflow, []string{"docs/"}, false)

	lanes := []string{"lane-1", "lane-2"}
	decisions := planDecisions(t, f.storePath, Pass{Now: "2026-11-23T18:00:00Z", AvailableLanes: &lanes, StorePath: f.storePath}, l.cfg.Policy, questions)

	if d := decisions[ironbarID]; d.Action != ActionDispatch {
		t.Fatalf("out-of-scope roadmap held item: %+v", d)
	}
	const wantReason = `icebox: "AutoQA" in docs/tillr/roadmap.md`
	if d := decisions[autoqaID]; d.Action != ActionHold || d.Reason != wantReason {
		t.Fatalf("in-scope roadmap item = %+v, want Action=%q Reason=%q", d, ActionHold, wantReason)
	}
}
