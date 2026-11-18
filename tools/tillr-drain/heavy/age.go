package heavy

import "time"

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
