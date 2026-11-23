package drain

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAllDefinedHoldReasonsAreClassified(t *testing.T) {
	// Success/settlement reasons share the Reason prefix but never hold work.
	nonHolds := map[string]bool{
		"ready": true, "failover": true, "seat-degraded": true,
		ReasonRetry: true, "conflict-answered": true, ReasonReturned: true,
		"item-already-terminal": true,
		ReasonRestagedStaleBase: true,
		// Dispatch reasons of the runs that answer a repeating-call ending.
		ReasonRepeatingCallFresh: true,
	}
	packages, err := parser.ParseDir(token.NewFileSet(), ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, file := range packages["drain"].Files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checkLiteral := func(expr ast.Expr) {
			ast.Inspect(expr, func(n ast.Node) bool {
				if literal, ok := n.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					value, _ := strconv.Unquote(literal.Value)
					if value != "" && value != ": " && value != "\n" {
						t.Errorf("bare literal hold %q in %s; use a named constant", value, name)
					}
				}
				return true
			})
		}
		ast.Inspect(file, func(n ast.Node) bool {
			// Catch separately written Action and Reason assignments, not only tuples.
			if block, ok := n.(*ast.BlockStmt); ok {
				held := map[string]bool{}
				for _, stmt := range block.List {
					assign, ok := stmt.(*ast.AssignStmt)
					if !ok || len(assign.Lhs) != len(assign.Rhs) {
						continue
					}
					for i, lhs := range assign.Lhs {
						field, ok := lhs.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						owner, ok := field.X.(*ast.Ident)
						if !ok {
							continue
						}
						if field.Sel.Name == "Action" {
							action, ok := assign.Rhs[i].(*ast.Ident)
							held[owner.Name] = ok && action.Name == "ActionHold"
						}
						if field.Sel.Name == "Reason" && held[owner.Name] {
							checkLiteral(assign.Rhs[i])
						}
					}
				}
			}
			if assign, ok := n.(*ast.AssignStmt); ok && len(assign.Lhs) == len(assign.Rhs) {
				holds := false
				for _, expr := range assign.Rhs {
					if id, ok := expr.(*ast.Ident); ok && id.Name == "ActionHold" {
						holds = true
					}
				}
				if holds {
					for i, lhs := range assign.Lhs {
						if field, ok := lhs.(*ast.SelectorExpr); ok && field.Sel.Name == "Reason" {
							checkLiteral(assign.Rhs[i])
						}
					}
				}
			}
			if literal, ok := n.(*ast.CompositeLit); ok {
				holds := false
				var reason ast.Expr
				for _, expr := range literal.Elts {
					if entry, ok := expr.(*ast.KeyValueExpr); ok {
						key, ok := entry.Key.(*ast.Ident)
						if !ok {
							continue
						}
						if key.Name == "Action" {
							if id, ok := entry.Value.(*ast.Ident); ok && id.Name == "ActionHold" {
								holds = true
							}
						}
						if key.Name == "Reason" {
							reason = entry.Value
						}
					}
				}
				if holds && reason != nil {
					checkLiteral(reason)
				}
			}
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, id := range spec.Names {
				if !strings.HasPrefix(id.Name, "Reason") || i >= len(spec.Values) {
					continue
				}
				literal, ok := spec.Values[i].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				reason, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				_, landing := landingHolds[reason]
				_, general := generalHolds[reason]
				if landing && general {
					t.Errorf("%s is classified twice", reason)
				}
				if !landing && !general && !nonHolds[reason] {
					t.Errorf("%s (%s) is an unclassified reason", reason, name)
				}
			}
			return true
		})
	}
	for name, reason := range allExportedReasons(t) {
		_, landing := landingHolds[reason]
		_, general := generalHolds[reason]
		if landing && general {
			t.Errorf("%s (%s) is classified twice", reason, name)
		}
		if !landing && !general && !nonHolds[reason] {
			t.Errorf("%s (%s) is an unclassified reason", reason, name)
		}
	}
}

func TestDefinitionReplacementInvalidatesPlannedDispatch(t *testing.T) {
	f, l := passFixture(t, "item-a")
	plans, _, err := l.passPlans(context.Background(), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].decision.Action != ActionDispatch {
		t.Fatalf("initial: %+v", plans[0])
	}
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.States = append(d.States, "custom-ready")
	d.Ready = "custom-ready"
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
	ok, _, err := l.applyPlan(context.Background(), plans[0], runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("claimed dispatch planned under the replaced definition")
	}
}

func TestConflictAnswerUsesDefinitionBlockedState(t *testing.T) {
	f, l := designQuotaFixture(t)
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := defaultDefinition()
	d.States = append(d.States, "waiting-for-answer")
	d.Blocked = "waiting-for-answer"
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='item-a'`, d.Blocked)
		return err
	})
	got := mustPlan(t, l)[0].decision
	if got.Action != ActionDispatch || got.Seat != "design" || got.ConflictID != "conflict-a" {
		t.Fatalf("custom blocked state lost its conflict answer: %+v", got)
	}
	d.Completes, d.Prerequisites = "hook", nil
	d.Hooks = &definition.Hooks{Argv: []string{"hook"}, Names: []string{"complete-item"}}
	raw, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
	got = mustPlan(t, l)[0].decision
	if got.Action != ActionHold || got.Reason != ReasonBlocked {
		t.Fatalf("hook definition routed a landing conflict: %+v", got)
	}
}

func TestCommentDispatchSkipsUnselectedPrerequisite(t *testing.T) {
	_, l := passFixture(t, "item-comments-example")
	d := defaultDefinition()
	d.Prerequisites = nil
	l.cfg.VantageURL = "http://127.0.0.1:1"
	ready, closed, err := l.checkCommentDispatch(context.Background(), plannedApplication{
		decision: Decision{ItemID: "item-comments-example"}, definition: &d,
	}, l.cfg.Now())
	if err != nil || !ready || closed {
		t.Fatalf("unselected comment prerequisite checked: ready=%v closed=%v err=%v", ready, closed, err)
	}
}

func TestDocumentOnlyPrerequisiteFailsClosed(t *testing.T) {
	d := defaultDefinition()
	d.Prerequisites = []string{definition.PrerequisiteDesignDocument}
	p := planner{definition: &d}
	row := itemRow{ItemID: "item-a", DesignDoc: "docs/a.md"}
	if got := p.questionState(row); got.reason != ReasonDesignDocMissing {
		t.Fatalf("absence of a reader admitted unchecked document: %+v", got)
	}
}

func TestLandingHoldsRequireLandingDefinition(t *testing.T) {
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	if holdEnabled(d, ReasonNoFence+": detail") {
		t.Error("default Git definition enables the legacy fence prerequisite")
	}
	d.Prerequisites = append(d.Prerequisites, definition.PrerequisiteFence)
	for reason := range landingHolds {
		if !holdEnabled(d, reason+": detail") {
			t.Errorf("Git definition lost %s", reason)
		}
	}
	d.Completes = "hook"
	for reason := range landingHolds {
		if holdEnabled(d, reason+": detail") {
			t.Errorf("hook definition permits %s", reason)
		}
	}
	d.Completes, d.Prerequisites = "landing", nil
	for _, reason := range []string{ReasonNoFence, ReasonTrunkPathDirty, ReasonQuestionOpen, ReasonQuestionsUnknown, ReasonDesignDocMissing, "design-under-comment", ReasonTriageNoBlock} {
		if holdEnabled(d, reason) {
			t.Errorf("unselected prerequisite permits %s", reason)
		}
	}
}

func TestPendingLandingHoldsAreDefinitionScoped(t *testing.T) {
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Completes, d.Prerequisites = "hook", nil
	original := &pendingWork{RepairHold: &repairHold{}, GateStall: &gateStallHold{}, RedCapHold: &redCapHold{}, DirtyHold: &dirtyHold{}, Gated: true}
	got := definitionWork(d, original)
	if got.RepairHold != nil || got.GateStall != nil || got.RedCapHold != nil || got.DirtyHold != nil || got.Gated {
		t.Fatalf("landing holds retained: %+v", got)
	}
	if original.RepairHold == nil || original.DirtyHold == nil || !original.Gated {
		t.Fatal("mutated pending work")
	}
}

func TestDurableLandingHoldRejectsHookDefinition(t *testing.T) {
	f, l := passFixture(t, "item-a")
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Name, d.Completes = "findings", "hook"
	d.Hooks = &definition.Hooks{Argv: []string{"hook"}, Names: []string{"complete-item"}}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
	for reason := range landingHolds {
		err := l.holdItemFault(context.Background(), Candidate{ItemID: "item-a"}, reason, "incompatible path")
		if err == nil || !strings.Contains(err.Error(), "does not enable hold") {
			t.Fatalf("%s: %v", reason, err)
		}
		err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
			return recordDirtyDecision(tx, Decision{ItemID: "item-a", Action: ActionHold, Reason: reason}, time.Unix(0, 0))
		})
		if err == nil || !strings.Contains(err.Error(), "does not enable hold") {
			t.Fatalf("decision %s: %v", reason, err)
		}
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		holds, err := ReadItemFaults(r)
		if len(holds) != 0 {
			t.Fatalf("disabled holds persisted: %+v", holds)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
