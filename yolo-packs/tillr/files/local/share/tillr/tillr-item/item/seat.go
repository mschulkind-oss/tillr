package item

import (
	"regexp"
	"strings"
)

var validSeatName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString

const invalidSeatName = "seat %q must start with a lowercase letter and contain only lowercase letters, digits and hyphens; seats are declared in the backend policy file"

// The seat vocabulary: the roles of the work an item can ask for.
//
// A seat is a role, not a model. The backend policy maps each seat to a backend, and that
// backend's entry names the model it runs, so an item that needs a designer asks for the
// design seat and never names a model id. Swapping which model fills a seat, or which
// backend a seat points at, is then an edit to the policy and touches no item.
//
// The vocabulary is closed here rather than in the policy because the seat is the item's
// own word: a policy that mapped a seat the item model does not define would be a typo
// that silently routed nothing, and a policy that leaves one of these unmapped is refused
// at plan time instead of falling back to the tick's default backend.
const (
	// SeatDesign is document and design work: reading the tree, settling a design, writing
	// the document a later build works from.
	SeatDesign = "design"
	// SeatBuild is implementation work: the change, its tests, and the verification the
	// item's brief names.
	SeatBuild = "build"
	// SeatReview is judgment of a candidate: reading a diff against the work it claims.
	SeatReview = "review"
	// SeatScout is read-only reconnaissance: finding the files, symbols and call sites a
	// brief should carry, without editing anything.
	SeatScout = "scout"
)

// seats is the vocabulary in the order a refusal prints it: the order the item brief names
// the four roles, not alphabetical, so the message reads like the sentence that defined
// them.
var seats = []string{SeatDesign, SeatBuild, SeatReview, SeatScout}

// KnownSeat reports whether seat is one of the four roles the item model defines. An empty
// seat is not a seat: it is an item that asked for none, and the planner then uses the
// tick's default backend.
func KnownSeat(seat string) bool {
	for _, known := range seats {
		if known == seat {
			return true
		}
	}
	return false
}

// SeatList answers the vocabulary as a refusal prints it: "design, build, review, scout".
func SeatList() string {
	return strings.Join(seats, ", ")
}
