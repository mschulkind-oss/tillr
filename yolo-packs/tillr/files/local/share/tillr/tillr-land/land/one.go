package land

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// OneRequest is one manual landing: the branch to land, the trunk it lands on, and the gate
// program that judges it.
type OneRequest struct {
	Branch string
	Trunk  string
	// Run is the run the landing is filed under. A manual landing names it explicitly; it
	// is what a non-landing pins refs/tillr/runs/<run_id> at.
	Run string
	// Item is the board item this landing is for. A manual landing of a branch outside an
	// item names the run, which is the one value that is true of every landing and of no
	// item.
	Item string
	// Gate is the gate program and its own arguments. It is a program rather than a
	// library call because the gate is the caller's choice (§5.9): this face composes the
	// pipeline a drain composes, and judges nothing itself.
	Gate []string
	At   time.Time
	// CapNow is the live landing-authorization clock, sampled after gate and lock wait.
	CapNow func() time.Time
	// LockPath names another lander's lock file, for the migration; empty takes the
	// repository's own land lock.
	LockPath string
	// LockWait is how long the landing waits for the land lock. Zero waits until the
	// context ends.
	LockWait time.Duration
}

// One gates and lands one branch in one command: the manual single-candidate landing that
// `bin/land <branch>` was, expressed as the two steps it always was underneath.
//
// The gate runs first, outside the land lock, so a gate that takes minutes does not hold
// the trunk. The change is the trunk's merge base with the branch, so a lane that branched
// from the trunk lands what it changed and nothing the trunk already had. The verdict is
// then presented to the same landing transaction a drain uses, under the same land lock, so
// a manual landing is recorded in .tillr/state.db and squashes onto a moved trunk exactly
// like an unattended one.
func One(ctx context.Context, s *store.Store, t *Trunk, req OneRequest) (Landing, error) {
	if req.Branch == "" || req.Trunk == "" || len(req.Gate) == 0 {
		return Landing{}, Usagef("one needs --branch, --trunk and --gate")
	}
	head, err := t.Head(req.Branch)
	if err != nil {
		return Landing{}, err
	}
	trunkHead, err := t.Head(req.Trunk)
	if err != nil {
		return Landing{}, err
	}
	base, err := t.MergeBase(trunkHead, head)
	if err != nil {
		return Landing{}, err
	}
	run := req.Run
	if run == "" {
		run = "manual-" + head[:12]
	}
	item := req.Item
	if item == "" {
		item = run
	}
	verdict, err := judge(t, req, base, head)
	if err != nil {
		return Landing{}, err
	}
	return Land(ctx, s, t, LandingRequest{
		ItemID:   item,
		RunID:    run,
		BaseRef:  req.Trunk,
		BaseSHA:  base,
		HeadSHA:  head,
		Verdict:  verdict,
		At:       req.At,
		CapNow:   req.CapNow,
		LockPath: req.LockPath,
		LockWait: req.LockWait,
	})
}

// judge runs the gate program over the change and answers the verdict it emitted.
//
// The exit code and the record must agree, because both are read by callers: the drain maps
// the exit code onto its gate step, and this face hands the record to the landing. 0 is a
// pass and 1 is a failing verdict about the code, which is a completed judgment and not an
// error. Any other exit is a gate that could not judge: the record's own result is replaced
// with error so the landing refuses rather than reading it as a failure of the code
// (§5.7).
func judge(t *Trunk, req OneRequest, base, head string) (Verdict, error) {
	argv := append(append([]string{}, req.Gate...),
		"--base", base, "--head", head, "--worktree", t.repo)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = t.repo
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	code := record.ExitOK
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Verdict{}, Externalf("running the gate %q in %s: %v: %s",
				strings.Join(argv, " "), t.repo, err, strings.TrimSpace(stderr.String()))
		}
		code = exit.ExitCode()
	}
	verdict, err := DecodeVerdict(out)
	if err != nil {
		return Verdict{}, Usagef("the gate %q answered no gate.verdict record: %v", argv[0], err)
	}
	switch code {
	case record.ExitOK:
		if verdict.Result != VerdictPass {
			return Verdict{}, Usagef("the gate %q exited 0 but recorded result %q", argv[0], verdict.Result)
		}
	case record.ExitNegative:
		if verdict.Result != VerdictFail {
			return Verdict{}, Usagef("the gate %q exited 1 but recorded result %q", argv[0], verdict.Result)
		}
	default:
		verdict.Result = VerdictError
	}
	return verdict, nil
}

// Clock answers the record clock a face stamps its records with. The clock is an input so a
// fixture run is reproducible (§5.1); an empty value means the wall clock.
func Clock(now string) (time.Time, error) {
	if now == "" {
		return time.Now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, now)
	if err != nil {
		return time.Time{}, Usagef("--now %q is not an RFC 3339 timestamp: %v", now, err)
	}
	return parsed.UTC(), nil
}
