// Package checkresult classifies observations of checks without consulting a
// checkout, starting a command, or settling a verdict.
package checkresult

// Source identifies the protocol that produced the raw result. Equal values
// from different protocols do not necessarily have the same meaning.
type Source string

const (
	CheckCommand   Source = "check-command"
	JudgingProgram Source = "judging-program"
	ResultWord     Source = "result-word"
	CachedRecord   Source = "cached-record"
	GoModuleBuild  Source = "go-module-build"
	DocumentRender Source = "document-render"
	LegacyCheck    Source = "legacy-check"
)

// WatchdogSilentSamples is the number of consecutive silent samples that
// turns a command observation into a stall.
const WatchdogSilentSamples = 60

// Observation is one raw result. Check and the judged Commit or Worktree are
// context for the caller; rule 4 uses only the raw result and its source.
// A nil ExitCode means the process supplied no exit code. ProcessError is for
// infrastructure errors (such as a failure to wait), not a nonzero exit.
type Observation struct {
	Check         string       `json:"check,omitempty"`
	Commit        string       `json:"commit,omitempty"`
	Worktree      string       `json:"worktree,omitempty"`
	Source        Source       `json:"source"`
	ExitCode      *int         `json:"exit_code,omitempty"`
	ProcessError  bool         `json:"process_error,omitempty"`
	SilentSamples int          `json:"silent_samples,omitempty"`
	Word          string       `json:"word,omitempty"`
	Detail        string       `json:"detail,omitempty"`
	ReportRead    bool         `json:"report_read,omitempty"` // DocumentRender only.
	HasFinding    bool         `json:"has_finding,omitempty"` // DocumentRender only.
	Legacy        *LegacyFacts `json:"legacy,omitempty"`
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

type Result struct {
	Outcome   Outcome `json:"outcome"`
	Detail    string  `json:"detail"`
	RawWord   string  `json:"raw_word,omitempty"`
	RawDetail string  `json:"raw_detail,omitempty"`
}

// Classify applies only the raw-result rule (D-GR23 rule 4). It neither infers
// disabling from stored evidence nor interprets inherited-failure settlements.
func Classify(o Observation) Result {
	outcome, detail := classifyRaw(o)
	return Result{Outcome: outcome, Detail: detail, RawWord: o.Word, RawDetail: o.Detail}
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
	case CachedRecord:
		switch o.Word {
		case "pass":
			return Pass, "cached"
		case "fail":
			return Fail, "failed"
		}
	case ResultWord:
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
		return Unknown, "error"
	}
}
