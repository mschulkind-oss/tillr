package drain

import (
	"github.com/mschulkind-oss/tillr-drain/presence"
)

type streamState = presence.StreamState

const (
	streamAbsent    = presence.StreamAbsent
	streamUnwritten = presence.StreamUnwritten
	streamWritten   = presence.StreamWritten
)

var (
	openForWriting     = presence.OpenForWriting
	loopPresencePath   = presence.LoopPresencePath
	openLoopPresence   = presence.OpenLoopPresence
	holdLoopPresence   = presence.HoldLoopPresence
	fromOtherDomain    = presence.FromOtherDomain
	endedSessionOwners = presence.EndedSessionOwners
	foreignOwnerGone   = presence.ForeignOwnerGone
)

// runStreamLocator is a Steps that starts run faces with their streams in per-run files,
// and says where, so a loop in another process domain can judge the face by them.
type runStreamLocator interface {
	runOutputFiles(runID string) []string
}

// foreignRunLive answers whether a run started from another process domain may still be
// running: some process holds one of its stream files open for writing, or the files
// cannot be located or judged. Absent files mean no face was started there:
// ProcessSteps creates both before it starts the face. A reboot (!SameBoot) proves
// the run is gone even without a locator.
func foreignRunLive(locator runStreamLocator, runID, ownerDomain, hereDomain string) bool {
	if runID == "" {
		return false
	}
	var paths []string
	hasLocator := locator != nil
	if hasLocator {
		paths = locator.runOutputFiles(runID)
	}
	return presence.ForeignRunLive(paths, hasLocator, ownerDomain, hereDomain)
}
