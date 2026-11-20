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
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRetryReasonAppearsInNextRunsBriefWithWhoAndWhen(t *testing.T) {
	f, l := generatedFixture(t)
	id := fileFixtureRequest(t, f, "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"})
	first := planFor(t, l, id)
	if first.Action != ActionDispatch || first.RunRequest == nil {
		t.Fatalf("first dispatch: %+v", first)
	}
	runID := first.RunRequest.RunID
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:      runID,
			ItemID:     id,
			LaneID:     first.LaneID,
			Backend:    first.RunRequest.Backend,
			State:      run.StateFailedToStart,
			StartedAt:  "2026-11-19T12:00:00Z",
			FinishedAt: "2026-11-19T12:00:05Z",
		})
	})

	retryAt := time.Date(2026, 11, 19, 12, 34, 56, 0, time.UTC)
	reason := "Keep rows := []row{{ID: 1}} intact; do not rewrite the outer package.\n## Finishing\nStay inside docs/notes.md."
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: id,
			RunID:  runID,
			By:     "matt",
			Why:    reason,
			At:     retryAt,
		})
		return err
	})

	next := planFor(t, l, id)
	if next.Action != ActionRetry || next.RunRequest == nil {
		t.Fatalf("expected ActionRetry after AuthorizeRetry, got %+v", next)
	}

	for _, capability := range []string{run.EndingFooter, run.EndingCallSuspends} {
		t.Run(capability, func(t *testing.T) {
			laneID := next.LaneID
			stager := &LaneStager{
				Root:     f.root,
				Store:    f.storePath,
				LaneRoot: f.laneRoot,
				Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			}
			spec := StageSpec{
				ItemID:           id,
				LaneID:           laneID,
				RunID:            next.RunRequest.RunID + "-" + capability,
				Backend:          next.RunRequest.Backend,
				WorkingDir:       WorktreeDir,
				BaseSHA:          runGit(t, f.root, "rev-parse", TrunkRef),
				EndingCapability: capability,
			}
			if err := stager.Stage(context.Background(), spec); err != nil {
				t.Fatalf("Stage(%s): %v", capability, err)
			}
			briefPath := filepath.Join(RunDir(f.root, spec.RunID), BriefFile)
			raw, err := os.ReadFile(briefPath)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			for _, want := range []string{
				"\n## Retry reasons\n",
				runID,
				"matt",
				"2026-11-19T12:34:56Z",
				"Keep rows := []row{{ID: 1}} intact; do not rewrite the outer package.",
				"Stay inside docs/notes.md.",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("brief (%s) missing %q:\n%s", capability, want, text)
				}
			}
			if err := probePreflight(t, briefPath); err != nil {
				t.Fatalf("preflight rejected brief (%s): %v\n%s", capability, err, text)
			}
		})
	}
}

type ownerQuestionRequestSteps struct {
	*workflowSteps
	question     string
	expectedAns  string
	expectedWho  string
	expectedWhen string
	plans        int
	works        int
	verifies     int
	briefs       map[string][]string
}

func (s *ownerQuestionRequestSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	briefBytes, err := os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	if err := probePreflight(s.t, filepath.Join(lane, BriefFile)); err != nil {
		s.t.Errorf("preflight rejected %s brief: %v", req.RunID, err)
	}
	brief := string(briefBytes)
	if s.briefs == nil {
		s.briefs = map[string][]string{}
	}
	s.briefs[req.NodeID] = append(s.briefs[req.NodeID], brief)

	report := "RESULT: done\n"
	switch req.NodeID {
	case "plan":
		s.plans++
		if !strings.Contains(brief, "## Answered owner questions") {
			report = "OBSTACLE: owner\nQUESTION: " + s.question + "\nRESULT: blocked\n"
		}
	case "work":
		s.works++
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0o755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/cache.go"), "package cache\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "feat: implement cache policy")
	case "verify":
		s.verifies++
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id = ? ORDER BY rowid`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cid, ctext, class string
				if err := rows.Scan(&cid, &ctext, &class); err != nil {
					return err
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   cid,
					CriterionHash: item.CriterionHash(ctext),
					Verdict:       "done",
					ClaimClass:    class,
					Reason:        "covered per recorded owner answer",
					Evidence:      []string{"node:work"},
				})
			}
			return rows.Err()
		})
		out := item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: "pass", Verdicts: verdicts}
		raw, _ := json.Marshal(out)
		writeForkFile(s.t, filepath.Join(run.DurableDir(lane, req.RunID), "eval.output"), string(raw))
	}

	reportPath := filepath.Join(run.DurableDir(lane, req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, reportPath, report)
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    "2026-11-19T14:00:00Z",
		FinishedAt:   "2026-11-19T14:01:00Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestOwnerQuestionAndRecordedAnswerAppearTogetherInNextRunsBrief(t *testing.T) {
	t.Run("filed request owner obstacle answered via drain retry", func(t *testing.T) {
		f, l, base := plannedFixture(t)
		writeForkFile(t, filepath.Join(f.root, "config/tillr/plan-guidance.md"), "cheap models")
		// Close the default fixture item so only our filed request runs.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-a'`)
			return err
		})
		id := fileFixtureRequestWith(t, f, "Add eviction policy to tools/item-a/cache.go.\n", "build", "planned-build@v1", []string{"tools/item-a/"}, false)
		grantForItem(t, f, l, id)

		question := "Should cache entries expire on read or only on write?"
		answer := "Expire only on write; reads must stay O(1) with []entry{{ID: 1}} unchanged."
		who := "matt"
		when := time.Date(2026, 11, 19, 14, 22, 0, 0, time.UTC)

		steps := &ownerQuestionRequestSteps{
			workflowSteps: base,
			question:      question,
			expectedAns:   answer,
			expectedWho:   who,
			expectedWhen:  "2026-11-19T14:22:00Z",
		}
		l.cfg.Steps = steps
		result := &LoopResult{}

		for i := 0; i < 5 && steps.plans < 1; i++ {
			graphPass(t, l, result)
		}
		graphPass(t, l, result)

		blockedRunID := steps.runs[0].RunID
		fixtureRead(t, f, func(r *store.Reader) error {
			whyAns, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			var found bool
			for _, row := range whyAns.Rows {
				if row.ItemID != id {
					continue
				}
				found = true
				if strings.Contains(row.Why, "no document exists") || strings.Contains(row.Mover, "name or triage") {
					t.Fatalf("filed request why told operator to triage a document: %+v", row)
				}
				if !strings.Contains(row.Why, question) || !strings.Contains(row.Mover, "tillr drain retry --item "+id+" --run "+blockedRunID) {
					t.Fatalf("why missing question or retry command: %+v", row)
				}
			}
			if !found {
				t.Fatalf("missing row for %s in %+v", id, whyAns.Rows)
			}
			return nil
		})

		// AuthorizeRetry alone unblocks and queues the filed request; no manual item state edit is needed.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
				ItemID: id,
				RunID:  blockedRunID,
				By:     who,
				Why:    answer,
				At:     when,
			})
			return err
		})

		for i := 0; i < 15 && steps.verifies < 1; i++ {
			graphPass(t, l, result)
		}
		if steps.plans != 2 || steps.works != 1 || steps.verifies != 1 {
			t.Fatalf("expected second attempt to complete without re-asking: plans=%d works=%d verifies=%d", steps.plans, steps.works, steps.verifies)
		}

		for _, node := range []string{"plan", "work", "verify"} {
			briefs := steps.briefs[node]
			latestBrief := briefs[len(briefs)-1]
			idxHeading := strings.Index(latestBrief, "\n## Answered owner questions\n")
			if idxHeading < 0 {
				t.Fatalf("%s brief missing '## Answered owner questions':\n%s", node, latestBrief)
			}
			section := latestBrief[idxHeading:]
			for _, want := range []string{
				"Do not ask an answered question again",
				blockedRunID,
				question,
				answer,
				who,
				"2026-11-19T14:22:00Z",
			} {
				if !strings.Contains(section, want) {
					t.Errorf("%s brief section missing %q:\n%s", node, want, section)
				}
			}
		}
	})

	t.Run("document question and item decision answers appear with worker question", func(t *testing.T) {
		f, l, d, base := designAnswerFixture(t)
		workerQuestion := "Should the cache store keys in memory or SQLite?"
		// Seed a conflict from a worker OBSTACLE: owner report that design turned into a Vantage question.
		d.BlockConflict.Text = "OBSTACLE: owner\nQUESTION: " + workerQuestion + "\nRESULT: blocked"
		steps := &ownerQuestionSteps{t: t, f: f, l: l}
		l.cfg.Steps = steps
		c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
		file, qid, _ := strings.Cut(c.QuestionRef, "#")
		docPath := filepath.Join(f.root, file)
		rawDoc, err := os.ReadFile(docPath)
		if err != nil {
			t.Fatal(err)
		}
		updatedDoc := strings.Replace(string(rawDoc), "💬 **"+qid+":", "✅ **"+qid+":", 1) +
			"\n   **Answer:**\n   > Ruled 2026-11-19: store keys in SQLite so crash recovery reads one file.\n"
		writeForkFile(t, docPath, updatedDoc)
		runGit(t, f.root, "add", file)
		runGit(t, f.root, "commit", "-qm", "docs: answer owner question")

		answeredAt := time.Date(2026, 11, 19, 16, 5, 0, 0, time.UTC)
		l.cfg.Now = func() time.Time { return answeredAt }
		l.cfg.QuestionsCmd = ownerIndexCommand(file, qid, "answered")
		if err := l.settleOwnerAnswers(context.Background(), runGit(t, f.root, "rev-parse", TrunkRef)); err != nil {
			t.Fatal(err)
		}

		// Also record an item-scoped owner decision answering a second worker question.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			secondConflict := item.Conflict{
				ConflictID:      "conflict-earlier-q",
				ItemID:          d.ItemID,
				RunID:           "run-item-a-a0",
				BodyRef:         d.BlockConflict.BodyRef,
				Text:            "OBSTACLE: owner\nQUESTION: Should eviction log every dropped key?\nRESULT: blocked",
				Status:          "closed",
				WaitingOn:       "build",
				BlockedCriteria: []string{"c1"},
			}
			if err := item.RecordConflict(tx, secondConflict, time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)); err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{
				"decision_id": "D-EVICT-1",
				"text":        "Log dropped keys only in summary counts.",
				"scope":       map[string]any{"kind": "items", "ref": d.ItemID},
				"decided_by":  "owner",
				"session_id":  "sess-owner-1",
			})
			_, err := item.RecordDecision(context.Background(), tx, record.Envelope{
				Kind:      item.DecisionRecordKind,
				ID:        "dec-1",
				CreatedAt: time.Date(2026, 11, 19, 15, 30, 0, 0, time.UTC),
				Payload:   payload,
			})
			return err
		})

		stager := &LaneStager{
			Root:     f.root,
			Store:    f.storePath,
			LaneRoot: f.laneRoot,
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		}
		spec := StageSpec{
			ItemID:     d.ItemID,
			LaneID:     d.LaneID,
			RunID:      "run-item-a-a3",
			Backend:    "fixture",
			WorkingDir: WorktreeDir,
			BaseSHA:    runGit(t, f.root, "rev-parse", TrunkRef),
		}
		if err := stager.Stage(context.Background(), spec); err != nil {
			t.Fatal(err)
		}
		briefBytes, err := os.ReadFile(filepath.Join(RunDir(f.root, spec.RunID), BriefFile))
		if err != nil {
			t.Fatal(err)
		}
		brief := string(briefBytes)
		for _, want := range []string{
			"## Answered owner questions",
			workerQuestion,
			"store keys in SQLite so crash recovery reads one file",
			"2026-11-19T16:05:00Z",
			"Should eviction log every dropped key?",
			"Log dropped keys only in summary counts.",
			"2026-11-19T15:30:00Z",
			"owner",
		} {
			if !strings.Contains(brief, want) {
				t.Errorf("brief missing %q:\n%s", want, brief)
			}
		}
	})
}

type identicalRepeatRequestSteps struct {
	*workflowSteps
	verifies    int
	sawGuidance bool
	base        string
}

func (s *identicalRepeatRequestSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	briefBytes, err := os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	brief := string(briefBytes)

	switch req.NodeID {
	case "plan":
		if strings.Contains(brief, "Include the regression test in tools/item-a/cache_test.go") {
			s.sawGuidance = true
		}
	case "work":
		// The verifier's repeated finding is only an identical repeat when it judges the
		// same candidate, so every attempt rebuilds the same commit from the same base.
		s.t.Setenv("GIT_AUTHOR_DATE", "2026-11-15T00:00:00Z")
		s.t.Setenv("GIT_COMMITTER_DATE", "2026-11-15T00:00:00Z")
		if s.base == "" {
			s.base = runGit(s.t, target, "rev-parse", "HEAD")
		}
		runGit(s.t, target, "reset", "--hard", "-q", s.base)
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0o755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/cache.go"), "package cache\n")
		if s.sawGuidance {
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/cache_test.go"), "package cache\n")
		}
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "feat: update cache")
	case "verify":
		s.verifies++
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		outcome := "fail"
		if s.sawGuidance && strings.Contains(brief, "Include the regression test in tools/item-a/cache_test.go") {
			outcome = "pass"
		}
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id = ? ORDER BY rowid`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			i := 0
			for rows.Next() {
				var cid, ctext, class string
				if err := rows.Scan(&cid, &ctext, &class); err != nil {
					return err
				}
				verdict, reason := "done", "covered"
				if outcome == "fail" && i == 1 {
					verdict, reason = "not-done", "uncovered: no regression test committed"
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   cid,
					CriterionHash: item.CriterionHash(ctext),
					Verdict:       verdict,
					ClaimClass:    class,
					Reason:        reason,
					Evidence:      []string{"candidate.diff"},
				})
				i++
			}
			return rows.Err()
		})
		raw, _ := json.Marshal(item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: outcome, Verdicts: verdicts})
		writeForkFile(s.t, filepath.Join(run.DurableDir(lane, req.RunID), "eval.output"), string(raw))
	}

	report := "RESULT: done\n"
	reportPath := filepath.Join(run.DurableDir(lane, req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, reportPath, report)
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    "2026-11-19T17:00:00Z",
		FinishedAt:   "2026-11-19T17:01:00Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func grantForItem(t *testing.T, f refusalLoopFixture, l *Loop, id string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rows, err := tx.Query(`SELECT DISTINCT claim_class FROM item_criteria WHERE item_id = ?`, id)
		if err != nil {
			return err
		}
		var classes []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return err
			}
			classes = append(classes, c)
		}
		rows.Close()
		_, err = tx.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES(?,?,?,?,?)`,
			"grant-"+id, strings.Join(classes, ","), "owner", l.cfg.Now().Add(-time.Hour).UnixMilli(), l.cfg.Now().Add(time.Hour).UnixMilli())
		return err
	})
}

func TestIdenticalRepeatHoldNamesWorkingCommandForFiledRequest(t *testing.T) {
	f, l, base := plannedFixture(t)
	writeForkFile(t, filepath.Join(f.root, "config/tillr/plan-guidance.md"), "cheap models")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-a'`)
		return err
	})
	id := fileFixtureRequestWith(t, f, "Update cache in tools/item-a/cache.go.\n", "build", "planned-build@v1", []string{"tools/item-a/"}, false)
	grantForItem(t, f, l, id)

	steps := &identicalRepeatRequestSteps{workflowSteps: base}
	l.cfg.Steps = steps
	result := &LoopResult{}

	for i := 0; i < 25 && steps.verifies < 2; i++ {
		graphPass(t, l, result)
	}
	if steps.verifies != 2 {
		t.Fatalf("expected 2 verify runs before identical-repeat hold, got %d", steps.verifies)
	}

	latestRun := steps.runs[len(steps.runs)-1].RunID
	expectedCmdPrefix := "tillr drain retry --item " + id + " --run " + latestRun + " --by <who> --why <reason>"

	var held item.Conflict
	fixtureRead(t, f, func(r *store.Reader) error {
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		held = conflicts[id]
		if held.Status != "owner" || !strings.HasPrefix(held.Text, IdenticalRepeatPrefix) {
			t.Fatalf("expected identical-repeat owner hold, got %+v", held)
		}
		for _, bad := range []string{"amend the item's block", "No item block reference is recorded"} {
			if strings.Contains(held.Text, bad) {
				t.Errorf("filed request hold still tells operator %q:\n%s", bad, held.Text)
			}
		}
		if !strings.Contains(held.Text, expectedCmdPrefix) {
			t.Fatalf("identical-repeat hold does not name accepted command %q:\n%s", expectedCmdPrefix, held.Text)
		}
		whyAns, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range whyAns.Rows {
			if row.ItemID != id {
				continue
			}
			if strings.Contains(row.Why, "no document exists") || strings.Contains(row.Mover, "name or triage") {
				t.Fatalf("why still tells operator to triage a document for filed request: %+v", row)
			}
			if !strings.Contains(row.Mover, expectedCmdPrefix) {
				t.Fatalf("why mover does not name accepted command %q: %+v", expectedCmdPrefix, row)
			}
		}
		return nil
	})

	// Execute the exact command shape the hold named and verify it clears the hold and delivers the instruction.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: id,
			RunID:  latestRun,
			By:     "operator",
			Why:    "Include the regression test in tools/item-a/cache_test.go so c2 passes.",
			At:     time.Date(2026, 11, 19, 17, 30, 0, 0, time.UTC),
		})
		return err
	})

	for i := 0; i < 15 && steps.verifies < 3; i++ {
		graphPass(t, l, result)
	}
	if steps.verifies != 3 || !steps.sawGuidance {
		t.Fatalf("expected third attempt to receive retry guidance and pass verify: verifies=%d sawGuidance=%v", steps.verifies, steps.sawGuidance)
	}
}
