package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	drainModulePath = "github.com/mschulkind-oss/tillr-drain"
	drainPkgPath    = "github.com/mschulkind-oss/tillr-drain/drain"
	internalPrefix  = "github.com/mschulkind-oss/tillr-drain/internal/"

	maxLoopFields  = 159
	maxLoopMutexes = 26
)

// drainAllowlist lists every non-test .go file permitted in tools/tillr-drain/drain
// (excluding *_aliases.go and *_bridge.go) after E01.
var drainAllowlist = map[string]bool{
	"adapter_build.go":            true,
	"alarm_report.go":             true,
	"alert_triage.go":             true,
	"already_on_trunk.go":         true,
	"answered_obstacle.go":        true,
	"backend_availability.go":     true,
	"backend_outcomes.go":         true,
	"base_replay_events.go":       true,
	"blocked.go":                  true,
	"blocked_repeat.go":           true,
	"boundary.go":                 true,
	"brief_ending.go":             true,
	"build.go":                    true,
	"build_readers.go":            true,
	"build_restart.go":            true,
	"candidate_size.go":           true,
	"claim_gate.go":               true,
	"command.go":                  true,
	"comment_intake.go":           true,
	"comment_observatory.go":      true,
	"comments.go":                 true,
	"commit_continuation.go":      true,
	"completion_handoff.go":       true,
	"conflict.go":                 true,
	"conflict_restart.go":         true,
	"defect.go":                   true,
	"definition.go":               true,
	"definition_already_fixed.go": true,
	"definition_hold.go":          true,
	"delivery.go":                 true,
	"deny_list.go":                true,
	"design_answer.go":            true,
	"design_continuation.go":      true,
	"design_repeat.go":            true,
	"design_retry.go":             true,
	"design_transport_error.go":   true,
	"design_unanswered.go":        true,
	"designer.go":                 true,
	"designer_compaction.go":      true,
	"dirty_hold.go":               true,
	"dispatch_decline.go":         true,
	"dispatches.go":               true,
	"document_prerequisite.go":    true,
	"end_unfinished_runs.go":      true,
	"ended_item_lane.go":          true,
	"events.go":                   true,
	"examine_coordinator.go":      true,
	"extension_roll.go":           true,
	"failover.go":                 true,
	"fixer_gate.go":               true,
	"fixer_paths.go":              true,
	"fixer_rebase.go":             true,
	"fixer_resolve.go":            true,
	"fixer_settle.go":             true,
	"fixer_wait.go":               true,
	"flaky_repair.go":             true,
	"fork_check_triage.go":        true,
	"fork_checkout.go":            true,
	"fork_drift.go":               true,
	"fork_leftover.go":            true,
	"format_check.go":             true,
	"gate_conflict.go":            true,
	"gate_no_verdict.go":          true,
	"gate_recovery.go":            true,
	"gate_spans.go":               true,
	"gate_stall.go":               true,
	"gate_start_error.go":         true,
	"gate_timing.go":              true,
	"gate_usage_error.go":         true,
	"gathered.go":                 true,
	"graph_proposal.go":           true,
	"handoff_timing.go":           true,
	"heavy_aliases.go":            true,
	"heavy_aliases_linux.go":      true,
	"heavy_go.go":                 true,
	"heavy_link.go":               true,
	"heavy_loop.go":               true,
	"heavy_revert.go":             true,
	"heavy_steps.go":              true,
	"held_fork.go":                true,
	"held_fork_inputs.go":         true,
	"held_lane.go":                true,
	"held_lane_liveness.go":       true,
	"hook_completion.go":          true,
	"hooks.go":                    true,
	"icebox.go":                   true,
	"idea.go":                     true,
	"idle.go":                     true,
	"index_lock.go":               true,
	"infrastructure_hold.go":      true,
	"item_fault.go":               true,
	"judge.go":                    true,
	"judge_landing.go":            true,
	"landing_call.go":             true,
	"landing_call_lifecycle.go":   true,
	"landing_measurements.go":     true,
	"lane_base.go":                true,
	"lane_children.go":            true,
	"lane_copy.go":                true,
	"lane_rotation.go":            true,
	"legacy_watches.go":           true,
	"live_scale.go":               true,
	"loop.go":                     true,
	"loop_ended.go":               true,
	"loop_pass.go":                true,
	"looprecord.go":               true,
	"missing_candidate.go":        true,
	"node_brief.go":               true,
	"node_outputs.go":             true,
	"obstacle.go":                 true,
	"obstacle_hold.go":            true,
	"obstacle_route.go":           true,
	"once.go":                     true,
	"orphan.go":                   true,
	"owner_answer.go":             true,
	"owner_migration.go":          true,
	"owner_question.go":           true,
	"pack_mirror.go":              true,
	"parallel.go":                 true,
	"parallel_boundary.go":        true,
	"parallel_failover.go":        true,
	"parallel_restart.go":         true,
	"pass.go":                     true,
	"pass_observation.go":         true,
	"pass_spend.go":               true,
	"paused_judge_unreadable.go":  true,
	"pending_cache.go":            true,
	"pi_roll.go":                  true,
	"pipeline_observation.go":     true,
	"plain_stage.go":              true,
	"plan.go":                     true,
	"policy.go":                   true,
	"policy_reload.go":            true,
	"policy_seats.go":             true,
	"post_landing.go":             true,
	"presence.go":                 true,
	"preserve_hold.go":            true,
	"preserved.go":                true,
	"preserved_lane.go":           true,
	"process_cpu.go":              true,
	"process_priority.go":         true,
	"profile.go":                  true,
	"prose_rewrite.go":            true,
	"push_refused.go":             true,
	"push_state.go":               true,
	"questions.go":                true,
	"quota_continue.go":           true,
	"quota_probe.go":              true,
	"quota_window.go":             true,
	"readmit.go":                  true,
	"recovery.go":                 true,
	"red_cap.go":                  true,
	"refs.go":                     true,
	"refusal.go":                  true,
	"refusal_cases.go":            true,
	"remote_sync.go":              true,
	"remote_sync_conflict.go":     true,
	"remote_sync_filing.go":       true,
	"remote_sync_landing.go":      true,
	"remote_sync_recovery.go":     true,
	"remote_sync_tree.go":         true,
	"repair_hold.go":              true,
	"repeated_ending.go":          true,
	"repeating_call.go":           true,
	"replay_go_affected.go":       true,
	"request.go":                  true,
	"restart.go":                  true,
	"restart_examination.go":      true,
	"restart_judge.go":            true,
	"restart_rolling.go":          true,
	"restart_work.go":             true,
	"resume.go":                   true,
	"retire.go":                   true,
	"retired_order.go":            true,
	"retries.go":                  true,
	"retry_brief.go":              true,
	"retry_candidate.go":          true,
	"retry_supersede.go":          true,
	"returned.go":                 true,
	"returned_lane.go":            true,
	"revert_withdraw.go":          true,
	"run_output.go":               true,
	"running_agents.go":           true,
	"schema.go":                   true,
	"schema_roll.go":              true,
	"seat_escalation.go":          true,
	"seat_review.go":              true,
	"seat_shares.go":              true,
	"settings_reload.go":          true,
	"settle_judge.go":             true,
	"split.go":                    true,
	"sqlite_trace.go":             true,
	"stage.go":                    true,
	"stale_base.go":               true,
	"start_claim.go":              true,
	"start_coordinator.go":        true,
	"start_item_sync.go":          true,
	"start_proposal.go":           true,
	"start_repair_inputs.go":      true,
	"start_stage.go":              true,
	"start_store_sync.go":         true,
	"start_version.go":            true,
	"start_view.go":               true,
	"start_view_notes.go":         true,
	"start_view_refresh.go":       true,
	"start_watcher_linux.go":      true,
	"start_watcher_other.go":      true,
	"step_check.go":               true,
	"steps.go":                    true,
	"stop.go":                     true,
	"store_busy.go":               true,
	"support_revision.go":         true,
	"surviving_processes.go":      true,
	"sync.go":                     true,
	"terminal_item.go":            true,
	"test_fix_hold.go":            true,
	"trigger.go":                  true,
	"trunk.go":                    true,
	"trunk_branch.go":             true,
	"trunk_history.go":            true,
	"trunk_lock.go":               true,
	"trunk_release.go":            true,
	"trunk_repair.go":             true,
	"trunk_rewrite.go":            true,
	"trunk_worktree.go":           true,
	"unlaunched_claim.go":         true,
	"verify_return.go":            true,
	"verify_triage.go":            true,
	"wal_keeper.go":               true,
	"why.go":                      true,
	"why_refusals.go":             true,
	"worker_gate.go":              true,
	"worker_session.go":           true,
	"workers.go":                  true,
	"workflow.go":                 true,
	"workflow_defaults.go":        true,
	"workflow_evaluator.go":       true,
	"workflow_exit.go":            true,
	"workflow_exit_record.go":     true,
	"workflow_failover.go":        true,
	"workflow_order.go":           true,
	"worktree_authority.go":       true,
	"worktree_registry.go":        true,
}

type archListPackage struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

type archLayerDecl struct {
	pkg     string
	layer   int
	imports map[string]bool
}

func TestArchitecture(t *testing.T) {
	pkgs := listDrainPackages(t)

	// 1. Universal D-GR47 isolation: no package under tools/tillr-drain/... other than
	// root tillr-drain and drain itself may import drain or root tillr-drain.
	for _, pkg := range pkgs {
		if pkg.ImportPath == drainModulePath || pkg.ImportPath == drainPkgPath {
			continue
		}
		for _, imp := range allImports(pkg) {
			if imp == drainPkgPath || imp == drainModulePath {
				t.Errorf("D-GR47 violation: %s imports %s", pkg.ImportPath, imp)
			}
		}
	}

	// 2. Self-declared layer DAG in tools/tillr-drain/internal/*/doc.go (skipping internal/testinputs).
	decls := readInternalArchDecls(t)
	checkArchDAG(t, decls)
	for _, pkg := range pkgs {
		if !strings.HasPrefix(pkg.ImportPath, internalPrefix) {
			continue
		}
		short := strings.TrimPrefix(pkg.ImportPath, internalPrefix)
		if short == "testinputs" {
			continue
		}
		decl, ok := decls[short]
		if !ok {
			t.Errorf("internal package %s missing // tillr-arch declaration in doc.go", short)
			continue
		}
		for _, imp := range allImports(pkg) {
			if !strings.HasPrefix(imp, internalPrefix) {
				continue
			}
			target := strings.TrimPrefix(imp, internalPrefix)
			if target == short || target == "testinputs" {
				continue
			}
			if !decl.imports[target] {
				t.Errorf("internal/%s imports undeclared internal/%s (declare in doc.go if layer-valid)", short, target)
			}
		}
	}

	// 3. Monotonic filename allowlist and bridge/alias AST check on tools/tillr-drain/drain.
	checkDrainFiles(t)

	// 4. struct Loop field and mutex ceiling in tools/tillr-drain/drain/loop.go.
	checkLoopCeiling(t)
}

func listDrainPackages(t *testing.T) []archListPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -json ./...: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []archListPackage
	for {
		var p archListPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func allImports(p archListPackage) []string {
	out := make([]string, 0, len(p.Imports)+len(p.TestImports)+len(p.XTestImports))
	out = append(out, p.Imports...)
	out = append(out, p.TestImports...)
	out = append(out, p.XTestImports...)
	return out
}

func readInternalArchDecls(t *testing.T) map[string]archLayerDecl {
	t.Helper()
	entries, err := os.ReadDir("internal")
	if err != nil {
		t.Fatalf("reading internal/: %v", err)
	}
	decls := make(map[string]archLayerDecl)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "testinputs" {
			continue
		}
		name := entry.Name()
		docPath := filepath.Join("internal", name, "doc.go")
		raw, err := os.ReadFile(docPath)
		if err != nil {
			t.Errorf("missing %s: %v", docPath, err)
			continue
		}
		decl, err := parseArchHeader(name, string(raw))
		if err != nil {
			t.Errorf("%s: %v", docPath, err)
			continue
		}
		decls[name] = decl
	}
	return decls
}

func parseArchHeader(pkg, src string) (archLayerDecl, error) {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "// tillr-arch:") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, "// tillr-arch:"))
		var layerSet bool
		decl := archLayerDecl{pkg: pkg, imports: map[string]bool{}}
		for _, field := range strings.Fields(body) {
			k, v, ok := strings.Cut(field, "=")
			if !ok {
				return archLayerDecl{}, fmt.Errorf("malformed tillr-arch field %q", field)
			}
			switch k {
			case "layer":
				n, err := strconv.Atoi(v)
				if err != nil {
					return archLayerDecl{}, fmt.Errorf("invalid layer %q: %v", v, err)
				}
				decl.layer = n
				layerSet = true
			case "imports":
				for _, item := range strings.Split(v, ",") {
					item = strings.TrimSpace(item)
					if item != "" {
						decl.imports[item] = true
					}
				}
			default:
				return archLayerDecl{}, fmt.Errorf("unknown tillr-arch key %q", k)
			}
		}
		if !layerSet {
			return archLayerDecl{}, fmt.Errorf("tillr-arch header missing layer=<N>")
		}
		return decl, nil
	}
	return archLayerDecl{}, fmt.Errorf("missing // tillr-arch: layer=<N> imports=<csv> header")
}

func checkArchDAG(t *testing.T, decls map[string]archLayerDecl) {
	t.Helper()
	for pkg, decl := range decls {
		for dep := range decl.imports {
			target, ok := decls[dep]
			if !ok {
				t.Errorf("internal/%s declares unknown internal import %q", pkg, dep)
				continue
			}
			sameLayerAllowed := pkg == "runhistory" && dep == "storelog" && decl.layer == target.layer
			if target.layer >= decl.layer && !sameLayerAllowed {
				t.Errorf("layer violation: internal/%s (layer %d) cannot depend on internal/%s (layer %d)", pkg, decl.layer, dep, target.layer)
			}
		}
	}
	// Cycle check via DFS.
	state := make(map[string]int, len(decls))
	var visit func(string)
	visit = func(node string) {
		state[node] = 1
		for dep := range decls[node].imports {
			switch state[dep] {
			case 1:
				t.Errorf("cycle in internal/* architecture DAG involving %s -> %s", node, dep)
			case 0:
				if _, exists := decls[dep]; exists {
					visit(dep)
				}
			}
		}
		state[node] = 2
	}
	for pkg := range decls {
		if state[pkg] == 0 {
			visit(pkg)
		}
	}
}

func checkDrainFiles(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir("drain")
	if err != nil {
		t.Fatalf("reading drain/: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if strings.HasSuffix(name, "_aliases.go") || strings.HasSuffix(name, "_bridge.go") {
			checkBridgeAST(t, fset, filepath.Join("drain", name))
			continue
		}
		if !drainAllowlist[name] {
			t.Errorf("new domain file %q added to drain/; extract into tools/tillr-drain/internal/<pkg>/ instead", name)
		}
	}
}

func checkBridgeAST(t *testing.T, fset *token.FileSet, path string) {
	t.Helper()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.IMPORT, token.CONST, token.VAR:
			case token.TYPE:
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					if !ts.Assign.IsValid() {
						t.Errorf("%s: type %s must be a type alias (=), not a new type definition", path, ts.Name.Name)
					}
				}
			default:
				t.Errorf("%s: unexpected declaration token %v", path, d.Tok)
			}
		case *ast.FuncDecl:
			if d.Name.Name == "init" {
				continue
			}
			lines := fset.Position(d.End()).Line - fset.Position(d.Pos()).Line + 1
			if lines > 10 {
				t.Errorf("%s: function %s is %d lines (max 10 for bridge wrappers)", path, d.Name.Name, lines)
			}
		}
	}
}

func checkLoopCeiling(t *testing.T) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("drain", "loop.go"), nil, 0)
	if err != nil {
		t.Fatalf("parsing drain/loop.go: %v", err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts := spec.(*ast.TypeSpec)
			if ts.Name.Name != "Loop" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("Loop is not a struct")
			}
			var fields, mutexes int
			for _, field := range st.Fields.List {
				n := len(field.Names)
				if n == 0 {
					n = 1
				}
				fields += n
				if isSyncMutex(field.Type) {
					mutexes += n
				}
			}
			if fields > maxLoopFields {
				t.Errorf("drain.Loop has %d fields (ceiling %d)", fields, maxLoopFields)
			}
			if mutexes > maxLoopMutexes {
				t.Errorf("drain.Loop has %d sync.Mutex/sync.RWMutex fields (ceiling %d)", mutexes, maxLoopMutexes)
			}
			return
		}
	}
	t.Fatalf("type Loop struct not found in drain/loop.go")
}

func isSyncMutex(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "sync" {
		return false
	}
	return sel.Sel.Name == "Mutex" || sel.Sel.Name == "RWMutex"
}
