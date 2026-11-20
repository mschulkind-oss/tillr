package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/orderrecord"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const orderRecordKind = "drain.order-record"
const orderRecordSubject = item.OrderRecordID
const orderRecordMessage = "docs: record the queue's order"

func init() {
	if err := event.Register(orderRecordKind, Utility); err != nil {
		panic(err)
	}
}

type orderRecordWork struct {
	Phase     string     `json:"phase"`
	Cleaned   bool       `json:"cleaned,omitempty"`
	Owner     *LoopOwner `json:"owner,omitempty"`
	Seq       int64      `json:"hand_event_seq"`
	Path      string     `json:"path"`
	Body      string     `json:"body"`
	AtMS      int64      `json:"at_ms"`
	Reason    string     `json:"reason,omitempty"`
	Candidate Candidate  `json:"candidate"`
}

func readOrderWork(r *store.Reader) (*orderRecordWork, error) {
	var raw []byte
	err := r.QueryRow("SELECT payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1", orderRecordKind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var work orderRecordWork
	err = json.Unmarshal(raw, &work)
	return &work, err
}
func appendOrderWork(tx *store.Tx, w *orderRecordWork, at time.Time) error {
	return appendEffect(tx, orderRecordKind, w.Candidate.RunID, w, at)
}
func (l *Loop) saveOrderWork(ctx context.Context, w *orderRecordWork) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error { return appendOrderWork(tx, w, l.cfg.Now().UTC()) })
}

// recordOrder runs ahead of item admission. Its separate, detached worktree uses
// no agent and changes no item state. A transaction claims the sole maintenance
// candidate before the gate; a replacement recovers the same candidate rather
// than generating a second one. Only Steps.Land can publish its change.
func (l *Loop) recordOrder(ctx context.Context, base string, result *LoopResult, timing *PassTiming) (err error) {
	if l.nonLanding {
		return nil
	}
	cfg, err := settings.Read(l.cfg.Root)
	if err != nil {
		return err
	}
	if cfg.Order.Record == "" {
		return nil
	}
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	var work, terminal *orderRecordWork
	idTaken := false
	at := l.cfg.Now().UTC()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		prior, err := readOrderWork(tx.Reader())
		if err != nil {
			return err
		}
		if prior != nil && prior.Phase == "done" && !prior.Cleaned {
			// Do not hide unfinished cleanup behind a newer claim. Complete it
			// outside this transaction; a subsequent pass may then claim work.
			terminal = prior
			return nil
		}
		if prior != nil && prior.Phase != "done" {
			if prior.Owner != nil {
				dead, err := ownerDead(prior.Owner, here)
				if err != nil {
					return err
				}
				if fromOtherDomain(prior.Owner, here) {
					ended, err := endedSessionOwners(tx.Reader())
					if err != nil {
						return err
					}
					identity, err := processStoreIdentity(l.cfg.Store)
					if err != nil {
						return err
					}
					dead = foreignOwnerGone(identity, *prior.Owner, *here, ended)
				}
				if !dead {
					return nil
				} // never steal an in-flight gate, even in this process
			}
			prior.Owner = here
			work = prior
		} else {
			status, receipt, err := orderrecord.Read(ctx, tx.Reader(), l.cfg.Root)
			if err != nil {
				return err
			}
			if status == nil || status.Recorded || at.UnixMilli()-status.ChangedAtMS < cfg.Order.SettleMinutes*60000 {
				return nil
			}
			if prior != nil && prior.Reason != "" && status.HandEventSeq <= prior.Seq && at.UnixMilli()-prior.AtMS < time.Hour.Milliseconds() {
				return nil
			}
			var taken int
			if err := tx.QueryRow("SELECT count(*) FROM items WHERE item_id=?", item.OrderRecordID).Scan(&taken); err != nil {
				return err
			}
			if taken != 0 {
				if timing.TickID == "" {
					timing.TickID, err = allocateGeneration(tx)
					if err != nil {
						return err
					}
				}
				refused := &orderRecordWork{Phase: "done", Cleaned: true, Seq: status.HandEventSeq, AtMS: at.UnixMilli(), Reason: "id-taken"}
				if err := appendOrderWork(tx, refused, at); err != nil {
					return err
				}
				idTaken = true
				return recordPassDecision(tx, Decision{DecisionID: timing.TickID + "-order-record-refused", ItemID: item.OrderRecordID, PassID: timing.TickID, Action: "order-record-refused", Reason: "id-taken"}, at)
			}
			order, err := item.OrderRecord(tx.Reader())
			if err != nil {
				return err
			}
			_, ids, _ := strings.Cut(order, "\n")
			body := receipt + ids
			seq, err := tx.Reader().EventHeadSeq()
			if err != nil {
				return err
			}
			id := fmt.Sprintf("run-order-record-%d-%d", status.HandEventSeq, seq+1)
			work = &orderRecordWork{Phase: "claimed", Owner: here, Seq: status.HandEventSeq, Path: cfg.Order.Record, Body: body, AtMS: at.UnixMilli(), Candidate: Candidate{ItemID: orderRecordSubject, LaneID: orderRecordSubject, RunID: id, BaseRef: "refs/heads/" + TrunkRef, BaseSHA: base, Worktree: filepath.Join(l.cfg.LaneRoot, ".order-record", id, WorktreeDir)}}
		}
		if timing.TickID == "" {
			timing.TickID, err = allocateGeneration(tx)
			if err != nil {
				return err
			}
		}
		work.Candidate.TickID = timing.TickID
		if err := appendOrderWork(tx, work, at); err != nil {
			return err
		}
		return recordPassDecision(tx, Decision{DecisionID: work.Candidate.RunID + "-claim-" + timing.TickID, ItemID: orderRecordSubject, PassID: timing.TickID, Action: "order-record", Reason: "hand-order-settled"}, at)
	})
	s.Close()
	if err == nil && idTaken {
		if timing.DecisionCounts == nil {
			timing.DecisionCounts = map[string]int{}
		}
		timing.DecisionCounts["order-record-refused"]++
		timing.Applied++
		l.reportf("loop: order-record-refused: id-taken\n")
	}
	claimCommitted := err == nil
	defer func() {
		// Release ownership on failure, retaining the durable candidate/gate.
		// A terminal phase is set only with its pass decision in one transaction.
		if err != nil && claimCommitted && work != nil && work.Phase != "done" {
			work.Owner = nil
			if saveErr := l.saveOrderWork(context.WithoutCancel(ctx), work); saveErr != nil {
				err = fmt.Errorf("%v; releasing order record ownership: %w", err, saveErr)
			}
		}
	}()
	if err == nil && terminal != nil {
		err = l.cleanOrderWork(ctx, terminal)
	}
	if err != nil || work == nil {
		return err
	}
	timing.Applied++
	if timing.DecisionCounts == nil {
		timing.DecisionCounts = map[string]int{}
	}
	timing.DecisionCounts["order-record"]++
	result.Candidates++
	refuse := func(reason string) error {
		if err := l.finishOrderWork(ctx, work, timing, "order-record-refused", reason); err != nil {
			return err
		}
		result.Rejected++
		return l.cleanOrderWork(ctx, work)
	}
	if work.Phase == "claimed" {
		if err := l.prepareOrderRecord(ctx, work); err != nil {
			return refuse("candidate: " + err.Error())
		}
		work.Phase = "ready"
		if err := l.saveOrderWork(ctx, work); err != nil {
			return err
		}
	}
	if work.Phase == "ready" {
		started := time.Now()
		gate, gateErr := l.cfg.Steps.Gate(ctx, work.Candidate)
		timing.addGateTime(time.Since(started).Milliseconds(), false)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if gateErr != nil {
			return refuse("gate: " + gateErr.Error())
		}
		if gate.Outcome != GatePassed && gate.Outcome != GatePassedInherited {
			return refuse("gate: " + gate.Outcome)
		}
		work.Phase = "gated"
		if err := l.saveOrderWork(ctx, work); err != nil {
			return err
		}
	}
	// Check the persisted head and whole diff again after a restart or a gate.
	if err := l.checkOrderCandidate(ctx, work); err != nil {
		return refuse("candidate: " + err.Error())
	}
	history, err := l.observeTrunk(ctx, "")
	if err != nil {
		return err
	}
	if history.Lost != "" {
		return refuse("land: " + ReasonTrunkRewritten)
	}
	// Also bind recovered pre-upgrade candidates to main rather than the root
	// checkout's currently selected branch.
	work.Candidate.BaseRef = "refs/heads/" + TrunkRef
	work.Candidate.ObservedTrunk = history.Observed
	started := time.Now()
	out, landErr := l.landWithActions(ctx, work.Candidate)
	timing.LandMS += time.Since(started).Milliseconds()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if landErr != nil {
		return refuse("land: " + landErr.Error())
	}
	if out.Result != LandLanded {
		return refuse("land: " + out.Result + ": " + out.Reason)
	}
	if err := l.finishOrderWork(ctx, work, timing, "order-record-landed", ""); err != nil {
		return err
	}
	result.Landed++
	if err := l.bumpLandings(ctx); err != nil {
		return err
	}
	return l.cleanOrderWork(ctx, work)
}
func (l *Loop) finishOrderWork(ctx context.Context, w *orderRecordWork, timing *PassTiming, action, refusal string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	terminal := *w
	terminal.Phase, terminal.Owner, terminal.Reason, terminal.AtMS = "done", nil, refusal, l.cfg.Now().UnixMilli()
	reason := refusal
	if reason == "" {
		reason = "order-recorded"
	}
	l.reportf("loop: %s: %s\n", action, reason)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendOrderWork(tx, &terminal, l.cfg.Now()); err != nil {
			return err
		}
		return recordPassDecision(tx, Decision{DecisionID: w.Candidate.RunID + "-" + action, ItemID: orderRecordSubject, PassID: timing.TickID, Action: action, Reason: reason}, l.cfg.Now())
	})
	if err == nil {
		*w = terminal
		if timing.DecisionCounts == nil {
			timing.DecisionCounts = map[string]int{}
		}
		timing.DecisionCounts[action]++
	}
	return err
}

// Terminal state and the pass decision are durable before cleanup. Each pass
// retries cleanup before any new claim. Pin the authored commit before removing
// its worktree, then acknowledge cleanup without overwriting a newer attempt.
func (l *Loop) cleanOrderWork(ctx context.Context, w *orderRecordWork) error {
	if err := withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
		// Check under the lock: two observers may help the same cleanup.
		if _, err := os.Stat(w.Candidate.Worktree); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		if w.Candidate.HeadSHA != "" {
			if err := l.pin(ctx, w.Candidate.RunID, w.Candidate.HeadSHA); err != nil {
				return err
			}
		}
		_, err := l.git(ctx, l.cfg.Root, "worktree", "remove", "--force", w.Candidate.Worktree)
		return err
	}); err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		latest, err := readOrderWork(tx.Reader())
		if err != nil {
			return err
		}
		if latest == nil || latest.Candidate.RunID != w.Candidate.RunID || latest.Phase != "done" || latest.Cleaned {
			return nil
		}
		latest.Cleaned = true
		return appendOrderWork(tx, latest, l.cfg.Now())
	})
}
func (l *Loop) prepareOrderRecord(ctx context.Context, w *orderRecordWork) error {
	c := &w.Candidate
	for _, part := range strings.Split(w.Path, "/") {
		if part == ".git" || part == ".tillr" {
			return fmt.Errorf("order record cannot overwrite repository metadata: %s", w.Path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(c.Worktree), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(c.Worktree, ".git")); os.IsNotExist(err) {
		if err := withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
			_, err := l.git(ctx, l.cfg.Root, "worktree", "add", "--detach", c.Worktree, c.BaseSHA)
			return err
		}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	// A crash after the commit but before 'ready' is recoverable without creating
	// another commit: bind its exact blob, parent, message and one-file diff.
	head, err := l.revParse(ctx, c.Worktree, "HEAD")
	if err != nil {
		return err
	}
	if head != c.BaseSHA {
		c.HeadSHA = head
		return l.checkOrderCandidate(ctx, w)
	}
	path := filepath.Join(c.Worktree, filepath.FromSlash(w.Path))
	for p := path; p != c.Worktree; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("record path crosses symlink %s", p)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(w.Body), 0644); err != nil {
		return err
	}
	if _, err := l.git(ctx, c.Worktree, "add", "--", w.Path); err != nil {
		return err
	}
	if _, err := l.git(ctx, c.Worktree, "commit", "-m", orderRecordMessage); err != nil {
		return err
	}
	c.HeadSHA, err = l.revParse(ctx, c.Worktree, "HEAD")
	if err != nil {
		return err
	}
	return l.checkOrderCandidate(ctx, w)
}
func (l *Loop) checkOrderCandidate(ctx context.Context, w *orderRecordWork) error {
	c := w.Candidate
	checks := []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "HEAD"}, c.HeadSHA},
		{[]string{"rev-list", "--parents", "-n", "1", c.HeadSHA}, c.HeadSHA + " " + c.BaseSHA},
		{[]string{"log", "-1", "--format=%s", c.HeadSHA}, orderRecordMessage},
		{[]string{"status", "--porcelain", "--untracked-files=all"}, ""},
		{[]string{"diff", "--name-only", c.BaseSHA, c.HeadSHA, "--"}, w.Path},
		{[]string{"show", c.HeadSHA + ":" + w.Path}, strings.TrimSpace(w.Body)},
	}
	for _, check := range checks {
		got, err := l.git(ctx, c.Worktree, check.args...)
		if err != nil {
			return err
		}
		if strings.TrimSpace(got) != check.want {
			return fmt.Errorf("order candidate %s failed %s", c.RunID, strings.Join(check.args, " "))
		}
	}
	return nil
}
