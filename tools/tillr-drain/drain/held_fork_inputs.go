package drain

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
)

// fork-gate copies these into the build checkout, which can also seed a
// missing shared fork cache at delivery. Keep them independent of both lanes
// and the cache, just like the candidate commit.
type heldForkInputs struct {
	Upstream string `json:"upstream,omitempty"`
	Refs     string `json:"refs,omitempty"`
}

const heldForkRefFormat = "--format=%(refname) %(objectname)"

func (s *heldForkInputs) prefix(c Candidate) string {
	binding := candidatePinRef(c) + "\x00" + s.Upstream + "\x00" + s.Refs
	return fmt.Sprintf("refs/tillr/held-fork-inputs/%x/", sha256.Sum256([]byte(binding)))
}

func forkRefLines(refs string) []string {
	if refs == "" {
		return nil
	}
	return strings.Split(refs, "\n")
}

func (l *Loop) forkSupportRefs(ctx context.Context, dir string) (string, error) {
	out, err := l.git(ctx, dir, "for-each-ref", heldForkRefFormat, "refs/remotes/upstream/", "refs/tags/")
	return strings.TrimSpace(out), err
}

func (l *Loop) forkUpstream(ctx context.Context, dir string) (string, error) {
	out, err := l.git(ctx, dir, "remote")
	if err != nil {
		return "", err
	}
	for _, name := range strings.Fields(out) {
		if name == "upstream" {
			url, err := l.git(ctx, dir, "config", "--get", "remote.upstream.url")
			return strings.TrimSpace(url), err
		}
	}
	return "", nil
}

func (l *Loop) saveHeldForkInputs(ctx context.Context, c Candidate) (*heldForkInputs, error) {
	refs, err := l.forkSupportRefs(ctx, c.Worktree)
	if err != nil {
		return nil, err
	}
	url, err := l.forkUpstream(ctx, c.Worktree)
	if err != nil {
		return nil, err
	}
	s := &heldForkInputs{Upstream: url, Refs: refs}
	prefix := s.prefix(c)
	stored, err := l.git(ctx, l.cfg.Root, "for-each-ref", heldForkRefFormat, prefix)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(stored) == "" && refs != "" {
		args := []string{"fetch", "--atomic", "--no-tags", "--no-write-fetch-head", "--", c.Worktree}
		for _, line := range forkRefLines(refs) {
			name, oid, _ := strings.Cut(line, " ")
			args = append(args, oid+":"+prefix+name)
		}
		if _, err := l.git(ctx, l.cfg.Root, args...); err != nil {
			return nil, err
		}
	}
	return s, l.verifyHeldForkRefs(ctx, c, s)
}

func (l *Loop) verifyHeldForkRefs(ctx context.Context, c Candidate, s *heldForkInputs) error {
	if s == nil {
		return Externalf("saved fork gate inputs are missing")
	}
	prefix := s.prefix(c)
	var expected []string
	for _, line := range forkRefLines(s.Refs) {
		name, _, ok := strings.Cut(line, " ")
		if !ok || !(strings.HasPrefix(name, "refs/remotes/upstream/") || strings.HasPrefix(name, "refs/tags/")) {
			return Externalf("invalid saved fork support ref %q", name)
		}
		expected = append(expected, prefix+line)
	}
	// Reading the object type proves the refs still have their objects. A
	// fourth field would indicate a symbolic ref, not an independent pin.
	out, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(refname) %(objectname) %(objecttype) %(symref)", prefix)
	if err != nil {
		return err
	}
	lines := forkRefLines(strings.TrimSpace(out))
	if len(lines) != len(expected) {
		return Externalf("saved fork support refs failed readback")
	}
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0]+" "+fields[1] != expected[i] {
			return Externalf("saved fork support refs failed readback")
		}
	}
	return nil
}

func (l *Loop) restoreHeldForkRefs(ctx context.Context, c Candidate, path string, s *heldForkInputs) error {
	url, err := l.forkUpstream(ctx, path)
	if err != nil {
		return err
	}
	refuse := func() error {
		return laneRefusalf(c.LaneID, ReasonLaneUnlandedChange, "inspect the fork checkout before clearing it", "fork checkout %s has different upstream inputs", c.Worktree)
	}
	if url != "" && s.Upstream != "" && url != s.Upstream {
		return refuse()
	}
	refs, err := l.forkSupportRefs(ctx, path)
	if err != nil {
		return err
	}
	dest := map[string]string{}
	for _, line := range forkRefLines(refs) {
		name, oid, _ := strings.Cut(line, " ")
		dest[name] = oid
	}
	args := []string{"fetch", "--atomic", "--no-tags", "--no-write-fetch-head", "--", l.cfg.Root, candidatePinRef(c)}
	for _, line := range forkRefLines(s.Refs) {
		name, oid, _ := strings.Cut(line, " ")
		if have := dest[name]; have != "" {
			if have != oid {
				return refuse()
			}
			continue
		}
		args = append(args, s.prefix(c)+name+":"+name)
	}
	if _, err := l.git(ctx, path, args...); err != nil {
		return err
	}
	if url == "" && s.Upstream != "" {
		_, err = l.git(ctx, path, "remote", "add", "--", "upstream", s.Upstream)
	}
	return err
}
