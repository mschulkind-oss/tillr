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

func printStatus(ctx context.Context, e watch.Evaluator, path string, asJSON bool, out, stderr io.Writer) error {
	var s watch.Status
	if err := store.ReadWithoutCreating(ctx, path, func(db *store.Store) error {
		e.Store = db
		var err error
		s, err = e.ReadStatus(ctx, func(owner string) (bool, error) { return isAttached(path, owner) })
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
	if asJSON {
		return json.NewEncoder(out).Encode(struct {
			Rules  []statusRule        `json:"rules"`
			Alerts []watch.AlertStatus `json:"alerts"`
			Owners []watch.OwnerStatus `json:"owners"`
		}{rules, s.Alerts, s.Owners})
	}
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
	for _, a := range s.Alerts {
		suffix := ""
		if a.By != nil {
			suffix = " by=" + jsonString(*a.By)
		}
		if a.Until != "" {
			suffix = " until=" + a.Until
		}
		if a.ItemID != nil {
			suffix += " item=" + jsonString(*a.ItemID)
		}
		if _, err := fmt.Fprintf(out, "alert %s rule=%s subject=%s state=%s severity=%s opened=%s observed=%d%s\n", a.ID, a.RuleID, jsonString(a.Subject), a.State, a.Severity, a.Opened, a.Observed, suffix); err != nil {
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
