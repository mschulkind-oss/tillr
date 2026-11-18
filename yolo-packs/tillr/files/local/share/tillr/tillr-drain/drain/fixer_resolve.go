package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// FixerProvedKind records a trunk fixer resolved by a green heavy proof: the proved
// commit, its proof time and the fixer's checks the proof passed. It is the evidence an
// acquittal waiting on that fixer is released by (repairedTrunkWaiter).
const FixerProvedKind = "drain.fixer-proved"

func init() {
	if err := event.Register(FixerProvedKind, Utility); err != nil {
		panic(err)
	}
}

// fixerProof is the payload of FixerProvedKind.
type fixerProof struct {
	FixerItem string   `json:"fixer_item"`
	Green     string   `json:"green"`
	ProvedAt  string   `json:"proved_at"`
	Checks    []string `json:"checks"`
}

// fixerTriageNote is appended to a triage conflict a trunk fixer was wrongly given.
const fixerTriageNote = "A trunk fixer is filed by the loop and needs no document; triage does not apply, so the fixer returns to its worker."

// releaseFixerTriage undoes the triage hold a trunk fixer received when document-fed
// triage still treated it as an undeclared request. Its triage conflict is closed and the
// fixer goes back in the queue; nothing in it was the owner's to answer.
func (l *Loop) releaseFixerTriage(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	pending := func(r *store.Reader) ([]item.Conflict, error) {
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return nil, err
		}
		out := []item.Conflict{}
		for id, c := range conflicts {
			if !IsTrunkFixer(id) || c.TriageVersion == "" || c.Status == "closed" || c.Status == "answered" {
				continue
			}
			var state string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return nil, err
			}
			if state == StateBlocked {
				out = append(out, c)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ItemID < out[j].ItemID })
		return out, nil
	}
	var found []item.Conflict
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		found, err = pending(r)
		return err
	}); err != nil || len(found) == 0 {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		found, err := pending(tx.Reader())
		if err != nil {
			return err
		}
		for _, c := range found {
			c.Status, c.WaitingOn = "closed", "build"
			if c.RunID == "" {
				c.RunID = c.DesignRunID
			}
			c.Text = strings.TrimSpace(c.Text) + "\n" + fixerTriageNote
			if err := item.RecordConflict(tx, c, l.cfg.Now().UTC()); err != nil {
				return err
			}
			l.reportf("loop: %s triage conflict %s closed: %s\n", c.ItemID, c.ConflictID, fixerTriageNote)
		}
		return nil
	})
}

// readGreenProof answers the heavy runner's green pointer and its recorded proof, or a
// zero proof when there is no pointer or no proof for it.
func readGreenProof(ctx context.Context, root string) (heavy.GreenProof, error) {
	green, err := heavyGreen(ctx, root)
	if err != nil || green == "" {
		return heavy.GreenProof{}, err
	}
	dir, err := heavyDirectory(ctx, root)
	if err != nil {
		return heavy.GreenProof{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "green-"+green+".json"))
	if os.IsNotExist(err) {
		return heavy.GreenProof{}, nil
	}
	if err != nil {
		return heavy.GreenProof{}, err
	}
	var proof heavy.GreenProof
	if err := json.Unmarshal(raw, &proof); err != nil {
		return heavy.GreenProof{}, err
	}
	if proof.Commit != green {
		return heavy.GreenProof{}, nil
	}
	return proof, nil
}

// resolveCommit answers the full sha a fixer id's short sha names, or "" when the
// repository holds no such commit.
func resolveCommit(ctx context.Context, root, sha string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return "", Externalf("resolving fixer commit %s: %v", sha, err)
}

// provedFixer is one open fixer a green proof covers.
type provedFixer struct {
	id     string
	checks []string
}

// resolveProvedFixers settles every open trunk fixer a green heavy proof has made moot:
// the proved commit contains the fixer's red commit, the proof was recorded after the
// fixer's last red observation, and every check the fixer names passed in that proof.
// The fixer's own state does not matter — one parked on the owner is resolved the same
// way — because the trunk-broken hold it carries is about the trunk, and the proof shows
// the trunk green. An older proof that does not list its passed checks resolves nothing.
func (l *Loop) resolveProvedFixers(ctx context.Context) error {
	if l.cfg.Root == "" {
		return nil
	}
	proof, err := readGreenProof(ctx, l.cfg.Root)
	if err != nil || proof.Commit == "" || proof.ProvedAt.IsZero() || proof.Passed == nil {
		return err
	}
	passed := map[string]bool{}
	for _, id := range proof.Passed {
		passed[id] = true
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	type open struct {
		id, text string
		seen     int64
	}
	read := func(r *store.Reader) ([]open, error) {
		lifecycle, err := definition.ForReader(r)
		if err != nil {
			return nil, err
		}
		declared := lifecycle.Parsed
		rows, err := r.Query(`SELECT i.item_id, i.state, COALESCE(c.text, '') FROM items i LEFT JOIN item_criteria c ON c.item_id=i.item_id AND c.criterion_id='trunk-green' WHERE i.item_id LIKE 'fixer-main-%' ORDER BY i.item_id`)
		if err != nil {
			return nil, err
		}
		found := []open{}
		for rows.Next() {
			var o open
			var state string
			if err := rows.Scan(&o.id, &state, &o.text); err != nil {
				rows.Close()
				return nil, err
			}
			if !finishedState(declared, state) {
				found = append(found, o)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for i := range found {
			// The last red observation: the fixer's filing or a later red tip attached
			// to it. A proof recorded before it cannot speak for that observation.
			var seen sql.NullInt64
			if err := r.QueryRow(`SELECT MAX(ts) FROM events WHERE object_id=? AND kind IN (?, ?)`, found[i].id, item.AddKind, FixerAttachedKind).Scan(&seen); err != nil {
				return nil, err
			}
			found[i].seen = seen.Int64
		}
		return found, nil
	}
	var candidates []open
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		candidates, err = read(r)
		return err
	}); err != nil || len(candidates) == 0 {
		return err
	}
	covered := []provedFixer{}
	for _, o := range candidates {
		if proof.ProvedAt.UnixMilli() <= o.seen {
			continue
		}
		red, err := resolveCommit(ctx, l.cfg.Root, TrunkFixerSHA(o.id))
		if err != nil {
			return err
		}
		if red == "" {
			continue
		}
		contains, err := containsCommit(ctx, l.cfg.Root, red, proof.Commit)
		if err != nil {
			return err
		}
		if !contains {
			continue
		}
		checks := fixerChecks(o.text)
		all := true
		for _, check := range checks {
			all = all && passed[check]
		}
		if all {
			covered = append(covered, provedFixer{id: o.id, checks: checks})
		}
	}
	if len(covered) == 0 {
		return nil
	}
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		lifecycle, err := definition.ForReader(tx.Reader())
		if err != nil {
			return err
		}
		declared := lifecycle.Parsed
		for _, f := range covered {
			var state string
			if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, f.id).Scan(&state); err != nil {
				return err
			}
			if finishedState(declared, state) {
				continue
			}
			checks := "no named check"
			if len(f.checks) > 0 {
				checks = strings.Join(f.checks, ", ")
			}
			reason := "heavy proof green at " + proof.Commit + " (" + proof.ProvedAt.UTC().Format("2006-01-02T15:04:05Z") + "), which contains the red commit and passed " + checks
			if _, err := item.SetStateWithReceipt(ctx, tx, f.id, StateResolvedElsewhere, at, item.MoveReceipt{Reason: reason, By: Utility, ResolvesVia: "commit:" + proof.Commit}); err != nil {
				return err
			}
			if f.checks == nil {
				f.checks = []string{}
			}
			if err := appendEffect(tx, FixerProvedKind, f.id, fixerProof{FixerItem: f.id, Green: proof.Commit, ProvedAt: proof.ProvedAt.UTC().Format("2006-01-02T15:04:05.000Z"), Checks: f.checks}, at); err != nil {
				return err
			}
			l.reportf("loop: %s resolved: %s\n", f.id, reason)
		}
		return nil
	})
}

// fixerProvedGreen answers the green commit a FixerProvedKind event recorded for a
// fixer, or "" when no proof resolved it.
func fixerProvedGreen(r *store.Reader, fixer string) (string, error) {
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, FixerProvedKind, fixer).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var p fixerProof
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	return p.Green, nil
}
