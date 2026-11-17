package item

import (
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// MoveReceipt records why a finished state is entered or left and who decided.
// ResolvesVia points to the evidence for entering a finished state.
type MoveReceipt struct {
	Reason      string
	By          string
	ResolvesVia string
	// Flags preserves explicit empty CLI arguments for flag-not-stored refusals.
	Flags []string
}

func itemDefinition(r *store.Reader) (definition.Definition, error) {
	rec, err := definition.ForReader(r)
	if err != nil {
		return definition.Definition{}, Internalf("reading item lifecycle definition: %v", err)
	}
	return rec.Parsed, nil
}

func checkState(d definition.Definition, itemID, state string) error {
	if frozenStates[state] {
		return Rejectionf("state-frozen", itemID, "%q is a frozen legacy state no transition may write", state)
	}
	if d.HasState(state) {
		return nil
	}
	return Usagef("%q is not a state in definition %q: want one of %s", state, d.Name, strings.Join(d.States, ", "))
}

// New items and same-state writes do not move between states. Legacy rows can be
// migrated out of their frozen vocabulary, but no write can move back into it.
func checkMove(d definition.Definition, id, from, to string, receipt MoveReceipt) error {
	if from == "" || from == to || frozenStates[from] {
		return nil
	}
	if !d.Allows(from, to) {
		// Keep the established Git-landing terminal refusal (including malformed targets).
		// Its policy still comes from the declaration's absence of outgoing moves.
		if d.Name == definition.GitLandingName && d.IsFinished(from) && from != d.Closed {
			return terminalRejection(id, from)
		}
		return Rejectionf("transition-not-allowed", id, "item %q cannot move from %q to %q: definition %q does not list that transition", id, from, to, d.Name)
	}
	if d.IsFinished(from) {
		if strings.TrimSpace(receipt.Reason) == "" {
			return Rejectionf("transition-reason-missing", id, "leaving finished state %q needs --reason", from)
		}
		if strings.TrimSpace(receipt.By) == "" {
			return Usagef("leaving finished state %q needs --by <who>", from)
		}
	}
	return nil
}

// Closed-role moves use the dedicated commands, which preserve the saved state.
func checkClosedTransition(d definition.Definition, id, from, to string, apply bool) error {
	if from == d.Closed && (!apply || to != from) {
		return Rejectionf("closed-use-reopen", id, "item %q is closed; use tillr item reopen --item %s --reason <text> --by <who>", id, id)
	}
	if to == d.Closed && from != d.Closed {
		return Rejectionf("closed-use-close", id, "use tillr item close --item %s --reason <text> --by <who>", id)
	}
	return nil
}
