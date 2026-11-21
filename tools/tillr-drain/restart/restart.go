package restart

// Package restart is the restart protocol both long-running roles share: a request is a
// store fact addressed to a role, an answer is another fact naming the request and both
// builds, and the newest request is the only one that is acted on.
// Architecture and invariants: docs/reference/tillr-restart.md
// It is a package of its own,
// with no dependency on the drain package, because the observatory server imports it and
// must not pull in the loop. The loop's own steps (what it stops starting, when it
// replaces itself) are in drain/restart.go.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const owner = "drain"

const (
	// RequestKind and AnswerKind are the two events. Both are keyed by the
	// request's id, with object type "restart".
	RequestKind       = "drain.restart-request"
	AnswerKind        = "drain.restart-answer"
	restartObjectType = "restart"

	// RoleDrain and RoleView are the roles a request is addressed to.
	RoleDrain = "drain"
	RoleView  = "view"

	AnswerRestarted      = "restarted"
	AnswerAlreadyCurrent = "already-current"
	AnswerRefused        = "refused"
	AnswerSuperseded     = "superseded"

	// RequestEnv and FromEnv carry a restart across the in-place exec: the
	// replacement answers the request, because only it knows the build it now runs.
	RequestEnv = "TILLR_RESTART_REQUEST"
	// LauncherEnv names the pack launcher that started the process; LauncherResolveEnv
	// makes that launcher print its current binary instead of running it.
	LauncherEnv        = "TILLR_LAUNCHER"
	LauncherResolveEnv = "TILLR_LAUNCHER_RESOLVE"
	FromEnv            = "TILLR_RESTART_FROM"
)

// Request is one request to replace a role's process.
type Request struct {
	ID   string    `json:"id"`
	Role string    `json:"role"`
	At   time.Time `json:"at"`
}

// Answer is the one event that settles a request.
type Answer struct {
	RequestID string    `json:"request_id"`
	Role      string    `json:"role"`
	Answer    string    `json:"answer"`
	FromBuild string    `json:"from_build"`
	ToBuild   string    `json:"to_build"`
	ToDigest  string    `json:"to_digest,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	At        time.Time `json:"at"`
}

func init() {
	for _, kind := range []string{RequestKind, AnswerKind, BuildKind} {
		if err := event.Register(kind, owner); err != nil {
			panic(err)
		}
	}
}

func validRole(role string) error {
	if role != RoleDrain && role != RoleView {
		return fmt.Errorf("a restart is addressed to %q or %q, not %q", RoleDrain, RoleView, role)
	}
	return nil
}

// File writes one request for role and returns it. It needs no running loop: the
// next process of that role answers it at startup.
func File(ctx context.Context, storePath, role string, at time.Time) (Request, error) {
	if err := validRole(role); err != nil {
		return Request{}, err
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return Request{}, fmt.Errorf("minting a restart id: %v", err)
	}
	request := Request{ID: fmt.Sprintf("restart-%s-%d-%x", role, at.UnixMilli(), suffix), Role: role, At: at.UTC()}
	// The caller can already be the replacement build, while the old loop is
	// still writing. Filing a request must never migrate its store.
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		return Request{}, err
	}
	defer s.Close()
	body, err := json.Marshal(request)
	if err != nil {
		return Request{}, err
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: RequestKind, ObjectType: restartObjectType, ObjectID: &request.ID, Payload: body, At: request.At})
		return err
	})
	if err != nil {
		return Request{}, fmt.Errorf("recording the restart request: %v", err)
	}
	return request, nil
}

// State is what the store says about one role's requests.
type State struct {
	// Pending are the role's unanswered requests, oldest first. The last is the newest.
	Pending []Request
	// Latest is the answer to the role's most recent answered request, nil before any.
	Latest *Answer
	// LatestRestarted is the most recent `restarted` answer: the roll this role's running
	// build may still owe work for (Restarter.Owing).
	LatestRestarted *Answer
}

// ReadState reads one role's requests and answers from the event log.
func ReadState(r *store.Reader, role string) (State, error) {
	rows, err := r.Query(`SELECT kind, payload FROM events WHERE kind IN (?, ?) ORDER BY seq`, RequestKind, AnswerKind)
	if err != nil {
		return State{}, err
	}
	defer rows.Close()
	state := State{}
	var requests []Request
	answered := map[string]bool{}
	for rows.Next() {
		var kind string
		var payload []byte
		if err := rows.Scan(&kind, &payload); err != nil {
			return State{}, err
		}
		switch kind {
		case RequestKind:
			var request Request
			if err := json.Unmarshal(payload, &request); err != nil {
				return State{}, err
			}
			if request.Role == role {
				requests = append(requests, request)
			}
		case AnswerKind:
			var answer Answer
			if err := json.Unmarshal(payload, &answer); err != nil {
				return State{}, err
			}
			if answer.Role == role {
				answered[answer.RequestID] = true
				a := answer
				state.Latest = &a
				if answer.Answer == AnswerRestarted {
					state.LatestRestarted = &a
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return State{}, err
	}
	for _, request := range requests {
		if !answered[request.ID] {
			state.Pending = append(state.Pending, request)
		}
	}
	return state, nil
}

// ReadAnswers answers the latest answer of each role that has one.
func ReadAnswers(ctx context.Context, storePath string) (map[string]Answer, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	out := map[string]Answer{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		for _, role := range []string{RoleDrain, RoleView} {
			state, err := ReadState(r, role)
			if err != nil {
				return err
			}
			if state.Latest != nil {
				out[role] = *state.Latest
			}
		}
		return nil
	})
	return out, err
}

// AwaitAnswer blocks until request id is answered and returns the answer.
func AwaitAnswer(ctx context.Context, storePath, id string, poll time.Duration) (Answer, error) {
	for {
		answer, err := readAnswer(ctx, storePath, id)
		if err != nil || answer != nil {
			if answer == nil {
				return Answer{}, err
			}
			return *answer, err
		}
		select {
		case <-ctx.Done():
			return Answer{}, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func readAnswer(ctx context.Context, storePath, id string) (*Answer, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var found *Answer
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var payload []byte
		err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq DESC LIMIT 1`, AnswerKind, id).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var answer Answer
		if err := json.Unmarshal(payload, &answer); err != nil {
			return err
		}
		found = &answer
		return nil
	})
	return found, err
}

// String is the line `--wait` prints.
func (a Answer) String() string {
	line := fmt.Sprintf("%s: %s from %s to %s", a.RequestID, a.Answer, a.FromBuild, a.ToBuild)
	if a.Detail != "" {
		line += ": " + a.Detail
	}
	return line
}

// Build names the build a process runs: the one line it prints and the digest that tells
// two builds apart.
type Build struct {
	Line   string
	Digest string
}

// Restarter is one role's side of the protocol. The loop and the observatory server each
// build one around their own running build and their own binary on disk.
type Restarter struct {
	Store string
	Role  string
	// Running is the build this process runs; Executable is where its binary lives now.
	Running    Build
	Executable string
	// Disk reads the digest of the binary at a path.
	Disk func(path string) string
	// Candidate names the binary a restart would run now. A program started by a pack
	// launcher runs from an immutable cache file that can never name its successor, so the
	// launcher, the stable entry point, is asked (LauncherCandidate). Nil means Executable.
	Candidate     func(ctx context.Context) (string, error)
	pinned        string
	checkedDigest string
	// Check runs the binary on disk with the process's own arguments and --check. Its error
	// message is the one line a refusal records.
	Check func(ctx context.Context, executable string) error
	Now   func() time.Time
}

func (rs *Restarter) now() time.Time {
	if rs.Now != nil {
		return rs.Now().UTC()
	}
	return time.Now().UTC()
}

// Pending reports whether this role has an unanswered request.
func (rs *Restarter) Pending(ctx context.Context) (bool, error) {
	s, err := store.OpenRead(ctx, rs.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	pending := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		state, err := ReadState(r, rs.Role)
		pending = len(state.Pending) > 0
		return err
	})
	return pending, err
}

// Record writes one answer; the roll's own steps and tests use it.
func (rs *Restarter) Record(ctx context.Context, answer Answer) error { return rs.record(ctx, answer) }

func (rs *Restarter) record(ctx context.Context, answer Answer) error {
	answer.Role = rs.Role
	answer.At = rs.now()
	body, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	// A refusal may be reporting a migration that could not commit.
	s, err := store.Open(ctx, rs.Store, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: AnswerKind, ObjectType: restartObjectType, ObjectID: &answer.RequestID, Payload: body, At: answer.At})
		return err
	})
}

// Settle answers every pending request that does not need the process replaced and reports
// the newest one that does. Older requests are answered `superseded`; a binary identical to
// the running one answers `already-current`; a binary that fails its check answers
// `refused` with the check's message. The caller replaces itself only when replace is true,
// and the replacement answers `restarted` (Adopt), so a failed exec leaves nothing wrongly
// answered.
func (rs *Restarter) Settle(ctx context.Context) (request Request, replace bool, err error) {
	for {
		state, err := rs.state(ctx)
		if err != nil || len(state.Pending) == 0 {
			return Request{}, false, err
		}
		from := rs.Running.Line
		newest := state.Pending[len(state.Pending)-1]
		for _, older := range state.Pending[:len(state.Pending)-1] {
			if err := rs.record(ctx, Answer{RequestID: older.ID, Answer: AnswerSuperseded, FromBuild: from, ToBuild: from, Detail: "superseded by " + newest.ID}); err != nil {
				return Request{}, false, err
			}
		}
		candidate := rs.Executable
		if rs.Candidate != nil {
			var cerr error
			if candidate, cerr = rs.Candidate(ctx); cerr != nil {
				return newest, false, rs.record(ctx, Answer{RequestID: newest.ID, Answer: AnswerRefused, FromBuild: from, ToBuild: "unresolved", Detail: oneLine(cerr.Error())})
			}
		}
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr != nil {
			return newest, false, rs.Refuse(ctx, newest, resolveErr.Error())
		}
		candidate = resolved
		rs.pinned = candidate
		disk := rs.Disk(candidate)
		rs.checkedDigest = disk
		var answer *Answer
		switch {
		case disk == "":
			answer = &Answer{RequestID: newest.ID, Answer: AnswerRefused, FromBuild: from, ToBuild: "unreadable", Detail: "cannot read the binary at " + candidate}
		case disk == rs.Running.Digest:
			answer = &Answer{RequestID: newest.ID, Answer: AnswerAlreadyCurrent, FromBuild: from, ToBuild: from}
		case rs.Check != nil:
			if checkErr := rs.Check(ctx, candidate); checkErr != nil {
				answer = &Answer{RequestID: newest.ID, Answer: AnswerRefused, FromBuild: from, ToBuild: "digest " + shortDigest(disk), Detail: oneLine(checkErr.Error())}
			}
		}
		// A newer request that arrived while the binary was being read or checked makes
		// this selection stale: it is answered superseded, and the newest is judged afresh.
		if latest, err := rs.newest(ctx); err != nil {
			return Request{}, false, err
		} else if latest.ID != newest.ID {
			continue
		}
		if answer != nil {
			return newest, false, rs.record(ctx, *answer)
		}
		return newest, true, nil
	}
}

func (rs *Restarter) state(ctx context.Context) (State, error) {
	s, err := store.OpenRead(ctx, rs.Store)
	if err != nil {
		return State{}, err
	}
	defer s.Close()
	var state State
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		state, err = ReadState(r, rs.Role)
		return err
	})
	return state, err
}

// newest is the newest unanswered request, or the zero Request when none is pending.
func (rs *Restarter) newest(ctx context.Context) (Request, error) {
	state, err := rs.state(ctx)
	if err != nil || len(state.Pending) == 0 {
		return Request{}, err
	}
	return state.Pending[len(state.Pending)-1], nil
}

// ErrSuperseded is what Replace answers when a newer request arrived after Settle chose
// the one being replaced for: the caller settles again instead of executing.
var ErrSuperseded = fmt.Errorf("a newer restart request arrived")

// Owing reports whether this process is the build of the role's latest accepted restart.
// A roll's obligations (pausing every run from an older build) are then recoverable from
// the request and answer facts alone, so a supervisor that died before asking for them
// all, and was started again, still owes them. Nothing restarts because of it.
func (rs *Restarter) Owing(ctx context.Context) (bool, error) {
	state, err := rs.state(ctx)
	if err != nil {
		return false, err
	}
	if state.LatestRestarted == nil {
		return false, nil
	}
	if state.LatestRestarted.ToDigest != "" {
		return state.LatestRestarted.ToDigest == rs.Running.Digest, nil
	}
	return state.LatestRestarted.ToBuild == rs.Running.Line, nil
}

// Refuse answers a request refused after Settle said to replace, when the exec itself failed.
func (rs *Restarter) Refuse(ctx context.Context, request Request, detail string) error {
	return rs.record(ctx, Answer{RequestID: request.ID, Answer: AnswerRefused, FromBuild: rs.Running.Line, ToBuild: "digest " + shortDigest(rs.Disk(rs.pinnedPath())), Detail: oneLine(detail)})
}

// Adopt is the replacement's first act: when the environment names a request this process
// was started to answer, it records `restarted` naming both builds and clears the
// environment so nothing it launches inherits the request. It reports whether it adopted.
func (rs *Restarter) Adopt(ctx context.Context) (bool, error) {
	id := os.Getenv(RequestEnv)
	if id == "" {
		return false, nil
	}
	from := os.Getenv(FromEnv)
	os.Unsetenv(RequestEnv)
	os.Unsetenv(FromEnv)
	return true, rs.record(ctx, Answer{RequestID: id, Answer: AnswerRestarted, FromBuild: from, ToBuild: rs.Running.Line, ToDigest: rs.Running.Digest})
}

func (rs *Restarter) pinnedPath() string {
	if rs.pinned != "" {
		return rs.pinned
	}
	return rs.Executable
}

// Replace execs the pinned binary over this process: the same process id and arguments.
// It refuses with ErrSuperseded when a newer request has arrived since Settle chose.
func (rs *Restarter) Replace(ctx context.Context, request Request, argv []string, exec func(string, []string, []string) error) error {
	if exec == nil {
		exec = syscall.Exec
	}
	if latest, err := rs.newest(ctx); err != nil {
		return err
	} else if latest.ID != request.ID {
		return ErrSuperseded
	}
	// A rename after --check invalidates the check, even at an immutable-looking path.
	for rs.Disk(rs.pinnedPath()) != rs.checkedDigest {
		rs.checkedDigest = rs.Disk(rs.pinnedPath())
		if rs.checkedDigest == "" {
			return fmt.Errorf("cannot read the binary at %s", rs.pinnedPath())
		}
		if rs.Check != nil {
			if err := rs.Check(ctx, rs.pinnedPath()); err != nil {
				return err
			}
		}
		if latest, err := rs.newest(ctx); err != nil {
			return err
		} else if latest.ID != request.ID {
			return ErrSuperseded
		}
	}
	env := append(withoutEnv(os.Environ(), RequestEnv, FromEnv), RequestEnv+"="+request.ID, FromEnv+"="+rs.Running.Line)
	return exec(rs.pinnedPath(), argv, env)
}

func withoutEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
outer:
	for _, kv := range env {
		for _, key := range keys {
			if strings.HasPrefix(kv, key+"=") {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return out
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func oneLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return "check failed"
}

// FileDigest is the SHA-256 of one file, empty when it cannot be read: the identity two
// builds of one program are told apart by when no launcher names them.
func FileDigest(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// LauncherCandidate resolves the binary a restart would run now. When the process was
// started by a pack launcher (TILLR_LAUNCHER) the launcher is asked, with
// TILLR_LAUNCHER_RESOLVE=1, which cached binary is current; otherwise the fallback path
// is the candidate. A launcher that cannot answer is an error, never a silent fallback to
// the immutable file this process runs from.
func LauncherCandidate(fallback string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		launcher := os.Getenv(LauncherEnv)
		if launcher == "" {
			return fallback, nil
		}
		cmd := exec.CommandContext(ctx, launcher)
		cmd.Env = append(withoutEnv(os.Environ(), LauncherResolveEnv), LauncherResolveEnv+"=1")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("the launcher %s could not name the current build: %v", launcher, err)
		}
		path := oneLine(string(out))
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("the launcher %s named %s: %v", launcher, path, err)
		}
		return resolved, nil
	}
}

// BuildKind is the event a long-running process writes when it starts or adopts a restart:
// the build it is running, keyed by role. It is what the observatory's header reads for the
// running build of a role, because a terminal loop result exists only after the process
// stops, and an exec'd loop never writes the one its predecessor would have.
const BuildKind = "drain.build-started"

// BuildFact is the payload of BuildKind.
type BuildFact struct {
	Role       string    `json:"role"`
	Revision   string    `json:"revision"`
	Time       string    `json:"time"`
	Modified   bool      `json:"modified"`
	Digest     string    `json:"digest"`
	Executable string    `json:"executable"`
	PID        int       `json:"pid"`
	At         time.Time `json:"at"`
}

// PublishBuild records the running build of one role.
func PublishBuild(ctx context.Context, storePath string, fact BuildFact) error {
	if fact.At.IsZero() {
		fact.At = time.Now().UTC()
	}
	body, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	id := fmt.Sprintf("%s-%d", fact.Role, fact.PID)
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: BuildKind, ObjectType: "build", ObjectID: &id, Payload: body, At: fact.At})
		return err
	})
}

// ReadBuild answers the latest published build of a role, nil when none was published.
func ReadBuild(r *store.Reader, role string) (*BuildFact, error) {
	rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq DESC`, BuildKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var fact BuildFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			return nil, err
		}
		if fact.Role == role {
			return &fact, nil
		}
	}
	return nil, rows.Err()
}
