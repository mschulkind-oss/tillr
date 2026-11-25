// Reference: docs/reference/tillr-muse-backend.md
package drain

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/workflow"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
)

// Backend is one named backend in the policy: the adapter program staging puts in the
// request's argv, the model data it hands that adapter, and the bound its runs are held to.
//
// The type is run's, because what it holds is the request-shaped half of a backend and run
// derives the request from it: drain's policy names the values, and run spells them into the
// record both this loop and the standalone `run request` face emit. A second struct here
// would be a second spelling of the values a request is built from.
type Backend = run.Backend

// Policy is the document `policy_ref` names: the backends a tick may dispatch to, the one
// it dispatches to by default, the seat assignments, and the per-item overrides
// that choose a backend for individual items (docs/reference/tillr-muse-backend.md, D-MM8).
//
// Per-item selection lives here and never on the item: a backend is an execution choice
// made at dispatch time, so it belongs where changing it is one policy edit rather than a
// mutation of the item's description of the work. The item record gains no backend field.
//
// A-POLICY: design §5.5 requires a dispatch decision to carry a *complete* run.request,
// and run exec refuses a request with no command, no backend or no positive timeout. None
// of those is derivable from the store — no item column and no lane column names one — and
// design §13.1 settles whose it is: drain emits the request, and what drain decides is
// "whatever policy_ref names". So the policy's shape is drain's own, and Check is the
// refusal that keeps a dispatch from carrying a request no runner would accept.
type Policy struct {
	ReadOnlySeats []string          `json:"read_only_seats,omitempty"`
	SeatPurposes  map[string]string `json:"seat_purposes,omitempty"`
	Ref           string            `json:"-"` // Source path, when loaded from a file.
	Root          string            `json:"-"` // Root repository for relative adapter paths.

	DefaultBackend string `json:"default_backend"`
	// Seats maps each seat the item model defines (seat.go) to the backend that fills it. It is where a role reaches a model: an item names the seat and never a model
	// id, so swapping which model designs, or moving the design seat to another backend, is
	// an edit here and touches no item.
	//
	// Seats holds the single backend or the head of a shared seat for previews.
	// Claims use the shares and pick to choose a shared seat's backend.
	Seats map[string]string `json:"seats,omitempty"`
	// SeatLists retains array entries from the document, including singleton arrays.
	// It is not a top-level policy key.
	SeatLists map[string][]string `json:"-"`
	// SeatShares and SeatPicks are decoded from seat entries, not top-level keys.
	SeatShares map[string][]float64 `json:"-"`
	SeatPicks  map[string]string    `json:"-"`
	seatDraw   func() float64
	// Overrides routes one named item to another backend for its first dispatch. It is the
	// specific statement next to Seats' general one, so it outranks the seat: an item that
	// needs one particular harness for one dispatch says so by id without moving the role.
	Overrides map[string]string `json:"overrides,omitempty"`
	// Escalate lists, per seat, the backends a seat obstacle climbs through
	// (seat_escalation.go). Past its end the design seat plans or splits the item.
	Escalate map[string][]string `json:"escalate,omitempty"`
	Backends map[string]Backend  `json:"backends"`
}

const invalidPolicySeatName = workflow.InvalidPolicySeatName
const defaultSeatDeclaration = workflow.DefaultSeatDeclaration

func (p Policy) seatDeclaration() string {
	if p.Ref != "" {
		return "seats are declared in " + p.Ref
	}
	return defaultSeatDeclaration
}

// UnmarshalJSON reads a policy document whose `seats` values may each be a backend name or
// an ordered list of backend names. Every other field decodes exactly as the struct tags
// say; only the seat map needs two shapes, and it is split into Seats (each seat's first
// backend) and SeatLists (the whole list, for an array).
func (p *Policy) UnmarshalJSON(raw []byte) error {
	// plain has Policy's fields and none of its methods, so decoding into it does not call
	// this function again. The outer Seats shadows the embedded one, so the `seats` key
	// lands in the two-shape decoder below.
	type plain Policy
	var doc struct {
		plain
		Seats map[string]json.RawMessage `json:"seats,omitempty"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	*p = Policy(doc.plain)
	p.Seats, p.SeatLists = nil, nil
	p.SeatShares, p.SeatPicks = map[string][]float64{}, map[string]string{}
	for seat, rawValue := range doc.Seats {
		var value seatValue
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return fmt.Errorf("seat %q: %w", seat, err)
		}
		if value.shares != nil {
			p.SeatShares[seat] = value.shares
		}
		if value.pick != "" {
			p.SeatPicks[seat] = value.pick
		}
		if p.Seats == nil {
			p.Seats = map[string]string{}
		}
		if value.list {
			if p.SeatLists == nil {
				p.SeatLists = map[string][]string{}
			}
			p.SeatLists[seat] = value.names
		}
		// An empty list has no first backend. The seat is still recorded in SeatLists so
		// Check refuses it by name, instead of the seat vanishing and every item that asks
		// for it being refused later as "not mapped".
		if len(value.names) > 0 {
			p.Seats[seat] = value.names[0]
		}
	}
	return nil
}

// seatValue is one `seats` value as written: a JSON string (one backend, today's form) or
// a list (optionally wrapped in an object specifying the pick). Check requires shares
// when the list names more than one backend.
type seatValue struct {
	names  []string
	list   bool
	shares []float64
	pick   string
}

func (v *seatValue) UnmarshalJSON(raw []byte) error {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		v.names, v.list = []string{name}, false
		return nil
	}
	var config struct {
		Backends json.RawMessage `json:"backends"`
		Pick     string          `json:"pick"`
	}
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &config); err != nil {
			return err
		}
		if config.Backends == nil {
			return fmt.Errorf("a seat object needs backends")
		}
		if err := v.UnmarshalJSON(config.Backends); err != nil {
			return err
		}
		v.pick = config.Pick
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("a seat names a backend, a list, or an object with backends and pick, got %s", raw)
	}
	v.list = true
	shared, unshared := false, false
	for _, entry := range entries {
		var name string
		var share float64
		if err := json.Unmarshal(entry, &name); err == nil {
			unshared = true
		} else {
			var object struct {
				Backend string          `json:"backend"`
				Share   json.RawMessage `json:"share"`
			}
			if err := json.Unmarshal(entry, &object); err != nil {
				return err
			}
			name = object.Backend
			if object.Share == nil {
				unshared = true
			} else {
				shared = true
				if string(object.Share) == "null" || json.Unmarshal(object.Share, &share) != nil || share <= 0 {
					return fmt.Errorf("backend %q share must be a positive number", name)
				}
			}
		}
		v.names = append(v.names, name)
		v.shares = append(v.shares, share)
	}
	if shared && unshared {
		return fmt.Errorf("a seat list must give every backend a share or none")
	}
	if !shared {
		v.shares = nil
	}
	return nil
}

// seatList answers the ordered backends a seat names: the document's list when the seat
// was written as one, else the single backend Seats maps it to, else nil for a seat the
// policy does not map.
func (p Policy) seatList(seat string) []string {
	if list, ok := p.SeatLists[seat]; ok {
		return list
	}
	if name, ok := p.Seats[seat]; ok {
		return []string{name}
	}
	return nil
}

// seatNames answers every seat the policy maps, in either field, in a stable order.
func (p Policy) seatNames() []string {
	seen := map[string]bool{}
	names := []string{}
	for seat := range p.Seats {
		if !seen[seat] {
			seen[seat] = true
			names = append(names, seat)
		}
	}
	for seat := range p.SeatLists {
		if !seen[seat] {
			seen[seat] = true
			names = append(names, seat)
		}
	}
	sort.Strings(names)
	return names
}

// Check refuses a policy that cannot produce a complete request for every backend it
// names, before the store is read: a dispatch built from such a policy would carry a
// run.request a runner refuses, which is a record asserting a request that cannot run.
//
// Every refusal names the field and the backend, so an operator fixing the document does
// not have to guess which entry is wrong. An override naming an unknown *item* cannot be
// refused here — this face has not read the item set — so Plan refuses that one, against
// the items it did read (design §2: the override map's rot is refused mechanically rather
// than left to fall through a silent default).
func (p Policy) Check() error {
	for _, seat := range p.ReadOnlySeats {
		if len(p.seatList(seat)) == 0 {
			return Usagef("read_only_seats names seat %q, which the policy does not map", seat)
		}
	}
	for seat := range p.SeatPurposes {
		if len(p.seatList(seat)) == 0 {
			return Usagef("seat_purposes names seat %q, which the policy does not map", seat)
		}
	}
	if len(p.Backends) == 0 {
		return Usagef("the policy names no backends: a dispatch's run.request needs an adapter program, a model and a timeout")
	}
	if p.DefaultBackend == "" {
		return Usagef("the policy names no default_backend: the one backend a tick dispatches to when no override names another")
	}
	if _, ok := p.Backends[p.DefaultBackend]; !ok {
		return Usagef("the policy's default_backend %q is not one of its backends: want one of %v", p.DefaultBackend, backendNames(p.Backends))
	}
	for _, name := range backendNames(p.Backends) {
		if err := checkBackend(name, p.Backends[name]); err != nil {
			return err
		}
	}
	for itemID, name := range p.Overrides {
		if _, ok := p.Backends[name]; !ok {
			return Usagef("the policy overrides item %q to backend %q, which is not one of its backends: want one of %v",
				itemID, name, backendNames(p.Backends))
		}
	}
	for _, seat := range p.seatNames() {
		if !item.ValidSeatName(seat) {
			return Usagef(invalidPolicySeatName, seat, p.seatDeclaration())
		}
		list := p.seatList(seat)
		if len(list) > 1 && len(p.SeatShares[seat]) == 0 {
			return Usagef("seat %q must name one backend or give every backend a share", seat)
		}
		if pick := p.SeatPicks[seat]; pick != "" && pick != "in-order" && pick != "round-robin" && pick != "random" {
			return Usagef("seat %q pick %q must be in-order, round-robin, or random", seat, pick)
		}
		if shares, ok := p.SeatShares[seat]; ok {
			if len(shares) != len(list) {
				return Usagef("seat %q must give every backend a share", seat)
			}
			for _, share := range shares {
				if share <= 0 || math.IsNaN(share) || math.IsInf(share, 0) {
					return Usagef("seat %q share must be a positive number", seat)
				}
			}
		}
		if len(list) == 0 {
			return Usagef("the policy maps seat %q to an empty list: a seat names at least the one backend it dispatches to", seat)
		}
		if head, ok := p.Seats[seat]; ok && head != list[0] {
			// Only code can build this; the decoder keeps the two fields in step. A seat
			// whose dispatch backend is not its list's head would be read two ways.
			return Usagef("the policy maps seat %q to backend %q but its list starts with %q: a policy is refused rather than read two ways",
				seat, head, list[0])
		}
		seen := map[string]bool{}
		for _, name := range list {
			if _, ok := p.Backends[name]; !ok {
				return Usagef("the policy maps seat %q to backend %q, which is not one of its backends: want one of %v",
					seat, name, backendNames(p.Backends))
			}
			if seen[name] {
				return Usagef("the policy maps seat %q to backend %q twice", seat, name)
			}
			seen[name] = true
		}
	}
	if err := p.checkEscalate(); err != nil {
		return Usagef("%v", err)
	}
	return nil
}

// checkBackend refuses one backend entry that cannot produce a complete request, in this
// face's own error type: the fields a request needs are run's rule (run.Backend.Missing),
// and the exit is drain's, because a policy it cannot honour is drain's usage refusal.
func checkBackend(name string, b Backend) error {
	if missing := b.Missing(); missing != "" {
		return Usagef("the policy's backend %q %s", name, missing)
	}
	return nil
}

// firstBackend answers the backend an item's first dispatch names: its per-item override
// when the policy carries one, else the backend its seat maps to, else the tick's default.
//
// The order is the specificity of the statement. An override is one item's own dispatch
// decision, so it outranks the seat, which is a statement about the kind of work, which in
// turn outranks the default backend for unseated work.
//
// The planner holds items whose explicit seat is not mapped, before it offers a
// dispatch. The default is for work that did not ask for a seat.
func (p Policy) firstBackend(row itemRow) string {
	if name, ok := p.Overrides[row.ItemID]; ok {
		return name
	}
	if row.Seat != "" {
		if list := p.seatList(row.Seat); len(list) > 0 {
			return list[0]
		}
	}
	return p.DefaultBackend
}

// request is the complete run.request one dispatch carries, built from the backend the
// item's attempt resolved to.
//
// The derivation is run.Backend.Request, the same function the `request` face calls: the
// route is drawn before the run starts and lands in the argv's `--model` element, which is
// the recorded provenance the design rules for a model id (design §2: because a
// drain.decision embeds the full run.request, a model id in argv is recorded run evidence),
// and nothing else in the request names it. This face decides which backend; run decides
// what a request on it says.
const relBackendsPrefix = "tools/tillr-run/backends/"

// resolveAdapterPath resolves a repository-relative adapter path. When the
// adapter exists under root (the trunk checkout the loop runs from, such as
// tillr's own repository), it resolves against root so workers running in a
// lane worktree execute the trunk's adapter rather than their own lane's copy.
// When root does not carry the adapter and the path is under
// tools/tillr-run/backends/, it resolves against tillr's installation via
// run.ResolveBackendsDir (honoring TILLR_INSTALL_ROOT) so a consumer repository
// with no tools/tillr-run finds the installed adapter and its side files. Bare
// command names (no path separator) are left for PATH lookup, and absolute
// paths are returned unchanged.
func resolveAdapterPath(root, adapter string) string {
	if root == "" || adapter == "" || filepath.IsAbs(adapter) || !strings.ContainsRune(adapter, filepath.Separator) {
		return adapter
	}
	base := root
	if absRoot, err := filepath.Abs(root); err == nil {
		base = absRoot
	}
	rootPath := filepath.Join(base, adapter)
	if _, err := os.Stat(rootPath); err == nil {
		return rootPath
	}
	clean := filepath.ToSlash(filepath.Clean(adapter))
	if rest, ok := strings.CutPrefix(clean, relBackendsPrefix); ok && rest != "" {
		if backendsDir, err := run.ResolveBackendsDir(); err == nil {
			return filepath.Join(backendsDir, filepath.FromSlash(rest))
		}
	}
	return rootPath
}

func (p Policy) request(itemID, laneID, runID, backendName string) *run.Request {
	backend := p.Backends[backendName]
	backend.Adapter = resolveAdapterPath(p.Root, backend.Adapter)
	request := backend.Request(itemID, laneID, runID, backendName)
	return &request
}

// backendNames answers a backend map's names in a stable order, so a refusal naming the
// candidates reads the same twice.
func backendNames(backends map[string]Backend) []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}
