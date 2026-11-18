package drain

import (
	"context"
	"encoding/json"
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

const postLandingKind = "drain.post-landing-action"

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

func (l *Loop) landWithActions(ctx context.Context, candidate Candidate) (LandOutcome, error) {
	out, err := l.cfg.Steps.Land(ctx, candidate)
	if err == nil && out.Result == LandLanded && out.TrunkAfter != "" {
		l.afterLanding(context.WithoutCancel(ctx), out.TrunkAfter)
	}
	return out, err
}

func (l *Loop) afterLanding(ctx context.Context, tip string) {
	if p := l.cfg.PostLanding.Push; p.AfterLanding {
		l.pushAction.submit(tip, func(tip string) { l.recordAction(ctx, "push", tip, pushTrunk(ctx, l.cfg.Root, p)) })
	}
	if r := l.cfg.PostLanding.Roll; r.Command != "" {
		changed, err := actionCommand(ctx, l.cfg.Root, "git", "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", tip+"^", tip, "--")
		if err != nil {
			l.recordAction(ctx, "roll", tip, err)
			return
		}
		if !matchesRollPaths(changed, r.Paths) {
			return
		}
		l.rollAction.submit(tip, func(tip string) {
			_, err := actionCommand(ctx, l.cfg.Root, "sh", "-c", r.Command)
			if err == nil {
				for _, role := range []string{restart.RoleView, restart.RoleDrain} {
					if _, err = restart.File(ctx, l.cfg.Store, role, time.Now()); err != nil {
						break
					}
				}
			}
			l.recordAction(ctx, "roll", tip, err)
		})
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

func pushTrunk(ctx context.Context, root string, p settings.Push) error {
	remote, ref := p.Remote, p.Ref
	if remote == "" || ref == "" {
		upstream, err := actionCommand(ctx, root, "git", "for-each-ref", "--format=%(upstream:remotename)%09%(upstream:remoteref)", "refs/heads/"+TrunkRef)
		if err != nil {
			return err
		}
		pair := strings.SplitN(strings.TrimRight(upstream, "\n"), "\t", 2)
		if len(pair) != 2 {
			return fmt.Errorf("trunk has no configured upstream")
		}
		if remote == "" {
			remote = pair[0]
		}
		if ref == "" {
			ref = pair[1]
		}
	}
	if remote == "" || ref == "" {
		return fmt.Errorf("trunk has no configured upstream")
	}
	// Supply an explicit non-forcing refspec, independent of remote push defaults.
	_, err := actionCommand(ctx, root, "git", "push", "--", remote, "refs/heads/"+TrunkRef+":"+ref)
	return err
}

func actionCommand(ctx context.Context, root, program string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
		if len(lines) > 20 {
			lines = lines[len(lines)-20:]
		}
		return "", fmt.Errorf("%s: %w: %s", program, err, strings.Join(lines, "\n"))
	}
	return string(output), nil
}

func (l *Loop) recordAction(ctx context.Context, action, tip string, failure error) {
	detail := ""
	if failure != nil {
		detail = failure.Error()
	}
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err == nil {
		defer s.Close()
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			body, err := json.Marshal(map[string]string{"action": action, "detail": detail})
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
