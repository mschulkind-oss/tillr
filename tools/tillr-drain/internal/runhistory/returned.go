package runhistory

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
)

// EndingCheck is one failing gate check: its id, the log the gate wrote for it, and the
// digest of that log after NormalizeEvidence.
type EndingCheck struct {
	CheckID string `json:"check_id"`
	LogRef  string `json:"log_ref,omitempty"`
	Digest  string `json:"digest,omitempty"`
	// Failing is what the gate's own report named as failed in this run, read when the
	// refusal is recorded (ReadTriageBlock); empty when the report named nothing.
	Failing []FailingCheck `json:"failing,omitempty"`
}

// ReturnedCauses are the refusal steps whose ending goes back to the item's worker.
var ReturnedCauses = map[string]bool{
	RefusalGate:                     true,
	RefusalVerifyNotDone:            true,
	RefusalPlanWrong:                true,
	RefusalPlanOutput:               true,
	RefusalNoOp:                     true,
	RefusalFence:                    true,
	RefusalLand:                     true,
	drainmodel.RefusalDeliveryOrder: true,
	RefusalConflict:                 true,
	RefusalReverted:                 true,
}

// ReturnedReasons are the hold reasons retryHoldReason answers for those causes, the
// planner's spelling of the same outcomes.
var ReturnedReasons = map[string]string{
	drainmodel.ReasonGateFailed:          RefusalGate,
	RefusalVerifyNotDone:                 RefusalVerifyNotDone,
	RefusalVerifyNoVerdict:               RefusalVerifyNoVerdict,
	RefusalPlanWrong:                     RefusalPlanWrong,
	RefusalPlanOutput:                    RefusalPlanOutput,
	drainmodel.ReasonCandidateNoOp:       RefusalNoOp,
	drainmodel.ReasonBoundaryScopeEscape: RefusalFence,
	drainmodel.ReasonLandRefused:         RefusalLand,
	drainmodel.RefusalDeliveryOrder:      drainmodel.RefusalDeliveryOrder,
	drainmodel.ReasonConflictExhausted:   RefusalConflict,
	ReasonLandingReverted:                RefusalReverted,
}

var (
	evidenceTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	evidenceDuration  = regexp.MustCompile(`\b\d+(\.\d+)?\s?(ns|µs|us|ms|s|m|h)\b`)
	evidenceSHA       = regexp.MustCompile(`\b[0-9a-f]{40}\b|\b[0-9a-f]{64}\b`)
	evidenceTempDir   = regexp.MustCompile(`/(tmp|var/folders)/[^\s:'"]+`)
)

// NormalizeEvidence strips from a check's output what differs between two runs that failed
// the same way: the run's own id, timestamps, durations, full commit and digest hashes,
// and temporary directories.
func NormalizeEvidence(output, runID string) string {
	if runID != "" {
		output = strings.ReplaceAll(output, runID, "<run>")
	}
	output = evidenceTimestamp.ReplaceAllString(output, "<time>")
	output = evidenceSHA.ReplaceAllString(output, "<sha>")
	output = evidenceTempDir.ReplaceAllString(output, "<tmp>")
	output = evidenceDuration.ReplaceAllString(output, "<duration>")
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// The gate's watchdog note is a fact about the host, not about the candidate.
		if line == "" || strings.HasPrefix(line, "watchdog unavailable:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// EvidenceDigest is the digest of one normalized output.
func EvidenceDigest(output, runID string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(NormalizeEvidence(output, runID))))
}

// EndingKey is an ending's evidence key: its cause with the sorted failing check ids and
// their output digests, or the normalized refusal detail when no check is named.
func EndingKey(cause string, checks []EndingCheck, detail, runID string) string {
	if cause == "" {
		return ""
	}
	parts := []string{}
	for _, c := range checks {
		parts = append(parts, c.CheckID+"="+c.Digest)
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		parts = append(parts, "detail="+EvidenceDigest(detail, runID))
	}
	return cause + ":" + strings.Join(parts, ",")
}

// ConflictPaths answers the bare paths in the lander's conflict lines.
func ConflictPaths(reports []string) []string {
	paths := make([]string, 0, len(reports))
	seen := map[string]bool{}
	for _, report := range reports {
		path := ConflictPath(report)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

// ConflictPath answers the path in one CONFLICT line.
func ConflictPath(report string) string {
	line := strings.TrimSpace(report)
	if i := strings.Index(line, "Merge conflict in "); i >= 0 {
		return strings.TrimSpace(line[i+len("Merge conflict in "):])
	}
	if i := strings.Index(line, "): "); i >= 0 {
		rest := strings.TrimSpace(line[i+len("): "):])
		if fields := strings.Fields(rest); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// ConflictEndingKey is a squash conflict's evidence key: the conflicting paths, sorted, and
// the digest of the normalized three-way hunks the merge wrote for them.
func ConflictEndingKey(reports []string, markers, runID string) string {
	paths := ConflictPaths(reports)
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	return RefusalConflict + ":paths=" + strings.Join(paths, ",") + ",hunks=" + EvidenceDigest(markers, runID)
}

// ChainEnding is one ending of an item's chain as the repeat rule reads it.
type ChainEnding struct {
	RunID, Producer, Cause, Key string
}

// RepeatedEnding answers the earlier run whose ending latest repeats.
func RepeatedEnding(chain []ChainEnding, latest ChainEnding) (string, bool) {
	if latest.Key == "" || latest.Cause == "" {
		return "", false
	}
	for i := len(chain) - 1; i >= 0; i-- {
		earlier := chain[i]
		if earlier.RunID == latest.RunID {
			continue
		}
		if earlier.Producer == latest.Producer && earlier.Cause == latest.Cause && earlier.Key == latest.Key {
			return earlier.RunID, true
		}
	}
	return "", false
}
