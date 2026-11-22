package drain

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mschulkind-oss/tillr/checkresult"
)

const heavyReplayGuard = "heavy.replay-cleanup-pending"

type heavyCheckEndedKey struct{}

// heavyCheckEnded reports one check of a whole-tier judge the moment it ends, so a red
// one starts its recovery while the judge runs the rest.
func heavyCheckEnded(ctx context.Context, check HeavyCheck) {
	if report, ok := ctx.Value(heavyCheckEndedKey{}).(func(HeavyCheck)); ok {
		report(check)
	}
}

// heavyAttributed is what recovery found for one failing check before the run ended.
type heavyAttributed struct {
	check             HeavyCheck
	against           string
	bisected, offered bool
}

// heavyEarly recovers failing checks one at a time, in the order they ended, in a
// checkout of its own beside the proof's.
type heavyEarly struct {
	ctx      context.Context
	runner   HeavyRunner
	run      HeavyRun
	mu       sync.Mutex
	cond     *sync.Cond
	queue    []HeavyCheck
	closed   bool
	done     chan struct{}
	err      error
	handled  map[string]heavyAttributed
	reverted map[string]string
}

func (r HeavyRunner) startEarly(ctx context.Context, run HeavyRun) *heavyEarly {
	e := &heavyEarly{ctx: ctx, runner: r, run: run, done: make(chan struct{}), handled: map[string]heavyAttributed{}, reverted: map[string]string{}}
	e.cond = sync.NewCond(&e.mu)
	e.runner.Work = r.Work + "-replay"
	if own, ok := ctx.Value(heavyOwnershipKey{}).(heavyOwnership); ok {
		own.Guard = filepath.Join(filepath.Dir(own.Guard), heavyReplayGuard)
		e.ctx = context.WithValue(ctx, heavyOwnershipKey{}, own)
	}
	go e.loop()
	return e
}

func (e *heavyEarly) offer(check HeavyCheck) {
	// Attribute all failed tests in a suite before any of them can move the tip.
	if classifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome != checkresult.Fail || check.Stalls >= 2 || check.FirstRedLanding != "" || strings.HasPrefix(check.CheckID, goTestPrefix) {
		return
	}
	e.mu.Lock()
	if !e.closed {
		e.queue = append(e.queue, check)
		e.cond.Signal()
	}
	e.mu.Unlock()
}

func (e *heavyEarly) loop() {
	defer close(e.done)
	for {
		e.mu.Lock()
		for len(e.queue) == 0 && !e.closed {
			e.cond.Wait()
		}
		if len(e.queue) == 0 {
			e.mu.Unlock()
			return
		}
		check := e.queue[0]
		e.queue = e.queue[1:]
		e.mu.Unlock()
		if e.err == nil && e.ctx.Err() == nil {
			e.err = e.recover(check)
		}
	}
}

// wait ends the queue and answers once every check offered has been handled.
func (e *heavyEarly) wait() error {
	e.mu.Lock()
	e.closed = true
	e.cond.Broadcast()
	e.mu.Unlock()
	<-e.done
	return e.err
}

func (e *heavyEarly) recover(check HeavyCheck) error {
	against, bisected, err := e.runner.attribute(e.ctx, e.run, &check)
	if err != nil {
		return err
	}
	a := heavyAttributed{against: against, bisected: bisected}
	if bisected && e.runner.Revert != nil {
		a.offered = true
		if by := e.reverted[against]; by != "" {
			check.RevertedBy = by
		} else {
			out, err := e.runner.offerRevert(e.ctx, e.run.TipSHA, against, []HeavyCheck{check})
			if err != nil {
				return err
			}
			a.against, err = e.runner.resolveRevert(e.ctx, e.run, &check, against, out)
			if err != nil {
				return err
			}
			if check.RevertedBy != "" {
				e.reverted[a.against] = check.RevertedBy
			}
		}
	}
	a.check = check
	e.handled[check.CheckID] = a
	return nil
}
