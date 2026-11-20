package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

const (
	greenRef          = "refs/tillr/green"
	publishedRefStart = "refs/tillr/published/"
)

// pushSummary is the board's answer to "is proven work sitting unpublished": the counts
// come from repository refs and the state from the newest push outcome the drain recorded
// (docs/design/tillr-refused-push.md, Section 7.2). What is "none" when no push outcome
// was ever recorded, and the board then draws nothing. A count that could not be read is
// null, never zero.
type pushSummary struct {
	What                  string `json:"what"`
	Remote                string `json:"remote,omitempty"`
	Ref                   string `json:"ref,omitempty"`
	PublishedSHA          string `json:"published_sha,omitempty"`
	UnpublishedGreenCount *int   `json:"unpublished_green_count"`
	UnpublishedMainCount  *int   `json:"unpublished_main_count"`
	StateKind             string `json:"state_kind,omitempty"`
	Detail                string `json:"detail,omitempty"`
	Mover                 string `json:"mover,omitempty"`
	Since                 string `json:"since,omitempty"`
}

// readPushSummary reads the newest push outcome from the store and the refs from the
// repository at root. A missing events table or an unreadable repository degrades to
// what "none" or null counts, which the surfaces print as absence.
func readPushSummary(ctx context.Context, r *store.Reader, root string) *pushSummary {
	out := &pushSummary{What: "none"}
	var payload, tip string
	var ts int64
	err := r.QueryRow(`SELECT payload, COALESCE(object_id,''), ts FROM events
 WHERE kind = 'drain.post-landing-action' AND json_extract(payload,'$.action') = 'push' ORDER BY seq DESC LIMIT 1`).Scan(&payload, &tip, &ts)
	if err != nil {
		return out
	}
	ev, err := parsePushEvent(payload, tip, ts)
	if err != nil {
		return out
	}
	out.What = ev.What
	if out.What == "" {
		out.What = "tip"
	}
	out.Remote, out.Ref = ev.Remote, strings.TrimPrefix(ev.Ref, "refs/heads/")
	if ev.structured() {
		out.StateKind = ev.Kind
		out.Detail = firstLine(ev.Detail)
		if _, mover, _, ok := ev.pushRow(); ok {
			out.Mover = mover
		}
		out.Since = ev.since()
	}
	if out.Remote != "" && out.Ref != "" && root != "" {
		pub := gitCommit(ctx, root, publishedRefStart+out.Remote+"/"+out.Ref)
		out.PublishedSHA = pub
		out.UnpublishedGreenCount = commitsAhead(ctx, root, pub, greenRef)
		out.UnpublishedMainCount = commitsAhead(ctx, root, pub, "refs/heads/main")
	}
	return out
}

func gitCommit(ctx context.Context, root, ref string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// commitsAhead counts the commits tip holds that published lacks, or nil when either side
// is absent or the count fails.
func commitsAhead(ctx context.Context, root, published, tip string) *int {
	if published == "" || gitCommit(ctx, root, tip) == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-list", "--count", published+".."+tip).Output()
	if err != nil {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return nil
	}
	return &n
}

// unpublishedProven is the count of proven commits ahead of the published pointer in the
// summary's own mode: the green pointer in green mode, the trunk in tip mode.
func (p *pushSummary) unpublishedProven() int {
	count := p.UnpublishedGreenCount
	if p.What == "tip" {
		count = p.UnpublishedMainCount
	}
	if count == nil {
		return 0
	}
	return *count
}

// text is the line `tillr view status` prints: empty when nothing is parked.
func (p *pushSummary) text() string {
	if p == nil || p.What == "none" || (p.StateKind == "" && p.unpublishedProven() == 0) {
		return ""
	}
	line := "push: " + strconv.Itoa(p.unpublishedProven()) + " proven commit(s) unpublished"
	if dest := p.Remote + "/" + p.Ref; p.Remote != "" && p.Ref != "" {
		line += " to " + dest
	}
	if p.StateKind != "" {
		line += " — " + p.StateKind
		if p.Mover != "" {
			line += "; waits on " + p.Mover
		}
		if p.Since != "" {
			line += " since " + p.Since
		}
	}
	return line
}
