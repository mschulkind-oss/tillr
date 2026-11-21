package drain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/settings"
)

// PushOutcome is the recorded result of a synchronous publication turn.
type PushOutcome struct {
	Action string `json:"action"`
	What   string `json:"what"`
	Kind   string `json:"kind"`
	Tip    string `json:"tip,omitempty"`
	Detail string `json:"detail,omitempty"`
	Mover  string `json:"escalated_mover,omitempty"`
	OK     bool   `json:"ok"`
}

// PushOnce uses the loop's publication and locked replay paths. The loop owns deployment.
func PushOnce(ctx context.Context, root, storePath string) (PushOutcome, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return PushOutcome{}, err
	}
	cfg, err := settings.Read(root)
	if err != nil {
		return PushOutcome{}, err
	}
	if _, err := os.Stat(storePath); err != nil {
		return PushOutcome{}, err
	}
	l := &Loop{cfg: LoopConfig{Root: root, Store: storePath, Now: time.Now, Report: io.Discard, PostLanding: cfg}}
	var tip string
	switch p := cfg.Push; p.What {
	case settings.PushTip:
		tip, err = l.pushTipOnce(ctx, p)
	case settings.PushGreen:
		tip, err = l.pushGreenOnce(ctx, p)
	}
	return l.recordPush(ctx, cfg.Push.What, tip, err)
}

func (l *Loop) pushTipOnce(ctx context.Context, p settings.Push) (string, error) {
	tip, err := pushTrunkResult(ctx, l.cfg.Root, p)
	var refused *refusedPushError
	if errors.As(err, &refused) {
		if _, err = l.healRefusedPushResult(ctx, p, err); err == nil {
			return pushTrunkResult(ctx, l.cfg.Root, p)
		}
	}
	return tip, err
}

func (l *Loop) pushGreenOnce(ctx context.Context, p settings.Push) (string, error) {
	green, err := pushGreenChecked(ctx, l.cfg.Root, p, l.greenProofHold)
	var refused *refusedPushError
	if errors.As(err, &refused) {
		res, err := l.healRefusedPushResult(ctx, p, err)
		if err == nil {
			l.maybeRollAfterRemoteSync(ctx, res)
			err = &pushStateError{kind: pushKindProving, remoteTip: res.remoteTip, rebasedTip: res.newTip,
				detail: fmt.Sprintf("the remote had moved: %d unpublished commits were rebased onto it; %s", res.replayed, awaitingRebasedProof(res.newTip))}
		}
		return green, err
	}
	return green, err
}

func onMain(ctx context.Context, root string) bool {
	head, err := rawGit(ctx, root, nil, "symbolic-ref", "-q", "HEAD")
	return err == nil && strings.TrimSpace(head) == "refs/heads/"+TrunkRef
}

// Only a previously blocked push can be woken by a pass. Checking for clearance
// uses local state; the actual push turn fetches and revalidates under the usual replay rules.
func (l *Loop) maybeRecoverClearedPushHold(ctx context.Context) {
	p, root := l.cfg.PostLanding.Push, l.cfg.Root
	if (p.What != settings.PushGreen && p.What != settings.PushTip) || l.pushAction.busy() || l.cfg.Store == "" {
		return
	}
	last := l.lastPushRecord(ctx)
	switch last.Kind {
	case pushKindPatchMoveHeld:
		if l.patchMoveHeld(ctx) {
			return
		}
	case pushKindWorktreeDirty:
		if !onMain(ctx, root) || last.ResetTarget == "" || optionalCommit(ctx, root, "MERGE_HEAD") != "" {
			return
		}
		// Test the two-tree checkout that reset --keep would do, without writing
		// the index or worktree. Unrelated local changes don't block the move.
		if _, err := rawGit(ctx, root, nil, "read-tree", "--dry-run", "-m", "-u", "HEAD", last.ResetTarget); err != nil {
			return
		}
	default:
		return
	}
	l.triggerPushNow(context.WithoutCancel(ctx))
}

func (l *Loop) triggerPushNow(ctx context.Context) {
	p := l.cfg.PostLanding.Push
	if !HasUnpublishedCommits(ctx, l.cfg.Root, p) {
		return
	}
	switch p.What {
	case settings.PushGreen:
		l.afterGreen(ctx, "hold-cleared")
	case settings.PushTip:
		if tip := optionalCommit(ctx, l.cfg.Root, "refs/heads/"+TrunkRef); tip != "" {
			l.submitTipPush(ctx, p, tip)
		}
	}
}

// HasUnpublishedCommits reports whether the configured publication tip is ahead of the known remote tip.
func HasUnpublishedCommits(ctx context.Context, root string, p settings.Push) bool {
	remote, ref, err := pushDestination(ctx, root, p)
	if err != nil {
		return false
	}
	green, main := pushCounts(ctx, root, priorPublished(ctx, root, remote, ref))
	return p.What == settings.PushGreen && green > 0 || p.What == settings.PushTip && main > 0
}
