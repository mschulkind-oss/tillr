package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type missingVerifySteps struct {
	*verifyReturnSteps
	preflight, emptyOutput, brokenOutput bool
	exitCode                             *int
	missingRun, missingHead, nextHead    string
}

func (s *missingVerifySteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.NodeID == "verify" && s.missingRun != "" {
		target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
		s.nextHead = runGit(s.t, target, "rev-parse", "HEAD")
	}
	if req.NodeID != "verify" || s.missingRun != "" {
		return s.verifyReturnSteps.Run(ctx, req)
	}
	s.runs = append(s.runs, req)
	s.verifies++
	s.missingRun = req.RunID
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	s.missingHead = runGit(s.t, target, "rev-parse", "HEAD")
	log := "verifier exited without writing output\n"
	if s.preflight {
		log = "run-pi: refused: the brief still carries an unfilled template placeholder\n"
	}
	dir := run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID)
	path := filepath.Join(dir, "report.md")
	writeForkFile(s.t, path, log)
	if s.emptyOutput {
		writeForkFile(s.t, filepath.Join(dir, "eval.output"), "")
	}
	if s.brokenOutput {
		if err := os.Symlink("missing-target", filepath.Join(dir, "eval.output")); err != nil {
			return run.Record{}, "", err
		}
	}
	exitCode := s.exitCode
	if exitCode == nil {
		exitCode = countPtr(2)
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: exitCode, StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, log, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, log, err
}

func TestVerifyMissingVerdictReturnsToVerify(t *testing.T) {
	for _, preflight := range []bool{false, true} {
		for _, expanded := range []bool{false, true} {
			for _, cold := range []bool{false, true} {
				t.Run(fmt.Sprintf("preflight=%t/expanded=%t/cold=%t", preflight, expanded, cold), func(t *testing.T) {
					l, base, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
						return &verifyReturnSteps{workflowSteps: w, expanded: expanded}
					})
					s := &missingVerifySteps{verifyReturnSteps: base, preflight: preflight}
					l.cfg.Steps = s
					result := &LoopResult{}
					for i := 0; i < 30 && s.missingRun == ""; i++ {
						graphPass(t, l, result)
					}
					if s.missingRun == "" {
						t.Fatal("verify did not run")
					}
					var detail, step string
					fixtureRead(t, f, func(r *store.Reader) error {
						return r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.detail'),''), json_extract(payload,'$.step') FROM events WHERE kind=? AND object_id=?`, RefusalKind, s.missingRun).Scan(&detail, &step)
					})
					if step != RefusalVerifyNoVerdict {
						t.Errorf("refusal step = %q", step)
					}
					for _, want := range []string{s.missingRun, "exited with code 2 and wrote no verdict"} {
						if !strings.Contains(detail, want) {
							t.Errorf("refusal detail %q lacks %q", detail, want)
						}
					}
					fixtureRead(t, f, func(r *store.Reader) error {
						var findings int
						if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&findings); err != nil {
							return err
						}
						if findings != 0 {
							t.Errorf("absent output produced %d criterion findings", findings)
						}
						return nil
					})
					if preflight {
						if !strings.Contains(detail, "unfilled template placeholder") {
							t.Errorf("preflight words missing: %q", detail)
						}
						before := len(s.runs)
						for i := 0; i < 3; i++ {
							graphPass(t, l, result)
						}
						if len(s.runs) != before {
							t.Fatal("dispatched before clearance")
						}
						fixtureWrite(t, f, func(tx *store.Tx) error {
							faults, err := ReadItemFaults(tx.Reader())
							if err != nil {
								return err
							}
							fault, ok := faults["item-a"]
							if !ok || fault.Reason != ReasonAdapterRefused {
								t.Fatalf("fault=%+v", fault)
							}
							return ClearItemFault(tx, "item-a", fault.Seq, l.cfg.Now())
						})
					}
					if cold {
						offerColdLane(t, f, l, base)
					}
					for i := 0; i < 30 && s.verifies < 2; i++ {
						graphPass(t, l, result)
					}
					if s.verifies != 2 {
						t.Fatalf("verifies=%d plans=%d runs=%+v", s.verifies, s.plans, s.runs)
					}
					if s.plans != 1 || len(s.repairs) != 0 {
						t.Fatalf("plans=%d repairs=%d", s.plans, len(s.repairs))
					}
					if s.runs[len(s.runs)-1].NodeID != "verify" {
						t.Fatalf("next node=%s", s.runs[len(s.runs)-1].NodeID)
					}
					if s.nextHead != s.missingHead {
						t.Fatalf("candidate changed: %s -> %s", s.missingHead, s.nextHead)
					}
					if cold && s.runs[len(s.runs)-1].LaneID != "lane-2" {
						t.Errorf("second verify not on cold lane")
					}
					if result.InfraFailures != 0 {
						t.Errorf("infra failures=%d", result.InfraFailures)
					}
					fixtureRead(t, f, func(r *store.Reader) error {
						var closed int
						if err := r.QueryRow(`SELECT COALESCE(MAX(o.closes),0) FROM events e JOIN backend_outcomes o ON o.terminal_seq=e.seq WHERE e.object_id=? AND e.kind=?`, s.missingRun, run.FinishKind).Scan(&closed); err != nil {
							return err
						}
						if closed != 0 {
							t.Errorf("verify exit closed backend")
						}
						return nil
					})
					for i := 0; i < 30 && s.lands < 1; i++ {
						graphPass(t, l, result)
					}
					if s.lands != 1 || s.plans != 1 {
						fixtureRead(t, f, func(r *store.Reader) error {
							rows, err := r.Query(`SELECT object_id,payload FROM events WHERE kind=?`, RefusalKind)
							if err != nil {
								return err
							}
							defer rows.Close()
							for rows.Next() {
								var id, p string
								_ = rows.Scan(&id, &p)
								t.Logf("refusal %s %s", id, p)
							}
							return rows.Err()
						})
						t.Fatalf("land=%d plans=%d runs=%+v", s.lands, s.plans, s.runs)
					}
				})
			}
		}
	}
}

func TestMissingVerifyReportsActualExitCode(t *testing.T) {
	for _, code := range []int{0, 17} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			l, base, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps { return &verifyReturnSteps{workflowSteps: w} })
			s := &missingVerifySteps{verifyReturnSteps: base, exitCode: countPtr(code)}
			l.cfg.Steps = s
			result := &LoopResult{}
			for i := 0; i < 30 && s.missingRun == ""; i++ {
				graphPass(t, l, result)
			}
			if s.missingRun == "" {
				t.Fatal("verify did not run")
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var detail string
				if err := r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events WHERE kind=? AND object_id=?`, RefusalKind, s.missingRun).Scan(&detail); err != nil {
					return err
				}
				if !strings.Contains(detail, fmt.Sprintf("exited with code %d and wrote no verdict", code)) {
					t.Errorf("detail = %q", detail)
				}
				return nil
			})
			for i := 0; i < 30 && s.lands == 0; i++ {
				graphPass(t, l, result)
			}
			if s.lands != 1 || s.plans != 1 || s.nextHead != s.missingHead {
				t.Fatalf("land=%d plans=%d candidate=%s -> %s", s.lands, s.plans, s.missingHead, s.nextHead)
			}
		})
	}
}

func TestExistingUnreadableVerifyOutputIsNotMissing(t *testing.T) {
	l, base, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps { return &verifyReturnSteps{workflowSteps: w} })
	s := &missingVerifySteps{verifyReturnSteps: base, brokenOutput: true}
	l.cfg.Steps = s
	result := &LoopResult{}
	for i := 0; i < 30 && s.missingRun == ""; i++ {
		graphPass(t, l, result)
	}
	if s.missingRun == "" {
		t.Fatal("verify did not run")
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var step string
		if err := r.QueryRow(`SELECT json_extract(payload,'$.step') FROM events WHERE kind=? AND object_id=?`, RefusalKind, s.missingRun).Scan(&step); err != nil {
			return err
		}
		if step != ReasonJudgeUnreadable {
			t.Errorf("refusal step = %q, want %q", step, ReasonJudgeUnreadable)
		}
		return nil
	})
}

func TestExistingEmptyVerifyOutputIsNotMissing(t *testing.T) {
	l, base, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps { return &verifyReturnSteps{workflowSteps: w} })
	s := &missingVerifySteps{verifyReturnSteps: base, emptyOutput: true}
	l.cfg.Steps = s
	result := &LoopResult{}
	for i := 0; i < 30 && s.lands == 0; i++ {
		graphPass(t, l, result)
	}
	if s.lands != 1 {
		t.Fatalf("land=%d verifies=%d", s.lands, s.verifies)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND json_extract(payload,'$.step')=?`, RefusalKind, s.missingRun, RefusalVerifyNoVerdict).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Error("existing empty output was classified as absent")
		}
		return nil
	})
}
