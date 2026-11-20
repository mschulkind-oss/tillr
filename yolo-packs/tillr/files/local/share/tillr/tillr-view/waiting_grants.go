package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/judgegrants"
	"github.com/mschulkind-oss/tillr/store"
)

func waitingGrantLines(r *store.Reader, now time.Time, storePath string) ([]judgegrants.View, error) {
	return judgegrants.CurrentViews(r, now, storePath)
}

// waitingGrantWarnings surfaces a needs-you observation row for each active grant within
// judgegrants.WarnWindow (1 hour) of lapsing, before items begin stalling on expiry.
func waitingGrantWarnings(r *store.Reader, now time.Time, storePath string) ([]waitingRow, error) {
	grants, err := judgegrants.Current(r, now)
	if err != nil || len(grants) == 0 {
		return nil, err
	}
	cmd := judgegrants.IssueCommand(storePath)
	var out []waitingRow
	for _, g := range grants {
		if !g.ExpiringSoon(now) {
			continue
		}
		remaining := time.Duration(g.ExpiresAtMS-now.UnixMilli()) * time.Millisecond
		classes := strings.Join(g.ClaimClasses, ",")
		exp := g.ExpiresAt().Format(time.RFC3339)
		out = append(out, waitingRow{
			Observation: "grant:" + g.GrantID,
			Class:       "needs you",
			Where:       "judge",
			Why:         fmt.Sprintf("grant %s (%s) expires at %s (in %s)", g.GrantID, classes, exp, judgegrants.FormatRemaining(remaining)),
			Mover:       "operator; renew before expiry with " + cmd,
			Since:       g.IssuedAt().Format(time.RFC3339Nano),
			ClassSource: "rule",
		})
	}
	return out, nil
}
