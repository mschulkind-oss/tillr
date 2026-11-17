// Package redcap reads the repository's landing bound without changing refs or store state.
package redcap

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const Reason = "red-cap"
const GreenRef = "refs/tillr/green"

type Reader interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// Hold names the bound reached, the known red checks, and the item that can repair them.
// A pending proof has no failing check or fixer yet; Detail says so explicitly.
type Hold struct {
	GreenCommit   string   `json:"green_commit"`
	LandingsSince int64    `json:"landings_since"`
	MinutesSince  int64    `json:"minutes_since"`
	CheckIDs      []string `json:"check_ids"`
	FixerItem     string   `json:"fixer_item"`
	Detail        string   `json:"detail"`
}

func (h *Hold) Reason() string { return Reason + ": " + h.Detail }

func git(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("red cap: git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// Exempt identifies fixer items and fork patch deliveries before resolving a trunk ref.
func Exempt(ref, itemID string) bool {
	return strings.HasPrefix(itemID, "fixer-main-") || strings.HasPrefix(ref, "patch/") || strings.HasPrefix(ref, "refs/heads/patch/")
}

// Check reads today's bound against tip. The lander calls it under land.lock;
// the planner calls the same reader to hold work without re-gating or re-dispatching.
// With no proof pointer yet there is no measured green range, so bootstrap is not capped.
func Check(ctx context.Context, r Reader, root, ref, tip, itemID string, at time.Time) (*Hold, error) {
	policy, _, err := ReadGatePolicy(root)
	if err != nil {
		return nil, err
	}
	if policy.LandingStrategy != "after-merge" || (policy.RedCap.Landings == 0 && policy.RedCap.Minutes == 0) || Exempt(ref, itemID) {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "show-ref", "--verify", "--quiet", GreenRef)
	if err = cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("red cap: reading green pointer: %w", err)
	}
	green, err := git(ctx, root, "rev-parse", GreenRef+"^{commit}")
	if err != nil {
		return nil, err
	}
	if tip == green {
		return nil, nil
	}
	if _, err = git(ctx, root, "merge-base", "--is-ancestor", green, tip); err != nil {
		return nil, err
	}
	commits, err := git(ctx, root, "rev-list", "--first-parent", "--parents", green+".."+tip)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	parents := map[string]string{}
	for _, line := range strings.Split(commits, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			set[fields[0]], parents[fields[0]] = true, fields[1]
		}
	}
	h := &Hold{GreenCommit: green, CheckIDs: []string{}}
	var exists bool
	if err = r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		// Use landings_trunk's (before, after) index. The cap reads only the
		// unproved range, never every receipt the repository has accumulated.
		for sha, parent := range parents {
			var landed bool
			if err = r.QueryRow(`SELECT EXISTS(SELECT 1 FROM landings WHERE trunk_before=? AND trunk_after=? AND result='landed')`, parent, sha).Scan(&landed); err != nil {
				return nil, err
			}
			if landed {
				h.LandingsSince++
			}
		}
	}
	stamp, err := git(ctx, root, "show", "-s", "--format=%ct", green)
	if err != nil {
		return nil, err
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return nil, err
	}
	elapsed := at.Sub(time.Unix(seconds, 0))
	if elapsed > 0 {
		h.MinutesSince = int64(elapsed / time.Minute)
	}
	bounds := []string{}
	if policy.RedCap.Landings > 0 && h.LandingsSince >= policy.RedCap.Landings {
		bounds = append(bounds, fmt.Sprintf("red_cap.landings=%d (%d since green)", policy.RedCap.Landings, h.LandingsSince))
	}
	if policy.RedCap.Minutes > 0 && elapsed/time.Minute >= time.Duration(policy.RedCap.Minutes) {
		bounds = append(bounds, fmt.Sprintf("red_cap.minutes=%d (%d since green)", policy.RedCap.Minutes, h.MinutesSince))
	}
	if len(bounds) == 0 {
		return nil, nil
	}
	common, err := git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(common, "tillr-gate", "heavy.json"))
	redSHAs := map[string]bool{}
	if err == nil {
		var run struct {
			TipSHA string `json:"tip_sha"`
			Checks []struct {
				CheckID         string `json:"check_id"`
				Result          string `json:"result"`
				FirstRedLanding string `json:"first_red_landing"`
			}
		}
		if err = json.Unmarshal(raw, &run); err != nil {
			return nil, err
		}
		// An old report behind green is historical evidence, not today's red check.
		if set[run.TipSHA] {
			for _, c := range run.Checks {
				if c.Result == "fail" {
					h.CheckIDs = append(h.CheckIDs, c.CheckID)
					red := c.FirstRedLanding
					if red == "" {
						red = run.TipSHA
					}
					if set[red] {
						if len(red) > 12 {
							red = red[:12]
						}
						redSHAs["fixer-main-"+red] = true
					}
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(h.CheckIDs)
	if err = r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='items')`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		rows, err := r.Query(`SELECT item_id FROM items WHERE item_id LIKE 'fixer-main-%' AND state NOT IN ('landed','closed','resolved-elsewhere') ORDER BY item_id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if h.FixerItem == "" && redSHAs[id] {
				h.FixerItem = id
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	checks := strings.Join(h.CheckIDs, ",")
	if checks == "" {
		checks = "heavy proof pending; no red check recorded"
	}
	fixer := h.FixerItem
	if fixer == "" {
		fixer = "no open fixer recorded; heavy runner must prove trunk or file its fixer"
	}
	h.Detail = strings.Join(bounds, "; ") + "; checks: " + checks + "; waiting on " + fixer
	return h, nil
}
