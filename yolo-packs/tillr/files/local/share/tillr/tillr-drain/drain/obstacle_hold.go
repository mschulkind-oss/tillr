package drain

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonObstacleOwner      = "obstacle-owner"
	ReasonObstacleTrunk      = "obstacle-trunk"
	ReasonObstacleTooling    = "obstacle-tooling"
	ReasonObstacleSeat       = "obstacle-seat"
	ReasonObstacleDefinition = "obstacle-definition"
)

func init() {
	for _, kind := range []string{obstacles.HeldKind, obstacles.ClearedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

func ReadObstacleHolds(r *store.Reader) (map[string]obstacles.Hold, map[string]string, error) {
	return obstacles.Read(r)
}

// trunkProofLists checks against the inventory the trunk proof can actually run:
// declared heavy tree checks and the legacy rolling tier, not landing-only checks.
func (l *Loop) trunkProofLists(ctx context.Context, checks []string) (bool, error) {
	if len(checks) == 0 || l.cfg.Root == "" {
		return false, nil
	}
	heavy, err := heavyCheckDeclarations(l.cfg.Root)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	var remaining []string
	for _, id := range checks {
		if _, ok := heavy[id]; !ok {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) == 0 {
		return true, nil
	}
	legacy, err := legacyCheckDeclarations(ctx, l.cfg.Root)
	if err != nil {
		return false, err
	}
	var rolling []string
	for _, check := range legacy {
		if check.Tier == "rolling" {
			rolling = append(rolling, check.ID)
		}
	}
	return allIn(remaining, rolling), nil
}

func (l *Loop) recordObstacleHold(ctx context.Context, d Decision, obs Obstacle) error {
	baseSHA := d.NodeBaseSHA
	if baseSHA == "" && d.LaneID != "" {
		baseSHA = l.laneBase(d.LaneID)
	}
	if baseSHA == "" {
		baseSHA = obstacles.OneLine(obs.Field("BASE"))
	}
	runID := ""
	if d.RunRequest != nil {
		runID = d.RunRequest.RunID
	}
	headSHA := ""
	if d.LaneID != "" {
		headSHA = l.headOf(ctx, d.LaneID)
	}
	var checks []string
	if obs.Kind == ObstacleTrunk {
		checks = obs.trunkChecks()
		known, err := l.trunkProofLists(ctx, checks)
		if err != nil {
			return err
		}
		if !known {
			l.reportf("loop: %s named an invalid trunk obstacle: returned to its worker\n", d.ItemID)
			return l.recordRefusal(ctx, Candidate{
				ItemID: d.ItemID, RunID: runID, LaneID: d.LaneID,
				Worktree: l.laneTarget(d.LaneID), BaseSHA: baseSHA, HeadSHA: headSHA,
				RefusalDetail: invalidTrunkObstacleDetail(obs),
			}, RefusalNoOp)
		}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	var hold obstacles.Hold
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), d.ItemID, "blocked-report"); err != nil {
			return err
		}
		hold = obstacles.NewHold(d.ItemID, runID, obs.Kind, obs.Fields, checks, baseSHA, nil)
		hold.HeadSHA = headSHA
		hasLanded, err := obstacles.HasLanded(tx.Reader(), d.ItemID)
		if err != nil {
			return err
		}
		if hasLanded {
			hold = hold.WithoutCloseRemedy()
		}
		if err := requireHold(tx.Reader(), hold.ReasonCode()); err != nil {
			return err
		}
		if obs.Kind == ObstacleOwner {
			if _, err := item.SetState(ctx, tx, d.ItemID, StateBlocked, at, item.WaitingOn{
				Waiter: "owner",
				Reason: hold.Reason,
			}); err != nil {
				return err
			}
		}
		body, err := json.Marshal(hold)
		if err != nil {
			return err
		}
		id := d.ItemID
		_, err = tx.Append(event.Event{
			Kind:       obstacles.HeldKind,
			ObjectType: "item",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		return err
	})
	if err == nil {
		l.reportf("loop: %s held %s (%s): %s\n", d.ItemID, hold.ReasonCode(), hold.Mover, hold.Reason)
	}
	return err
}

func obstacleProofPassed(p checkProof, checks []string, seen int64) bool {
	if p.whole && p.provedAt.UnixMilli() <= seen {
		return false
	}
	needed := provableChecks(checks)
	if len(needed) == 0 {
		return p.whole && p.provedAt.UnixMilli() > seen
	}
	for _, check := range needed {
		if at, ok := p.at[check]; ok && at.UnixMilli() > seen {
			continue
		}
		matched := false
		if !strings.Contains(check, "/") {
			prefix := check + "/"
			for k, at := range p.at {
				if strings.HasPrefix(k, prefix) && at.UnixMilli() > seen {
					matched = true
					break
				}
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func (l *Loop) releaseTrunkObstacleHolds(ctx context.Context) error {
	if l.cfg.Root == "" {
		return nil
	}
	registerRewriteStore(l.cfg.Root, l.cfg.Store)
	proofs, err := readCheckProofs(ctx, l.cfg.Root)
	if err != nil || len(proofs) == 0 {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()

	var holds []obstacles.Hold
	var lost string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		th, err := readTrunkHistory(r)
		if err != nil {
			return err
		}
		lost = th.Lost
		active, _, err := obstacles.Read(r)
		if err != nil {
			return err
		}
		for _, h := range active {
			if h.Kind == ObstacleTrunk {
				holds = append(holds, h)
			}
		}
		return nil
	}); err != nil || len(holds) == 0 {
		return err
	}

	green, err := provedLaneBase(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
	if err != nil {
		return err
	}

	type release struct {
		hold   obstacles.Hold
		reason string
	}
	var ready []release
	for _, h := range holds {
		redCommit := ""
		if h.BaseSHA != "" {
			redCommit, err = resolveCommit(ctx, l.cfg.Root, h.BaseSHA)
			if err != nil {
				return err
			}
		}
		for _, p := range proofs {
			if !obstacleProofPassed(p, h.Checks, h.AtMS) {
				continue
			}
			if redCommit != "" {
				ok, err := proofPastRed(ctx, l.cfg.Root, lost, redCommit, p.commit)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			if green != "" {
				ok, err := containsCommit(ctx, l.cfg.Root, p.commit, green)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			ready = append(ready, release{hold: h, reason: p.describe(provableChecks(h.Checks))})
			break
		}
	}
	if len(ready) == 0 {
		return nil
	}

	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		active, _, err := obstacles.Read(tx.Reader())
		if err != nil {
			return err
		}
		for _, rel := range ready {
			cur, ok := active[rel.hold.ItemID]
			if !ok || cur.Seq != rel.hold.Seq {
				continue
			}
			body, err := json.Marshal(obstacles.ClearPayload{
				ItemID: rel.hold.ItemID,
				RunID:  rel.hold.RunID,
				Reason: rel.reason,
			})
			if err != nil {
				return err
			}
			id := rel.hold.ItemID
			if _, err := tx.Append(event.Event{
				Kind:       obstacles.ClearedKind,
				ObjectType: "item",
				ObjectID:   &id,
				Payload:    body,
				At:         at,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
