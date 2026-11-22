// Package checkresult classifies observations of checks without consulting a
// checkout, starting a command, or settling a verdict.
package checkresult

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Source identifies the protocol that produced the raw result. Equal values
// from different protocols do not necessarily have the same meaning.
type Source string

const (
	CheckCommand   Source = "check-command"
	JudgingProgram Source = "judging-program"
	BinaryProgram  Source = "binary-program"
	ResultWord     Source = "result-word"
	HeavyCheckWord Source = "heavy-check-word"
	FixerCheckWord Source = "fixer-check-word"
	LegacySpan     Source = "legacy-span"
	CheckSpan      Source = "check-span"
	TriageSpan     Source = "triage-span"
	FocusedGoTest  Source = "focused-go-test"
	CachedRecord   Source = "cached-record"
	BudgetNote     Source = "budget-note"
	GoModuleBuild  Source = "go-module-build"
	DocumentRender Source = "document-render"
	LegacyCheck    Source = "legacy-check"
)

// WatchdogSilentSamples is the number of consecutive silent samples that
// turns a command observation into a stall.
const WatchdogSilentSamples = 60

// StoredRecord is one remembered pass or fail record for a fingerprint.
type StoredRecord struct {
	At     string `json:"at"`
	LogRef string `json:"log_ref,omitempty"`
}

// DisabledMarker is the persisted disabled-check marker (`disabled/<hash>.json`).
type DisabledMarker struct {
	CheckID     string `json:"check_id,omitempty"`
	Episode     string `json:"episode,omitempty"`
	Fingerprint string `json:"fingerprint"`
	PassAt      string `json:"pass_at"`
	PassLogRef  string `json:"pass_log_ref"`
	FailAt      string `json:"fail_at"`
	FailLogRef  string `json:"fail_log_ref"`
	RepairItem  string `json:"repair_item,omitempty"`
}

// FormatEpisode builds `<fingerprint>|<pass_at>|<fail_at>`.
func FormatEpisode(fingerprint, passAt, failAt string) string {
	return fingerprint + "|" + passAt + "|" + failAt
}

// ParseEpisode validates `<fingerprint>|<pass_at>|<fail_at>` where both
// timestamps are RFC3339Nano UTC (`Z`).
func ParseEpisode(episode string) (fingerprint, passAt, failAt string, err error) {
	parts := strings.Split(episode, "|")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid episode %q: expected <fingerprint>|<pass_at>|<fail_at>", episode)
	}
	fingerprint, passAt, failAt = parts[0], parts[1], parts[2]
	if fingerprint == "" || strings.TrimSpace(fingerprint) != fingerprint || strings.ContainsAny(fingerprint, "/\\\x00") {
		return "", "", "", fmt.Errorf("invalid episode fingerprint %q", fingerprint)
	}
	if !IsRFC3339NanoUTC(passAt) {
		return "", "", "", fmt.Errorf("invalid episode pass_at %q: must be RFC3339Nano UTC", passAt)
	}
	if !IsRFC3339NanoUTC(failAt) {
		return "", "", "", fmt.Errorf("invalid episode fail_at %q: must be RFC3339Nano UTC", failAt)
	}
	return fingerprint, passAt, failAt, nil
}

// IsRFC3339NanoUTC reports whether value is an RFC3339Nano timestamp in UTC (`Z`).
func IsRFC3339NanoUTC(value string) bool {
	if !strings.HasSuffix(value, "Z") {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

// Observation is one check observation. Rules 1 to 3 consult Proving, Marker,
// StoredPass, StoredFail, Fingerprint and ResolvedEpisodes; rule 4 classifies
// the raw result from Source and its protocol-specific fields (or answers
// Unknown / DetailUnobserved when no raw result is supplied).
type Observation struct {
	Check            string          `json:"check,omitempty"`
	CheckID          string          `json:"check_id,omitempty"`
	Commit           string          `json:"commit,omitempty"`
	Worktree         string          `json:"worktree,omitempty"`
	Proving          bool            `json:"proving,omitempty"`
	Fingerprint      string          `json:"fingerprint,omitempty"`
	ObservedAt       string          `json:"observed_at,omitempty"`
	LogRef           string          `json:"log_ref,omitempty"`
	Marker           *DisabledMarker `json:"marker,omitempty"`
	StoredPass       *StoredRecord   `json:"stored_pass,omitempty"`
	StoredFail       *StoredRecord   `json:"stored_fail,omitempty"`
	ResolvedEpisodes []string        `json:"resolved_episodes,omitempty"`
	EnabledAt        string          `json:"enabled_at,omitempty"`
	Source           Source          `json:"source,omitempty"`
	ExitCode         *int            `json:"exit_code,omitempty"`
	ProcessError     bool            `json:"process_error,omitempty"`
	SilentSamples    int             `json:"silent_samples,omitempty"`
	Word             string          `json:"word,omitempty"`
	Detail           string          `json:"detail,omitempty"`
	ReportRead       bool            `json:"report_read,omitempty"` // DocumentRender only.
	HasFinding       bool            `json:"has_finding,omitempty"` // DocumentRender only.
	PackageAbsent    bool            `json:"package_absent,omitempty"`
	Passed           bool            `json:"passed,omitempty"`
	Failed           bool            `json:"failed,omitempty"`
	Skipped          bool            `json:"skipped,omitempty"`
	Legacy           *LegacyFacts    `json:"legacy,omitempty"`
}

// WorktreeEnabled reports whether the observation selects worktree declarations
// rather than a committed revision.
func (o Observation) WorktreeEnabled() bool {
	return o.Worktree != "" && o.Worktree != "false"
}

// UnmarshalJSON accepts either a JSON boolean (`true` / `false`) or a path string
// for `worktree` while keeping unknown-field rejection intact.
func (o *Observation) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	type plainObservation Observation
	aux := struct {
		*plainObservation
		Worktree json.RawMessage `json:"worktree"`
	}{
		plainObservation: (*plainObservation)(o),
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("unexpected trailing JSON in observation")
	}
	if len(aux.Worktree) > 0 {
		switch string(bytes.TrimSpace(aux.Worktree)) {
		case "true":
			o.Worktree = "true"
		case "false", "null":
			o.Worktree = ""
		default:
			var s string
			if err := json.Unmarshal(aux.Worktree, &s); err != nil {
				return err
			}
			o.Worktree = s
		}
	}
	return nil
}

// LegacyKindGroup describes what evidence a kind needs for a nonzero exit to
// be a verdict. Unlisted groups use the verdict rule.
type LegacyKindGroup string

const (
	LegacyVerdict   LegacyKindGroup = "verdict"
	LegacyUnable    LegacyKindGroup = "unable"
	LegacyNamed     LegacyKindGroup = "named"
	LegacyInvariant LegacyKindGroup = "invariant"
)

// LegacyFacts are observations about the plan and output of the legacy gate.
// The caller supplies facts, not a status inferred from them and the exit code.
type LegacyFacts struct {
	Group                 LegacyKindGroup `json:"group,omitempty"`
	NotRun                bool            `json:"not_run,omitempty"`
	Cached                bool            `json:"cached,omitempty"`
	Quarantined           bool            `json:"quarantined,omitempty"`
	SentinelSkip          bool            `json:"sentinel_skip,omitempty"`
	Stale                 bool            `json:"stale,omitempty"`
	ReservesExit4         bool            `json:"reserves_exit_4,omitempty"`
	FastTree              bool            `json:"fast_tree,omitempty"`
	PreflightRefused      bool            `json:"preflight_refused,omitempty"`
	LinkedWorktreeRefusal bool            `json:"linked_worktree_refusal,omitempty"`
	NamedCase             bool            `json:"named_case,omitempty"`
	NamedInvariant        bool            `json:"named_invariant,omitempty"`
}

// Outcome is the coarse classification. Detail distinguishes cases such as a
// stall from an error; RawWord and RawDetail preserve the producer's evidence.
type Outcome string

const (
	Pass        Outcome = "pass"
	Fail        Outcome = "fail"
	Unknown     Outcome = "unknown"
	Unavailable Outcome = "unavailable"
	Disabled    Outcome = "disabled"
)

const (
	DetailPassed             = "passed"
	DetailCached             = "cached"
	DetailRemembered         = "remembered"
	DetailFailed             = "failed"
	DetailError              = "error"
	DetailStalled            = "stalled"
	DetailUnavailable        = "unavailable"
	DetailSkipped            = "skipped"
	DetailDropped            = "dropped"
	DetailAbsent             = "absent"
	DetailQuarantined        = "quarantined"
	DetailDisabled           = "disabled"
	DetailUnobserved         = "unobserved"
	DetailOverBudget         = "over-budget"
	DetailNotRun             = "not-run"
	DetailStale              = "stale"
	DetailUnrecognized       = "unrecognized"
	DetailNotRetained        = "not-retained"
	DetailPackageAbsent      = "package not present at this commit"
	DetailFocusedTestSkipped = "test or enclosing test skipped at this commit"
	DetailFocusedTestAbsent  = "test not present at this commit"
)

type Result struct {
	Outcome     Outcome `json:"outcome"`
	Detail      string  `json:"detail"`
	RawWord     string  `json:"raw_word,omitempty"`
	RawDetail   string  `json:"raw_detail,omitempty"`
	CheckID     string  `json:"check_id,omitempty"`
	Episode     string  `json:"episode,omitempty"`
	Fingerprint string  `json:"fingerprint,omitempty"`
	PassAt      string  `json:"pass_at,omitempty"`
	PassLogRef  string  `json:"pass_log_ref,omitempty"`
	FailAt      string  `json:"fail_at,omitempty"`
	FailLogRef  string  `json:"fail_log_ref,omitempty"`
	RepairItem  string  `json:"repair_item,omitempty"`
}

// MarshalJSON preserves the minimal wire shape on ordinary classifications while
// always emitting `pass_log_ref` and `fail_log_ref` (`""` when unrecorded) on
// evidence-backed `disabled` classifications (D-GR41).
func (r Result) MarshalJSON() ([]byte, error) {
	if r.Outcome == Disabled && (r.Episode != "" || r.Fingerprint != "") {
		return json.Marshal(struct {
			Outcome     Outcome `json:"outcome"`
			Detail      string  `json:"detail"`
			RawWord     string  `json:"raw_word,omitempty"`
			RawDetail   string  `json:"raw_detail,omitempty"`
			CheckID     string  `json:"check_id,omitempty"`
			Episode     string  `json:"episode,omitempty"`
			Fingerprint string  `json:"fingerprint"`
			PassAt      string  `json:"pass_at"`
			PassLogRef  string  `json:"pass_log_ref"`
			FailAt      string  `json:"fail_at"`
			FailLogRef  string  `json:"fail_log_ref"`
			RepairItem  string  `json:"repair_item,omitempty"`
		}{
			Outcome:     r.Outcome,
			Detail:      r.Detail,
			RawWord:     r.RawWord,
			RawDetail:   r.RawDetail,
			CheckID:     r.CheckID,
			Episode:     r.Episode,
			Fingerprint: r.Fingerprint,
			PassAt:      r.PassAt,
			PassLogRef:  r.PassLogRef,
			FailAt:      r.FailAt,
			FailLogRef:  r.FailLogRef,
			RepairItem:  r.RepairItem,
		})
	}
	type plainResult Result
	return json.Marshal(plainResult(r))
}

// Classify applies rules 1 to 4 of D-GR23 / D-GR41 in order:
//  1. proving runs skip rules 2 and 3 and classify the raw result by rule 4;
//  2. an active disabled marker answers Disabled with the marker's evidence;
//  3. a contradicted fingerprint not matching a resolved episode answers Disabled;
//  4. otherwise rule 4 classifies the raw result, or answers Unknown / unobserved
//     when no raw result was supplied.
func Classify(o Observation) Result {
	if !o.Proving {
		if o.Marker != nil {
			ep := o.Marker.Episode
			if ep == "" && o.Marker.Fingerprint != "" && o.Marker.PassAt != "" && o.Marker.FailAt != "" {
				ep = FormatEpisode(o.Marker.Fingerprint, o.Marker.PassAt, o.Marker.FailAt)
			}
			checkID := o.CheckID
			if checkID == "" {
				checkID = o.Marker.CheckID
			}
			return Result{
				Outcome:     Disabled,
				Detail:      DetailDisabled,
				RawWord:     o.Word,
				RawDetail:   o.Detail,
				CheckID:     checkID,
				Episode:     ep,
				Fingerprint: o.Marker.Fingerprint,
				PassAt:      o.Marker.PassAt,
				PassLogRef:  o.Marker.PassLogRef,
				FailAt:      o.Marker.FailAt,
				FailLogRef:  o.Marker.FailLogRef,
				RepairItem:  o.Marker.RepairItem,
			}
		}
		if res, ok := classifyContradiction(o); ok {
			return res
		}
	}
	return classifyRule4(o)
}

func (o Observation) hasRawResult() bool {
	return o.Source != "" ||
		o.ExitCode != nil ||
		o.ProcessError ||
		o.SilentSamples != 0 ||
		o.Word != "" ||
		o.Detail != "" ||
		o.ReportRead ||
		o.HasFinding ||
		o.PackageAbsent ||
		o.Passed ||
		o.Failed ||
		o.Skipped ||
		o.Legacy != nil
}

func classifyRule4(o Observation) Result {
	if !o.hasRawResult() {
		return Result{
			Outcome: Unknown,
			Detail:  DetailUnobserved,
			CheckID: o.CheckID,
		}
	}
	outcome, detail := classifyRaw(o)
	return Result{
		Outcome:   outcome,
		Detail:    detail,
		RawWord:   o.Word,
		RawDetail: o.Detail,
		CheckID:   o.CheckID,
	}
}

func classifyContradiction(o Observation) (Result, bool) {
	if o.Fingerprint == "" {
		return Result{}, false
	}
	makeDisabled := func(pass, fail StoredRecord) (Result, bool) {
		var ep string
		if pass.At != "" && fail.At != "" {
			ep = FormatEpisode(o.Fingerprint, pass.At, fail.At)
			if slices.Contains(o.ResolvedEpisodes, ep) {
				return Result{}, false
			}
		}
		return Result{
			Outcome:     Disabled,
			Detail:      DetailDisabled,
			RawWord:     o.Word,
			RawDetail:   o.Detail,
			CheckID:     o.CheckID,
			Episode:     ep,
			Fingerprint: o.Fingerprint,
			PassAt:      pass.At,
			PassLogRef:  pass.LogRef,
			FailAt:      fail.At,
			FailLogRef:  fail.LogRef,
		}, true
	}
	if o.StoredPass != nil && o.StoredFail != nil {
		if res, ok := makeDisabled(*o.StoredPass, *o.StoredFail); ok {
			return res, true
		}
	}
	if !o.hasRawResult() || isStalePreRepairCache(o) {
		return Result{}, false
	}
	rawOutcome, _ := classifyRaw(o)
	switch {
	case rawOutcome == Fail && o.StoredPass != nil:
		return makeDisabled(*o.StoredPass, StoredRecord{At: o.ObservedAt, LogRef: o.LogRef})
	case rawOutcome == Pass && o.StoredFail != nil:
		return makeDisabled(StoredRecord{At: o.ObservedAt, LogRef: o.LogRef}, *o.StoredFail)
	default:
		return Result{}, false
	}
}

func isStalePreRepairCache(o Observation) bool {
	if o.Source != CachedRecord {
		return false
	}
	prefix := o.Fingerprint + "|"
	fingerprintWasRepaired := false
	for _, ep := range o.ResolvedEpisodes {
		if strings.HasPrefix(ep, prefix) {
			fingerprintWasRepaired = true
			if _, passAt, failAt, err := ParseEpisode(ep); err == nil {
				if o.ObservedAt == passAt || o.ObservedAt == failAt {
					return true
				}
			}
		}
	}
	if !fingerprintWasRepaired {
		return false
	}
	if o.ObservedAt == "" {
		return true
	}
	if o.EnabledAt != "" {
		obsTime, obsErr := time.Parse(time.RFC3339Nano, o.ObservedAt)
		enTime, enErr := time.Parse(time.RFC3339Nano, o.EnabledAt)
		if obsErr == nil && enErr == nil && !obsTime.After(enTime) {
			return true
		}
	}
	return false
}

func classifyRaw(o Observation) (Outcome, string) {
	switch o.Source {
	case CheckCommand:
		if o.SilentSamples >= WatchdogSilentSamples {
			return Unknown, "stalled"
		}
		if !o.ProcessError && o.ExitCode != nil {
			switch code := *o.ExitCode; {
			case code == 0:
				return Pass, "passed"
			case code > 0 && code != 4:
				return Fail, "failed"
			}
		}
	case JudgingProgram:
		if !o.ProcessError && o.ExitCode != nil {
			switch *o.ExitCode {
			case 0:
				return Pass, "passed"
			case 1:
				return Fail, "failed"
			case 6:
				return Unknown, "stalled"
			case 77:
				return Unavailable, "dropped"
			}
		}
	case BinaryProgram:
		if !o.ProcessError && o.ExitCode != nil {
			switch *o.ExitCode {
			case 0:
				return Pass, DetailPassed
			case 1:
				return Fail, DetailFailed
			}
		}
	case CachedRecord:
		switch o.Word {
		case "pass":
			return Pass, "cached"
		case "fail":
			return Fail, "failed"
		}
	case ResultWord:
		return classifyWord(o.Word)
	case HeavyCheckWord:
		return classifyHeavyCheckWord(o.Word)
	case FixerCheckWord:
		return classifyFixerCheckWord(o.Word)
	case LegacySpan:
		return classifyLegacySpan(o.Word)
	case CheckSpan:
		return classifyCheckSpan(o.Word)
	case TriageSpan:
		return classifyTriageSpan(o.Word)
	case FocusedGoTest:
		return classifyFocusedGoTest(o)
	case BudgetNote:
		if o.Word == DetailOverBudget {
			return Unknown, DetailOverBudget
		}
		return classifyWord(o.Word)
	case GoModuleBuild:
		if o.ExitCode != nil {
			if *o.ExitCode == 0 {
				return Pass, "passed"
			}
			return Fail, "failed"
		}
	case DocumentRender:
		if o.ExitCode != nil {
			switch {
			case *o.ExitCode == 0 && o.ReportRead:
				return Pass, "passed"
			case *o.ExitCode == 1 && o.HasFinding:
				return Fail, "failed"
			}
		}
	case LegacyCheck:
		return classifyLegacy(o.ExitCode, o.Legacy)
	}
	return Unknown, "error"
}

// classifyLegacy keeps the order of check_status and Check.conflict in
// tools/fork-check.py. A stale candidate and a watchdog stall are distinct.
func classifyLegacy(exit *int, facts *LegacyFacts) (Outcome, string) {
	var f LegacyFacts
	if facts != nil {
		f = *facts
	}
	switch {
	case f.NotRun:
		return Unknown, "not-run"
	case f.Cached:
		return Pass, "cached"
	case f.Quarantined:
		return Unavailable, "quarantined"
	case f.SentinelSkip:
		return Unavailable, "skipped"
	case f.Stale:
		return Unknown, "stale"
	case exit == nil:
		return Unknown, "error"
	case *exit == -1000: // CHECK_STALLED_RC from the legacy gate.
		return Unknown, "stalled"
	case *exit == 0:
		return Pass, "passed"
	}
	if legacyConflict(*exit, f) {
		return Fail, "failed"
	}
	return Unknown, "error"
}

func legacyConflict(exit int, f LegacyFacts) bool {
	switch exit {
	case 124, 125, 126, 127, 137: // COULD_NOT_ANSWER_CODES
		return false
	}
	switch {
	case exit >= -64 && exit < 0: // Popen encodes an outside signal as -N.
		return false
	case exit == 4 && f.ReservesExit4:
		return false
	case f.PreflightRefused && (f.FastTree || f.Group == LegacyUnable):
		return false
	case f.Group == LegacyUnable && f.LinkedWorktreeRefusal:
		return false
	case f.Group == LegacyNamed:
		return f.NamedCase
	case f.Group == LegacyInvariant:
		return f.NamedInvariant
	default:
		return true
	}
}

func classifyWord(word string) (Outcome, string) {
	switch word {
	case "pass", "PASS", "ok":
		return Pass, "passed"
	case "CACHED":
		return Pass, "cached"
	case "remembered":
		return Pass, "remembered"
	case "fail", "FAIL":
		return Fail, "failed"
	case "stalled", "STALLED":
		return Unknown, "stalled"
	case "skip", "skipped: no changed path is read":
		return Unavailable, "skipped"
	case "QUARANTINED":
		return Unavailable, "quarantined"
	case "unavailable", "dropped", "absent":
		return Unavailable, word
	case "disabled":
		return Disabled, "disabled"
	default:
		if strings.HasPrefix(word, "skipped:") {
			return Unavailable, DetailSkipped
		}
		return Unknown, "error"
	}
}

func classifyHeavyCheckWord(word string) (Outcome, string) {
	switch word {
	case "pass":
		return Pass, DetailPassed
	case "fail":
		return Fail, DetailFailed
	case "unavailable":
		return Unavailable, DetailUnavailable
	case "stalled":
		return Unknown, DetailStalled
	case "error":
		return Unknown, DetailError
	default:
		return Unknown, DetailUnrecognized
	}
}

func classifyFixerCheckWord(word string) (Outcome, string) {
	switch word {
	case "pass":
		return Pass, DetailPassed
	case "fail":
		return Fail, DetailFailed
	case "dropped":
		return Unavailable, DetailDropped
	case "absent":
		return Unavailable, DetailAbsent
	default:
		return Unknown, DetailError
	}
}

func classifyLegacySpan(word string) (Outcome, string) {
	switch word {
	case "PASS", "ok":
		return Pass, DetailPassed
	case "CACHED":
		return Pass, DetailCached
	case "skip":
		return Unavailable, DetailSkipped
	case "QUARANTINED":
		return Unavailable, DetailQuarantined
	case "FAIL":
		return Fail, DetailFailed
	case "STALLED":
		return Unknown, DetailStalled
	default:
		return Unknown, DetailError
	}
}

func classifyCheckSpan(word string) (Outcome, string) {
	switch word {
	case "PASS", "ok":
		return Pass, DetailPassed
	case "CACHED":
		return Pass, DetailCached
	case "skip":
		return Unavailable, DetailSkipped
	case "QUARANTINED":
		return Unavailable, DetailQuarantined
	case "FAIL":
		return Fail, DetailFailed
	case "STALLED":
		return Unknown, DetailStalled
	case "not-run":
		return Unknown, DetailNotRun
	case "STALE":
		return Unknown, DetailStale
	case "ERR":
		return Unknown, DetailError
	default:
		return Unknown, DetailUnrecognized
	}
}

func classifyTriageSpan(word string) (Outcome, string) {
	switch strings.ToUpper(word) {
	case "FAIL":
		return Fail, DetailFailed
	case "STALLED":
		return Unknown, DetailStalled
	case "ERROR":
		return Unknown, DetailError
	case "PASS", "OK", "CACHED":
		return Pass, DetailPassed
	case "SKIP", "QUARANTINED":
		return Unavailable, DetailSkipped
	default:
		return Unknown, DetailNotRetained
	}
}

func classifyFocusedGoTest(o Observation) (Outcome, string) {
	if o.PackageAbsent {
		return Pass, DetailPackageAbsent
	}
	if !o.ProcessError && o.ExitCode != nil {
		switch {
		case *o.ExitCode == 0 && !o.Failed:
			switch {
			case o.Passed:
				return Pass, DetailPassed
			case o.Skipped:
				return Pass, DetailFocusedTestSkipped
			default:
				return Pass, DetailFocusedTestAbsent
			}
		case *o.ExitCode == 1 && o.Failed:
			return Fail, DetailFailed
		}
	}
	return Unknown, DetailError
}
