package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// pushEvent is a drain.post-landing-action payload with action "push". Events written
// before the structured states carry only action, detail and what; Kind is empty for them
// and they keep the older reading (docs/reference/tillr-refused-push.md, "Showing the state and its
// mover while it lasts").
type pushEvent struct {
	What              string `json:"what"`
	Kind              string `json:"kind"`
	Detail            string `json:"detail"`
	Remote            string `json:"remote"`
	Ref               string `json:"ref"`
	RemoteTip         string `json:"remote_tip"`
	PreviousRemoteTip string `json:"previous_remote_tip"`
	RebasedTip        string `json:"rebased_tip"`
	EscalatedMover    string `json:"escalated_mover"`
	Since             string `json:"since_ts"`
	UnpublishedGreen  *int   `json:"unpublished_green_count"`
	UnpublishedMain   *int   `json:"unpublished_main_count"`
	tip               string
	atMS              int64
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// provedTip is the rebased tip a heal produced when the event names it, else the pushed tip.
func (e pushEvent) provedTip() string {
	if e.RebasedTip != "" {
		return e.RebasedTip
	}
	return e.tip
}

// moverLine is the text of the detail's "mover:" line, which names the escalated mover
// exactly as the drain wrote it (an escalated sync item or a gate-code conflict).
func moverLine(detail string) string {
	lines := strings.Split(detail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "mover:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func (e pushEvent) since() string {
	if e.Since != "" {
		return e.Since
	}
	if e.atMS > 0 {
		return time.UnixMilli(e.atMS).UTC().Format(time.RFC3339)
	}
	return "—"
}

func (e pushEvent) destination() string {
	branch := strings.TrimPrefix(e.Ref, "refs/heads/")
	switch {
	case e.Remote != "" && branch != "":
		return e.Remote + "/" + branch
	case e.Remote != "":
		return e.Remote
	case branch != "":
		return branch
	}
	return "the remote"
}

// firstLine is the detail's first line with white space collapsed: the drain appends a
// "mover:" line and operator instructions after it, which the Mover column already says.
func firstLine(detail string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(detail), "\n")
	return strings.Join(strings.Fields(line), " ")
}

// pushRow answers the class, mover and why of a structured push state, per design
// Section 7.1. The last result is false for a kind this build does not know, which then
// falls back to the older reading rather than disappearing.
func (e pushEvent) pushRow() (class, mover, why string, ok bool) {
	dest, remoteTip := e.destination(), shortSHA(e.RemoteTip)
	counts := ""
	if e.UnpublishedGreen != nil && e.UnpublishedMain != nil {
		counts = fmt.Sprintf("; %d proven / %d total local commit(s) unpublished", *e.UnpublishedGreen, *e.UnpublishedMain)
	} else if e.UnpublishedGreen != nil {
		counts = fmt.Sprintf("; %d proven commit(s) unpublished", *e.UnpublishedGreen)
	} else if e.UnpublishedMain != nil {
		counts = fmt.Sprintf("; %d total local commit(s) unpublished", *e.UnpublishedMain)
	}
	detail := firstLine(e.Detail)
	switch e.Kind {
	case "remote-sync-proving":
		return "waits on work", fmt.Sprintf("the heavy runner; prove rebased tip %s before pushing", shortSHA(e.provedTip())),
			detail + counts, true
	case "remote-sync-conflict":
		switch e.EscalatedMover {
		case "":
			id := "sync-remote-" + strings.ToLower(remoteTip)
			return "waits on work", fmt.Sprintf("%s; resolve rebase conflict onto %s (%s)", id, dest, remoteTip), detail + counts, true
		case "owner":
			if m := moverLine(e.Detail); strings.HasPrefix(m, "owner;") {
				return "needs you", m, detail + counts, true
			}
			return "needs you", fmt.Sprintf("owner; resolve gate-code conflict onto %s (%s)", dest, remoteTip), detail + counts, true
		default:
			return "needs you", fmt.Sprintf("operator; rebase main onto %s (%s)", dest, remoteTip), detail + counts, true
		}
	case "remote-sync-worktree-dirty":
		return "needs you", "operator; check out main or commit, stash, or restore clashing paths in the main checkout",
			fmt.Sprintf("cannot reset main to rebased tip %s because the main checkout is off main or has uncommitted edits on clashing paths", shortSHA(e.provedTip())) + counts, true
	case "remote-rewritten":
		return "needs you", fmt.Sprintf("operator; reconcile force-pushed %s (%s -> %s)", dest, shortSHA(e.PreviousRemoteTip), remoteTip),
			fmt.Sprintf("remote rewound or rewrote %s to %s; autopush stopped to avoid re-publishing dropped commits", shortSHA(e.PreviousRemoteTip), remoteTip) + counts, true
	case "push-patch-move-held":
		return "needs you", "operator; run tillr drain acknowledge-patch-move after checking the fork rewrite",
			"the pre-push hook held publication on an unacknowledged patch-branch move" + counts, true
	case "push-remote-declined":
		return "needs you", fmt.Sprintf("operator; inspect remote rejection on %s %s and run tillr drain push", e.Remote, e.Ref),
			fmt.Sprintf("remote server declined %s: %s", shortSHA(e.tip), detail) + counts, true
	case "push-failed":
		return "needs you", "operator; check git-broker approval or remote access, or run tillr drain push",
			fmt.Sprintf("push of %s did not reach a ref update (%s)", shortSHA(e.tip), detail) + counts, true
	}
	return "", "", "", false
}

func parsePushEvent(payload, tip string, atMS int64) (pushEvent, error) {
	var e pushEvent
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return e, err
	}
	e.tip, e.atMS = tip, atMS
	return e, nil
}
