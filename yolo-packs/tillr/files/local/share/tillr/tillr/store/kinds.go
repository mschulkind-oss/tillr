package store

import "github.com/mschulkind-oss/tillr/event"

// The store utility's own event kind. The event package is the mechanism and ships
// with no built-in kinds; a utility registers the kinds it appends, and this is the
// store's (design §5.2).
//
// store.report is deliberately NOT here: it is a stdout record kind, never an event,
// and registering it as appendable would claim a state transition that does not
// exist.
//
// Registration is in init because the registry's content is the set of packages
// linked into the binary, which is what the conformance suite checks: a kind that
// only registers when some caller remembers to call it is a kind the suite cannot
// see.
func init() {
	if err := event.Register("fixture.seed", "store"); err != nil {
		panic(err)
	}
}
