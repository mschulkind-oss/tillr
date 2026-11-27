package drain

// Architecture and invariants: docs/reference/tillr-restart.md

import (
	"context"
	"encoding/json"
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
	"github.com/mschulkind-oss/tillr/store"
)

const (
	postLandingKind = "drain.post-landing-action"
	rollPendingKind = "drain.roll-pending"
)

// pushTimeout bounds one push. The jail's git broker may ask the human and wait; a push that
// has not finished by then is recorded as failed and retried at the next trigger, rather than
// holding restarts (which wait for an idle push) for as long as it hangs.
const pushTimeout = 5 * time.Minute

func init() {
	if err := event.Register(postLandingKind, Utility); err != nil {
		panic(err)
	}
	if err := event.Register(rollPendingKind, Utility); err != nil {
		panic(err)
	}
}

// Each action has one worker and one replaceable pending landing. Replacing the
// pending tip is safe: both actions operate on the current trunk, not a lane.
type landingAction struct {
	mu      sync.Mutex
	running bool
	pending string
	done    chan struct{}
}

func (a *landingAction) busy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// idle returns a channel closed when the running worker drains its pending
// landing, or nil if no worker is running.
func (a *landingAction) idle() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.done
}

// submit returns the pending trigger it replaced, if any.
func (a *landingAction) submit(landing string, run func(string)) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		replaced := a.pending
		a.pending = landing
		return replaced
	}
	a.running = true
	done := make(chan struct{})
	a.done = done
	go func(tip string) {
		finished := false
		defer func() {
			if !finished {
				a.mu.Lock()
				a.running = false
				a.done = nil
				a.mu.Unlock()
				close(done)
			}
		}()
		for {
			run(tip)
			a.mu.Lock()
			tip, a.pending = a.pending, ""
			if tip == "" {
				a.running = false
				a.done = nil
				finished = true
				a.mu.Unlock()
				close(done)
				return
			}
			a.mu.Unlock()
		}
	}(landing)
	return ""
}

const piForkPatchRef = "refs/heads/patch/pi"

var piSeriesPaths = []string{"yolo-packs/pi-fork/", "bin/pi-forks.list"}

// rollBlocksDispatch reports whether a trunk landing that may roll or is rolling
// tillr's binaries is in progress, so no worker or judge run is dispatched against
// an adapter script from the new commit while old binaries are still installed.
func (l *Loop) rollBlocksDispatch() bool {
	return l.rollChecking.Load() > 0 || l.rollAction.busy()
}

func (l *Loop) recordRollPending(ctx context.Context, tip string) {
	if l.cfg.Store == "" || tip == "" {
		return
	}
	body, err := json.Marshal(struct {
		Commit string `json:"commit"`
	}{Commit: tip})
	if err != nil {
		return
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: rollPendingKind, ObjectType: "landing", ObjectID: &tip, Payload: body, At: time.Now().UTC()})
		return err
	})
}

func (l *Loop) landWithActions(ctx context.Context, candidate Candidate) (LandOutcome, error) {
	mayRoll := candidate.BaseRef == "" && l.postLandingSettings().Roll.Command != ""
	if mayRoll {
		l.rollChecking.Add(1)
		defer func() {
			if l.rollChecking.Add(-1) == 0 && !l.rollAction.busy() {
				l.signalPassWake()
			}
		}()
	}
	mayPiRoll := candidate.BaseRef == piForkPatchRef && l.cfg.PostLanding.Roll.Command != ""
	if mayPiRoll {
		l.piRollChecking.Add(1)
		defer l.piRollChecking.Add(-1)
	}
	out, err := l.cfg.Steps.Land(ctx, candidate)
	if err == nil && out.Result == LandLanded {
		if candidate.BaseRef == "" && l.cfg.Store != "" {
			if recErr := RecordTrunkBranchInStore(ctx, l.cfg.Store, l.trunkRef()); recErr != nil {
				return out, recErr
			}
		}
		if candidate.BaseRef == "" {
			tip := out.TrunkAfter
			if tip == "" && l.cfg.Root != "" {
				tip, _ = l.revParse(context.WithoutCancel(ctx), l.cfg.Root, l.trunkRef())
			}
			if tip != "" {
				l.checkActiveGatesOnTrunkMove(ctx, tip)
			}
		}
		if out.TrunkAfter != "" {
			l.afterLandingCandidate(context.WithoutCancel(ctx), candidate, out.TrunkAfter)
		}
	}
	return out, err
}

func (l *Loop) afterLanding(ctx context.Context, tip string) {
	l.afterLandingCandidate(ctx, Candidate{}, tip)
}

func (l *Loop) afterLandingCandidate(ctx context.Context, candidate Candidate, tip string) {
	if candidate.BaseRef == "" {
		l.reloadSettingsOnLanding(ctx, tip)
	}
	postLanding := l.postLandingSettings()
	if candidate.BaseRef == "" {
		if p := postLanding.Push; p.What == settings.PushTip {
			l.submitTipPush(ctx, p, tip)
		}
	}
	r := postLanding.Roll
	if candidate.BaseRef == piForkPatchRef {
		if r.Command == "" {
			l.recordSkippedPiRoll(ctx, tip, "no [roll] command configured")
			return
		}
		l.submitPiRoll(ctx, r.Command, tip, true)
		return
	}
	if r.Command == "" {
		return
	}
	if isExtensionPatchBranch(candidate.BaseRef) {
		l.submitExtensionRoll(ctx, r.Command, tip)
		return
	}
	if candidate.BaseRef != "" {
		return
	}
	l.rollChecking.Add(1)
	defer func() {
		if l.rollChecking.Add(-1) == 0 && !l.rollAction.busy() {
			l.signalPassWake()
		}
	}()
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
		l.recordRollPending(ctx, tip)
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
					if err = l.fileRestartRequest(ctx, role); err != nil {
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
		l.submitPiRoll(ctx, r.Command, tip, false)
	}
	if matchesExtensionRollPaths(changed) {
		l.submitExtensionRoll(ctx, r.Command, tip)
	}
}

// fileRestartRequest records one post-landing restart request for role, waiting
// on a busy store the same way the loop's pass writes do (no retry count, no
// time limit) until the write succeeds, ctx is cancelled, or a non-busy error
// occurs.
func (l *Loop) fileRestartRequest(ctx context.Context, role string) error {
	now := l.cfg.Now
	if now == nil {
		now = time.Now
	}
	for {
		start := time.Now()
		_, err := restart.FileWithOptions(ctx, l.cfg.Store, role, now(), l.sessionOptions...)
		if err == nil {
			return nil
		}
		if !store.IsBusy(err) || ctx.Err() != nil {
			return err
		}
		l.reportf("loop: store busy during %s restart request; will retry: %v\n", role, err)
		l.recordBusyWrite(role+"-restart-request", time.Since(start), now())
		delay := l.cfg.BackoffBase
		if delay <= 0 {
			delay = time.Second
		}
		if !waitFor(ctx, delay) {
			return ctx.Err()
		}
	}
}

func (l *Loop) submitTipPush(ctx context.Context, p settings.Push, tip string) {
	l.pushAction.submit(tip, func(tip string) {
		_, err := l.pushTipOnce(ctx, p)
		l.recordPush(ctx, settings.PushTip, tip, err)
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

// A restart waiting on this proof must go before new gates and proofs. Starting
// a push here would defer the restart and let those gates start in the old image.
func (l *Loop) offerGreenAdvance(ctx context.Context, tip string) {
	if l.postLandingSettings().Push.What != settings.PushGreen {
		l.reportf("loop: push of green %s skipped: [push] what is not green\n", shortSHA(tip))
		return
	}
	stop, err := l.restartStopsNextHeavyProof(ctx)
	if err != nil {
		l.reportf("loop: push of green %s skipped: could not check restart: %s\n", shortSHA(tip), strings.Join(strings.Fields(err.Error()), " "))
		return
	}
	if stop {
		l.reportf("loop: push of green %s deferred to the replacement loop\n", shortSHA(tip))
		return
	}
	l.afterGreen(context.WithoutCancel(ctx), tip)
}

func (l *Loop) offerStartupGreen(ctx context.Context) {
	if l.postLandingSettings().Push.What != settings.PushGreen || l.greenPushAtStart.Load() {
		return
	}
	// A pass can continue with a restart still pending behind an active proof.
	// Do not let the startup catch-up add a new reason to defer that restart.
	if l.cfg.Restart != nil {
		pending, err := l.restarterPendingWith(ctx, nil)
		if pending || err != nil {
			return
		}
	}
	if l.greenPushAtStart.CompareAndSwap(false, true) {
		l.afterGreen(context.WithoutCancel(ctx), "start")
	}
}

func isGreenAdvance(trigger string) bool {
	return trigger != "" && trigger != "start" && trigger != "hold-cleared"
}

// afterGreen runs when the green pointer advances. In green mode it pushes the pointer's
// commit, not the trunk tip: the remote only ever receives what the proof has passed. The
// argument names the advance for the log; what is pushed is recomputed from the pointer when
// the push runs, so a failed push needs no counter or timer to be retried, only the next
// advance. It is also called once at loop start, so a remote left behind while no loop ran
// catches up.
func (l *Loop) afterGreen(ctx context.Context, advanced string) {
	p := l.postLandingSettings().Push
	if p.What != settings.PushGreen {
		return
	}
	replaced := l.pushAction.submit(advanced, func(trigger string) {
		green, err := l.pushGreenOnce(ctx, p)
		// Proof, not publication, makes a roll due. Replay may have moved green.
		if l.postLandingSettings().Roll.Command != "" {
			l.flushPendingRemoteRoll(ctx, optionalCommit(ctx, l.cfg.Root, GreenRef))
		}
		if isGreenAdvance(trigger) {
			switch {
			case green != "" && green != trigger:
				l.reportf("loop: push of green %s skipped: pointer now at %s\n", shortSHA(trigger), shortSHA(green))
			case green == "" && err == nil:
				l.reportf("loop: push of green %s skipped: green pointer is absent\n", shortSHA(trigger))
			case green == "" && err != nil:
				green = trigger
			}
		}
		if green != "" || err != nil {
			l.recordPush(ctx, settings.PushGreen, green, err)
		}
	})
	if isGreenAdvance(replaced) && replaced != advanced {
		reason := "superseded by a newer push request"
		if isGreenAdvance(advanced) {
			reason = "superseded by green " + shortSHA(advanced)
		}
		l.reportf("loop: push of green %s skipped: %s\n", shortSHA(replaced), reason)
	}
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
		upstream, err := actionCommand(ctx, root, "git", "for-each-ref", "--format=%(upstream:remotename)%09%(upstream:remoteref)", "refs/heads/"+trunkBranchFor(root))
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
	trunkRef := trunkBranchFor(root)
	pushed := optionalCommit(ctx, root, "refs/heads/"+trunkRef)
	if err != nil {
		return pushed, err
	}
	if pushed == "" {
		return "", fmt.Errorf("cannot resolve %s for push", trunkRef)
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
	conflict, err := l.activeRemoteSyncConflict(ctx)
	if err != nil {
		l.reportf("loop: push of %s %s could not be classified: reading active remote-sync repair: %s\n", what, shortSHA(tip), strings.Join(strings.Fields(err.Error()), " "))
		return PushOutcome{}, err
	}
	if conflict != nil {
		failure = conflict
	} else if errors.Is(failure, errAlreadyPublished) {
		l.reportf("loop: push of %s %s skipped: %s\n", what, shortSHA(tip), errAlreadyPublished)
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
		l.reportf("loop: push of %s %s waits for proof: %s\n", what, shortSHA(tip), strings.Join(strings.Fields(failure.Error()), " "))
	} else if kind == pushKindConflict && state != nil && state.escalated == "" {
		l.reportf("loop: push of %s %s waits for conflict repair: %s\n", what, shortSHA(tip), strings.Join(strings.Fields(failure.Error()), " "))
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
