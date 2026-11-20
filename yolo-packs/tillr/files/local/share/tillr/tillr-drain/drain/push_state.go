package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The states a push can end in, recorded as `kind` on drain.post-landing-action beside the
// older action, detail and what (docs/design/tillr-refused-push.md, Sections 3 and 7). A
// successful push is pushKindOK; every other kind is a state with a mover that is not "the
// next green advance".
const (
	pushKindOK             = "push-ok"
	pushKindRemoteDeclined = "push-remote-declined"
	pushKindPatchMoveHeld  = "push-patch-move-held"
	pushKindFailed         = "push-failed"
	pushKindRewritten      = "remote-rewritten"
	pushKindWorktreeDirty  = "remote-sync-worktree-dirty"
	pushKindProving        = "remote-sync-proving"
	pushKindConflict       = "remote-sync-conflict"
)

// pushStateError is a push outcome with a named kind. Its text is the detail the older
// readers of the event show.
type pushStateError struct {
	kind, detail string
	remoteTip    string // the fetched remote tip R, when the outcome concerns it
	previousTip  string // the published pointer's commit before the fetch, for a rewrite
	noRefLine    bool   // git printed no porcelain status line for the pushed ref
	rebasedTip   string // the rebased tip T' a heal produced, awaiting proof
	escalated    string // who the state waits on once it needs a person: "operator" or "owner"
}

func (e *pushStateError) Error() string { return e.detail }

var (
	remoteRejectedLine = regexp.MustCompile(`(?m)^!\t[^\t]*\t\[remote rejected\]`)
	porcelainRefLine   = regexp.MustCompile(`(?m)^[ +\-*=!]\t[^\t]*:[^\t]*\t`)
)

// publishedRef names the published pointer of a remote branch.
func publishedRef(remote, ref string) string {
	return land.PublishedRefPrefix + remote + "/" + strings.TrimPrefix(ref, "refs/heads/")
}

// optionalCommit answers the commit a ref names, or "" when the ref does not exist.
func optionalCommit(ctx context.Context, root, ref string) string {
	out, err := rawGit(ctx, root, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// priorPublished answers the commit this clone last confirmed on the remote branch: the
// published pointer, or the remote-tracking ref only while no pointer has been written.
func priorPublished(ctx context.Context, root, remote, ref string) string {
	if sha := optionalCommit(ctx, root, publishedRef(remote, ref)); sha != "" {
		return sha
	}
	return optionalCommit(ctx, root, "refs/remotes/"+remote+"/"+strings.TrimPrefix(ref, "refs/heads/"))
}

// advancePublished points the published pointer at a commit the remote branch now holds.
func advancePublished(ctx context.Context, root, remote, ref, sha string) error {
	if _, err := rawGit(ctx, root, nil, "update-ref", publishedRef(remote, ref), sha); err != nil {
		return fmt.Errorf("pushed %s, but recording the published pointer failed: %w", shortSHA(sha), err)
	}
	return nil
}

// confirmPublished makes sure the published pointer holds at least sha, which a push that
// was not needed showed the remote already contains. A pointer already at or past sha stays,
// as does one that has diverged from it, which only a fetch can sort out.
func confirmPublished(ctx context.Context, root, remote, ref, sha string) error {
	cur := optionalCommit(ctx, root, publishedRef(remote, ref))
	if cur != "" {
		if ahead, err := trunkAncestor(ctx, root, sha, cur); err != nil || ahead {
			return err
		}
		if behind, err := trunkAncestor(ctx, root, cur, sha); err != nil || !behind {
			return err
		}
	}
	return advancePublished(ctx, root, remote, ref, sha)
}

// classifyPushFailure answers the kind of a push that returned err, and the text for it. The
// patch-move hold is read from the store: a pre-push hook that refused for a held patch move
// prints no porcelain status for the ref, the same as any other hook or broker refusal.
func (l *Loop) classifyPushFailure(ctx context.Context, failure error) (kind string, state *pushStateError) {
	var se *pushStateError
	if errors.As(failure, &se) {
		state = se
		kind = se.kind
	} else {
		kind = pushKindFailed
	}
	if kind == pushKindFailed && (state == nil || state.noRefLine) && l.patchMoveHeld(ctx) {
		kind = pushKindPatchMoveHeld
	}
	return kind, state
}

func (l *Loop) patchMoveHeld(ctx context.Context) bool {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false
	}
	defer s.Close()
	held := false
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		holds, err := patchmoves.Pending(r)
		held = err == nil && len(holds) > 0
		return nil
	})
	return held
}

// pushCounts answers how many commits the published pointer lacks, for the green pointer and
// the trunk, or -1 for one that cannot be counted.
func pushCounts(ctx context.Context, root, published string) (green, main int) {
	count := func(tip string) int {
		if published == "" || tip == "" {
			return -1
		}
		out, err := rawGit(ctx, root, nil, "rev-list", "--count", published+".."+tip)
		if err != nil {
			return -1
		}
		n, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return -1
		}
		return n
	}
	return count(optionalCommit(ctx, root, GreenRef)), count(optionalCommit(ctx, root, "refs/heads/"+TrunkRef))
}

// recordPushState writes a push outcome: the older {action, detail, what} fields, which stay
// as they were, and the structured state of design Section 7. since is kept across
// consecutive non-ok outcomes and cleared by an ok one.
func (l *Loop) recordPushState(ctx context.Context, what, tip, kind string, state *pushStateError, failure error) {
	payload := map[string]any{"action": "push", "what": what, "detail": "", "kind": kind}
	if failure != nil {
		payload["detail"] = failure.Error()
	}
	if remote, ref, err := pushDestination(ctx, l.cfg.Root, l.cfg.PostLanding.Push); err == nil {
		payload["remote"], payload["ref"] = remote, ref
		published := optionalCommit(ctx, l.cfg.Root, publishedRef(remote, ref))
		if published != "" {
			payload["remote_tip"] = published
		}
		if g, m := pushCounts(ctx, l.cfg.Root, priorPublished(ctx, l.cfg.Root, remote, ref)); g >= 0 || m >= 0 {
			if g >= 0 {
				payload["unpublished_green_count"] = g
			}
			if m >= 0 {
				payload["unpublished_main_count"] = m
			}
		}
	}
	if state != nil {
		if state.remoteTip != "" {
			payload["remote_tip"] = state.remoteTip
		}
		if state.previousTip != "" {
			payload["previous_remote_tip"] = state.previousTip
		}
		if state.rebasedTip != "" {
			payload["rebased_tip"] = state.rebasedTip
		}
		if state.escalated != "" {
			payload["escalated_mover"] = state.escalated
		}
	}
	now := l.cfg.Now().UTC()
	if kind != pushKindOK {
		payload["since"] = now.Format(time.RFC3339)
		if prev := l.lastPushSince(ctx); prev != "" {
			payload["since"] = prev
		}
	}
	l.appendPostLanding(ctx, "push", tip, payload, failure)
}

// lastPushSince answers the since of the newest push outcome when it was not ok, so a state
// that goes on keeps the time it began.
func (l *Loop) lastPushSince(ctx context.Context) string {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return ""
	}
	defer s.Close()
	var raw string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`, postLandingKind).Scan(&raw)
	})
	if err != nil {
		return ""
	}
	var prev struct {
		Kind  string `json:"kind"`
		Since string `json:"since"`
	}
	if json.Unmarshal([]byte(raw), &prev) != nil || prev.Kind == "" || prev.Kind == pushKindOK {
		return ""
	}
	return prev.Since
}

func (l *Loop) appendPostLanding(ctx context.Context, action, tip string, payload map[string]any, failure error) {
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err == nil {
		defer s.Close()
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			_, err = tx.Append(event.Event{Kind: postLandingKind, ObjectType: "landing", ObjectID: &tip, Payload: body, At: time.Now().UTC()})
			return err
		})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "post-landing %s at %s: recording outcome: %v (action failure: %v)\n", action, tip, err, failure)
	}
}

// rollAfterRemoteSync runs the roll command when the commits a remote rebase brought in
// (base..remote tip) changed what the roll watches, so an upstream change to tillr's own
// tools reaches the running binaries like a local landing of it would.
func (l *Loop) rollAfterRemoteSync(ctx context.Context, res healResult) {
	r := l.cfg.PostLanding.Roll
	if r.Command == "" || res.base == "" || res.remoteTip == "" || res.base == res.remoteTip {
		return
	}
	changed, err := actionCommand(ctx, l.cfg.Root, "git", "diff", "--name-only", "--no-renames", "-z", res.base, res.remoteTip, "--")
	if err != nil {
		l.reportRollFailure(res.newTip, "", err)
		l.recordAction(ctx, "roll", res.newTip, err)
		return
	}
	l.rollChanged(ctx, r, changed, res.newTip)
}

func (l *Loop) maybeRollAfterRemoteSync(ctx context.Context, res healResult) {
	r := l.cfg.PostLanding.Roll
	if r.Command == "" || res.base == "" || res.remoteTip == "" || res.base == res.remoteTip {
		return
	}
	green := optionalCommit(ctx, l.cfg.Root, GreenRef)
	if green != "" {
		if ok, err := trunkAncestor(ctx, l.cfg.Root, res.remoteTip, green); err == nil && ok {
			l.rollAfterRemoteSync(ctx, res)
			return
		}
	}
	l.remoteRollMu.Lock()
	copyRes := res
	l.pendingRemoteRoll = &copyRes
	l.remoteRollMu.Unlock()
}

func (l *Loop) flushPendingRemoteRoll(ctx context.Context, green string) {
	if green == "" {
		return
	}
	l.remoteRollMu.Lock()
	pending := l.pendingRemoteRoll
	l.remoteRollMu.Unlock()
	if pending == nil {
		return
	}
	if ok, err := trunkAncestor(ctx, l.cfg.Root, pending.remoteTip, green); err != nil || !ok {
		return
	}
	l.remoteRollMu.Lock()
	if l.pendingRemoteRoll == pending {
		l.pendingRemoteRoll = nil
	}
	l.remoteRollMu.Unlock()
	l.rollAfterRemoteSync(ctx, *pending)
}
