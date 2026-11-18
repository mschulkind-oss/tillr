package drain

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const fixtureWords = "Please {{improve}} this feature.\r\n  Preserve my words. 雪\nInclude {{USER_WORDS}} and literal {{ braces.\n## Saved idea words (verbatim; 999 bytes)\n## Task\n"
const fixtureWordsPath = "docs/ideas/item-request.txt"

func ideaTurnFixture(t *testing.T) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := triageFixture(t)
	intake := filepath.Join(filepath.Dir(f.storePath), "intake", "item-request.txt")
	if err := os.MkdirAll(filepath.Dir(intake), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, intake, fixtureWords)
	if err := os.MkdirAll(filepath.Join(f.root, ".agents/skills/tillr-prose-style"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, ".agents/skills/tillr-prose-style/SKILL.md"), "Whole prose guide\nKeep the contract.\n")
	runGit(t, f.root, "add", ".agents")
	runGit(t, f.root, "commit", "-qm", "idea guide")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET title=?,seat='design',body_ref='',rank='am',allowed_paths='["docs/"]' WHERE item_id='item-request'`, strings.SplitN(fixtureWords, "\n", 2)[0]); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO idea_intake(item_id,words_hash,words_size,words_path,intake_path) VALUES('item-request',?,?,?,?)`, fmt.Sprintf("%x", sha256.Sum256([]byte(fixtureWords))), len(fixtureWords), fixtureWordsPath, intake)
		return err
	})
	return f, l
}

func TestIdeaStageCarriesWordsGuideAndInstructions(t *testing.T) {
	for _, node := range []bool{false, true} {
		t.Run(fmt.Sprint(node), func(t *testing.T) {
			f, l := ideaTurnFixture(t)
			d := findTriagePlan(t, l, "item-request").decision
			if d.Action != ActionDispatch || d.Seat != "design" || d.BlockConflict != nil {
				t.Fatalf("idea not routed directly: %+v", d)
			}
			s := &LaneStager{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
			spec := StageSpec{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Backend: d.RunRequest.Backend, WorkingDir: WorktreeDir, BaseSHA: runGit(t, f.root, "rev-parse", TrunkRef)}
			if node {
				spec.Node = &WorkflowStage{ID: "design", Kind: "agent", Seat: "design", Access: "write", Prompt: "Shape the idea", WriteScope: WorkflowScope{Paths: []string{"docs/"}}}
			}
			if err := s.Stage(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(f.laneRoot, d.LaneID, BriefFile))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{fixtureWords, fixtureWordsPath, "Whole prose guide\nKeep the contract.", "1. Copy the words", `from="item-request"`, "3. Write a design document", "Route it from a roadmap", "4. File every decision", "Do not wait and do not guess"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("brief missing %q", want)
				}
			}
			if !strings.HasSuffix(string(raw), fixtureWords) {
				t.Fatal("words suffix altered")
			}
			preflight, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
			if err != nil {
				t.Fatal(err)
			}
			brief := filepath.Join(f.laneRoot, d.LaneID, BriefFile)
			probe := func() error {
				cmd := exec.Command("sh", "-c", `. "$1"; PROG=probe; preflight_brief "$2"`, "preflight", preflight, brief)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("%v: %s", err, out)
				}
				return nil
			}
			if err := probe(); err != nil {
				t.Fatalf("adapter rejected saved words: %v", err)
			}
			// Neither a wrong byte count nor headings in the words can hide bad template input.
			for _, bad := range []string{string(raw) + "extra", strings.Replace(string(raw), "## Task", "## Removed task", 1), strings.Replace(string(raw), "## Task", "## Task\n{{UNFILLED}}", 1)} {
				if err := os.WriteFile(brief, []byte(bad), 0600); err != nil {
					t.Fatal(err)
				}
				if err := probe(); err == nil {
					t.Fatal("adapter accepted invalid template/suffix")
				}
			}
		})
	}
}

func ideaOutcomeBlock(id string) string {
	return strings.Replace(strings.Replace(triageBlock, "item-declared", id, 1), `prose="2"`, `prose="2" from="item-request"`, 1)
}

func TestIdeaCandidateChecksBeforeGate(t *testing.T) {
	for _, change := range []string{"words-missing", "words-altered", "idea-no-outcome", "existing-outcome", "marked-existing", "stored-existing", "quoted-outcome", "valid"} {
		t.Run(change, func(t *testing.T) {
			f, l := ideaTurnFixture(t)
			if change == "existing-outcome" || change == "marked-existing" {
				old := ideaOutcomeBlock("item-result")
				if change == "marked-existing" {
					old = strings.Replace(old, ` from="item-request"`, "", 1)
				}
				writeForkFile(t, filepath.Join(f.root, "docs/existing.md"), old)
				runGit(t, f.root, "add", "docs")
				runGit(t, f.root, "commit", "-qm", "existing outcome")
			}
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			target := l.laneTarget("lane-1")
			runGit(t, target, "reset", "--hard", base)
			if change != "words-missing" {
				if err := os.MkdirAll(filepath.Join(target, "docs/ideas"), 0755); err != nil {
					t.Fatal(err)
				}
				words := fixtureWords
				if change == "words-altered" {
					words += "altered"
				}
				writeForkFile(t, filepath.Join(target, fixtureWordsPath), words)
			}
			outcome := ideaOutcomeBlock("item-result")
			if change == "stored-existing" {
				outcome = ideaOutcomeBlock("item-declared")
			}
			if change == "idea-no-outcome" {
				outcome = triageBlock
			}
			if change == "quoted-outcome" {
				outcome = "```markdown\n" + outcome + "```\n"
			}
			writeForkFile(t, filepath.Join(target, "docs/outcome.md"), outcome)
			runGit(t, target, "add", "docs")
			runGit(t, target, "commit", "-qm", "idea outcome")
			writeForkFile(t, filepath.Join(f.laneRoot, "lane-1", FenceFile), "docs/\n")
			c := Candidate{ItemID: "item-request", RunID: "run-idea-1", LaneID: "lane-1", Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD")}
			spy := &triageGateSpy{}
			l.cfg.Steps = spy
			_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
			if change == "valid" {
				if err != nil || !landed || spy.gates != 1 {
					t.Fatalf("valid: gates=%d landed=%v err=%v", spy.gates, landed, err)
				}
				return
			}
			want := change
			if change == "existing-outcome" || change == "marked-existing" || change == "stored-existing" || change == "quoted-outcome" {
				want = "idea-no-outcome"
			}
			if err != nil || landed || spy.gates != 0 || !strings.HasPrefix(c.RefusalDetail, want) {
				t.Fatalf("refusal: gates=%d landed=%v detail=%q err=%v", spy.gates, landed, c.RefusalDetail, err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var step string
				err := tx.Reader().QueryRow(`SELECT json_extract(payload,'$.step') FROM events WHERE kind='drain.refusal' ORDER BY seq DESC LIMIT 1`).Scan(&step)
				if step != want {
					t.Errorf("step %s want %s", step, want)
				}
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id,state) VALUES('lane-1','idle')`); err != nil {
					return err
				}
				_, err = tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,?,'lane-1','fixture','finished',1000,2000,'[]',(SELECT MAX(seq) FROM events))`, c.RunID, c.ItemID)
				return err
			})
			d := findTriagePlan(t, l, c.ItemID).decision
			if d.Action != ActionHold || d.Reason != want {
				t.Fatalf("lost named refusal: %+v", d)
			}
		})
	}
}

type ideaLandingSteps struct {
	triageGateSpy
	t *testing.T
	f refusalLoopFixture
	l *Loop
}

func (s *ideaLandingSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	runGit(s.t, s.f.root, "merge", "--ff-only", c.HeadSHA)
	return LandOutcome{Result: LandLanded, TrunkAfter: c.HeadSHA}, nil
}
func (s *ideaLandingSteps) ItemState(ctx context.Context, id, runID, state string, waiting ...item.WaitingOn) error {
	// The external state writer is a fixture boundary; observe exactly what the loop asks.
	if id != "item-request" || state != StateLanded {
		s.t.Fatalf("unexpected settlement %s %s", id, state)
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, state, id)
		return err
	})
	return nil
}
func (s *ideaLandingSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	docs, err := item.ReadSyncDocuments(s.f.root)
	if err != nil {
		return SyncOutcome{}, err
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
		report, err := item.Sync(ctx, tx.Reader(), tx, item.SyncInput{Root: s.f.root, Documents: docs, At: s.l.cfg.Now()})
		if len(report.Findings) != 0 {
			s.t.Fatalf("sync findings: %+v", report.Findings)
		}
		return err
	})
	return SyncOutcome{}, nil
}

func TestIdeaLandingImportsOutcomesAtItsPlace(t *testing.T) {
	for _, question := range []bool{false, true} {
		t.Run(fmt.Sprint(question), func(t *testing.T) {
			f, l := ideaTurnFixture(t)
			// Existing items bracket the idea, so appending outcomes would be visibly wrong.
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET rank='z' WHERE item_id='item-declared'`)
				return err
			})
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			target := l.laneTarget("lane-1")
			runGit(t, target, "reset", "--hard", base)
			if err := os.MkdirAll(filepath.Join(target, "docs/ideas"), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(target, fixtureWordsPath), fixtureWords)
			body := ideaOutcomeBlock("item-result-z")
			if question {
				body += "\n## Open questions\n\n1. 💬 **OQ-1: Which behavior?**\n\n<!-- vantage: question id=OQ-1 -->\n\nThe owner must choose.\n"
			} else {
				body += strings.Replace(ideaOutcomeBlock("item-result-a"), "Add the requested feature", "Add the second feature", 1)
			}
			writeForkFile(t, filepath.Join(target, "docs/outcome.md"), body)
			runGit(t, target, "add", "docs")
			runGit(t, target, "commit", "-qm", "idea outcomes")
			writeForkFile(t, filepath.Join(f.laneRoot, "lane-1", FenceFile), "docs/\n")
			steps := &ideaLandingSteps{t: t, f: f, l: l}
			l.cfg.Steps = steps
			c := Candidate{ItemID: "item-request", RunID: "run-idea-1", LaneID: "lane-1", Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD")}
			_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
			if err != nil || !landed || steps.gates != 1 {
				t.Fatalf("landing: %v %v gates=%d", landed, err, steps.gates)
			}
			if question {
				l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", `printf '%s' '{"index":{"documents":[{"path":"docs/outcome.md","questions":[{"id":"OQ-1","state":"open","title":"Which behavior?"}]}]}}'`}}
			}
			if err := l.syncDocuments(context.Background(), &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var state string
				if err := tx.Reader().QueryRow(`SELECT state FROM items WHERE item_id='item-request'`).Scan(&state); err != nil {
					return err
				}
				if state != StateLanded {
					t.Fatalf("idea state %s", state)
				}
				rows, err := tx.Reader().Query(`SELECT item_id FROM items WHERE state!='landed' ORDER BY rank,item_id`)
				if err != nil {
					return err
				}
				defer rows.Close()
				var ids []string
				for rows.Next() {
					var id string
					if err := rows.Scan(&id); err != nil {
						return err
					}
					ids = append(ids, id)
				}
				want := []string{"item-result-z", "item-result-a", "item-declared"}
				if question {
					want = []string{"item-result-z", "item-declared"}
				}
				if fmt.Sprint(ids) != fmt.Sprint(want) {
					t.Fatalf("order %v want %v", ids, want)
				}
				return rows.Err()
			})
			for _, id := range []string{"item-result-z", "item-result-a"} {
				if question && id == "item-result-a" {
					continue
				}
				d := findTriagePlan(t, l, id).decision
				if question {
					if d.Reason != ReasonQuestionOpen || d.Action != ActionHold {
						t.Fatalf("question not held: %+v", d)
					}
				} else if d.Action != ActionDispatch {
					t.Fatalf("outcome not ready: %+v", d)
				}
			}
		})
	}
}
