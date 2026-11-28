package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
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

func ReadObstacleHoldsForItems(r *store.Reader, itemIDs, runIDs []string) (map[string]obstacles.Hold, map[string]string, error) {
	return obstacles.ReadForItems(r, itemIDs, runIDs)
}

type trunkObstacleKind int

const (
	trunkObstacleInvalid trunkObstacleKind = iota
	trunkObstacleProofChecks
	trunkObstaclePlainCommand
)

type trunkObstacleCheck struct {
	Kind        trunkObstacleKind
	Entries     []string
	ProofChecks []string
	Rejected    []rejectedTrunkCheck
}

// gateCheckInventory reads every declared check in tools/gate/checks/*.json and any
// legacy registry, separating checks the trunk proof runs from other gate checks.
func (l *Loop) gateCheckInventory(ctx context.Context) (proof map[string]bool, other map[string]string, err error) {
	proof = map[string]bool{}
	other = map[string]string{}
	if l.cfg.Root == "" {
		return proof, other, nil
	}
	var checksDir string
	if l.settingsUnreadable() {
		checksDir = filepath.Join(l.cfg.Root, filepath.FromSlash(l.postLandingSettings().ChecksDir()))
	} else {
		configured, err := settings.ReadChecksDir(l.cfg.Root)
		if err != nil {
			return nil, nil, err
		}
		checksDir = filepath.Join(l.cfg.Root, filepath.FromSlash(configured))
	}
	entries, err := os.ReadDir(checksDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(checksDir, entry.Name()))
		if err != nil {
			return nil, nil, err
		}
		var check struct {
			ID, Scope, Tier string
		}
		if err := json.Unmarshal(raw, &check); err != nil {
			return nil, nil, err
		}
		if check.ID == "" {
			continue
		}
		if check.Scope == "tree" && check.Tier == "heavy" {
			proof[check.ID] = true
		} else {
			kind := strings.Trim(strings.Join([]string{check.Scope, check.Tier}, "/"), "/")
			if kind == "" {
				kind = "non-proof"
			}
			other[check.ID] = kind
		}
	}
	legacy, err := legacyCheckDeclarations(ctx, l.cfg.Root)
	if err != nil {
		return nil, nil, err
	}
	for _, check := range legacy {
		if check.ID == "" {
			continue
		}
		if check.Tier == "rolling" {
			proof[check.ID] = true
		} else if !proof[check.ID] {
			tier := check.Tier
			if tier == "" {
				tier = "non-proof"
			}
			other[check.ID] = tier
		}
	}
	return proof, other, nil
}

// trunkProofLists checks against the inventory the trunk proof can actually run:
// declared heavy tree checks and the legacy rolling tier, not landing-only checks.
func (l *Loop) trunkProofLists(ctx context.Context, checks []string) (bool, error) {
	if len(checks) == 0 || l.cfg.Root == "" {
		return false, nil
	}
	proof, _, err := l.gateCheckInventory(ctx)
	if err != nil {
		return false, err
	}
	for _, id := range checks {
		if !proof[id] {
			return false, nil
		}
	}
	return true, nil
}

// classifyTrunkObstacle classifies the entries in a trunk obstacle's CHECKS field:
//   - trunkObstacleProofChecks: every entry is a check the trunk proof runs;
//   - trunkObstaclePlainCommand: at least one entry is a plain command outside the
//     gate's checks, and no entry is an unknown check id, non-proof gate check, or prose;
//   - trunkObstacleInvalid: empty CHECKS or at least one rejected entry, each with its reason.
func (l *Loop) classifyTrunkObstacle(ctx context.Context, obs Obstacle) (trunkObstacleCheck, error) {
	entries := obs.trunkCheckEntries()
	if len(entries) == 0 {
		return trunkObstacleCheck{
			Kind:     trunkObstacleInvalid,
			Rejected: []rejectedTrunkCheck{{Reason: "no checks named"}},
		}, nil
	}
	proof, other, err := l.gateCheckInventory(ctx)
	if err != nil {
		return trunkObstacleCheck{}, err
	}
	allGate := make(map[string]bool, len(proof)+len(other))
	for id := range proof {
		allGate[id] = true
	}
	for id := range other {
		allGate[id] = true
	}
	var proofChecks, plainCommands []string
	var rejected []rejectedTrunkCheck
	for _, entry := range entries {
		switch {
		case proof[entry]:
			proofChecks = append(proofChecks, entry)
		case other[entry] != "":
			rejected = append(rejected, rejectedTrunkCheck{
				Entry:  entry,
				Reason: "gate check (" + other[entry] + "), not a trunk proof check",
			})
		case obstacleCheckID.MatchString(entry):
			rejected = append(rejected, rejectedTrunkCheck{
				Entry:  entry,
				Reason: "unknown check id",
			})
		case looksLikePlainCommand(entry, allGate):
			plainCommands = append(plainCommands, entry)
		default:
			rejected = append(rejected, rejectedTrunkCheck{
				Entry:  entry,
				Reason: "not a check id or command",
			})
		}
	}
	if len(rejected) > 0 {
		return trunkObstacleCheck{
			Kind:     trunkObstacleInvalid,
			Entries:  entries,
			Rejected: rejected,
		}, nil
	}
	if len(plainCommands) > 0 {
		return trunkObstacleCheck{
			Kind:    trunkObstaclePlainCommand,
			Entries: entries,
		}, nil
	}
	return trunkObstacleCheck{
		Kind:        trunkObstacleProofChecks,
		Entries:     entries,
		ProofChecks: proofChecks,
	}, nil
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
		classified, err := l.classifyTrunkObstacle(ctx, obs)
		if err != nil {
			return err
		}
		switch classified.Kind {
		case trunkObstacleProofChecks:
			checks = classified.ProofChecks
		case trunkObstaclePlainCommand:
			// Plain command outside the gate's checks: record an operator-held
			// obstacle (checks == nil -> Mover == "operator") so it alerts the
			// operator and does not send the worker back to land without it.
			checks = nil
		default:
			l.reportf("loop: %s named an invalid trunk obstacle (%s): returned to its worker\n", d.ItemID, formatInvalidTrunkObstacle(classified.Entries, classified.Rejected))
			return l.recordRefusal(ctx, Candidate{
				ItemID: d.ItemID, RunID: runID, LaneID: d.LaneID,
				Worktree: l.laneTarget(d.LaneID), BaseSHA: baseSHA, HeadSHA: headSHA,
				RefusalDetail: invalidTrunkObstacleDetail(obs, classified.Rejected...),
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
	if p.Whole && p.ProvedAt.UnixMilli() <= seen {
		return false
	}
	needed := provableChecks(checks)
	if len(needed) == 0 {
		return p.Whole && p.ProvedAt.UnixMilli() > seen
	}
	for _, check := range needed {
		if at, ok := p.At[check]; ok && at.UnixMilli() > seen {
			continue
		}
		matched := false
		if !strings.Contains(check, "/") {
			prefix := check + "/"
			for k, at := range p.At {
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
			if h.Kind == ObstacleTrunk && h.Mover == "drain" && len(h.Checks) > 0 {
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
				ok, err := proofPastRed(ctx, l.cfg.Root, lost, redCommit, p.Commit)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			if green != "" {
				ok, err := containsCommit(ctx, l.cfg.Root, p.Commit, green)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			ready = append(ready, release{hold: h, reason: p.Describe(provableChecks(h.Checks))})
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
