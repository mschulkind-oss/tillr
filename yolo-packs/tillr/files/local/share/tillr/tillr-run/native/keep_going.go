package native

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-run/claude"
	"github.com/mschulkind-oss/tillr-run/extract"
)

const (
	defaultMaxKeepGoingNudges  = 3
	defaultRepeatWarnThreshold = 4
	defaultRepeatHardCap       = 6
)

func extractAssistantText(blocks []ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == BlockText && blk.Text != "" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func footerResult(text string) string {
	if r := extract.FooterResult(text); r != "" {
		return r
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if strings.TrimSpace(lines[len(lines)-1]) == "RESULT: no_change" {
		return "no_change"
	}
	return ""
}

// keepGoingNudge decides whether an assistant end_turn response with no tool
// calls should be nudged back into work rather than settling. At most maxNudges
// (default 3) nudges are issued before allowing the run to settle.
func keepGoingNudge(ec endingConfig, stopReason, text string, nudges, maxNudges int) (string, bool) {
	if !ec.HasEndingContract {
		return "", false
	}
	if stopReason != StopEndTurn && stopReason != "" {
		return "", false
	}
	if maxNudges <= 0 {
		maxNudges = defaultMaxKeepGoingNudges
	}
	if nudges >= maxNudges {
		return "", false
	}

	footer := footerResult(text)

	if ec.CallSuspends() {
		if ec.ReadOnly {
			if footer == "done" || footer == "blocked" || footer == "trunk-broken" || (ec.AllowNoChange && footer == "no_change") {
				return "", false
			}
			msg := "You ended without a RESULT footer. Finish the read-only task and end with `RESULT: done`, or call `obstacle` when an obstacle holds."
			if ec.AllowNoChange {
				msg = "You ended without a RESULT footer. Finish the read-only task and end with `RESULT: done`, call `no_change` when reproductions show nothing in the repository needs changing, or call `obstacle` when an obstacle holds."
			}
			return msg, true
		}

		// Landing write run in call-suspends mode.
		var (
			head  string
			dirty []string
		)
		if ec.WorkDir != "" {
			head, _ = claude.HeadOf(ec.WorkDir)
			dirty, _ = claude.DirtyPaths(ec.WorkDir)
		}
		if ec.AllowUnchangedPiece && footer == "done" && head != "" && head == ec.BaseSHA && len(dirty) == 0 {
			return "", false
		}

		var missing []string
		if head != "" && head == ec.BaseSHA && !ec.AllowUnchangedPiece {
			shortBase := ec.BaseSHA
			if len(shortBase) > 12 {
				shortBase = shortBase[:12]
			}
			missing = append(missing, fmt.Sprintf("there is no commit (HEAD is still base %s)", shortBase))
		}
		if len(dirty) > 0 {
			shown := dirty
			suffix := ""
			if len(shown) > 20 {
				suffix = fmt.Sprintf("\n(and %d more)", len(shown)-20)
				shown = shown[:20]
			}
			missing = append(missing, "these paths are uncommitted:\n"+strings.Join(shown, "\n")+suffix)
		}

		var baseMsg string
		if ec.AllowNoChange {
			baseMsg = "You ended without calling `land`, `obstacle`, or `no_change`. Call `land` when your commit is ready, call `no_change` when reproductions show nothing in the repository needs changing, or call `obstacle` when an obstacle holds."
		} else {
			baseMsg = "You ended without calling `land` or `obstacle`. Call `land` when your commit is ready, or call `obstacle` when an obstacle holds."
		}
		if ec.AllowUnchangedPiece {
			baseMsg += " If the piece already holds at its unchanged, clean base, end with RESULT: done instead."
		}
		if len(missing) > 0 {
			return fmt.Sprintf("The run is not finished: %s.\n\n%s", strings.Join(missing, "; "), baseMsg), true
		}
		return baseMsg, true
	}

	// Footer mode.
	if !ec.NonLanding && !ec.ReadOnly && ec.BaseSHA != "" && ec.WorkDir != "" {
		head, _ := claude.HeadOf(ec.WorkDir)
		dirty, _ := claude.DirtyPaths(ec.WorkDir)
		committed := head != "" && head != ec.BaseSHA
		clean := len(dirty) == 0
		var unformatted []string
		if committed && clean {
			unformatted = claude.UnformattedPaths(ec.WorkDir)
		}

		if ec.AllowUnchangedPiece && footer == "done" && head == ec.BaseSHA && clean {
			return "", false
		}
		if ec.AllowNoChange && footer == "no_change" && head == ec.BaseSHA && clean {
			return "", false
		}
		if footer == "blocked" || footer == "trunk-broken" {
			return "", false
		}
		if committed && clean && len(unformatted) == 0 && footer != "" && footer != "failed" && footer != "no_change" {
			return "", false
		}

		var missing []string
		needsCommit := !ec.AllowUnchangedPiece && !(ec.AllowNoChange && footer == "no_change")
		if !committed && needsCommit {
			shortBase := ec.BaseSHA
			if len(shortBase) > 12 {
				shortBase = shortBase[:12]
			}
			missing = append(missing, fmt.Sprintf("there is no commit: HEAD is still the base %s", shortBase))
		}
		if !clean {
			shown := dirty
			suffix := ""
			if len(shown) > 20 {
				suffix = fmt.Sprintf("\n(and %d more)", len(shown)-20)
				shown = shown[:20]
			}
			missing = append(missing, "these paths are uncommitted:\n"+strings.Join(shown, "\n")+suffix)
		}
		if committed && clean && len(unformatted) > 0 {
			shown := unformatted
			suffix := ""
			if len(shown) > 20 {
				suffix = fmt.Sprintf("\n(and %d more)", len(shown)-20)
				shown = shown[:20]
			}
			missing = append(missing, "these files need formatting (run `just format`, plus `just pack-sync` if `tools/tillr*` changed, and amend the commit):\n"+strings.Join(shown, "\n")+suffix)
		}
		switch footer {
		case "":
			missing = append(missing, "your last message has no RESULT footer")
		case "done":
			if !committed && needsCommit {
				missing = append(missing, "it says RESULT: done, but nothing is committed")
			}
		case "failed":
			missing = append(missing, "RESULT: failed is not an ending")
		case "no_change":
			if !ec.AllowNoChange {
				missing = append(missing, "RESULT: no_change is not allowed on this stage")
			} else if committed {
				missing = append(missing, "a commit exists at HEAD: end with RESULT: done to deliver the commit, not RESULT: no_change")
			}
		}

		return fmt.Sprintf(
			"The run is not finished: %s.\n\nDo not write another report until the commit exists and its verification has run. Your next message must move the work forward, commit the change, and end with `RESULT: done` (or name a complete obstacle with `RESULT: blocked`).",
			strings.Join(missing, "; "),
		), true
	}

	// Read-only or non-landing footer mode.
	if footer == "done" || footer == "blocked" || footer == "trunk-broken" || (ec.AllowNoChange && footer == "no_change") {
		return "", false
	}
	if footer == "failed" {
		return "The run is not finished: RESULT: failed is not an ending. Finish the task and end with `RESULT: done`, or name a complete obstacle with `RESULT: blocked`.", true
	}
	return "The run is not finished: your last message has no RESULT footer. Finish the task and end with `RESULT: done`, or name a complete obstacle with `RESULT: blocked`.", true
}

// canonicalToolArgs normalizes tool call JSON arguments with sorted object keys
// and no insignificant whitespace so identical calls hash identically.
func canonicalToolArgs(raw json.RawMessage) (string, json.RawMessage) {
	text := "null"
	var parsed any
	if len(raw) > 0 && json.Unmarshal(raw, &parsed) == nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if enc.Encode(parsed) == nil {
			text = strings.TrimSuffix(buf.String(), "\n")
		}
	}
	return text, json.RawMessage(text)
}

type repeatTracker struct {
	warnThreshold int
	hardCap       int
	lastHash      [32]byte
	hasLast       bool
	streak        int
	warned        bool
}

func newRepeatTracker(warnThreshold, hardCap int) *repeatTracker {
	if warnThreshold <= 0 {
		warnThreshold = defaultRepeatWarnThreshold
	}
	if hardCap <= 0 {
		hardCap = defaultRepeatHardCap
	}
	if hardCap <= warnThreshold {
		hardCap = warnThreshold + 1
	}
	return &repeatTracker{
		warnThreshold: warnThreshold,
		hardCap:       hardCap,
	}
}

// Observe records one tool call and reports whether it hit the recovery
// threshold ("recovered"), the hard cap ("ended"), or neither ("").
func (r *repeatTracker) Observe(call ContentBlock) (action string, streak int, canonical json.RawMessage) {
	canonStr, canonRaw := canonicalToolArgs(call.Input)
	h := sha256.Sum256([]byte(call.Name + "\x00" + canonStr))
	if r.hasLast && h == r.lastHash {
		r.streak++
	} else {
		r.lastHash = h
		r.hasLast = true
		r.streak = 1
		r.warned = false
	}
	if r.streak >= r.hardCap {
		return "ended", r.streak, canonRaw
	}
	if r.streak >= r.warnThreshold && !r.warned {
		r.warned = true
		return "recovered", r.streak, canonRaw
	}
	return "", r.streak, canonRaw
}

func repeatRecoveryWarning(toolName string, streak int) string {
	return fmt.Sprintf(
		"Warning: you have called `%s` with identical arguments %d times in a row. Stop repeating the same call; inspect the previous result and take a different action, or report an obstacle.",
		toolName, streak,
	)
}
