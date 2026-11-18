package drain

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

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
// it dispatches to by default, the ordered preference a failover walks, the seat map that
// routes a role to a backend, and the per-item overrides that choose one item's first
// backend (design §2, D-MM8).
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
	Ref string `json:"-"` // Source path, when loaded from a file.

	DefaultBackend string   `json:"default_backend"`
	Preference     []string `json:"preference"`
	MaxFailovers   *int     `json:"max_failovers"`
	// Seats maps each seat the item model defines (seat.go) to the backend that fills it. It is where a role reaches a model: an item names the seat and never a model
	// id, so swapping which model designs, or moving the design seat to another backend, is
	// an edit here and touches no item.
	//
	// In the document a seat's value is a backend name or an ordered list of them
	// (docs/design/tillr-claude-backend.md §4, D-CB3). Seats always holds the seat's first
	// backend for ordered previews; claims apply shares or pick. SeatLists holds the list for a seat the
	// document spelled as an array. The two fields exist so that a seat which names one
	// backend reads, decodes and is written in code exactly as it was before lists existed.
	Seats map[string]string `json:"seats,omitempty"`
	// SeatLists is the ordered list of backends a seat falls back through, for a seat that
	// names more than one. A failover for an item on that seat walks this list forward
	// instead of the policy's preference, so design work falls back to a design backend
	// rather than to whatever the build seat runs. It is filled by UnmarshalJSON from the
	// `seats` array form and is never a key of its own in the document.
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

const invalidPolicySeatName = "seat %q must start with a lowercase letter and contain only lowercase letters, digits and hyphens; %s"
const defaultSeatDeclaration = "seats are declared in the backend policy file"

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
// a JSON array of strings (an ordered fallback list). Anything else is a decode error,
// which readPolicy reports as the usage refusal it reports every malformed document as.
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

// DefaultMaxFailovers is the failover budget a policy that names none carries: one
// re-dispatch per item, which is the whole multiplication a tick can do to an item's cost
// (design §2, "What bounds the spend").
const DefaultMaxFailovers = 1

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
	if len(p.Backends) == 0 {
		return Usagef("the policy names no backends: a dispatch's run.request needs an adapter program, a model and a timeout")
	}
	if p.DefaultBackend == "" {
		return Usagef("the policy names no default_backend: the one backend a tick dispatches to when no override names another")
	}
	if _, ok := p.Backends[p.DefaultBackend]; !ok {
		return Usagef("the policy's default_backend %q is not one of its backends: want one of %v", p.DefaultBackend, backendNames(p.Backends))
	}
	if len(p.Preference) > 0 && p.Preference[0] != p.DefaultBackend {
		// A default that is not the preference's head would be read two ways: as what a
		// tick stamps and as where a failover starts. Refused rather than read twice.
		return Usagef("the policy's default_backend %q is not the head of its preference %v: a policy is refused rather than read two ways",
			p.DefaultBackend, p.Preference)
	}
	seen := map[string]bool{}
	for _, name := range p.Preference {
		if _, ok := p.Backends[name]; !ok {
			return Usagef("the policy's preference names %q, which is not one of its backends: want one of %v", name, backendNames(p.Backends))
		}
		if seen[name] {
			return Usagef("the policy's preference names %q twice: the walk would visit it twice", name)
		}
		seen[name] = true
	}
	if p.MaxFailovers != nil && *p.MaxFailovers < 0 {
		return Usagef("the policy names max_failovers %d: a failover budget is zero or more re-dispatches", *p.MaxFailovers)
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
				return Usagef("the policy maps seat %q to backend %q twice: the fallback walk would visit it twice", seat, name)
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

// failovers is the failover budget, with the policy's default filled in: a policy that
// names none carries one re-dispatch per item.
func (p Policy) failovers() int {
	if p.MaxFailovers == nil {
		return DefaultMaxFailovers
	}
	return *p.MaxFailovers
}

// firstBackend answers the backend an item's first dispatch names: its per-item override
// when the policy carries one, else the backend its seat maps to, else the tick's default.
//
// The order is the specificity of the statement. An override is one item's own dispatch
// decision, so it outranks the seat, which is a statement about the kind of work, which in
// turn outranks the backend an item that asked for nothing gets (design §2's precedence: a
// statement about the work outranks the preference for the first dispatch).
//
// A seat the policy does not map never reaches here: Plan refuses such a plan before it
// builds a decision (checkSeats), because the alternative is dispatching design work to the
// build model and recording it as intended. The fall-through below is therefore the default
// backend for the item that asked for no seat, not a silent default for one that did.
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

// nextBackend answers the backend a failover after `failed` walks to: the next one in the
// preference order, never one before it (design §2, "never backward"). A backend that is
// not in the preference at all — one reached by an override — starts the walk at the head,
// because there is no position in the order to continue from.
//
// It also answers the seat whose backend the walk leaves, or "" when it leaves no seat. A
// seat is a statement about the work — the design seat runs on the design backend — so a
// walk that leaves that backend is a model switch, and it is named rather than performed
// silently: the caller records the seat on the decision and spells it into the reason. The
// seat is the item's, not a reverse lookup of the backend: several seats may share one
// backend (build, review and scout all reach the default), and only the failed backend's
// identity against the item's own seat says whether the walk is a degradation.
//
// It answers false when the walk has nowhere to go, which the caller reports as the
// item's failover budget being spent rather than dispatching on the backend that just
// failed.
//
// A seat that names a list of backends (docs/design/tillr-claude-backend.md §4) has its own
// order, and that list replaces the preference for the item's walk: forward from the failed
// backend, and no hop at the list's end, so a design item never falls onto the build
// backend. Every backend on the list was named for that seat, so a step along it is not a
// degradation and names no seat. A failed backend that is not on the list (one an
// override reached) has no position to continue from, so the walk starts at the list's
// head, the same rule the preference walk uses. A seat with one backend, and an item with
// no seat, walk the preference exactly as before lists existed.
func (p Policy) nextBackend(failed, seat string) (backend, degraded string, ok bool) {
	if list := p.seatList(seat); seat != "" && len(list) > 1 {
		for i, name := range list {
			if name != failed {
				continue
			}
			if i+1 < len(list) {
				return list[i+1], "", true
			}
			return "", "", false
		}
		return list[0], "", true
	}
	seatBackend := ""
	if seat != "" {
		seatBackend = p.Seats[seat]
	}
	leavesSeat := func(next string) string {
		if seatBackend != "" && failed == seatBackend && next != seatBackend {
			return seat
		}
		return ""
	}
	for i, name := range p.Preference {
		if name != failed {
			continue
		}
		if i+1 < len(p.Preference) {
			next := p.Preference[i+1]
			return next, leavesSeat(next), true
		}
		return "", "", false
	}
	if len(p.Preference) > 0 {
		next := p.Preference[0]
		return next, leavesSeat(next), true
	}
	return "", "", false
}

// NextRequest is the request a failover after failedBackend dispatches: the same item on
// the same lane, on the next backend in the policy's preference. It also answers the seat
// the walk left, or "" when the walk leaves no seat, so the caller can record the
// degradation rather than let a model switch pass as an ordinary failover. It answers false
// when the walk has no hop left, which the caller reports as an exhausted failover budget
// rather than spending the backend that just failed.
//
// It is exported because the loop performs the one failover the design permits inside a
// tick (design §4.1 step 5), and the walk is the policy's: a second spelling of "the next
// backend" outside this file is a second answer to the same question.
func (p Policy) NextRequest(itemID, laneID, runID, failedBackend, seat string) (*run.Request, string, bool) {
	next, degraded, ok := p.nextBackend(failedBackend, seat)
	if !ok {
		return nil, "", false
	}
	return p.request(itemID, laneID, runID, next), degraded, true
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
func (p Policy) request(itemID, laneID, runID, backendName string) *run.Request {
	request := p.Backends[backendName].Request(itemID, laneID, runID, backendName)
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
