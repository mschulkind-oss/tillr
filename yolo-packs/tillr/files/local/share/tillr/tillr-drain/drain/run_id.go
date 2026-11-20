package drain

import (
	"fmt"
	"regexp"
	"strconv"
)

// A node's run id is its attempt's id plus one marker for what produced this run: a
// failover to another backend, a restart, a fresh session, or an interruption. The marker
// used to be appended again at every turn, so a node that failed over a hundred times had an
// id a hundred markers long, until a ref name built from it could no longer be stored. The
// marker is now one counter: the first is `-<kind>`, and the second and later are
// `-<count>-<kind>` with the count padded to three digits, so the 2nd and the 30th id of a
// node are the same length and the id ends in `-<kind>` as readers of it expect.
var runIDMarker = regexp.MustCompile(`(?:-([0-9]{3}))?-(failover|restart|fresh|interrupted)$`)

// splitRunID answers the id with every trailing marker removed, how many turns those
// markers recorded, and the kind of the last one.
func splitRunID(id string) (base string, turns int, kind string) {
	for {
		m := runIDMarker.FindStringSubmatch(id)
		if m == nil {
			return id, turns, kind
		}
		if kind == "" {
			kind = m[2]
		}
		n := 1
		if m[1] != "" {
			n, _ = strconv.Atoi(m[1])
		}
		turns += n
		id = id[:len(id)-len(m[0])]
	}
}

// markRunID answers the run id for the next turn of the node that ran as id, produced by
// kind. It never grows past the three-digit counter.
func markRunID(id, kind string) string {
	base, turns, _ := splitRunID(id)
	turns++
	if turns <= 1 {
		return base + "-" + kind
	}
	if turns > 999 {
		turns = 999
	}
	return fmt.Sprintf("%s-%03d-%s", base, turns, kind)
}

// runIDAncestors answers the ids a run id came from, nearest first and ending with the
// node's own id, for a reader that walks back to the nearest run whose files exist.
func runIDAncestors(id string) []string {
	base, turns, kind := splitRunID(id)
	if turns == 0 {
		return nil
	}
	var out []string
	for n := turns - 1; n >= 1; n-- {
		out = append(out, markRunIDTurns(base, kind, n))
	}
	return append(out, base)
}

func markRunIDTurns(base, kind string, turns int) string {
	if turns <= 1 {
		return base + "-" + kind
	}
	return fmt.Sprintf("%s-%03d-%s", base, turns, kind)
}
