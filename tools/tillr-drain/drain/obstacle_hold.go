package drain

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonObstacleOwner      = "obstacle-owner"
	ReasonObstacleFence      = "obstacle-fence"
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
	checks := obs.List("CHECKS")
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
		var allowed []string
		var rawAllowed string
		if err := tx.QueryRow(`SELECT COALESCE(allowed_paths, '') FROM items WHERE item_id = ?`, d.ItemID).Scan(&rawAllowed); err != nil {
			return err
		}
		if rawAllowed != "" {
			_ = json.Unmarshal([]byte(rawAllowed), &allowed)
		}
		hold = obstacles.NewHold(d.ItemID, runID, obs.Kind, obs.Fields, checks, baseSHA, allowed)
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
