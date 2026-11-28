// Reference: docs/reference/tillr-muse-backend.md
package run

import (
	"encoding/json"
	"hash/fnv"
	"math"
	"strconv"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// Backend is one named backend a dispatch policy names: the adapter program staging puts in
// the request's argv, the model data it hands that adapter, and the bound its runs are held
// to. It is the request-shaped half of a policy — the values a run.request is derived from —
// and the policy's own half, which backend an item is dispatched to and how a failover walks,
// stays with the emitter that owns it (drain's A-POLICY: the policy's shape is drain's own).
//
// Model and Models are the same datum in two shapes. Model names one id; Models names the
// ordered rotation the adapter draws a route from, which is how one wire model reached
// through several client ids stays attributable per run. A backend naming both draws from
// Models and keeps Model as the head of that rotation, so the two can never disagree about
// which id a fresh policy names first.
//
// Effort is optional because the harness may have no such flag: agy carries the effort
// tier inside the model id and refuses `--effort`, so a policy that names no effort for it is
// what that adapter requires, and an effort named here is always passed.
//
// TimeoutSeconds is optional: omitted or 0 means the run has no wall, and it is stamped
// into the request as timeout_ms, which is what the run's lease expires on. A wall is a
// budget and not a fault detector — the retired 1800s default killed runs that had already
// committed (design D-RW1, docs/reference/run-wall-ruling.md) — so no backend is required to
// carry one. A negative value is refused, because a wall cannot run backwards.
//
// A positive value is passed to the adapter as `--timeout <seconds>` as well as into the
// request's own bound, so the runner's bound and the harness's cannot silently diverge.
//
// HarnessArgs are arguments for the harness itself, not for the adapter: which tools exist,
// which servers it sees, what standing text it gets (docs/reference/tillr-claude-backend.md,
// "Controlling what a seat may use", D-CB2). They are appended after a `--` separator and the adapter hands them over
// unchanged, so the policy holds the data while the spelling stays the harness's own, and
// tillr does not invent one permission vocabulary for several harnesses. An adapter that
// takes no harness arguments refuses the separator as it refuses any argument it does not
// know, so a row naming them for such an adapter fails at the adapter's preflight.
type Backend struct {
	// Quota is an optional argv that reads quota without starting a model session.
	// QuotaGroup is an optional pool name ([a-z0-9][a-z0-9_-]*) grouping backends that
	// share one subscription account across adapters or commands.
	// Reserve is the fraction of every reported window left for the owner (default 0).
	Quota          []string `json:"quota,omitempty"`
	QuotaGroup     string   `json:"quota_group,omitempty"`
	Reserve        float64  `json:"reserve,omitempty"`
	Adapter        string   `json:"adapter"`
	Model          string   `json:"model,omitempty"`
	Models         []string `json:"models,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	Account        string   `json:"account,omitempty"`
	WorkingDir     string   `json:"working_dir,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	HarnessArgs    []string `json:"harness_args,omitempty"`
}

// WorktreeDir is the one working directory a lane's run may name: a git worktree of the
// drained repository, inside the lane directory and outside the tree the change lands in. A
// backend that names nothing gets it, and staging refuses any other value rather than run the
// adapter somewhere no worktree was prepared.
const WorktreeDir = "worktree"

// Request answers the complete run.request one dispatch on this backend carries: the
// adapter's argv with the route this run draws, the bound its lease expires on, and the
// lane's worktree as the working directory.
//
// It is the one derivation of a run.request, so the two producers cannot spell one
// differently: drain when it plans a dispatch, and the `request` face when a standalone
// driver asks for the same record without running the loop.
func (b Backend) Request(itemID, laneID, runID, backendName string) Request {
	workingDir := b.WorkingDir
	if workingDir == "" {
		workingDir = WorktreeDir
	}
	return Request{
		RunID:      runID,
		ItemID:     itemID,
		LaneID:     laneID,
		Backend:    backendName,
		Command:    b.command(backendName, b.route(runID)),
		WorkingDir: workingDir,
		TimeoutMS:  b.TimeoutSeconds * 1000,
		EnvRefs:    []string{},
	}
}

// Missing answers the first thing about this backend that makes a request derived from it
// unrunnable, as a clause that follows the backend's name ("names no adapter: ..."), or the
// empty string when it names everything a run needs.
//
// It is stated here rather than beside a policy because what a request needs is this
// package's contract: the policy that names the values and the face that prints the record
// both ask this, so neither can emit a request the runner refuses at claim.
func (b Backend) Missing() string {
	if math.IsNaN(b.Reserve) || math.IsInf(b.Reserve, 0) || b.Reserve < 0 || b.Reserve > 1 {
		return "names reserve outside [0,1]: reserve is a fraction of each quota window"
	}
	if b.QuotaGroup != "" && !ValidQuotaGroupName(b.QuotaGroup) {
		return "names invalid quota_group " + strconv.Quote(b.QuotaGroup) + ": quota_group must match [a-z0-9][a-z0-9_-]*"
	}
	for _, arg := range b.Quota {
		if arg == "" {
			return "names an empty quota argument: quota is a nonempty command argv"
		}
	}
	if b.Adapter == "" {
		return "names no adapter: the request's command[0] is the adapter program"
	}
	if b.TimeoutSeconds < 0 {
		return "names timeout_seconds " + strconv.Itoa(b.TimeoutSeconds) + ": a wall is zero or more seconds, and 0 is an unbounded run"
	}
	if len(b.Models) == 0 && b.Model == "" {
		return "names no model: the adapter refuses an unset model, and an adapter default would be a model id in a tree that names no vendor"
	}
	for i, model := range b.Models {
		if model == "" {
			return "names an empty route at models[" + strconv.Itoa(i) + "]: a rotation is a list of model ids"
		}
	}
	return ""
}

// ValidQuotaGroupName reports whether name matches [a-z0-9][a-z0-9_-]*.
func ValidQuotaGroupName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if i == 0 {
			if !isLower && !isDigit {
				return false
			}
			continue
		}
		if !isLower && !isDigit && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// command is the adapter's argv as staging hands it over: the adapter program, the backend
// name it must be told to be, the model id it must be told to reach, the effort only when
// the policy names one, and the wall only when it names one.
//
// The effort is omitted rather than defaulted because a harness may have no such flag
// (agy carries the tier in the model id and refuses `--effort`), and because an adapter
// default would be a value in the tillr tree that the policy did not supply. The wall is
// the same shape from the other side: an omitted wall is stamped on the request as 0 and
// the adapter is told nothing, so neither the runner nor the harness invents a bound.
func (b Backend) command(name, model string) []string {
	argv := []string{b.Adapter, "--backend", name, "--model", model}
	if b.Effort != "" {
		argv = append(argv, "--effort", b.Effort)
	}
	if b.Account != "" {
		argv = append(argv, "--account", b.Account)
	}
	if b.TimeoutSeconds > 0 {
		argv = append(argv, "--timeout", strconv.Itoa(b.TimeoutSeconds))
	}
	// The separator is written only when there is something after it: a row with no harness
	// arguments must stage the same argv it always did, because the adapters that take none
	// refuse a `--` they do not know.
	if len(b.HarnessArgs) > 0 {
		argv = append(argv, "--")
		argv = append(argv, b.HarnessArgs...)
	}
	return argv
}

// Route answers the model id this request's adapter was handed, read back out of the
// request's own argv — the `--model` element command wrote — and never re-drawn here.
//
// The draw happens once, at staging, and the argv is the finished artifact: a record that
// re-derived the route from the backend's rotation would be a second draw that can
// disagree with the process that actually ran, and the disagreement would be invisible
// because both values look like a route. Reading the argv is what makes the record's
// answer the adapter's own, which is the attribution an eval needs (a wire model reached
// through several client ids stays attributable per run).
//
// A request whose argv names no model — a fixture run, or a request written by hand —
// answers the empty string, and its record then carries no route rather than an invented
// one.
func (r Request) Route() string {
	if r.Backend == "command" {
		return ""
	}
	for i, element := range r.Command {
		if element == "--model" && i+1 < len(r.Command) {
			return r.Command[i+1]
		}
	}
	return ""
}

// route answers the model id this run draws from the backend's rotation.
//
// The draw is uniform over the rotation and keyed by the run id, so two runs of one tick land
// on different routes while two plans of one tick produce the same bytes — the determinism
// `plan` promises, kept without fixing the rotation to one id. A backend with a single model
// (or only `model`) has nothing to draw: it answers that id.
func (b Backend) route(runID string) string {
	if len(b.Models) == 0 {
		return b.Model
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(runID))
	return b.Models[int(h.Sum32()%uint32(len(b.Models)))]
}

// RequestRecord wraps a request in design §5.1's envelope, so the record is one `exec`
// consumes as it stands. The envelope is derived here for the same reason the request is:
// the loop and the `request` face both emit this record, and a second spelling of it is a
// second place to get its kind, id or subject wrong.
//
// producer is the emitting utility's, not this package's: drain emits the requests it plans,
// and the run utility emits the one a standalone driver asked for, so the record says who
// made it.
func RequestRecord(req Request, producer record.Producer, at time.Time) (record.Envelope, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the run.request for %q: %v", req.RunID, err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          RequestKind,
		ID:            "run-request-" + req.RunID,
		Producer:      producer,
		CreatedAt:     at.UTC(),
		Subject:       &record.Subject{Kind: "run", ID: req.RunID},
		// The request's correlation is the tick's, carried on the envelope and in the
		// payload: a consumer that decodes only the payload still has the key, and one that
		// reads only the envelope has the same value.
		CorrelationID: req.CorrelationID,
		Payload:       body,
	}, nil
}
