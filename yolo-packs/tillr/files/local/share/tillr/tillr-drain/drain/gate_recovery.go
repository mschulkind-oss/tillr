package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// GateRecoveryKind is the disclosure event emitted when a gate-fenced candidate's
	// own tillr-gate build binds gate.verdict after an installed-gate fault (OQ-GR4, B).
	GateRecoveryKind = "drain.gate-recovery"

	gateBinaryDefectCommand = "tillr-gate"
)

// gateBinaryDefectPaths is the fence for the deduplicated defect item filed when
// direct execution proves the installed tillr-gate binary is at fault.
var gateBinaryDefectPaths = []string{
	"tools/tillr-gate/",
	"yolo-packs/tillr/files/local/share/tillr/tillr-gate/",
}

type gateRecoveryEvent struct {
	ItemID          string `json:"item_id"`
	RunID           string `json:"run_id"`
	LaneID          string `json:"lane_id"`
	HeadSHA         string `json:"head_sha"`
	Trigger         string `json:"trigger"`
	InstalledDigest string `json:"installed_digest"`
	CandidateDigest string `json:"candidate_digest"`
}

// candidateGateRunner is an optional Steps capability that runs the full landing gate
// using a candidate-built tillr-gate binary (OQ-GR4 option B recovery path).
type candidateGateRunner interface {
	GateWithBinary(ctx context.Context, candidate Candidate, gateBin string) (GateOutcome, error)
}

func init() {
	if err := event.Register(GateRecoveryKind, Utility); err != nil {
		panic(err)
	}
}

// InstalledGateDigest returns the SHA-256 hex digest of the installed tillr-gate binary
// on PATH, or "" when tillr-gate cannot be located or read.
func InstalledGateDigest() string {
	path, err := exec.LookPath("tillr-gate")
	if err != nil {
		return ""
	}
	return fileDigest(path)
}

func (l *Loop) installedGateDigest() string {
	return InstalledGateDigest()
}

// releaseAdvancedGateDigestFaults clears active gate-stalled and gate-harness-error
// holds whose recorded installed tillr-gate digest differs from the current installed
// binary's digest (OQ-GR4 option B).
func (l *Loop) releaseAdvancedGateDigestFaults(ctx context.Context) error {
	if l.cfg.Store == "" {
		return nil
	}
	currentDigest := l.installedGateDigest()
	if currentDigest == "" {
		return nil
	}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var toClear []ItemFault
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		var err error
		toClear, err = advancedGateDigestFaults(tx, currentDigest)
		return err
	})
	r.Close()
	if err != nil || len(toClear) == 0 {
		return err
	}
	st, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	at := l.cfg.Now().UTC()
	var cleared []ItemFault
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		current, err := advancedGateDigestFaults(tx.Reader(), currentDigest)
		if err != nil {
			return err
		}
		for _, f := range current {
			if err := clearItemFault(tx, f, at); err != nil {
				return err
			}
			cleared = append(cleared, f)
		}
		return nil
	})
	if err == nil {
		for _, f := range cleared {
			l.reportf("loop: cleared %s hold on %s: installed tillr-gate digest advanced (%s -> %s)\n",
				f.Reason, f.ItemID, shortSHA(f.GateDigest), shortSHA(currentDigest))
		}
	}
	return err
}

func advancedGateDigestFaults(r *store.Reader, currentDigest string) ([]ItemFault, error) {
	if currentDigest == "" {
		return nil, nil
	}
	faults, err := ReadItemFaults(r)
	if err != nil {
		return nil, err
	}
	var out []ItemFault
	for _, f := range faults {
		if f.Reason != ReasonGateStalled && f.Reason != StopGateHarnessError {
			continue
		}
		if f.GateDigest != "" && f.GateDigest != currentDigest {
			out = append(out, f)
		}
	}
	return out, nil
}

type directCheckDecl struct {
	ID      string   `json:"id"`
	Command []string `json:"command"`
	Scope   string   `json:"scope"`
	Tier    string   `json:"tier"`
	Reads   []string `json:"reads"`
	Needs   []string `json:"needs"`
}

// runDirectGateChecks reads the candidate worktree's configured checks_dir and runs
// every fast declaration (tier == "fast") and landing check (scope == "change", passing
// base and head, respecting declared reads when applicable) directly in the candidate
// worktree without invoking the installed tillr-gate binary. It returns passed=true
// only when declarations are loaded and every executed check classifies as Pass.
func runDirectGateChecks(ctx context.Context, worktree, baseSHA, headSHA, laneRoot string) (bool, string, error) {
	if worktree == "" {
		return false, "no candidate worktree", nil
	}
	configured, err := settings.ReadChecksDir(worktree)
	if err != nil {
		return false, fmt.Sprintf("reading .tillr.toml in %s: %v", worktree, err), nil
	}
	checksDir := filepath.Join(worktree, filepath.FromSlash(configured))
	entries, err := os.ReadDir(checksDir)
	if err != nil {
		return false, fmt.Sprintf("reading check directory %s: %v", checksDir, err), nil
	}
	var decls []directCheckDecl
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(checksDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Sprintf("reading check declaration %s: %v", path, err), nil
		}
		var decl directCheckDecl
		if err := json.Unmarshal(raw, &decl); err != nil {
			return false, fmt.Sprintf("parsing check declaration %s: %v", path, err), nil
		}
		if decl.ID == "" {
			return false, fmt.Sprintf("check declaration %s has no id", path), nil
		}
		if decl.Tier == "fast" || decl.Scope == "change" {
			decls = append(decls, decl)
		}
	}
	if len(decls) == 0 {
		return false, fmt.Sprintf("no fast or landing check declarations in %s", checksDir), nil
	}

	var changedPaths []string
	hasChangedPaths := false
	if baseSHA != "" && headSHA != "" {
		out, diffErr := exec.CommandContext(ctx, "git", "-C", worktree, "diff", "--name-only", "-z", baseSHA+".."+headSHA).Output()
		if ctx.Err() != nil {
			return false, "", ctx.Err()
		}
		if diffErr == nil {
			hasChangedPaths = true
			for _, p := range strings.Split(string(out), "\x00") {
				if p = strings.TrimSpace(p); p != "" {
					changedPaths = append(changedPaths, p)
				}
			}
		}
	}

	baseEnv := withGitCeilingEnv(
		append(withoutProcessEnv(os.Environ(), "TILLR_SQLITE_TRACE", "PYTHONDONTWRITEBYTECODE"), "PYTHONDONTWRITEBYTECODE=1"),
		laneRoot,
	)

	for _, decl := range decls {
		if hasChangedPaths && len(decl.Reads) > 0 && !directReadsMatch(decl.Reads, changedPaths) {
			continue
		}
		if len(decl.Command) == 0 {
			return false, fmt.Sprintf("direct check %s has no command", decl.ID), nil
		}
		for _, need := range decl.Needs {
			if strings.HasPrefix(need, "ref:") {
				continue
			}
			needPath := need
			if !filepath.IsAbs(needPath) {
				needPath = filepath.Join(worktree, needPath)
			}
			if _, statErr := os.Stat(needPath); statErr != nil {
				return false, fmt.Sprintf("direct check %s missing needed path %s: %v", decl.ID, need, statErr), nil
			}
		}
		argv := append([]string(nil), decl.Command...)
		if decl.Scope == "change" {
			if baseSHA == "" || headSHA == "" {
				return false, fmt.Sprintf("direct check %s requires base and head commits", decl.ID), nil
			}
			argv = append(argv, baseSHA, headSHA)
		}
		prog := argv[0]
		if strings.Contains(prog, "/") && !filepath.IsAbs(prog) {
			prog = filepath.Join(worktree, prog)
		}
		cmd := exec.CommandContext(ctx, prog, argv[1:]...)
		cmd.Dir = worktree
		cmd.Env = baseEnv
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		runErr := cmd.Run()
		if ctx.Err() != nil {
			return false, "", ctx.Err()
		}
		obs := checkresult.Observation{
			Check:    decl.ID,
			Commit:   headSHA,
			Worktree: worktree,
			Source:   checkresult.CheckCommand,
		}
		if runErr == nil {
			zero := 0
			obs.ExitCode = &zero
		} else {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				rc := exitErr.ExitCode()
				obs.ExitCode = &rc
			} else {
				obs.ProcessError = true
			}
		}
		res := classifyCheck(obs)
		if res.Outcome != checkresult.Pass {
			msg := fmt.Sprintf("direct check %s did not pass (%s: %s)", decl.ID, res.Outcome, res.Detail)
			if tail := strings.TrimSpace(outBuf.String()); tail != "" {
				msg += ": " + clip(tail, 300)
			}
			return false, msg, nil
		}
	}
	return true, "", nil
}

func directReadsMatch(reads, changedPaths []string) bool {
	for _, p := range changedPaths {
		if readsCoverPath(reads, p) {
			return true
		}
	}
	return false
}

// candidateFenceEntries reads the candidate's fence from its lane fence file, falling
// back to items.allowed_paths in the store when no lane fence file is present.
func (l *Loop) candidateFenceEntries(ctx context.Context, c Candidate) []string {
	if c.LaneID != "" && l.cfg.LaneRoot != "" {
		if fencePath, err := l.itemFencePath(c.LaneID); err == nil && fencePath != "" {
			if body, err := os.ReadFile(fencePath); err == nil {
				if entries := fenceEntries(string(body)); len(entries) > 0 {
					return entries
				}
			}
		}
	}
	if l.cfg.Store != "" && c.ItemID != "" {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err == nil {
			defer s.Close()
			var raw string
			_ = s.ReadTx(ctx, func(r *store.Reader) error {
				return r.QueryRow(`SELECT COALESCE(allowed_paths, '[]') FROM items WHERE item_id = ?`, c.ItemID).Scan(&raw)
			})
			if raw != "" {
				var entries []string
				if json.Unmarshal([]byte(raw), &entries) == nil {
					return entries
				}
			}
		}
	}
	return nil
}

func isUnrestrictedFence(fence []string) bool {
	if len(fence) == 0 {
		return true
	}
	for _, entry := range fence {
		clean := filepath.ToSlash(strings.TrimSpace(entry))
		if clean == "." || clean == "./" {
			return true
		}
	}
	return false
}

func fencePermitsGateSource(fence []string) bool {
	for _, entry := range fence {
		clean := filepath.ToSlash(strings.TrimSpace(entry))
		if clean == "" {
			continue
		}
		if insideFence("tools/tillr-gate/main.go", []string{clean}) ||
			strings.HasPrefix(clean, gateBinaryDir) {
			return true
		}
	}
	return false
}

func pathsTouchGateSource(paths []string) bool {
	for _, p := range paths {
		if strings.HasPrefix(filepath.ToSlash(strings.TrimSpace(p)), gateBinaryDir) {
			return true
		}
	}
	return false
}

// candidatePermitsGateSource answers whether c's fence permits tools/tillr-gate/.
// When the fence is unrestricted or empty, it requires that the candidate actually
// touches tools/tillr-gate/.
func (l *Loop) candidatePermitsGateSource(ctx context.Context, c Candidate) bool {
	fence := l.candidateFenceEntries(ctx, c)
	if isUnrestrictedFence(fence) {
		return pathsTouchGateSource(candidateChangedPaths(ctx, c))
	}
	return fencePermitsGateSource(fence)
}

func gateRecoveryDir(root, storePath, worktree, runID string) string {
	return filepath.Join(detachedGateDir(root, storePath, worktree, runID), "gate-recovery")
}

// attemptGateRecovery implements the OQ-GR4 option B candidate-build recovery path
// when the installed tillr-gate stalls, errors, or disagrees with its wrapper on a
// candidate whose fence permits tools/tillr-gate/.
func (l *Loop) attemptGateRecovery(ctx context.Context, c Candidate, trigger string) (GateOutcome, bool, error) {
	if c.Worktree == "" || !l.candidatePermitsGateSource(ctx, c) {
		return GateOutcome{}, false, nil
	}
	gateSrcDir := filepath.Join(c.Worktree, "tools", "tillr-gate")
	if _, err := os.Stat(filepath.Join(gateSrcDir, "go.mod")); err != nil {
		return GateOutcome{}, false, nil
	}
	runner, ok := l.cfg.Steps.(candidateGateRunner)
	if !ok {
		return GateOutcome{}, false, nil
	}

	// (a) Direct execution of every fast declaration and landing check must pass.
	directOK, detail, err := runDirectGateChecks(ctx, c.Worktree, c.BaseSHA, c.HeadSHA, l.cfg.LaneRoot)
	if err != nil {
		return GateOutcome{}, false, err
	}
	if !directOK {
		l.reportf("loop: gate recovery on %s skipped (%s): direct checks did not pass: %s\n", c.ItemID, trigger, detail)
		return GateOutcome{}, false, nil
	}

	// (b) Build the candidate's own tillr-gate into a run-local scratch dir.
	recoveryDir := gateRecoveryDir(l.cfg.Root, l.cfg.Store, c.Worktree, c.RunID)
	if err := os.MkdirAll(recoveryDir, 0o755); err != nil {
		l.reportf("loop: gate recovery on %s failed creating scratch dir: %v\n", c.ItemID, err)
		return GateOutcome{}, false, nil
	}
	candidateBin := filepath.Join(recoveryDir, "tillr-gate")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", candidateBin, ".")
	buildCmd.Dir = gateSrcDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return GateOutcome{}, false, ctx.Err()
		}
		l.reportf("loop: gate recovery on %s failed building candidate tillr-gate: %v (%s)\n", c.ItemID, err, clip(string(out), 300))
		return GateOutcome{}, false, nil
	}
	candidateDigest := fileDigest(candidateBin)
	if candidateDigest == "" {
		return GateOutcome{}, false, nil
	}

	// (c) Run the full landing gate under the candidate-built tillr-gate binary.
	outcome, err := runner.GateWithBinary(ctx, c, candidateBin)
	if err != nil {
		if ctx.Err() != nil {
			return GateOutcome{}, false, err
		}
		l.reportf("loop: gate recovery on %s failed under candidate tillr-gate: %v\n", c.ItemID, err)
		return GateOutcome{}, false, nil
	}
	if outcome.Outcome != GatePassed && outcome.Outcome != GatePassedInherited {
		l.reportf("loop: gate recovery on %s did not pass under candidate tillr-gate (outcome %s)\n", c.ItemID, outcome.Outcome)
		return GateOutcome{}, false, nil
	}

	// (d) Both (a) and (c) passed: record disclosure event with both binary digests.
	installedDigest := l.installedGateDigest()
	ev := gateRecoveryEvent{
		ItemID:          c.ItemID,
		RunID:           c.RunID,
		LaneID:          c.LaneID,
		HeadSHA:         c.HeadSHA,
		Trigger:         trigger,
		InstalledDigest: installedDigest,
		CandidateDigest: candidateDigest,
	}
	if l.cfg.Store != "" {
		if err := appendJudgment(ctx, l.cfg.Store, GateRecoveryKind, "run", c.RunID, ev, l.cfg.Now().UTC()); err != nil {
			return GateOutcome{}, false, err
		}
	}
	l.reportf("loop: gate recovery succeeded on %s (trigger=%s, installed=%s, candidate=%s)\n",
		c.ItemID, trigger, shortSHA(installedDigest), shortSHA(candidateDigest))
	return outcome, true, nil
}

// GateWithBinary runs the landing gate using a candidate-built tillr-gate binary,
// passing --tillr-gate when invoking tillr-loop and prepending the binary's directory
// to PATH so the candidate build writes and binds gate.verdict.
func (s *ProcessSteps) GateWithBinary(ctx context.Context, candidate Candidate, gateBin string) (GateOutcome, error) {
	if err := CheckTrunkWorktree(ctx, s.Root); err != nil {
		return GateOutcome{}, err
	}
	program := s.GateProgram
	fork := strings.TrimPrefix(candidate.BaseRef, "refs/heads/patch/")
	if s.hooks == nil && fork != candidate.BaseRef {
		program = "tillr-drain fork-gate --fork " + fork
	}
	if program == "" {
		return GateOutcome{}, Usagef("the loop was given no program for this step: it cannot judge a candidate without one")
	}
	env := append(s.forkRootEnv(), "PYTHONDONTWRITEBYTECODE=1")
	if s.Root != "" {
		runDir := RunDir(s.Root, candidate.RunID)
		for _, name := range []string{GateVerdictRecord, GateSpansFile, GateFastVerdictFile, GateChecksFile, filepath.Join("change-logs", GateChecksFile)} {
			if err := os.Remove(filepath.Join(runDir, name)); err != nil && !os.IsNotExist(err) {
				return GateOutcome{}, Externalf("clearing preceding gate measurements: %v", err)
			}
		}
		env = append(env, "TILLR_GATE_SPANS_OUT="+filepath.Join(runDir, GateSpansFile),
			"TILLR_FAST_VERDICT_OUT="+filepath.Join(runDir, GateFastVerdictFile))
	}
	pathVal := filepath.Dir(gateBin)
	if cur := os.Getenv("PATH"); cur != "" {
		pathVal += string(os.PathListSeparator) + cur
	}
	env = append(env, "PATH="+pathVal, "TILLR_FORK_DRIFT=1", "TILLR_ORDINARY_GATE_TIMING=")

	var extra []string
	if s.standardAdapterArg(s.programArgv(program, candidate)) >= 0 {
		extra = append(extra, "--tillr-gate", gateBin)
	}

	code, _, span, err := s.programWithEnv(ctx, "gate", candidate.Worktree, program, candidate, env, extra...)
	if err != nil {
		return GateOutcome{}, err
	}
	classified := classifyCheck(checkresult.Observation{
		Commit:   candidate.HeadSHA,
		Worktree: candidate.Worktree,
		Source:   checkresult.JudgingProgram,
		ExitCode: &code,
	})
	outcome := GateOutcome{
		Outcome:    GateErrored,
		DurationMS: span.FinishedAtMS - span.StartedAtMS,
	}
	if classified.Outcome == checkresult.Pass {
		outcome.Outcome = GatePassed
		if s.Root != "" {
			if payload, ok := readRecordPayload(filepath.Join(RunDir(s.Root, candidate.RunID), GateVerdictRecord)); ok {
				var v struct {
					Result            string             `json:"result"`
					InheritedFailures []InheritedFailure `json:"inherited_failures"`
				}
				if json.Unmarshal(payload, &v) == nil && v.Result == GatePassedInherited {
					outcome.Outcome = GatePassedInherited
					outcome.InheritedFailures = v.InheritedFailures
				}
			}
		}
		judgment := gateStepJudgment{
			TickID:     candidate.TickID,
			Run:        runRef{RunID: candidate.RunID, ItemID: candidate.ItemID, LaneID: candidate.LaneID},
			Outcome:    outcome.Outcome,
			Program:    span.Argv,
			ExitCode:   exitCode(span),
			DurationMS: outcome.DurationMS,
		}
		if err := s.recordGateJudgment(ctx, candidate, judgment); err != nil {
			return GateOutcome{}, err
		}
	}
	return outcome, nil
}

// gateBinaryDefectID returns the deduplicated defect item id for an installed
// tillr-gate binary fault. Scoping by the installed digest (when known) deduplicates
// all items hitting the same faulty binary while allowing a future distinct binary
// fault to file a fresh defect item after the earlier one landed.
func (l *Loop) gateBinaryDefectID() string {
	key := gateBinaryDefectCommand
	if digest := l.installedGateDigest(); digest != "" {
		key += "@" + shortSHA(digest)
	}
	return DefectItemID(key)
}

func mergeGateDefectPaths(existing, add []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{existing, add} {
		for _, p := range list {
			if p = strings.TrimSpace(p); p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// fileGateBinaryDefectItem files or attaches to ONE deduplicated defect item fenced
// to the gate's source (tools/tillr-gate/ and its pack mirror), preserving gate paths
// and keeping IsDefectItem true so releaseTrunkAcquittals releases acquitted items.
func (l *Loop) fileGateBinaryDefectItem(ctx context.Context, reporter, trigger, evidence string) (string, error) {
	id := l.gateBinaryDefectID()
	paths := mergeGateDefectPaths(nil, gateBinaryDefectPaths)
	criterionText := fmt.Sprintf(
		"The installed `%s` binary judges candidates without %s, and the failure below no longer reproduces. Evidence from %s (direct execution passed):\n\n%s",
		gateBinaryDefectCommand, trigger, reporter, clip(evidence, defectEvidenceLimit),
	)
	payload, err := json.Marshal(map[string]any{
		"item_id":       id,
		"title":         "repair installed `tillr-gate` binary fault",
		"state":         StateInQueue,
		"blocked_by":    []string{},
		"rank":          TrunkFixerRank,
		"allowed_paths": paths,
		"seat":          TrunkFixerSeat,
		"criteria": []map[string]string{{
			"criterion_id": DefectCriterion,
			"text":         criterionText,
			"state":        "not-done",
		}},
	})
	if err != nil {
		return "", Internalf("encoding the gate defect item: %v", err)
	}
	env := record.Envelope{
		RecordVersion: 1,
		Kind:          item.RecordKind,
		ID:            "item-" + id,
		Producer:      record.Producer{System: "tillr", Utility: Utility, Version: Version},
		CreatedAt:     l.cfg.Now().UTC(),
		Payload:       payload,
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return "", Incompatiblef("%v", err)
	}
	defer s.Close()
	created := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var state, fence string
		scanErr := tx.QueryRow(`SELECT state, COALESCE(allowed_paths, '[]') FROM items WHERE item_id = ?`, id).Scan(&state, &fence)
		switch {
		case scanErr == nil:
			declared, err := definition.ForReader(tx.Reader())
			if err != nil {
				return err
			}
			if finishedState(declared.Parsed, state) {
				return nil
			}
			var old []string
			if err := json.Unmarshal([]byte(fence), &old); err != nil {
				return err
			}
			merged := mergeGateDefectPaths(old, paths)
			raw, err := json.Marshal(merged)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET allowed_paths=? WHERE item_id=?`, string(raw), id); err != nil {
				return err
			}
			return appendEffect(tx, FixerAttachedKind, id, map[string]any{
				"item_id":        id,
				"defect_item":    id,
				"reporter":       reporter,
				"allowed_paths":  merged,
				"kept_candidate": false,
			}, l.cfg.Now())
		case errors.Is(scanErr, sql.ErrNoRows):
		default:
			return readFailure(scanErr)
		}
		if _, err := item.Apply(ctx, tx, env); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return "", classifyWrite(err)
	}
	if created {
		l.reportf("loop: filed %s for the installed tillr-gate binary fault seen on %s (fence %s)\n", id, reporter, strings.Join(paths, ", "))
	}
	return id, nil
}

// acquitInstalledGateFault verifies whether direct execution passes on c's worktree
// (proving the installed tillr-gate binary is at fault). When it passes, it files or
// attaches to the deduplicated gate defect item and acquits c behind it.
func (l *Loop) acquitInstalledGateFault(ctx context.Context, c Candidate, trigger, detail string) (bool, error) {
	if l.cfg.Store == "" || c.Worktree == "" {
		return false, nil
	}
	if c.ItemID == l.gateBinaryDefectID() {
		return false, nil
	}
	directOK, _, err := runDirectGateChecks(ctx, c.Worktree, c.BaseSHA, c.HeadSHA, l.cfg.LaneRoot)
	if err != nil {
		return false, err
	}
	if !directOK {
		return false, nil
	}
	defectID, err := l.fileGateBinaryDefectItem(ctx, c.ItemID, trigger, detail)
	if err != nil {
		return false, err
	}
	if err := l.holdBehindDefect(ctx, c, defectID, gateBinaryDefectCommand); err != nil {
		return false, err
	}
	l.reportf("loop: acquitted %s behind %s after %s (direct checks passed)\n", c.ItemID, defectID, trigger)
	return true, nil
}

// hasPriorGateNoVerdict reports whether this run already recorded a gate-no-verdict
// hold, making another GateErrored on the same run a repeatable gate error.
func (l *Loop) hasPriorGateNoVerdict(ctx context.Context, c Candidate) (bool, error) {
	if l.cfg.Store == "" || c.ItemID == "" || c.RunID == "" {
		return false, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var count int
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT COUNT(*) FROM dispatches WHERE item_id=? AND run_id=? AND action=? AND reason=?`,
			c.ItemID, c.RunID, ActionHold, ReasonGateNoVerdict,
		).Scan(&count)
	})
	return count > 0, err
}
