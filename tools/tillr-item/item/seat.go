package item

import "regexp"

var seatNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidSeatName checks only the name's shape. Backend policy declares which seats exist.
func ValidSeatName(seat string) bool { return seatNamePattern.MatchString(seat) }

const invalidSeatName = "seat %q must start with a lowercase letter and contain only lowercase letters, digits and hyphens; seats are declared in the backend policy file"

// Conventional seats used by built-in workflows.
//
// A seat is a role, not a model. The backend policy maps each seat to a backend, and that
// backend's entry names the model it runs, so an item that needs a designer asks for the
// design seat and never names a model id. Swapping which model fills a seat, or which
// backend a seat points at, is then an edit to the policy and touches no item.

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
