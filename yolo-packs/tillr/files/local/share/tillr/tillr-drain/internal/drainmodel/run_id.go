package drainmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// ConflictFixSuffix is the marker a conflict-fix run's id carries.
	ConflictFixSuffix = "-cfix"
	// MaxRunItemSlugLen bounds an item id's slug inside AttemptRunID so even a long item id
	// leaves room for -a<N>, -n<K>, -judge/-cfix, and continuation counters well under 120 chars.
	MaxRunItemSlugLen = 56
	// MaxRunIDBaseLen bounds the non-continuation stem of any run id (88 + 25 max marker = 113 < 120).
	MaxRunIDBaseLen = 88
)

var (
	// RunIDMarker parses a node's restart, fresh-session, or interruption marker.
	RunIDMarker = regexp.MustCompile(`(?:-([0-9]{3,12}))?-(failover|restart|fresh|interrupted)$`)

	RunIDContinuationKinds = []string{"failover", "restart", "fresh", "interrupted"}
)

// RunItemSlug returns itemID unchanged when short, or a deterministic prefix-plus-digest
// slug of at most MaxRunItemSlugLen characters when longer.
func RunItemSlug(itemID string) string {
	if len(itemID) <= MaxRunItemSlugLen {
		return itemID
	}
	sum := sha256.Sum256([]byte(itemID))
	digest := hex.EncodeToString(sum[:8])
	keep := MaxRunItemSlugLen - 1 - len(digest)
	prefix := strings.TrimRight(itemID[:keep], "-")
	return prefix + "-" + digest
}

// BoundRunIDBase bounds a run id stem to at most MaxRunIDBaseLen characters using a
// deterministic SHA-256 digest while preserving a trailing -judge or -cfix tag and
// remaining idempotent when called again on an already bounded stem.
func BoundRunIDBase(base string) string {
	if len(base) <= MaxRunIDBaseLen {
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
	keep := MaxRunIDBaseLen - 1 - len(digest) - len(tag)
	prefix := strings.TrimRight(base[:keep], "-")
	return prefix + "-" + digest + tag
}

func JudgeRunID(runID string) string {
	return BoundRunIDBase(runID + "-judge")
}

func ConflictFixRunID(runID string) string {
	return BoundRunIDBase(runID + ConflictFixSuffix)
}

// AttemptRunID names one item's ordinal attempt.
func AttemptRunID(itemID string, attempt int) string {
	return BoundRunIDBase("run-" + RunItemSlug(itemID) + "-a" + strconv.Itoa(attempt))
}

// AttemptFromRunID answers the attempt N of `run-<item>-a<N>` or `run-<item>-a<N>-n<K>`,
// or 0 for any other run id.
func AttemptFromRunID(itemID, runID string) int {
	rest, ok := strings.CutPrefix(runID, "run-"+itemID+"-a")
	if !ok {
		if slug := RunItemSlug(itemID); slug != itemID {
			rest, ok = strings.CutPrefix(runID, "run-"+slug+"-a")
		}
	}
	if !ok {
		return 0
	}
	digits := rest
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		digits = rest[:i]
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// SplitRunID answers the id with every trailing marker removed, how many turns those
// markers recorded, and the kind of the last one.
func SplitRunID(id string) (base string, turns int, kind string) {
	for {
		m := RunIDMarker.FindStringSubmatch(id)
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

// MarkRunID answers the run id for the next turn of the node that ran as id, produced by
// kind. Its counter advances without clamping and its stem stays bounded, so every run id
// stays strictly under 120 characters across any number of restarts.
func MarkRunID(id, kind string) string {
	base, turns, _ := SplitRunID(id)
	turns++
	return MarkRunIDTurns(BoundRunIDBase(base), kind, turns)
}

// RunIDAncestors answers the ids a run id came from, nearest first and ending with the
// node's own id, for a reader that walks back to the nearest run whose files exist.
func RunIDAncestors(id string) []string {
	base, turns, kind := SplitRunID(id)
	if turns == 0 {
		return nil
	}
	out := make([]string, 0, turns+1)
	for n := turns - 1; n >= 1; n-- {
		out = append(out, MarkRunIDTurns(base, kind, n))
	}
	out = append(out, base)
	if bounded := BoundRunIDBase(base); bounded != base {
		out = append(out, bounded)
	}
	return out
}

func MarkRunIDTurns(base, kind string, turns int) string {
	if turns <= 1 {
		return base + "-" + kind
	}
	return fmt.Sprintf("%s-%03d-%s", base, turns, kind)
}
