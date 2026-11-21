package drain

// Architecture and invariants: docs/reference/tillr-restart.md

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
)

const postLandingKind = "drain.post-landing-action"

// pushTimeout bounds one push. The jail's git broker may ask the human and wait; a push that
// has not finished by then is recorded as failed and retried at the next trigger, rather than
// holding restarts (which wait for an idle push) for as long as it hangs.
const pushTimeout = 5 * time.Minute

func init() {
	if err := event.Register(postLandingKind, Utility); err != nil {
		panic(err)
	}
}

// Each action has one worker and one replaceable pending landing. Replacing the
// pending tip is safe: both actions operate on the current trunk, not a lane.
type landingAction struct {
	mu      sync.Mutex
	running bool
	pending string
}

func (a *landingAction) busy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

func (a *landingAction) submit(landing string, run func(string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		a.pending = landing
		return
	}
	a.running = true
	go func(tip string) {
		for {
			run(tip)
			a.mu.Lock()
			tip, a.pending = a.pending, ""
			if tip == "" {
				a.running = false
				a.mu.Unlock()
				return
			}
			a.mu.Unlock()
		}
	}(landing)
}

const piForkPatchRef = "refs/heads/patch/pi"

var piSeriesPaths = []string{"yolo-packs/pi-fork/", "bin/pi-forks.list"}

func (l *Loop) landWithActions(ctx context.Context, candidate Candidate) (LandOutcome, error) {
	out, err := l.cfg.Steps.Land(ctx, candidate)
	if err == nil && out.Result == LandLanded && out.TrunkAfter != "" {
		l.afterLandingCandidate(context.WithoutCancel(ctx), candidate, out.TrunkAfter)
	}
	return out, err
}

func (l *Loop) afterLanding(ctx context.Context, tip string) {
	l.afterLandingCandidate(ctx, Candidate{}, tip)
}

func (l *Loop) afterLandingCandidate(ctx context.Context, candidate Candidate, tip string) {
	if candidate.BaseRef == "" {
		if p := l.cfg.PostLanding.Push; p.What == settings.PushTip {
			l.pushAction.submit(tip, func(tip string) {
				_, err := l.pushTipOnce(ctx, p)
				l.recordPush(ctx, settings.PushTip, tip, err)
			})
		}
	}
	r := l.cfg.PostLanding.Roll
	if r.Command == "" {
		return
	}
	if candidate.BaseRef == piForkPatchRef {
		l.submitPiRoll(ctx, r.Command, tip)
		return
	}
	if isExtensionPatchBranch(candidate.BaseRef) {
		l.submitExtensionRoll(ctx, r.Command, tip)
		return
	}
	if candidate.BaseRef != "" {
		return
	}
	changed, err := actionCommand(ctx, l.cfg.Root, "git", "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", tip+"^", tip, "--")
	if err != nil {
		l.reportRollFailure(tip, "", err)
		l.recordAction(ctx, "roll", tip, err)
		return
	}
	l.rollChanged(ctx, r, changed, tip)
}

// rollChanged submits the deployment actions selected by NUL-separated paths.
func (l *Loop) rollChanged(ctx context.Context, r settings.Roll, changed, tip string) {
	if matchesRollPaths(changed, r.Paths) {
		l.rollAction.submit(tip, func(tip string) {
			// After-merge proof owns the suites for a landing. The deployment only
			// builds and smoke-checks the replacement; it does not queue another proof.
			program, args := "sh", []string{"-c", r.Command}
			if l.gatePolicy.LandingStrategy == "after-merge" {
				program, args = "env", append([]string{"TILLR_ROLL_VERIFICATION=after-merge", program}, args...)
			}
			output, err := actionCommand(ctx, l.cfg.Root, program, args...)
			if err == nil {
				roles := []string{restart.RoleView, restart.RoleDrain}
				for _, line := range strings.Split(output, "\n") {
					if line == SchemaRollCheckLine {
						// The replacement requests the view after activating its binaries.
						roles = []string{restart.RoleDrain}
						break
					}
				}
				for _, role := range roles {
					if _, err = restart.File(ctx, l.cfg.Store, role, time.Now()); err != nil {
						l.alarmStoreRoll(ctx, tip, role+" restart request", err)
						break
					}
				}
			}
			l.reportRollFailure(tip, "", err)
			l.recordAction(ctx, "roll", tip, err)
		})
	}
	if matchesRollPaths(changed, piSeriesPaths) {
		l.submitPiRoll(ctx, r.Command, tip)
	}
	if matchesExtensionRollPaths(changed) {
		l.submitExtensionRoll(ctx, r.Command, tip)
	}
}

func (l *Loop) submitPiRoll(ctx context.Context, command, tip string) {
	l.piRollAction.submit(tip, func(tip string) {
		_, err := actionCommand(ctx, l.cfg.Root, "sh", "-c", command+" --pi")
		l.reportRollFailure(tip, "pi", err)
		l.recordActionWhat(ctx, "roll", "pi", tip, err)
	})
}

// reportRollFailure puts a failed post-landing roll in the loop's report, as recordPush does
// for a push: the stored event alone is read only by whoever asks for it.
func (l *Loop) reportRollFailure(tip, what string, failure error) {
	if failure == nil {
		return
	}
	kind := "roll"
	if what != "" {
		kind = what + " roll"
	}
	l.reportf("loop: post-landing %s at %s failed: %s\n", kind, shortSHA(tip), strings.Join(strings.Fields(failure.Error()), " "))
}

// afterGreen runs when the green pointer advances. In green mode it pushes the pointer's
// commit, not the trunk tip: the remote only ever receives what the proof has passed. The
// argument names the advance for the log; what is pushed is recomputed from the pointer when
// the push runs, so a failed push needs no counter or timer to be retried, only the next
// advance. It is also called once at loop start, so a remote left behind while no loop ran
// catches up.
func (l *Loop) afterGreen(ctx context.Context, advanced string) {
	p := l.cfg.PostLanding.Push
	if p.What != settings.PushGreen {
		return
	}
	l.pushAction.submit(advanced, func(string) {
		green, err := l.pushGreenOnce(ctx, p)
		// Proof, not publication, makes a roll due. Replay may have moved green.
		if l.cfg.PostLanding.Roll.Command != "" {
			l.flushPendingRemoteRoll(ctx, optionalCommit(ctx, l.cfg.Root, GreenRef))
		}
		if green != "" || err != nil {
			l.recordPush(ctx, settings.PushGreen, green, err)
		}
	})
}

func matchesRollPaths(changed string, patterns []string) bool {
	for _, file := range strings.Split(changed, "\x00") {
		if file == "" {
			continue
		}
		for _, pattern := range patterns {
			pattern = strings.TrimSuffix(pattern, "/")
			for part := file; part != "."; part = path.Dir(part) {
				if ok, _ := path.Match(pattern, part); ok {
					return true
				}
			}
		}
	}
	return false
}

// pushDestination resolves where a push goes: the settings' remote and ref, each defaulting
// to the trunk branch's configured upstream.
func pushDestination(ctx context.Context, root string, p settings.Push) (remote, ref string, err error) {
	remote, ref = p.Remote, p.Ref
	if remote == "" || ref == "" {
		upstream, err := actionCommand(ctx, root, "git", "for-each-ref", "--format=%(upstream:remotename)%09%(upstream:remoteref)", "refs/heads/"+TrunkRef)
		if err != nil {
			return "", "", err
		}
		pair := strings.SplitN(strings.TrimRight(upstream, "\n"), "\t", 2)
		if len(pair) != 2 {
			return "", "", fmt.Errorf("trunk has no configured upstream")
		}
		if remote == "" {
			remote = pair[0]
		}
		if ref == "" {
			ref = pair[1]
		}
	}
	if remote == "" || ref == "" {
		return "", "", fmt.Errorf("trunk has no configured upstream")
	}
	return remote, ref, nil
}

// pushTrunk pushes the trunk tip (mode "tip"), proven or not.
func pushTrunk(ctx context.Context, root string, p settings.Push) error {
	_, err := pushTrunkResult(ctx, root, p)
	return err
}

func pushTrunkResult(ctx context.Context, root string, p settings.Push) (string, error) {
	remote, ref, err := pushDestination(ctx, root, p)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	_, err = guardPublished(ctx, root, remote, ref)
	pushed := optionalCommit(ctx, root, "refs/heads/"+TrunkRef)
	if err != nil {
		return pushed, err
	}
	if pushed == "" {
		return "", fmt.Errorf("cannot resolve %s for push", TrunkRef)
	}
	// Push exactly the commit we will record, even if a landing advances main meanwhile.
	if err := pushCommand(ctx, root, "--", remote, pushed+":"+ref); err != nil {
		return pushed, err
	}
	return pushed, advancePublished(ctx, root, remote, ref, pushed)
}

// pushGreen pushes the commit `refs/tillr/green` holds now to the remote branch, unless the
// remote branch already contains it. It answers the commit it considered, empty when there
// is no pointer yet. The refspec has no `+`, so a remote that is not an ancestor of the
// commit refuses it; nothing here pulls, rebases or merges to make it succeed. The pre-push
// hook runs as for any push (see docs/reference/tillr-restart.md, "Pushing what the proof has passed").
func pushGreen(ctx context.Context, root string, p settings.Push) (string, error) {
	return pushGreenChecked(ctx, root, p, nil)
}

func pushGreenChecked(ctx context.Context, root string, p settings.Push, proofHold func(context.Context, string, string, string) (string, error)) (string, error) {
	green, err := heavyGreen(ctx, root)
	if err != nil || green == "" {
		return green, err
	}
	remote, ref, err := pushDestination(ctx, root, p)
	if err != nil {
		return green, err
	}
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	remoteTip, err := guardPublished(ctx, root, remote, ref)
	if err != nil {
		return green, err
	}
	alreadyPublished := false
	if remoteTip != "" {
		alreadyPublished, err = trunkAncestor(ctx, root, green, remoteTip)
		if err != nil {
			return green, err
		}
	}
	if proofHold != nil {
		rebased, err := proofHold(ctx, remote, ref, green)
		if err != nil {
			return green, fmt.Errorf("checking the rebased tip's proof: %w", err)
		}
		if rebased != "" {
			detail := "publication deferred"
			if alreadyPublished {
				detail = errAlreadyPublished.Error()
			}
			return green, &pushStateError{kind: pushKindProving, rebasedTip: rebased,
				detail: detail + "; " + awaitingRebasedProof(rebased)}
		}
	}
	if alreadyPublished {
		return green, errAlreadyPublished
	}
	if err := pushCommand(ctx, root, "--", remote, green+":"+ref); err != nil {
		return green, err
	}
	return green, advancePublished(ctx, root, remote, ref, green)
}

// errAlreadyPublished marks the push that was not needed; it is a success, not a failure.
var errAlreadyPublished = errors.New("remote already holds the green commit")

// recordPush records and logs the outcome of the push turn, after any replay and retry.
func (l *Loop) recordPush(ctx context.Context, what, tip string, failure error) (PushOutcome, error) {
	if errors.Is(failure, errAlreadyPublished) {
		return l.recordPushState(ctx, what, tip, pushKindOK, nil, nil)
	}
	kind, state := pushKindOK, (*pushStateError)(nil)
	if failure != nil {
		kind, state = l.classifyPushFailure(ctx, failure)
	}
	retry := "retried at the next landing"
	if what == settings.PushGreen {
		retry = "retried at the next green advance"
	}
	if kind == pushKindProving {
		l.reportf("loop: push of %s %s waits for proof: %s\n", what, shortSHA(tip), failure)
	} else if failure != nil {
		l.reportf("loop: push of %s %s failed as %s (%s; the loop goes on): %s\n", what, shortSHA(tip), kind, retry, strings.Join(strings.Fields(failure.Error()), " "))
	} else {
		l.reportf("loop: pushed %s %s\n", what, shortSHA(tip))
	}
	return l.recordPushState(ctx, what, tip, kind, state, failure)
}

func actionCommand(ctx context.Context, root, program string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = root
	cmd.Env = withoutProcessEnv(os.Environ(), "TILLR_SQLITE_TRACE")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", program, err, summarizeActionOutput(output))
	}
	return string(output), nil
}

func isShardSlotNoticeLine(line string) bool {
	return strings.HasPrefix(line, "go-test-shards: ") &&
		(strings.Contains(line, " waiting for a test slot") || strings.Contains(line, " yielding to a waiting "))
}

func parseShardFailureLine(trimmed string) (string, []string) {
	candidate := trimmed
	if strings.HasPrefix(candidate, "tillr-roll: ") {
		if idx := strings.Index(candidate, "go-test-shards: "); idx >= 0 {
			candidate = candidate[idx:]
		}
	}
	if strings.HasPrefix(candidate, "go-test-shards: ") {
		if isShardSlotNoticeLine(candidate) || strings.Contains(candidate, " tests passed,") {
			return "", nil
		}
		return candidate, shardFailureTestNames(candidate)
	}
	fields := strings.Fields(candidate)
	if len(fields) >= 4 && fields[1] == "shard" && (fields[3] == "failed" || fields[3] == "timed" || fields[3] == "cannot" || fields[3] == "missing") {
		normalized := "go-test-shards: " + candidate
		return normalized, shardFailureTestNames(normalized)
	}
	return "", nil
}

func shardFailureTestNames(line string) []string {
	idx := strings.Index(line, "failed (exit ")
	if idx < 0 {
		return nil
	}
	rest := line[idx:]
	colon := strings.Index(rest, "):")
	if colon < 0 {
		return nil
	}
	var names []string
	for _, part := range strings.Split(rest[colon+2:], ",") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func appendUnique(dst []string, seen map[string]bool, items ...string) []string {
	for _, item := range items {
		if item != "" && !seen[item] {
			seen[item] = true
			dst = append(dst, item)
		}
	}
	return dst
}

// summarizeActionOutput strips opt-in store/git trace lines and, when a test or shard
// runner failed, records the shard runner's failure lines and failing test names rather
// than the trailing bytes of passing test output.
func summarizeActionOutput(output []byte) string {
	rawLines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	var lines []string
	for _, line := range rawLines {
		if isTraceLine([]byte(line)) || isShardSlotNoticeLine(strings.TrimSpace(line)) {
			continue
		}
		lines = append(lines, line)
	}
	var shardLines, failedTests, diagnostics []string
	seenShards := map[string]bool{}
	seenTests := map[string]bool{}
	seenDiag := map[string]bool{}
	var pendingIndented []string
	inTest := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if shardLine, names := parseShardFailureLine(trimmed); shardLine != "" {
			shardLines = appendUnique(shardLines, seenShards, shardLine)
			failedTests = appendUnique(failedTests, seenTests, names...)
			continue
		}
		if strings.HasPrefix(trimmed, "=== RUN ") || strings.HasPrefix(trimmed, "=== CONT ") || strings.HasPrefix(trimmed, "=== PAUSE ") {
			pendingIndented = nil
			inTest = true
			continue
		}
		if strings.HasPrefix(trimmed, "--- PASS:") {
			pendingIndented = nil
			inTest = false
			continue
		}
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "--- FAIL:"))
			if fields := strings.Fields(rest); len(fields) > 0 {
				failedTests = appendUnique(failedTests, seenTests, fields[0])
			}
			diagnostics = appendUnique(diagnostics, seenDiag, trimmed)
			if len(pendingIndented) > 2 {
				pendingIndented = pendingIndented[len(pendingIndented)-2:]
			}
			diagnostics = appendUnique(diagnostics, seenDiag, pendingIndented...)
			pendingIndented = nil
			inTest = false
			continue
		}
		if inTest && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			pendingIndented = append(pendingIndented, trimmed)
		}
	}

	if len(shardLines) > 0 || len(failedTests) > 0 {
		summary := append([]string(nil), shardLines...)
		joined := strings.Join(summary, "\n")
		var missing []string
		for _, name := range failedTests {
			if !strings.Contains(joined, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			summary = append(summary, "failing tests: "+strings.Join(missing, ", "))
		}
		if len(diagnostics) > 12 {
			diagnostics = diagnostics[:12]
		}
		summary = append(summary, diagnostics...)
		return strings.Join(summary, "\n")
	}

	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}

func (l *Loop) recordAction(ctx context.Context, action, tip string, failure error) {
	l.recordActionWhat(ctx, action, "", tip, failure)
}

func (l *Loop) recordActionWhat(ctx context.Context, action, what, tip string, failure error) {
	detail := ""
	if failure != nil {
		detail = failure.Error()
	}
	l.appendPostLanding(ctx, action, tip, map[string]any{"action": action, "detail": detail, "what": what}, failure)
}
