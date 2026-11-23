package remotesync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr/checkresult"
)

const (
	// ForkDriftCheck is the check kind for INV-2 fork drift observations.
	ForkDriftCheck = "patch-branch-inv2"
	// ForkRebasePrefix identifies generated fork-rebase repair items.
	ForkRebasePrefix = "rebase-fork-"
)

// IsForkRebaseItem reports whether id names a generated fork-rebase repair.
func IsForkRebaseItem(id string) bool { return strings.HasPrefix(id, ForkRebasePrefix) }

// ForkObservation records a single INV-2 fork conflict observation.
type ForkObservation struct {
	CheckID  string `json:"check_id,omitempty"`
	Fork     string `json:"fork"`
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	Commit   string `json:"commit"`
	Delivery string `json:"delivery"`
}

var (
	forkNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	// ForkCommitPattern matches a lowercase 40-hex-character git commit SHA.
	ForkCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
)

// Valid reports whether the observation has well-formed fork, commit, ref, delivery, and kind fields.
func (o ForkObservation) Valid() bool {
	return forkNamePattern.MatchString(o.Fork) && o.Fork != "." && o.Fork != ".." &&
		ForkCommitPattern.MatchString(o.Commit) && o.Ref != "" && filepath.IsLocal(o.Delivery) && o.Delivery != "." &&
		(o.Kind == "release" || o.Kind == "head" || o.Kind == "pin" || o.Kind == "base")
}

// Pin returns the repo-relative fork pin path for the observed fork.
func (o ForkObservation) Pin() string { return "yolo-packs/fork-pins/" + o.Fork }

// PatchRef returns the patch branch ref for the observed fork.
func (o ForkObservation) PatchRef() string { return ForkPatchPrefix + o.Fork }

// Requestable reports whether the observation can trigger a fork-rebase repair request.
func (o ForkObservation) Requestable() bool {
	return o.Kind == "release" && o.Delivery == "patch/"+o.Fork
}

// Touches reports whether any of paths touches the observed fork pin or delivery path.
func (o ForkObservation) Touches(paths []string) bool {
	for _, path := range paths {
		if path == o.Pin() || (o.Delivery != "patch/"+o.Fork && readsCoverPath([]string{o.Delivery}, path)) {
			return true
		}
	}
	return false
}

type forkSpansFile struct {
	CheckSpans []struct {
		CheckID        string `json:"check_id"`
		Kind           string `json:"kind"`
		Status         string `json:"status"`
		AttributesJSON string `json:"attributes_json"`
	} `json:"check_spans"`
	SkippedChecks []struct {
		Kind    string `json:"kind"`
		Planned bool   `json:"planned"`
		Owed    bool   `json:"owed"`
	} `json:"skipped_checks"`
}

// ReadForkConflicts reads only the check's own observation. An INV-2 failure without this
// evidence is not replayable as a drift verdict, including when an older checker wrote the span.
func ReadForkConflicts(path string) (observations map[string]ForkObservation, unfinished, unansweredFork bool) {
	body, err := os.ReadFile(path)
	var file forkSpansFile
	if err != nil || json.Unmarshal(body, &file) != nil {
		return nil, false, false
	}
	observations = map[string]ForkObservation{}
	for _, skip := range file.SkippedChecks {
		unfinished = unfinished || (skip.Planned && skip.Owed)
		unansweredFork = unansweredFork || (skip.Kind == ForkDriftCheck && skip.Planned && skip.Owed)
	}
	for _, check := range file.CheckSpans {
		res := ClassifyCheck(checkresult.Observation{
			Check:  check.CheckID,
			Source: checkresult.CheckSpan,
			Word:   check.Status,
		})
		if res.Outcome == checkresult.Unknown {
			switch res.Detail {
			case checkresult.DetailNotRun:
				unfinished = true
			case checkresult.DetailError, checkresult.DetailStale:
				unfinished = true
				unansweredFork = unansweredFork || check.Kind == ForkDriftCheck
			}
		}
		if check.Kind != ForkDriftCheck || res.Outcome != checkresult.Fail {
			continue
		}
		var payload struct {
			ForkConflict ForkObservation `json:"fork_conflict"`
		}
		_ = json.Unmarshal([]byte(check.AttributesJSON), &payload)
		payload.ForkConflict.CheckID = check.CheckID
		observations[check.CheckID] = payload.ForkConflict
	}
	return observations, unfinished, unansweredFork
}

// ReplayForkConflict parses a single replay-target observation from check output text.
func ReplayForkConflict(check, text string) (ForkObservation, bool) {
	const prefix = "fork-check replay target: "
	var observed ForkObservation
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if body, ok := strings.CutPrefix(line, prefix); ok {
			if json.Unmarshal([]byte(body), &observed) != nil {
				return ForkObservation{}, false
			}
			count++
		}
	}
	observed.CheckID = check
	return observed, count == 1 && observed.Valid()
}
