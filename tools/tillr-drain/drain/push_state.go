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

const (
	pushKindOK             = "push-ok"
	pushKindRemoteDeclined = "push-remote-declined"
	pushKindPatchMoveHeld  = "push-patch-move-held"
	pushKindFailed         = "push-failed"
)

// pushStateError records what the push reported without changing its detail for older readers.
type pushStateError struct {
	kind, detail string
	noRefLine    bool
	escalated    string
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

// advancePublished points the published pointer at a commit the remote branch now holds.
func advancePublished(ctx context.Context, root, remote, ref, sha string) error {
	if _, err := rawGit(ctx, root, nil, "update-ref", publishedRef(remote, ref), sha); err != nil {
		return fmt.Errorf("recording %s as the published pointer failed: %w", shortSHA(sha), err)
	}
	return nil
}

// confirmPublished records a known remote tip without replacing a pointer read as
// already at that tip, ahead of it, or diverged from it.
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
		remoteTip := optionalCommit(ctx, root, publishedRef(remote, ref))
		if remoteTip == "" {
			tracking := "refs/remotes/" + remote + "/" + strings.TrimPrefix(ref, "refs/heads/")
			remoteTip = optionalCommit(ctx, root, tracking)
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
