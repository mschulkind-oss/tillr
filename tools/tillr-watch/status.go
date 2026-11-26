package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

type statusRule struct {
	ID         string `json:"id"`
	Skipped    *int   `json:"skipped,omitempty"`
	Unreadable string `json:"unreadable,omitempty"`
	OutOfScope string `json:"out_of_scope,omitempty"`
}

func printStatus(ctx context.Context, e watch.Evaluator, path string, onlyUnsettled, asJSON bool, out, stderr io.Writer) error {
	var s watch.Status
	if err := store.ReadWithoutCreating(ctx, path, func(db *store.Store) error {
		e.Store = db
		var err error
		s, err = e.ReadStatusWithOptions(ctx, func(owner string) (bool, error) { return isAttached(path, owner) }, watch.StatusOptions{OnlyUnsettled: onlyUnsettled})
		return err
	}); err != nil {
		return err
	}
	rules := make([]statusRule, 0, len(s.Rules))
	for _, r := range s.Rules {
		if r.Unsupported != "" {
			fmt.Fprintf(stderr, "tillr watch: rule %s not evaluated: %s\n", r.ID, r.Unsupported)
		}
		if r.Disabled || r.Unsupported != "" {
			continue
		}
		row := statusRule{ID: r.ID, Unreadable: r.Unreadable, OutOfScope: r.OutOfScope}
		if row.Unreadable == "" && row.OutOfScope == "" {
			row.Skipped = &r.Skipped
		}
		rules = append(rules, row)
	}
	if s.Silences == nil {
		s.Silences = []watch.SilenceStatus{}
	}
	if asJSON {
		return json.NewEncoder(out).Encode(struct {
			Summary  watch.StatusSummary   `json:"summary"`
			Rules    []statusRule          `json:"rules"`
			Alerts   []watch.AlertStatus   `json:"alerts"`
			Silences []watch.SilenceStatus `json:"silences"`
			Owners   []watch.OwnerStatus   `json:"owners"`
		}{s.Summary, rules, s.Alerts, s.Silences, s.Owners})
	}
	if _, err := fmt.Fprintf(out, "summary unsettled=%d (uninvestigated=%d investigating=%d investigated=%d) settled=%d active_silences=%d\n",
		s.Summary.Unsettled, s.Summary.Uninvestigated, s.Summary.Investigating, s.Summary.Investigated,
		s.Summary.Settled, s.Summary.ActiveSilences); err != nil {
		return err
	}
	if !onlyUnsettled {
		for _, r := range rules {
			field := ""
			switch {
			case r.Unreadable != "":
				field = "unreadable=" + jsonString(r.Unreadable)
			case r.OutOfScope != "":
				field = "out-of-scope=" + jsonString(r.OutOfScope)
			default:
				field = fmt.Sprintf("skipped=%d", *r.Skipped)
			}
			if _, err := fmt.Fprintf(out, "rule %s %s\n", r.ID, field); err != nil {
				return err
			}
		}
	}
	for _, a := range s.Alerts {
		suffix := ""
		if a.Settlement != "" {
			suffix += " settlement=" + a.Settlement
		}
		if a.UnsettledFor != "" {
			suffix += " unsettled_for=" + a.UnsettledFor
		}
		if a.EscalatedTo != "" {
			suffix += " escalated_to=" + a.EscalatedTo
		}
		if a.By != nil {
			suffix += " by=" + jsonString(*a.By)
		}
		if a.Until != "" {
			suffix += " until=" + a.Until
		}
		if a.ItemID != nil {
			suffix += " item=" + jsonString(*a.ItemID)
		}
		varsMap := a.Vars
		if varsMap == nil {
			varsMap = map[string]string{}
		}
		varsJSON, _ := json.Marshal(varsMap)
		if _, err := fmt.Fprintf(out, "alert %s rule=%s subject=%s state=%s severity=%s opened=%s observed=%d vars=%s%s\n", a.ID, a.RuleID, jsonString(a.Subject), a.State, a.Severity, a.Opened, a.Observed, string(varsJSON), suffix); err != nil {
			return err
		}
	}
	if !onlyUnsettled {
		for _, sil := range s.Silences {
			varsMap := sil.Vars
			if varsMap == nil {
				varsMap = map[string]string{}
			}
			varsJSON, _ := json.Marshal(varsMap)
			if _, err := fmt.Fprintf(out, "silence %s rule=%s vars=%s item=%s by=%s until=%s matched_alerts=%d\n",
				sil.SilenceID, sil.RuleID, string(varsJSON), jsonString(sil.ItemID), jsonString(sil.By), sil.Until, sil.MatchedAlerts); err != nil {
				return err
			}
		}
		for _, o := range s.Owners {
			attachment := "unattached"
			if o.Attached {
				attachment = "attached since=" + statusTimeOrDash(o.Since)
			}
			if _, err := fmt.Fprintf(out, "owner %s watcher=%s last_wait=%s pending=%d\n", o.Owner, attachment, statusTimeOrDash(o.LastWait), o.Pending); err != nil {
				return err
			}
		}
	}
	return nil
}

func statusTimeOrDash(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func jsonString(s string) string {
	quoted, _ := json.Marshal(s)
	return string(quoted)
}
