package drain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// PushOnce uses the loop's publication and locked replay paths, without rolling a deployment.
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
			err = &pushStateError{kind: pushKindProving, remoteTip: res.remoteTip, rebasedTip: res.newTip,
				detail: fmt.Sprintf("the remote had moved: %d unpublished commits were rebased onto it; %s", res.replayed, awaitingRebasedProof(res.newTip))}
		}
		return green, err
	}
	return green, err
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
