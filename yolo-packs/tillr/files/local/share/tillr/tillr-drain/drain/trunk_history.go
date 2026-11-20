package drain

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

// ReasonTrunkRewritten holds landings until the trunk contains its lost history again.
const ReasonTrunkRewritten = "trunk-rewritten"
const candidateGatedKind = "drain.gated"
const observedTrunkRef = "refs/tillr/trunk-history/observed"
const lostTrunkRef = "refs/tillr/trunk-history/lost"
const trunkHistoryDDL = `
CREATE TABLE IF NOT EXISTS trunk_history (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 observed_tip TEXT NOT NULL,
 lost_tip TEXT NOT NULL DEFAULT ''
);
`

var errTrunkRewritten = errors.New("landing waits on restored trunk history")

type trunkHistory struct{ Observed, Lost string }

func (h trunkHistory) reason() string {
	return ReasonTrunkRewritten + ": " + h.Lost + " -> " + h.Observed
}

func readTrunkHistory(r *store.Reader) (trunkHistory, error) {
	var h trunkHistory
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='trunk_history')`).Scan(&exists); err != nil {
		return h, err
	}
	if !exists {
		return h, nil
	} // old read-only stores have not observed history yet
	err := r.QueryRow(`SELECT observed_tip,lost_tip FROM trunk_history WHERE id=1`).Scan(&h.Observed, &h.Lost)
	if err == sql.ErrNoRows {
		return h, nil
	}
	return h, err
}

// A negative ancestry answer is not a Git failure. Missing objects and other Git
// errors refuse observation rather than fabricating a rewrite.
func trunkAncestor(ctx context.Context, root, old, tip string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", old, tip)
	cmd.Env = boundaryGitEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, Externalf("checking trunk ancestry %s -> %s: %v: %s", old, tip, err, strings.TrimSpace(string(out)))
}

// Every pass persists observation before it considers candidates. Landing workers
// recheck the read snapshot in the write transaction so replacing a loop cannot
// forget a candidate-free rewrite. Git work finishes before that transaction.
// expected additionally preserves a rewrite the lander caught under its lock.
func (l *Loop) observeTrunk(ctx context.Context, expected string) (trunkHistory, error) {
	return l.observeTrunkWith(ctx, expected, nil)
}

// observeTrunkWith is observeTrunk for a caller that rewrote the trunk itself under
// land.lock and so knows each old commit's replacement: known names them (docs/design/
// tillr-refused-push.md, Section 4.1). The patch-id proof still answers first, so a clean
// replay is recorded as it always was; the known pairs answer when a replayed commit's patch
// changed (shifted context, an empty supersession), where no patch could match.
func (l *Loop) observeTrunkWith(ctx context.Context, expected string, known *rewriteAbsorption) (trunkHistory, error) {
	if l.nonLanding {
		return trunkHistory{}, nil
	}
	registerRewriteStore(l.cfg.Root, l.cfg.Store)
	l.historyMu.Lock()
	defer l.historyMu.Unlock()
	if err := l.observeIdentity(ctx); err != nil {
		return trunkHistory{}, err
	}
	l.mu.Lock()
	ready := l.historyPinned
	l.mu.Unlock()
	if ready {
		// Commit ancestry is immutable. An unchanged observed tip cannot restore
		// a lost tip or introduce a rewrite. Do not migrate or take the writer
		// lock merely to record the same row again while runs write their records.
		r, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return trunkHistory{}, err
		}
		var h trunkHistory
		err = r.ReadTx(ctx, func(tx *store.Reader) error { var err error; h, err = readTrunkHistory(tx); return err })
		r.Close()
		if err != nil {
			return trunkHistory{}, err
		}
		tip, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return trunkHistory{}, err
		}
		if h.Observed == tip && (expected == "" || expected == tip) {
			return h, nil
		}
	}
	// The lander also writes these refs and this row. Its existing repository
	// lock serializes the whole observation across processes, without holding
	// the SQLite writer during Git work. Keep the unchanged-tip path lock-free.
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return trunkHistory{}, err
	}
	defer release()
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return trunkHistory{}, err
	}
	defer s.Close()
	var h trunkHistory
	for {
		var absorb *rewriteAbsorption
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			h, err = readTrunkHistory(r)
			return err
		})
		if err != nil {
			return h, err
		}
		before := h
		var tip string
		tip, err = l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return h, err
		}
		if h.Lost == "" {
			for _, old := range []string{h.Observed, expected} {
				if old == "" {
					continue
				}
				ancestor, err := trunkAncestor(ctx, l.cfg.Root, old, tip)
				if err != nil {
					return h, err
				}
				if !ancestor {
					h.Lost = old
					break
				}
			}
		}
		if h.Lost != "" {
			restored, err := trunkAncestor(ctx, l.cfg.Root, h.Lost, tip)
			if err != nil {
				return h, err
			}
			if !restored {
				t, err := land.OpenTrunk(l.cfg.Root)
				if err != nil {
					return h, err
				}
				t.SetContext(ctx)
				mapped, same, err := t.IdentityRewrite(h.Lost, tip)
				if err != nil {
					return h, err
				}
				if same {
					absorb = &rewriteAbsorption{Lost: h.Lost, Tip: tip, Pairs: map[string]string{}, Evidence: "each replaced commit has the same tree and message; only authorship or dates differ"}
					for old, now := range mapped {
						if old != now {
							absorb.Pairs[old] = now
							absorb.Order = append(absorb.Order, old)
						}
					}
					sort.Strings(absorb.Order)
				} else if absorb, err = l.proveRewrite(ctx, h.Lost, tip); err != nil {
					return h, err
				}
				if !absorb.proved() && known != nil {
					covers := known.Lost == h.Lost
					if !covers {
						covers, _ = trunkAncestor(ctx, l.cfg.Root, h.Lost, known.Lost)
					}
					if covers {
						if ok, err := trunkAncestor(ctx, l.cfg.Root, known.Tip, tip); err != nil {
							return h, err
						} else if ok {
							adopted := *known
							adopted.Tip = tip
							absorb = &adopted
						}
					}
				}
				if absorb.proved() && known != nil && known.GreenOverride != "" {
					absorb.GreenOverride = known.GreenOverride
				}
				if absorb.proved() {
					// A proof names an unchanged tree. Carry it to the matching
					// commit before clearing the lost-history hold.
					green, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(objectname)", GreenRef)
					if err != nil {
						return h, err
					}
					green = strings.TrimSpace(green)
					if absorb.GreenOverride != "" {
						if green != "" && absorb.GreenOverride != green {
							if _, err := l.git(ctx, l.cfg.Root, "update-ref", GreenRef, absorb.GreenOverride, green); err != nil {
								return h, err
							}
						}
					} else if replacement := absorb.Pairs[green]; replacement != "" {
						if _, err := l.git(ctx, l.cfg.Root, "update-ref", GreenRef, replacement, green); err != nil {
							return h, err
						}
					}
					// A run's landed marker names the commit that replaced the one it named.
					markers, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(refname) %(objectname)", strings.TrimSuffix(land.LandedRefPrefix, "/"))
					if err != nil {
						return h, err
					}
					for _, line := range strings.Split(strings.TrimSpace(markers), "\n") {
						f := strings.Fields(line)
						if len(f) != 2 {
							continue
						}
						if replacement := absorb.Pairs[f[1]]; replacement != "" {
							if _, err := l.git(ctx, l.cfg.Root, "update-ref", f[0], replacement, f[1]); err != nil {
								return h, err
							}
						}
					}
					// Keep the replaced history readable once the lost ref goes.
					if _, err := l.git(ctx, l.cfg.Root, "update-ref", acceptedTrunkPrefix+h.Lost, h.Lost); err != nil {
						return h, err
					}
					restored = true
				}
			}
			if restored {
				h.Lost = ""
			}
		}
		h.Observed = tip
		// Ref updates precede the row. On a rewrite pin the lost commit before
		// replacing the observed ref; on restoration it remains reachable from
		// the new observed tip. A crash cannot leave a stored tip collectible.
		if h.Lost != "" {
			if _, err := l.git(ctx, l.cfg.Root, "update-ref", lostTrunkRef, h.Lost); err != nil {
				return h, err
			}
		}
		if _, err := l.git(ctx, l.cfg.Root, "update-ref", observedTrunkRef, tip); err != nil {
			return h, err
		}
		if h.Lost == "" {
			if _, err := l.git(ctx, l.cfg.Root, "update-ref", "-d", lostTrunkRef); err != nil {
				return h, err
			}
		}
		// Git and ref pins are complete before acquiring the SQLite writer.
		// Recheck the read snapshot under that lock: a replacement observer
		// must not have its history overwritten by this observation.
		changed := false
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			current, err := readTrunkHistory(tx.Reader())
			if err != nil {
				return err
			}
			if current != before {
				changed = true
				return nil
			}
			if absorb != nil {
				if err := absorb.record(tx, l.cfg.Now()); err != nil {
					return err
				}
			}
			_, err = tx.Exec(`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET observed_tip=excluded.observed_tip,lost_tip=excluded.lost_tip`, h.Observed, h.Lost)
			return err
		})
		if err != nil || !changed {
			break
		}
		if err := ctx.Err(); err != nil {
			return h, err
		}
	}
	if err == nil {
		l.mu.Lock()
		l.historyPinned = true
		l.mu.Unlock()
	}
	return h, err
}

func (l *Loop) holdRewrite(ctx context.Context, c Candidate, h trunkHistory) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "trunk-rewritten"); err != nil {
			return err
		}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Seat: c.Seat, From: "candidate", Action: ActionHold, Reason: rewriteReason(tx.Reader(), h), PassID: c.TickID}
		identifyDecision(&d)
		return recordDirtyDecision(tx, d, l.cfg.Now().UTC())
	})
	if err != nil {
		return err
	}
	l.reportf("loop: holding %s (%s); restore the trunk's history\n", c.ItemID, h.reason())
	return errTrunkRewritten
}
