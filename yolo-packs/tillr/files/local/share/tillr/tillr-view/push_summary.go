package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// pushSummary combines the latest push outcome with current ref counts. Green measures
// proven work and main measures total work, regardless of what the push attempted.
// Unreadable counts are null, not zero.
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

func readPushSummary(ctx context.Context, r *store.Reader, root string) *pushSummary {
	out := &pushSummary{What: "none"}
	var payload, tip string
	var ts int64
	if err := r.QueryRow(`SELECT payload, COALESCE(object_id,''), ts FROM events
 WHERE kind = 'drain.post-landing-action' AND json_extract(payload,'$.action') = 'push' ORDER BY seq DESC LIMIT 1`).Scan(&payload, &tip, &ts); err != nil {
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
	if ev.Kind != "" && ev.Kind != "push-ok" && ev.Kind != "remote-sync-cleared" {
		out.StateKind, out.Detail, out.Since = ev.Kind, firstLine(ev.Detail), ev.since()
		_, out.Mover, _, _ = ev.pushRow()
	}
	if root != "" && out.Remote != "" && out.Ref != "" {
		out.PublishedSHA = pushRefCommit(ctx, root, "refs/tillr/published/"+out.Remote+"/"+out.Ref)
		out.UnpublishedGreenCount = commitsAhead(ctx, root, out.PublishedSHA, "refs/tillr/green")
		out.UnpublishedMainCount = commitsAhead(ctx, root, out.PublishedSHA, "refs/heads/main")
	}
	return out
}

func pushRefCommit(ctx context.Context, root, ref string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func commitsAhead(ctx context.Context, root, published, ref string) *int {
	if published == "" {
		return nil
	}
	tip := pushRefCommit(ctx, root, ref)
	if tip == "" {
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

func pushCountLabel(count *int) string {
	if count == nil {
		return "unknown"
	}
	return strconv.Itoa(*count)
}

func (p *pushSummary) text() string {
	if p == nil || p.What == "none" {
		return ""
	}
	kind := p.StateKind
	if kind == "push-ok" || kind == "remote-sync-cleared" {
		kind = ""
	}
	green, main := p.UnpublishedGreenCount, p.UnpublishedMainCount
	if kind == "" && (green == nil || *green == 0) && (main == nil || *main == 0) {
		return ""
	}
	line := "push: " + pushCountLabel(green) + " proven of " + pushCountLabel(main) + " total commit(s) unpublished"
	if p.Remote != "" && p.Ref != "" {
		line += " to " + p.Remote + "/" + p.Ref
	}
	if kind != "" {
		line += " — " + kind
		if p.Mover != "" {
			line += "; waits on " + p.Mover
		}
		if p.Since != "" {
			line += " since " + p.Since
		}
	}
	return line
}

// pushRefDeps names the files the summary's git reads depend on, as they are now, so the
// summary can be shared until a ref moves: packed-refs and the three refs it resolves
// (the published ref, refs/tillr/green and the main branch), each of which git stores
// either as a loose file or in packed-refs. ok is false when the checkout's git directory
// is not a plain directory (a linked worktree or a bare layout), where the refs cannot be
// watched cheaply and the summary is read fresh instead.
func pushRefDeps(root string, summary *pushSummary) (deps []fileDep, ok bool) {
	if summary == nil || summary.What == "none" || root == "" || summary.Remote == "" || summary.Ref == "" {
		// No git read happened, so the summary depends on the store alone.
		return nil, true
	}
	gitDir := filepath.Join(root, ".git")
	info, err := os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	for _, p := range []string{
		filepath.Join(gitDir, "packed-refs"),
		filepath.Join(gitDir, "refs", "tillr", "published", summary.Remote, filepath.FromSlash(summary.Ref)),
		filepath.Join(gitDir, "refs", "tillr", "green"),
		filepath.Join(gitDir, "refs", "heads", "main"),
	} {
		if fi, err := os.Stat(p); err == nil {
			deps = append(deps, depOf(p, fi))
		} else {
			deps = append(deps, fileDep{path: p})
		}
	}
	return deps, true
}
