package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	forkDriftCheck      = remotesync.ForkDriftCheck
	forkRebaseKind      = "drain.fork-rebase-request"
	forkDriftNoticeKind = "drain.fork-drift-inherited"
	forkRebasePrefix    = remotesync.ForkRebasePrefix
	forkRebaseCriterion = "rebase-release"
)

func init() {
	for _, kind := range []string{forkRebaseKind, forkDriftNoticeKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

func isForkRebaseItem(id string) bool { return remotesync.IsForkRebaseItem(id) }

type forkObservation = remotesync.ForkObservation

var forkCommitPattern = remotesync.ForkCommitPattern

func readForkConflicts(path string) (observations map[string]forkObservation, unfinished, unansweredFork bool) {
	return remotesync.ReadForkConflicts(path)
}

func replayForkConflict(check, text string) (forkObservation, bool) {
	return remotesync.ReplayForkConflict(check, text)
}

// settleGateFailure keeps INV-2 out of trunk blame. The upstream question is judged
// on the candidate and its base, not by a replay on whatever trunk has since landed.
func (l *Loop) settleGateFailure(ctx context.Context, c *Candidate, gate GateOutcome, result *LoopResult) (string, bool, error) {
	if len(gate.forkConflicts) == 0 {
		if gate.unansweredFork {
			return GateErrored, false, nil
		}
		return l.settleTrunkAlreadyRed(ctx, c, gate, result)
	}
	var regular []string
	var inherited, requests, waits []forkObservation
	refused, unknown := false, false
	paths := gate.CandidateChangedPaths
	if len(paths) == 0 {
		paths = gate.SuspectPaths
	}
	base := c.BaseSHA
	if base == "" {
		base = l.laneBase(c.LaneID)
	}
	for _, check := range gate.FailingChecks {
		observed, inv2 := gate.forkConflicts[check]
		if !inv2 {
			regular = append(regular, check)
			continue
		}
		if !observed.Valid() {
			unknown = true
			continue
		}
		if observed.Kind == "pin" || observed.Kind == "base" {
			refused = true
			continue
		}
		replay, ok := l.cfg.Steps.(ReplayStep)
		if !ok || base == "" {
			unknown = true
			continue
		}
		out, _, err := l.judgeTrunkChecks(ctx, replay, *c, base, []string{check}, false)
		if err != nil {
			return "", false, err
		}
		if !out.Known {
			l.reportf("loop: %s could not judge on base %s\n", check, shortSHA(base))
			unknown = true
			continue
		}
		if !allIn([]string{check}, out.Red) {
			refused = true
			continue
		}
		prior, ok := out.forkConflicts[check]
		if !ok || !prior.Valid() || observed.Fork != prior.Fork {
			unknown = true
			continue
		}
		if observed.Ref != prior.Ref || observed.Kind != prior.Kind {
			l.reportf("loop: %s target changed between candidate (%s) and base (%s); gate again\n", check, observed.Ref, prior.Ref)
			unknown = true
			continue
		}
		if observed.Requestable() {
			requests = append(requests, observed)
		}
		if len(paths) == 0 {
			unknown = true
		} else if observed.Touches(paths) {
			if observed.Requestable() {
				waits = append(waits, observed)
			} else {
				refused = true
			}
		} else {
			inherited = append(inherited, observed)
		}
	}
	ids := map[string]string{}
	for _, observation := range requests {
		id, err := l.fileForkRebase(ctx, observation)
		if err != nil {
			return "", false, err
		}
		ids[observation.Fork] = id
	}
	// A genuine refusal must still reach the worker. In particular a request's
	// own repository part must pass INV-2, never clear its own failure by waiting.
	if refused || (len(regular) > 0 && (unknown || gate.unfinished)) {
		return "", false, nil
	}
	if unknown {
		return GateErrored, false, nil
	}
	for _, wait := range waits {
		if id := ids[wait.Fork]; id == "" || id == c.ItemID || IsTrunkFixer(c.ItemID) {
			return "", false, nil
		}
	}
	if len(waits) > 0 {
		// The item store records one named waiter. Re-dispatch after that
		// landing rechecks any other fork against the new base.
		wait := waits[0]
		id := ids[wait.Fork]
		return "", true, l.cfg.Steps.ItemState(ctx, c.ItemID, c.RunID, StateBlocked, item.WaitingOn{Waiter: "item:" + id,
			Reason: fmt.Sprintf("fork %s conflicts with %s (%s); waiting for %s", wait.Fork, wait.Ref, wait.CheckID, id)})
	}
	if gate.unfinished {
		return GateErrored, false, nil
	}
	if len(regular) > 0 {
		gate.FailingChecks = regular
		c.InheritedDrift = inherited
		outcome, held, err := l.settleTrunkAlreadyRed(ctx, c, gate, result)
		if outcome != GatePassedInherited {
			c.InheritedDrift = nil
		}
		return outcome, held, err
	}
	if len(inherited) == 0 {
		return "", false, nil
	}
	c.InheritedDrift = inherited
	return GatePassedInherited, false, l.promoteVerdictPassInherited(ctx, *c, "", nil)
}

// An unanswered base replay is not a persisted refusal. Keep the observations
// in their logs and spans but mark the verdict envelope unusable for landing.
func (l *Loop) markForkGateUnanswered(c Candidate) error {
	if l.cfg.Root == "" || c.RunID == "" {
		return nil
	}
	path := filepath.Join(RunDir(l.cfg.Root, c.RunID), GateVerdictRecord)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	env, err := record.Decode(raw)
	if err != nil {
		return err
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(env.Payload, &v); err != nil {
		return err
	}
	v["result"] = json.RawMessage(`"error"`)
	env.Payload, err = json.Marshal(v)
	if err != nil {
		return err
	}
	raw, err = record.Encode(env)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0644)
}

// The patch ref, not the stale pin, answers whether a fork part is required. A
// release not yet known locally cannot be in the delivered tip's ancestry.
func (l *Loop) forkRebaseDelivery(ctx context.Context, o forkObservation) (deliveryDecision, error) {
	tip, err := l.revParse(ctx, l.cfg.Root, o.PatchRef()+"^{commit}")
	if err != nil {
		return deliveryDecision{}, err
	}
	if _, err := l.git(ctx, l.cfg.Root, "cat-file", "-e", o.Commit+"^{commit}"); err == nil {
		contains, err := trunkAncestor(ctx, l.cfg.Root, o.Commit, tip)
		if err != nil {
			return deliveryDecision{}, err
		}
		if contains {
			return deliveryDecision{Kind: "repo"}, nil
		}
	}
	return deliveryDecision{Kind: "both", Fork: o.Fork}, nil
}

func (l *Loop) fileForkRebase(ctx context.Context, o forkObservation) (string, error) {
	if !o.Valid() || !o.Requestable() {
		return "", nil
	}
	l.forkRebaseMu.Lock()
	defer l.forkRebaseMu.Unlock()
	pin, err := l.git(ctx, l.cfg.Root, "show", l.trunkRef()+":"+o.Pin())
	if err != nil {
		// A candidate may propose creating a fork; the request must already have
		// a tracked pin to repair on the trunk.
		return "", nil
	}
	lines := strings.Split(strings.TrimSpace(pin), "\n")
	if len(lines) != 3 || !forkCommitPattern.MatchString(lines[0]) || lines[1] == "" || !forkCommitPattern.MatchString(lines[2]) {
		return "", nil
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return "", err
	}
	defer db.Close()
	at := l.cfg.Now().UTC()
	var id string
	filed := false
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		events, err := tx.Reader().EventsOfKind(0, forkRebaseKind)
		if err != nil {
			return err
		}
		var open string
		var last forkObservation
		for _, e := range events {
			if e.Kind != forkRebaseKind {
				continue
			}
			var prev forkObservation
			if err := json.Unmarshal(e.Payload, &prev); err != nil {
				return err
			}
			if prev.Fork != o.Fork {
				continue
			}
			seen := *e.ObjectID
			var state string
			if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, seen).Scan(&state); err != nil {
				return err
			}
			active := state != StateLanded && state != "resolved-elsewhere" && state != "closed"
			if active {
				open, last = seen, prev
			}
			if prev.Ref == o.Ref {
				if active {
					id = seen
				}
				return nil
			}
		}
		if open != "" && last.Commit != o.Commit {
			// A late gate of an older base must not roll an open request back.
			// The fork input cache has both release objects when both were observed.
			cache := filepath.Join(l.cfg.Root, ".forks", "inputs", o.Fork)
			for _, repo := range []string{l.cfg.Root, cache} {
				if _, err := l.git(ctx, repo, "merge-base", "--is-ancestor", o.Commit, last.Commit); err == nil {
					id = open
					return nil
				}
			}
		}
		delivery, err := l.forkRebaseDelivery(ctx, o)
		if err != nil {
			return err
		}
		id = open
		repoWork := fmt.Sprintf("Set the three lines of %s to the current %s commit, the unchanged remote, and %s. Confirm the release commit is an ancestor of the delivered head before committing the one-file pin change. The ordinary gate must pass %s.", o.Pin(), o.PatchRef(), o.Commit, o.CheckID)
		text := fmt.Sprintf("Fork %s: %s conflicts with release %s (%s). ", o.Fork, o.CheckID, o.Ref, o.Commit)
		if delivery.Kind == "both" {
			text += fmt.Sprintf("Fork part: in the staged fork checkout, fetch %s from upstream if the object is absent and verify it resolves to %s. Run `git rebase --onto %s %s main` (the old base is the pin's third line). Resolve conflicts, keep the history linear, and report patches dropped because upstream already contains them. Leave the rebased head on main; never run bin/pi-forks export or rebase. Wait for the lander to deliver. Repository part: ", o.Ref, o.Commit, o.Commit, lines[2])
		} else {
			text += "The delivered head already contains the release; there is no fork part. Repository part: "
		}
		text += repoWork
		title := fmt.Sprintf("rebase %s onto %s", o.Fork, o.Ref)
		if id == "" {
			id = forkRebasePrefix + o.Fork + "-" + hexDigest(o.Ref)[:12]
			payload, err := json.Marshal(map[string]any{
				"item_id": id, "title": title, "state": StateInQueue,
				"rank": TrunkFixerRank, "seat": TrunkFixerSeat, "allowed_paths": []string{o.Pin()},
				"criteria": []map[string]string{{"criterion_id": forkRebaseCriterion, "text": text, "state": "not-done", "claim_class": "verification-only"}},
			})
			if err != nil {
				return err
			}
			if _, err := item.Apply(ctx, tx, record.Envelope{RecordVersion: 1, Kind: item.RecordKind,
				ID: "item-" + id, Producer: record.Producer{System: "tillr", Utility: Utility, Version: Version}, CreatedAt: at, Payload: payload}); err != nil {
				return err
			}
			filed = true
		} else {
			if _, err := tx.Exec(`UPDATE items SET title=? WHERE item_id=?`, title, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE item_criteria SET text=? WHERE item_id=? AND criterion_id=?`, text, id, forkRebaseCriterion); err != nil {
				return err
			}
		}
		if err := appendEffectOn(tx, deliveryDecisionKind, "item", id, delivery, at); err != nil {
			return err
		}
		return appendEffectOn(tx, forkRebaseKind, "item", id, o, at)
	})
	if err != nil {
		return "", classifyWrite(err)
	}
	if filed {
		l.reportf("loop: filed %s for %s at %s (%s)\n", id, o.Fork, o.Ref, o.Commit)
	}
	return id, nil
}

// checkForkRebasePiece prevents an in-flight worker for an older observation
// from closing the updated request. The ordinary gate still judges the candidate.
func (l *Loop) checkForkRebasePiece(ctx context.Context, c Candidate) (string, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var raw []byte
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, forkRebaseKind, c.ItemID).Scan(&raw)
	})
	if err != nil {
		return "", err
	}
	var target forkObservation
	if err := json.Unmarshal(raw, &target); err != nil {
		return "", err
	}
	stale := fmt.Sprintf("this piece does not deliver the current rebase target for %s: %s (%s); re-read the request", target.Fork, target.Ref, target.Commit)
	if c.BaseRef != "" {
		if c.BaseRef != target.PatchRef() {
			return stale, nil
		}
		if _, err := l.git(ctx, c.Worktree, "merge-base", "--is-ancestor", target.Commit, c.HeadSHA); err != nil {
			return stale, nil
		}
		return "", nil
	}
	tip, err := l.revParse(ctx, l.cfg.Root, target.PatchRef()+"^{commit}")
	if err != nil {
		return "", err
	}
	if _, err := l.git(ctx, l.cfg.Root, "merge-base", "--is-ancestor", target.Commit, tip); err != nil {
		return stale, nil
	}
	before, err := l.git(ctx, c.Worktree, "show", c.BaseSHA+":"+target.Pin())
	if err != nil {
		return stale, nil
	}
	after, err := l.git(ctx, c.Worktree, "show", c.HeadSHA+":"+target.Pin())
	if err != nil {
		return stale, nil
	}
	old, new := strings.Split(strings.TrimSpace(before), "\n"), strings.Split(strings.TrimSpace(after), "\n")
	if len(old) != 3 || len(new) != 3 || new[0] != tip || new[1] != old[1] || new[2] != target.Commit || before == after {
		return stale, nil
	}
	return "", nil
}

func (l *Loop) reportInheritedDrift(ctx context.Context, c Candidate) {
	for _, o := range c.InheritedDrift {
		if o.Requestable() {
			continue
		}
		key := o.Ref
		if o.Kind == "head" {
			key = o.Commit
		}
		id := o.Fork + "-" + hexDigest(key)
		db, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			l.reportf("loop: could not record inherited drift: %v\n", err)
			continue
		}
		added := false
		err = db.WriteTx(ctx, func(tx *store.Tx) error {
			var exists bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_id=?)`, forkDriftNoticeKind, id).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			added = true
			return appendEffectOn(tx, forkDriftNoticeKind, "fork", id, o, l.cfg.Now().UTC())
		})
		db.Close()
		if err != nil {
			l.reportf("loop: could not record inherited drift: %v\n", err)
		} else if added {
			l.reportf("loop: %s inherited %s for fork %s at %s (%s)\n", c.ItemID, o.CheckID, o.Fork, o.Ref, o.Commit)
		}
	}
}
