// Package judgegrants reads delegated sign-off grants without registering writer schemas.
package judgegrants

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// WarnWindow is how far ahead of expiry tillr view warns that a grant is lapsing.
const WarnWindow = time.Hour

// Grant is one recorded delegation from item_grants.
type Grant struct {
	GrantID      string   `json:"grant_id"`
	ClaimClasses []string `json:"claim_classes"`
	Grantor      string   `json:"grantor"`
	IssuedAtMS   int64    `json:"issued_at_ms"`
	ExpiresAtMS  int64    `json:"expires_at_ms"`
}

// View describes a current or unreplaced expired grant for status and Observatory payloads.
type View struct {
	GrantID      string   `json:"grant_id"`
	ClaimClasses []string `json:"claim_classes"`
	Grantor      string   `json:"grantor"`
	IssuedAtMS   int64    `json:"issued_at_ms"`
	ExpiresAtMS  int64    `json:"expires_at_ms"`
	IssuedAt     string   `json:"issued_at"`
	ExpiresAt    string   `json:"expires_at"`
	State        string   `json:"state"`
	Warning      bool     `json:"warning"`
	Summary      string   `json:"summary"`
	RenewCommand string   `json:"renew_command"`
}

func (g Grant) IssuedAt() time.Time  { return time.UnixMilli(g.IssuedAtMS).UTC() }
func (g Grant) ExpiresAt() time.Time { return time.UnixMilli(g.ExpiresAtMS).UTC() }

func (g Grant) Active(now time.Time) bool {
	ms := now.UnixMilli()
	return g.IssuedAtMS <= ms && g.ExpiresAtMS > ms
}

func (g Grant) Expired(now time.Time) bool {
	ms := now.UnixMilli()
	return g.IssuedAtMS <= ms && g.ExpiresAtMS <= ms
}

// ExpiringSoon reports whether an active grant is within WarnWindow of lapsing.
func (g Grant) ExpiringSoon(now time.Time) bool {
	if !g.Active(now) {
		return false
	}
	return g.ExpiresAtMS-now.UnixMilli() <= WarnWindow.Milliseconds()
}

func (g Grant) CoversAll(classes []string) bool {
	if len(classes) == 0 {
		return false
	}
	have := make(map[string]bool, len(g.ClaimClasses))
	for _, c := range g.ClaimClasses {
		have[c] = true
	}
	for _, c := range classes {
		if c == "owner-only" || !have[c] {
			return false
		}
	}
	return true
}

// IssueCommand returns the CLI command that issues a new grant on storePath.
func IssueCommand(storePath string) string {
	target := "<path>"
	if strings.TrimSpace(storePath) != "" {
		target = shellQuote(storePath)
	}
	return fmt.Sprintf("tillr item grant issue --store %s --input <record> --now <RFC3339>", target)
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func splitClasses(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func tableExists(r *store.Reader, name string) (bool, error) {
	var ok bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&ok)
	return ok, err
}

func columnExists(r *store.Reader, table, column string) (bool, error) {
	var ok bool
	err := r.QueryRow(fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('%s') WHERE name=?)`, table), column).Scan(&ok)
	return ok, err
}

// Read returns all grants in item_grants, or nil when the store predates the table.
func Read(r *store.Reader) ([]Grant, error) {
	ok, err := tableExists(r, "item_grants")
	if err != nil || !ok {
		return nil, err
	}
	rows, err := r.Query(`SELECT grant_id, claim_classes, grantor, issued_at, expires_at FROM item_grants ORDER BY expires_at DESC, grant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		var classes string
		if err := rows.Scan(&g.GrantID, &classes, &g.Grantor, &g.IssuedAtMS, &g.ExpiresAtMS); err != nil {
			return nil, err
		}
		g.ClaimClasses = splitClasses(classes)
		out = append(out, g)
	}
	return out, rows.Err()
}

// Current returns every active grant (soonest expiry first) followed by the most recent
// expired grant for any claim-class set that no active grant covers.
func Current(r *store.Reader, now time.Time) ([]Grant, error) {
	all, err := Read(r)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	var active, expired []Grant
	for _, g := range all {
		switch {
		case g.Active(now):
			active = append(active, g)
		case g.Expired(now):
			expired = append(expired, g)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].ExpiresAtMS != active[j].ExpiresAtMS {
			return active[i].ExpiresAtMS < active[j].ExpiresAtMS
		}
		return active[i].GrantID < active[j].GrantID
	})
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].ExpiresAtMS != expired[j].ExpiresAtMS {
			return expired[i].ExpiresAtMS > expired[j].ExpiresAtMS
		}
		return expired[i].GrantID < expired[j].GrantID
	})
	kept := append([]Grant(nil), active...)
	for _, g := range expired {
		covered := false
		for _, k := range kept {
			if k.CoversAll(g.ClaimClasses) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, g)
		}
	}
	return kept, nil
}

// CurrentViews projects Current into serialized View records for status, running, and snapshot.
func CurrentViews(r *store.Reader, now time.Time, storePath string) ([]View, error) {
	grants, err := Current(r, now)
	if err != nil || len(grants) == 0 {
		return nil, err
	}
	cmd := IssueCommand(storePath)
	out := make([]View, 0, len(grants))
	for _, g := range grants {
		state := "active"
		warning := false
		if g.ExpiringSoon(now) {
			state = "expiring"
			warning = true
		} else if g.Expired(now) {
			state = "expired"
			warning = true
		}
		out = append(out, View{
			GrantID:      g.GrantID,
			ClaimClasses: append([]string(nil), g.ClaimClasses...),
			Grantor:      g.Grantor,
			IssuedAtMS:   g.IssuedAtMS,
			ExpiresAtMS:  g.ExpiresAtMS,
			IssuedAt:     g.IssuedAt().Format(time.RFC3339),
			ExpiresAt:    g.ExpiresAt().Format(time.RFC3339),
			State:        state,
			Warning:      warning,
			Summary:      FormatStatusLine(g, now, storePath),
			RenewCommand: cmd,
		})
	}
	return out, nil
}

// ExpiredForItem returns the most recent expired grant that covered every stored
// criterion of itemID, or nil when no covering grant has expired.
func ExpiredForItem(r *store.Reader, itemID string, now time.Time) (*Grant, error) {
	if itemID == "" {
		return nil, nil
	}
	for _, tbl := range []string{"item_grants", "item_criteria"} {
		ok, err := tableExists(r, tbl)
		if err != nil || !ok {
			return nil, err
		}
	}
	hasClass, err := columnExists(r, "item_criteria", "claim_class")
	if err != nil || !hasClass {
		return nil, err
	}
	ms := now.UnixMilli()
	var g Grant
	var classes string
	err = r.QueryRow(`SELECT g.grant_id, g.claim_classes, g.grantor, g.issued_at, g.expires_at
		FROM item_grants g
		WHERE g.expires_at <= ? AND g.issued_at <= ?
		  AND EXISTS(SELECT 1 FROM item_criteria c WHERE c.item_id=?)
		  AND NOT EXISTS(SELECT 1 FROM item_criteria c WHERE c.item_id=? AND
		    (c.claim_class='owner-only' OR instr(',' || g.claim_classes || ',', ',' || c.claim_class || ',')=0))
		ORDER BY g.expires_at DESC, g.grant_id
		LIMIT 1`, ms, ms, itemID, itemID).Scan(&g.GrantID, &classes, &g.Grantor, &g.IssuedAtMS, &g.ExpiresAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.ClaimClasses = splitClasses(classes)
	return &g, nil
}

// FormatRemaining formats a positive remaining duration concisely (e.g. "45m", "1h", "23h15m", "30s").
func FormatRemaining(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	secs := int64(d.Round(time.Second) / time.Second)
	if secs <= 0 {
		secs = 1
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	remSecs := secs % 60
	if mins < 60 {
		if remSecs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%02ds", mins, remSecs)
	}
	hours := mins / 60
	remMins := mins % 60
	if remMins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%02dm", hours, remMins)
}

// FormatStatusLine formats one grant's status or warning line for tillr view.
func FormatStatusLine(g Grant, now time.Time, storePath string) string {
	classes := strings.Join(g.ClaimClasses, ",")
	exp := g.ExpiresAt().Format(time.RFC3339)
	cmd := IssueCommand(storePath)
	remaining := time.Duration(g.ExpiresAtMS-now.UnixMilli()) * time.Millisecond
	if g.ExpiringSoon(now) {
		return fmt.Sprintf("warning: grant %s (%s) expires at %s (in %s); renew with %s", g.GrantID, classes, exp, FormatRemaining(remaining), cmd)
	}
	if g.Active(now) {
		return fmt.Sprintf("grant: %s (%s) expires at %s (in %s)", g.GrantID, classes, exp, FormatRemaining(remaining))
	}
	return fmt.Sprintf("warning: grant %s (%s) expired at %s; issue a new grant with %s", g.GrantID, classes, exp, cmd)
}

// FormatHold returns the Why detail and Mover action for an item waiting as judge-grant-absent.
func FormatHold(expired *Grant, storePath string) (detail, mover string) {
	cmd := IssueCommand(storePath)
	if expired == nil {
		return "no unexpired grant covers the item's criteria; issue one with " + cmd,
			"issue a grant covering the item's criteria: " + cmd
	}
	exp := expired.ExpiresAt().Format(time.RFC3339)
	msg := fmt.Sprintf("grant %s expired at %s; issue a new grant with %s", expired.GrantID, exp, cmd)
	return msg, msg
}
