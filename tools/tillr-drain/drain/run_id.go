package drain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// maxRunItemSlugLen bounds an item id's slug inside attemptRunID so even a long item id
	// leaves room for -a<N>, -n<K>, -judge/-cfix, and continuation counters well under 120 chars.
	maxRunItemSlugLen = 56
	// maxRunIDBaseLen bounds the non-continuation stem of any run id (88 + 25 max marker = 113 < 120).
	maxRunIDBaseLen = 88
)

var (
	// A node's run id carries a restart, fresh-session or interruption marker.
	// Failover markers are parsed for history only. The first marker is
	// `-<kind>`, and the second and later are
	// `-<count>-<kind>` with the count padded to at least three digits and advancing past
	// 999 without clamping or repeating words.
	runIDMarker = regexp.MustCompile(`(?:-([0-9]{3,12}))?-(failover|restart|fresh|interrupted)$`)

	runIDContinuationKinds = []string{"failover", "restart", "fresh", "interrupted"}
)

// runItemSlug returns itemID unchanged when short, or a deterministic prefix-plus-digest
// slug of at most maxRunItemSlugLen characters when longer.
func runItemSlug(itemID string) string {
	if len(itemID) <= maxRunItemSlugLen {
		return itemID
	}
	sum := sha256.Sum256([]byte(itemID))
	digest := hex.EncodeToString(sum[:8])
	keep := maxRunItemSlugLen - 1 - len(digest)
	prefix := strings.TrimRight(itemID[:keep], "-")
	return prefix + "-" + digest
}

// boundRunIDBase bounds a run id stem to at most maxRunIDBaseLen characters using a
// deterministic SHA-256 digest while preserving a trailing -judge or -cfix tag and
// remaining idempotent when called again on an already bounded stem.
func boundRunIDBase(base string) string {
	if len(base) <= maxRunIDBaseLen {
		return base
	}
	tag := ""
	for _, suffix := range []string{"-judge", ConflictFixSuffix} {
		if strings.HasSuffix(base, suffix) {
			tag = suffix
			break
		}
	}
	sum := sha256.Sum256([]byte(base))
	digest := hex.EncodeToString(sum[:8])
	keep := maxRunIDBaseLen - 1 - len(digest) - len(tag)
	prefix := strings.TrimRight(base[:keep], "-")
	return prefix + "-" + digest + tag
}

func judgeRunID(runID string) string {
	return boundRunIDBase(runID + "-judge")
}

func conflictFixRunID(runID string) string {
	return boundRunIDBase(runID + ConflictFixSuffix)
}

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
// kind. Its counter advances without clamping and its stem stays bounded, so every run id
// stays strictly under 120 characters across any number of restarts.
func markRunID(id, kind string) string {
	base, turns, _ := splitRunID(id)
	turns++
	return markRunIDTurns(boundRunIDBase(base), kind, turns)
}

// runIDAncestors answers the ids a run id came from, nearest first and ending with the
// node's own id, for a reader that walks back to the nearest run whose files exist.
func runIDAncestors(id string) []string {
	base, turns, kind := splitRunID(id)
	if turns == 0 {
		return nil
	}
	out := make([]string, 0, turns+1)
	for n := turns - 1; n >= 1; n-- {
		out = append(out, markRunIDTurns(base, kind, n))
	}
	out = append(out, base)
	if bounded := boundRunIDBase(base); bounded != base {
		out = append(out, bounded)
	}
	return out
}

func markRunIDTurns(base, kind string, turns int) string {
	if turns <= 1 {
		return base + "-" + kind
	}
	return fmt.Sprintf("%s-%03d-%s", base, turns, kind)
}
