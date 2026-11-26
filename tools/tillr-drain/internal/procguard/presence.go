package procguard

import (
	"github.com/mschulkind-oss/tillr-drain/presence"
)

type StreamState = presence.StreamState

const (
	StreamAbsent    = presence.StreamAbsent
	StreamUnwritten = presence.StreamUnwritten
	StreamWritten   = presence.StreamWritten
)

var (
	OpenForWriting     = presence.OpenForWriting
	LoopPresencePath   = presence.LoopPresencePath
	OpenLoopPresence   = presence.OpenLoopPresence
	HoldLoopPresence   = presence.HoldLoopPresence
	FromOtherDomain    = presence.FromOtherDomain
	EndedSessionOwners = presence.EndedSessionOwners
	ForeignOwnerGone   = presence.ForeignOwnerGone
)

// ForeignRunLive answers whether a run started from another process domain may still be
// running: some process holds one of its stream files open for writing, or the files
// cannot be located or judged. Absent files mean no face was started there:
// ProcessSteps creates both before it starts the face. A reboot (!SameBoot) proves
// the run is gone even without a locator.
func ForeignRunLive(locate func(runID string) []string, runID, ownerDomain, hereDomain string) bool {
	if runID == "" {
		return false
	}
	var paths []string
	hasLocator := locate != nil
	if hasLocator {
		paths = locate(runID)
	}
	return presence.ForeignRunLive(paths, hasLocator, ownerDomain, hereDomain)
}
