package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// RetryPinLimit bounds obsolete examined attempts, not live or retained candidates.
const RetryPinLimit = 8

// RefSweepRequest names the repository and its store. DryRun reports without writes.
type RefSweepRequest struct {
	Root, Store string
	DryRun      bool
	At          time.Time
}

// RefDisposition explains one ref in the sweep, including operator-owned refs.
type RefDisposition struct {
	Ref, SHA, Reason string
	Symbolic         string
	Remove           bool
}

type refRun struct {
	item string
	at   int64
}
type refLanding struct{ item, head, after string }

// SweepRefs is also the loop's settlement cleanup. The landing lock protects ref
// enumeration and deletion from concurrent marker writes, before any store writer.
// The short write transaction excludes dispatch/lease acquisition while
// Git deletes the planned refs, guarded by their observed object IDs in one transaction.
// A crash leaves either the old refs or the deleted refs; replay computes the same plan.
func SweepRefs(ctx context.Context, req RefSweepRequest) ([]RefDisposition, error) {
	if req.Root == "" || req.Store == "" {
		return nil, Usagef("sweep-refs needs --root <repository> and --store <path>")
	}
	if req.At.IsZero() {
		req.At = time.Now().UTC()
	}
	root, err := filepath.Abs(req.Root)
	if err != nil {
		return nil, err
	}
	req.Root = root
	l := &Loop{cfg: LoopConfig{Root: req.Root, Store: req.Store, Now: func() time.Time { return req.At }}}
	unlock, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	raw, err := l.git(ctx, req.Root, "for-each-ref", "--format=%(refname) %(objectname) %(symref)", "refs/tillr")
	if err != nil {
		return nil, err
	}
	var refs []RefDisposition
	operators := map[string][]string{}
	contained := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ref := RefDisposition{Ref: fields[0], SHA: fields[1]}
		if len(fields) > 2 {
			ref.Symbolic = fields[2]
		}
		refs = append(refs, ref)
		if operatorRef(fields[0]) {
			operators[fields[1]] = append(operators[fields[1]], fields[0])
			cmd := exec.CommandContext(ctx, "git", "-C", req.Root, "merge-base", "--is-ancestor", fields[1], TrunkRef)
			if err := cmd.Run(); err == nil {
				contained[fields[0]] = true
			} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
				return nil, Externalf("checking trunk containment of %s: %v", fields[0], err)
			}
		}
	}
	if len(refs) == 0 {
		return refs, nil
	}
	var s *store.Store
	if req.DryRun {
		s, err = store.OpenRead(ctx, req.Store)
	} else {
		s, err = store.Open(ctx, req.Store, true)
	}
	if err != nil {
		return nil, err
	}
	defer s.Close()
	plan := func(r *store.Reader) error {
		var err error
		refs, err = planRefSweep(r, refs, operators, contained, req.Root)
		return err
	}
	if req.DryRun {
		err = s.ReadTx(ctx, plan)
	} else {
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			if err := plan(tx.Reader()); err != nil {
				return err
			}
			var commands strings.Builder
			for _, ref := range refs {
				if ref.Remove {
					fmt.Fprintf(&commands, "option no-deref\ndelete %s %s\n", ref.Ref, ref.SHA)
				}
			}
			if commands.Len() == 0 {
				return nil
			}
			cmd := exec.CommandContext(ctx, "git", "-C", req.Root, "update-ref", "--stdin")
			cmd.Stdin = strings.NewReader("start\n" + commands.String() + "prepare\ncommit\n")
			if out, err := cmd.CombinedOutput(); err != nil {
				return Externalf("deleting settled refs: %v: %s", err, out)
			}
			saved, err := ReadPreservedWork(tx.Reader())
			if err != nil {
				return err
			}
			for _, ref := range refs {
				if ref.Remove && strings.HasPrefix(ref.Ref, preservedPrefix) {
					for _, p := range saved {
						if p.Ref == ref.Ref && !p.Pruned {
							if err := appendEffect(tx, workPrunedKind, p.RunID, p, req.At); err != nil {
								return err
							}
						}
					}
				}
			}
			return nil
		})
	}
	return refs, err
}

func operatorRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/tillr/held/") || strings.HasPrefix(ref, "refs/tillr/recovery/")
}

func planRefSweep(r *store.Reader, refs []RefDisposition, operators map[string][]string, contained map[string]bool, root string) ([]RefDisposition, error) {
	declared, err := definition.ForReader(r)
	if err != nil {
		return nil, err
	}
	states := map[string]string{}
	rows, err := r.Query(`SELECT item_id, state FROM items`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			rows.Close()
			return nil, err
		}
		states[id] = state
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	runs := map[string]refRun{}
	rows, err = r.Query(`SELECT run_id,item_id,started_at FROM runs WHERE item_id IS NOT NULL AND item_id != '' UNION ALL SELECT run_id,item_id,dispatched_at FROM dispatches WHERE run_id IS NOT NULL AND item_id IS NOT NULL AND item_id != '' ORDER BY 3`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var run refRun
		if err := rows.Scan(&id, &run.item, &run.at); err != nil {
			rows.Close()
			return nil, err
		}
		runs[id] = run
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	landings := map[string]refLanding{}
	landedItems := map[string]bool{}
	rows, err = r.Query(`SELECT run_id,item_id,head_sha,trunk_after FROM landings WHERE result='landed'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var land refLanding
		if err := rows.Scan(&id, &land.item, &land.head, &land.after); err != nil {
			rows.Close()
			return nil, err
		}
		landings[id] = land
		landedItems[land.item] = true
		if _, ok := runs[id]; !ok {
			runs[id] = refRun{item: land.item}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	current, history, err := readPendingHistory(r)
	if err != nil {
		return nil, err
	}
	needed := map[string]bool{}
	examined := map[string]bool{}
	superseded := map[string]bool{}
	baseRefs := map[string]string{}
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.Kind == "run.allocate" {
			var claim struct {
				RunID  string `json:"run_id"`
				ItemID string `json:"item_id"`
			}
			if err := json.Unmarshal(e.Payload, &claim); err != nil {
				return nil, err
			}
			if _, known := runs[claim.RunID]; !known && claim.RunID != "" && claim.ItemID != "" {
				runs[claim.RunID] = refRun{item: claim.ItemID, at: e.At.UnixMilli()}
			}
		}
		if e.ObjectID != nil && e.ObjectType == "run" && e.Kind == examinationDoneKind {
			examined[*e.ObjectID] = true
		}
		if e.ObjectID != nil && e.ObjectType == "run" && e.Kind == candidateSupersededKind {
			superseded[*e.ObjectID] = true
		}
		if e.Kind == "land.landing" {
			var receipt struct {
				RunID   string `json:"run_id"`
				BaseRef string `json:"base_ref"`
				Result  string `json:"result"`
			}
			if err := json.Unmarshal(e.Payload, &receipt); err != nil {
				return nil, err
			}
			if receipt.Result == "landed" && receipt.BaseRef != "" {
				baseRefs[receipt.RunID] = receipt.BaseRef
			}
		}
	}
	inFlight := false
	for id, work := range history {
		runs[id] = refRun{item: work.Decision.ItemID, at: runs[id].at}
		if !work.Done && current[work.Decision.ItemID] == work {
			inFlight = true
			// A terminal item's unfinished examination may still need its pin to
			// replay a lane reset after a crash. Settlement retires that need.
			needed[id] = true
			if work.ParentRunID != "" {
				needed[work.ParentRunID] = true
			}
		}
	}
	live := map[string]bool{}
	liveItems := map[string]bool{}
	// Even an expired lease is retained until reaping records the lane's release.
	rows, err = r.Query(`SELECT run_id FROM lanes WHERE state='leased'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id sql.NullString
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		inFlight = true
		if id.Valid {
			live[id.String] = true
			if owner, known := runs[id.String]; known {
				liveItems[owner.item] = true
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Keep the newest eight obsolete examined pins for each retryable item, plus
	// every active/retained attempt. Finished items have no count-based exemption.
	byItem := map[string][]string{}
	for _, ref := range refs {
		if strings.HasPrefix(ref.Ref, "refs/tillr/runs/") {
			id := strings.TrimPrefix(ref.Ref, "refs/tillr/runs/")
			if run, ok := runs[id]; ok && examined[id] && !needed[id] && !live[id] {
				byItem[run.item] = append(byItem[run.item], id)
			}
		}
	}
	recent := map[string]bool{}
	for _, ids := range byItem {
		sort.Slice(ids, func(i, j int) bool {
			if runs[ids[i]].at == runs[ids[j]].at {
				return ids[i] > ids[j]
			}
			return runs[ids[i]].at > runs[ids[j]].at
		})
		for i, id := range ids {
			if i < RetryPinLimit {
				recent[id] = true
			}
		}
	}
	saved, err := ReadPreservedWork(r)
	if err != nil {
		return nil, err
	}
	preserved := map[string]PreservedWork{}
	for _, p := range saved {
		preserved[p.Ref] = p
	}
	for i := range refs {
		ref := &refs[i]
		switch {
		case operatorRef(ref.Ref):
			_, id, _ := strings.Cut(strings.TrimPrefix(ref.Ref, "refs/tillr/"), "/")
			state := states[id]
			if state == "" {
				state = "unknown"
			}
			ref.Reason = fmt.Sprintf("operator owns this ref; item state=%s; trunk contains commit=%t", state, contained[ref.Ref])
		case ref.Symbolic != "":
			ref.Reason = "unexpected symbolic ref; retain for diagnosis; target=" + ref.Symbolic
		case strings.HasPrefix(ref.Ref, "refs/tillr/runs/"):
			id := strings.TrimPrefix(ref.Ref, "refs/tillr/runs/")
			// Set-aside pins its reset destination alongside the candidate so a
			// crashed reset can replay. It belongs to the same run, not a new
			// unknown attempt. Prefer an exact run ID if one exists.
			if _, known := runs[id]; !known && strings.HasSuffix(id, "-set-aside-base") {
				id = strings.TrimSuffix(id, "-set-aside-base")
			}
			if _, known := runs[id]; !known && strings.HasSuffix(id, supersededHeadSuffix) {
				id = strings.TrimSuffix(id, supersededHeadSuffix)
			}
			run, known := runs[id]
			switch {
			case superseded[id]:
				// A retry superseded this candidate (retry_supersede.go). The pin is the only
				// copy the loop keeps of it, and the supersession promised it is never deleted.
				ref.Reason = "candidate superseded by a retry; retained"
			case live[id] || liveItems[run.item]:
				ref.Reason = "live lane may need this item's attempt history"
			case !known:
				ref.Reason = "unknown run; ownership cannot be proved"
			case needed[id]:
				ref.Reason = "pending examination needs this pin for settlement or retry"
			case landedItems[run.item]:
				ref.Remove = true
				ref.Reason = "item has a durable landed row"
			case states[run.item] == "" || finishedState(declared.Parsed, states[run.item]):
				ref.Remove = true
				ref.Reason = "item finished or removed"
			case !examined[id]:
				ref.Reason = "retryable unexamined or pending run needs this pin"
			case recent[id]:
				ref.Reason = fmt.Sprintf("retryable item; newest %d examined attempt pins", RetryPinLimit)
			default:
				ref.Remove = true
				ref.Reason = fmt.Sprintf("retryable item; older than newest %d examined attempt pins", RetryPinLimit)
			}
			if ref.Remove && len(operators[ref.SHA]) > 0 {
				ref.Reason += "; operator ref still holds commit: " + strings.Join(operators[ref.SHA], ", ")
			}
		case strings.HasPrefix(ref.Ref, "refs/tillr/landed/"):
			id := strings.TrimPrefix(ref.Ref, "refs/tillr/landed/")
			land, recorded := landings[id]
			switch {
			case !recorded:
				ref.Reason = "no durable landed row; crash retry needs marker"
			case land.after != ref.SHA:
				ref.Reason = "marker differs from durable landing; retain for diagnosis"
			case inFlight && (strings.HasPrefix(baseRefs[id], "refs/heads/patch/") || (baseRefs[id] == "" && land.head == land.after)):
				ref.Reason = "fork delivery; in-flight boundary comparisons still need marker"
			default:
				ref.Remove = true
				ref.Reason = "durable landed row; no boundary reader needs marker"
			}
		case strings.HasPrefix(ref.Ref, preservedPrefix):
			p, known := preserved[ref.Ref]
			switch {
			case !known || filepath.Clean(p.Repository) != root:
				ref.Reason = "saved edits; no preservation record for this repository"
			case live[p.RunID]:
				ref.Reason = "live lane needs saved edits"
			case p.Pruned || states[p.ItemID] == "" || finishedState(declared.Parsed, states[p.ItemID]):
				ref.Remove = true
				ref.Reason = "saved edits; item finished or removed"
			default:
				ref.Reason = "saved edits; item can still be retried"
			}
		default:
			ref.Reason = "outside run-pin and landing-marker cleanup ownership"
		}
	}
	return refs, nil
}

func (l *Loop) pruneRunRefs(ctx context.Context) error {
	// Proof and operator refs alone need no lifecycle replay on an idle pass.
	raw, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--count=1", "--format=%(refname)", "refs/tillr/runs", "refs/tillr/landed", preservedPrefix)
	if err != nil {
		return err
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	refs, err := SweepRefs(ctx, RefSweepRequest{Root: l.cfg.Root, Store: l.cfg.Store, At: l.cfg.Now()})
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.Remove {
			l.reportf("loop: removed %s (%s): %s\n", ref.Ref, ref.SHA, ref.Reason)
		}
	}
	return nil
}
