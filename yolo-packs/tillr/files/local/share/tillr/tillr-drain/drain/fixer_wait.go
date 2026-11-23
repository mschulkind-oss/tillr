package drain

import (
	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr/store"
)

func fixerChecks(text string) []string {
	return fixer.FixerChecks(text)
}

// TrunkFixerWait names the repair, its checks and its next mover in both waiting
// faces. Owner conflicts preserve the complete report, including requested paths.
func TrunkFixerWait(r *store.Reader, id string) (string, bool, error) {
	detail, owner, err := fixer.TrunkFixerWait(r, id)
	return detail, owner, wrapFixerError(err)
}
