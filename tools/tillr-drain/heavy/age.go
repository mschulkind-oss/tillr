package heavy

import (
	"fmt"
	"strings"
	"time"
)

// ProofAge never substitutes a commit's author date for the time it was proved.
// Older proof pointers have no recorded time and say so explicitly.
func ProofAge(status HeavyStatus, now time.Time) string {
	if status.GreenProvedAt.IsZero() {
		return "unknown"
	}
	age := now.Sub(status.GreenProvedAt)
	if age < 0 {
		age = 0
	}
	return age.Truncate(time.Second).String()
}

// UnprovedLine is the one-line statement that the trunk is unproved: for how long, at
// which commits, and which check is failing. It is "" when the trunk is proved (the
// green pointer is the tip and no check fails) or when there is no tip to speak of.
func UnprovedLine(status HeavyStatus, now time.Time) string {
	if status.TipCommit == "" || status.GreenCommit == status.TipCommit && len(status.FailingChecks) == 0 {
		return ""
	}
	fold := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	short := func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		if s == "" {
			return "none"
		}
		return s
	}
	var b strings.Builder
	b.WriteString("trunk: unproved")
	if age := ProofAge(status, now); age == "unknown" {
		b.WriteString(" (proof age unknown)")
	} else {
		b.WriteString(" for " + age)
	}
	fmt.Fprintf(&b, " (green %s, tip %s, landings %d); failing: ", short(status.GreenCommit), short(status.TipCommit), status.LandingsSince)
	if len(status.FailingChecks) > 0 {
		ids := make([]string, 0, len(status.FailingChecks))
		for _, c := range status.FailingChecks {
			ids = append(ids, c.CheckID)
		}
		b.WriteString(strings.Join(ids, ", "))
	} else {
		b.WriteString("none (heavy=" + status.Result)
		if d := fold(status.Detail); d != "" {
			if len(d) > 160 {
				d = d[:160] + "..."
			}
			b.WriteString(": " + d)
		}
		b.WriteString(")")
	}
	return b.String()
}
