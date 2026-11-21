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
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	pushKindOK             = "push-ok"
	pushKindRemoteDeclined = "push-remote-declined"
	pushKindPatchMoveHeld  = "push-patch-move-held"
	pushKindFailed         = "push-failed"
	pushKindRewritten      = "remote-rewritten"
	pushKindProving        = "remote-sync-proving"
)

// pushStateError records what the push reported without changing its detail for older readers.
type pushStateError struct {
	kind, detail           string
	remoteTip, previousTip string
	rebasedTip             string
	noRefLine              bool
	escalated              string
}

func (e *pushStateError) Error() string { return e.detail }

var (
	remoteRejectedLine = regexp.MustCompile(`(?m)^!\t[^\t]*\t\[remote rejected\]`)
	porcelainRefLine   = regexp.MustCompile(`(?m)^[ +\-*=!]\t[^\t]*:[^\t]*\t`)
)

// A local pre-push rejection emits no status for the ref, just like a transport
// failure. Consult the store, not the hook's human-readable output, for the hold.
func (l *Loop) classifyPushFailure(ctx context.Context, failure error) (string, *pushStateError) {
	var state *pushStateError
	if !errors.As(failure, &state) {
		return pushKindFailed, nil
	}
	if state.kind == pushKindFailed && state.noRefLine && l.patchMoveHeld(ctx) {
		return pushKindPatchMoveHeld, state
	}
	return state.kind, state
}

func (l *Loop) patchMoveHeld(ctx context.Context) bool {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false
	}
	defer s.Close()
	var held bool
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		holds, err := patchmoves.Pending(r)
		held = len(holds) > 0
		return err
	})
	return err == nil && held
}

// publishedRef names the published pointer of a remote branch.
func publishedRef(remote, ref string) string {
	return land.PublishedRefPrefix + remote + "/" + strings.TrimPrefix(ref, "refs/heads/")
}

// optionalCommit answers the commit a ref names, or "" when it cannot be resolved.
func optionalCommit(ctx context.Context, root, ref string) string {
	out, err := rawGit(ctx, root, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// advancePublished is the only writer of the published pointer. A tip confirmed
// by a fetch or a successful push may be stale by the time we record it.
func advancePublished(ctx context.Context, root, remote, ref, sha string) error {
	pointer := publishedRef(remote, ref)
	cur := optionalCommit(ctx, root, pointer)
	for cur != sha {
		if cur != "" {
			if forward, err := trunkAncestor(ctx, root, cur, sha); err != nil || !forward {
				return err
			}
		}
		// An empty old value requires the pointer not to exist. If another
		// writer won the swap, check ancestry against its value before retrying.
		_, err := rawGit(ctx, root, nil, "update-ref", "--no-deref", pointer, sha, cur)
		if err == nil {
			return nil
		}
		next := optionalCommit(ctx, root, pointer)
		if next == cur || ctx.Err() != nil {
			return fmt.Errorf("recording %s as the published pointer failed: %w", shortSHA(sha), err)
		}
		cur = next
	}
	return nil
}

// priorPublished uses the tracking ref until this clone has a published pointer.
func priorPublished(ctx context.Context, root, remote, ref string) string {
	if sha := optionalCommit(ctx, root, publishedRef(remote, ref)); sha != "" {
		return sha
	}
	return optionalCommit(ctx, root, "refs/remotes/"+remote+"/"+strings.TrimPrefix(ref, "refs/heads/"))
}

func guardPublished(ctx context.Context, root, remote, ref string) (string, error) {
	prev := priorPublished(ctx, root, remote, ref)
	if prev == "" {
		return "", nil
	}
	tip, err := fetchPushDestination(ctx, root, remote, ref)
	if err != nil {
		return "", err
	}
	if err := checkPublishedAncestry(ctx, root, remote, ref, prev, tip); err != nil {
		return "", err
	}
	if err := advancePublished(ctx, root, remote, ref, tip); err != nil {
		return "", err
	}
	return tip, nil
}

func fetchPushDestination(ctx context.Context, root, remote, ref string) (string, error) {
	// Fetch only into FETCH_HEAD: a rejected observation must not replace the
	// tracking fallback or change any other ref.
	if _, err := rawGit(ctx, root, nil, "fetch", "--refmap=", "--no-tags", "--no-prune", "--no-recurse-submodules", "--", remote, ref); err != nil {
		return "", &pushStateError{kind: pushKindFailed, detail: fmt.Sprintf("fetching %s %s failed: %v", remote, ref, err)}
	}
	out, err := rawGit(ctx, root, nil, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", &pushStateError{kind: pushKindFailed, detail: err.Error()}
	}
	return strings.TrimSpace(out), nil
}

func checkPublishedAncestry(ctx context.Context, root, remote, ref, prev, tip string) error {
	if prev == "" {
		return nil
	}
	forward, err := trunkAncestor(ctx, root, prev, tip)
	if err != nil {
		return &pushStateError{kind: pushKindFailed, detail: err.Error()}
	}
	if forward {
		return nil
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	return &pushStateError{kind: pushKindRewritten, remoteTip: tip, previousTip: prev, escalated: "operator",
		detail: fmt.Sprintf("remote-rewritten: %s/%s was force-pushed (%s -> %s); automatic pushes stopped so the commits it dropped are not published again.\nmover: operator; reconcile force-pushed %s/%s (%s -> %s). Nothing was changed",
			remote, branch, shortSHA(prev), shortSHA(tip), remote, branch, shortSHA(prev), shortSHA(tip))}
}

// greenProofHold reads replay facts independently of later push outcomes. Only
// tips still on main and not contained in the selected green commit can hold it.
func (l *Loop) greenProofHold(ctx context.Context, remote, ref, green string) (string, error) {
	root := l.cfg.Root
	main := optionalCommit(ctx, root, "refs/heads/"+TrunkRef)
	if main == "" {
		return "", fmt.Errorf("cannot resolve %s for the proof hold", TrunkRef)
	}
	if main == green {
		return "", nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var tips []string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT json_extract(payload,'$.rebased_tip') AS tip FROM events
 WHERE kind=? AND json_extract(payload,'$.action')='push' AND json_extract(payload,'$.what')=?
 AND json_extract(payload,'$.remote')=? AND json_extract(payload,'$.ref')=?
 AND json_extract(payload,'$.kind')=? AND json_extract(payload,'$.rebased_tip')<>''
 GROUP BY tip ORDER BY max(seq) DESC`, postLandingKind, settings.PushGreen, remote, ref, pushKindProving)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tip string
			if err := rows.Scan(&tip); err != nil {
				return err
			}
			tips = append(tips, tip)
		}
		return rows.Err()
	})
	if err != nil || len(tips) == 0 {
		return "", err
	}
	// Intersect the recorded tips with the unproved range in one Git call.
	out, err := rawGit(ctx, root, nil, "rev-list", green+".."+main, "--")
	if err != nil {
		return "", err
	}
	unproved := make(map[string]bool)
	for _, sha := range strings.Fields(out) {
		unproved[sha] = true
	}
	for _, tip := range tips {
		if unproved[tip] {
			return tip, nil
		}
	}
	return "", nil
}

func awaitingRebasedProof(tip string) string {
	return "mover: the heavy runner; prove rebased tip " + shortSHA(tip) + " before pushing"
}

// pushCounts counts what the known remote tip lacks, or returns -1 for an unknown count.
func pushCounts(ctx context.Context, root, remoteTip string) (green, main int) {
	count := func(ref string) int {
		if remoteTip == "" {
			return -1
		}
		out, err := rawGit(ctx, root, nil, "rev-list", "--count", remoteTip+".."+ref, "--")
		if err != nil {
			return -1
		}
		n, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return -1
		}
		return n
	}
	return count(GreenRef), count("refs/heads/" + TrunkRef)
}

func (l *Loop) recordPushState(ctx context.Context, what, tip, kind string, state *pushStateError, failure error) {
	payload := map[string]any{"action": "push", "what": what, "detail": "", "kind": kind}
	if failure != nil {
		payload["detail"] = failure.Error()
	}
	root := l.cfg.Root
	if remote, ref, err := pushDestination(ctx, root, l.cfg.PostLanding.Push); err == nil {
		payload["remote"], payload["ref"] = remote, ref
		remoteTip := priorPublished(ctx, root, remote, ref)
		if state != nil && state.remoteTip != "" {
			remoteTip = state.remoteTip
			payload["previous_remote_tip"] = state.previousTip
		}
		if remoteTip != "" {
			payload["remote_tip"] = remoteTip
		}
		g, m := pushCounts(ctx, root, remoteTip)
		if g >= 0 {
			payload["unpublished_green_count"] = g
		}
		if m >= 0 {
			payload["unpublished_main_count"] = m
		}
	}
	if state != nil && state.escalated != "" {
		payload["escalated_mover"] = state.escalated
	}
	if state != nil && state.rebasedTip != "" {
		payload["rebased_tip"] = state.rebasedTip
	}
	if kind != pushKindOK {
		since := l.lastPushSince(ctx)
		if since == "" {
			now := l.cfg.Now
			if now == nil {
				now = time.Now
			}
			since = now().UTC().Format(time.RFC3339)
		}
		payload["since_ts"] = since
	}
	l.appendPostLanding(ctx, "push", tip, payload, failure)
}

type pushRecord struct {
	Kind  string `json:"kind"`
	Since string `json:"since_ts"`
}

func (l *Loop) lastPushRecord(ctx context.Context) pushRecord {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return pushRecord{}
	}
	defer s.Close()
	var raw []byte
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`, postLandingKind).Scan(&raw)
	})
	var prev pushRecord
	if err != nil || json.Unmarshal(raw, &prev) != nil {
		return pushRecord{}
	}
	return prev
}

func (l *Loop) lastPushSince(ctx context.Context) string {
	prev := l.lastPushRecord(ctx)
	if prev.Kind == "" || prev.Kind == pushKindOK {
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
