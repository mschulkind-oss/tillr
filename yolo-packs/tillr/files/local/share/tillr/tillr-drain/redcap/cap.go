// Package redcap reads the repository's landing bound without changing refs or store state.
package redcap

import (
	"context"
	"database/sql"
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

// Exempt identifies fixer items and fork patch deliveries before resolving a trunk ref.
// An automatic revert (revert-main-) is a remedy too: holding it would keep the red it
// removes.
func Exempt(ref, itemID string) bool {
	return strings.HasPrefix(itemID, "fixer-main-") || strings.HasPrefix(itemID, "revert-main-") || strings.HasPrefix(ref, "patch/") || strings.HasPrefix(ref, "refs/heads/patch/")
}

// Check is retained as a no-op stub: OQ-GP7 removed the red cap, so landings
// are never held by landing count or elapsed time.
func Check(_ context.Context, _ Reader, _, _, _, _ string, _ time.Time) (*Hold, error) {
	return nil, nil
}
