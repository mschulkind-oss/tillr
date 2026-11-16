package drain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

// twoBackends is a policy whose preference walks a backend it names: the shape a failover
// needs, and the shape a single-backend policy degenerates from.
func twoBackends() Policy {
	return Policy{
		DefaultBackend: "first",
		Preference:     []string{"first", "second"},
		Backends: map[string]Backend{
			"first": {
				Adapter: "/adapter/first", Models: []string{"route-a", "route-b", "route-c"},
				Effort: "high", TimeoutSeconds: 1800,
			},
			"second": {Adapter: "/adapter/second", Model: "route-z", TimeoutSeconds: 60},
		},
	}
}

// A policy is refused before the store is read when a backend cannot produce a complete
// request: each refusal names the field, so an operator fixing the document does not have
// to guess which entry is wrong.
func TestPolicyCheckRefusesADocumentThatCannotBuildARequest(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Policy)
		names  string
	}{
		"no backends": {
			mutate: func(p *Policy) { p.Backends = nil },
			names:  "no backends",
		},
		"no default backend": {
			mutate: func(p *Policy) { p.DefaultBackend = "" },
			names:  "default_backend",
		},
		"default not one of the backends": {
			mutate: func(p *Policy) { p.DefaultBackend = "third" },
			names:  `"third"`,
		},
		"default not the preference head": {
			mutate: func(p *Policy) { p.Preference = []string{"second", "first"} },
			names:  "head of its preference",
		},
		"preference names an unknown backend": {
			mutate: func(p *Policy) { p.Preference = []string{"first", "third"} },
			names:  `"third"`,
		},
		"preference names a backend twice": {
			mutate: func(p *Policy) { p.Preference = []string{"first", "first"} },
			names:  "twice",
		},
		"negative failover budget": {
			mutate: func(p *Policy) { n := -1; p.MaxFailovers = &n },
			names:  "max_failovers",
		},
		"backend with no adapter": {
			mutate: func(p *Policy) {
				b := p.Backends["second"]
				b.Adapter = ""
				p.Backends["second"] = b
			},
			names: `"second" names no adapter`,
		},
		"backend with no model": {
			mutate: func(p *Policy) {
				b := p.Backends["second"]
				b.Model = ""
				p.Backends["second"] = b
			},
			names: `"second" names no model`,
		},
		"backend with an empty route": {
			mutate: func(p *Policy) {
				b := p.Backends["first"]
				b.Models = []string{"route-a", ""}
				p.Backends["first"] = b
			},
			names: "empty route",
		},
		"override names an unknown backend": {
			mutate: func(p *Policy) { p.Overrides = map[string]string{"item_042": "third"} },
			names:  `"third"`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			policy := twoBackends()
			c.mutate(&policy)
			err := policy.Check()
			if err == nil {
				t.Fatalf("the policy was accepted, want a refusal naming %q", c.names)
			}
			if !strings.Contains(err.Error(), c.names) {
				t.Fatalf("the refusal %q does not name %q", err, c.names)
			}
			if code := ExitCode(err); code != 2 {
				t.Fatalf("the refusal exits %d, want 2", code)
			}
		})
	}
}

// A wall is optional and a negative one is not: 0 means the run is unbounded (its lane
// lease carries no expiry, design D-RW1), so the policy accepts it and refuses -1 with a
// refusal that names the backend and the field.
func TestPolicyCheckRefusesANegativeTimeout(t *testing.T) {
	policy := twoBackends()
	backend := policy.Backends["first"]
	backend.TimeoutSeconds = -1
	policy.Backends["first"] = backend

	err := policy.Check()
	if err == nil {
		t.Fatalf("a backend with timeout_seconds -1 was accepted")
	}
	if !strings.Contains(err.Error(), `backend "first" names timeout_seconds`) {
		t.Fatalf("the refusal %q does not name the backend and the field", err)
	}

	unbounded := twoBackends()
	entry := unbounded.Backends["first"]
	entry.TimeoutSeconds = 0
	unbounded.Backends["first"] = entry
	if err := unbounded.Check(); err != nil {
		t.Fatalf("a backend with no wall was refused: %v", err)
	}
	request := unbounded.request("item_042", "lane-1", "run-1", "first")
	if request.TimeoutMS != 0 {
		t.Fatalf("an unbounded request carries timeout_ms %d, want 0", request.TimeoutMS)
	}
	for _, element := range request.Command {
		if element == "--timeout" {
			t.Fatalf("an unbounded request passes a wall to the adapter: %v", request.Command)
		}
	}
}

// A policy that passes the check builds a complete request: the timeout in the unit run
// reads, the backend name, and the adapter's argv with the model id staging supplied.
func TestPolicyBuildsACompleteRequestFromTheBackend(t *testing.T) {
	policy := twoBackends()
	request := policy.request("item_042", "lane-1", "run-item_042-20261110T000000Z", "first")

	if request.TimeoutMS != 1800*1000 {
		t.Fatalf("the request's timeout_ms is %d, want 1800000 from timeout_seconds 1800", request.TimeoutMS)
	}
	if request.Backend != "first" || request.LaneID != "lane-1" || request.ItemID != "item_042" {
		t.Fatalf("the request names backend %q / lane %q / item %q", request.Backend, request.LaneID, request.ItemID)
	}
	if request.WorkingDir != WorktreeDir || request.EnvRefs == nil {
		t.Fatalf("the request's working_dir %q / env_refs %v are not the defaults", request.WorkingDir, request.EnvRefs)
	}
	want := []string{"/adapter/first", "--backend", "first", "--model", routeIn(t, request), "--effort", "high", "--timeout", "1800"}
	if strings.Join(request.Command, " ") != strings.Join(want, " ") {
		t.Fatalf("the request's command is %v, want %v", request.Command, want)
	}
}

// routeIn answers the route the request drew, by reading it back out of the argv — which is
// where the design puts a model id, and therefore the only place a reader has.
func routeIn(t *testing.T, request *run.Request) string {
	t.Helper()
	argv := request.Command
	for i, element := range argv {
		if element == "--model" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("the request's argv %v names no model", argv)
	return ""
}

// A backend whose harness has no effort flag is handed no effort flag: the policy names
// none for it, and an adapter default would be a value in the tillr tree the policy did
// not supply.
func TestPolicyOmitsTheEffortFlagWhenTheBackendNamesNone(t *testing.T) {
	policy := twoBackends()
	request := policy.request("item_042", "lane-1", "run-item_042-20261110T000000Z", "second")
	for _, element := range request.Command {
		if element == "--effort" {
			t.Fatalf("the argv %v carries an effort the policy did not name", request.Command)
		}
	}
}

// An override chooses the item's first backend and outranks the preference; the preference
// orders everything after that, forward only, and a backend reached by an override that the
// preference does not name starts the walk at its head.
func TestSelectionFollowsOverrideThenPreference(t *testing.T) {
	policy := twoBackends()
	policy.Overrides = map[string]string{"item_042": "second"}

	if got := policy.firstBackend(itemRow{ItemID: "item_042"}); got != "second" {
		t.Fatalf("the overridden item's first backend is %q, want second", got)
	}
	if got := policy.firstBackend(itemRow{ItemID: "item_043"}); got != "first" {
		t.Fatalf("the un-overridden item's first backend is %q, want the default", got)
	}
	if got, degraded, ok := policy.nextBackend("first", ""); !ok || got != "second" || degraded != "" {
		t.Fatalf("the failover after first is %q / %q / %v, want second with no seat", got, degraded, ok)
	}
	if got, _, ok := policy.nextBackend("second", ""); ok {
		t.Fatalf("the failover after the last backend is %q, want no hop", got)
	}
	if got, degraded, ok := policy.nextBackend("unlisted", ""); !ok || got != "first" || degraded != "" {
		t.Fatalf("the failover after an unlisted backend is %q / %q / %v, want the preference head", got, degraded, ok)
	}
}

// A seat is a statement about the work, so a failover that leaves the seat's backend is a
// model switch and the walk says so: the answer names the seat that fell back. A walk whose
// failed backend is not the seat's backend (one an override reached) is an ordinary
// failover and names no seat.
func TestAFailoverOffTheSeatsBackendNamesTheSeat(t *testing.T) {
	policy := twoBackends()
	policy.Seats = map[string]string{"design": "designer", "build": "first"}

	// The seat's backend is not in the preference at all — the shape the design seat has in
	// the shipped policy (`pi-designer` is a model of the pi adapter, not a preference hop) —
	// so the walk starts at the head and leaves the seat.
	if got, degraded, ok := policy.nextBackend("designer", "design"); !ok || got != "first" || degraded != "design" {
		t.Fatalf("the failover off the design seat is %q / %q / %v, want first / design / true", got, degraded, ok)
	}
	// The seat's backend is in the preference and the walk continues along it: still a model
	// switch, because the seat names exactly one backend.
	if got, degraded, ok := policy.nextBackend("first", "build"); !ok || got != "second" || degraded != "build" {
		t.Fatalf("the failover off the build seat is %q / %q / %v, want second / build / true", got, degraded, ok)
	}
	// The failed backend is not the seat's backend: the walk is an ordinary failover.
	policy.Seats = map[string]string{"design": "second"}
	if got, degraded, ok := policy.nextBackend("first", "design"); !ok || got != "second" || degraded != "" {
		t.Fatalf("the failover from a non-seat backend is %q / %q / %v, want second / none / true", got, degraded, ok)
	}
}

// A policy that names no failover budget carries the default, and a policy that names zero
// walks nowhere: the budget is the policy's, not the code's.
func TestFailoverBudgetComesFromThePolicy(t *testing.T) {
	policy := twoBackends()
	if got := policy.failovers(); got != DefaultMaxFailovers {
		t.Fatalf("a policy naming no budget has %d failovers, want the default %d", got, DefaultMaxFailovers)
	}
	none := 0
	policy.MaxFailovers = &none
	if got := policy.failovers(); got != 0 {
		t.Fatalf("a policy naming zero failovers has %d", got)
	}
}

// A seat is a role, and the seat map is where a role names a backend: an item that asks for
// the design seat reaches the backend the policy points that seat at, with no item-id
// override anywhere. An override is the more specific statement and outranks the seat; an
// item that asks for no seat gets the tick's default.
func TestSelectionFollowsOverrideThenSeatThenDefault(t *testing.T) {
	policy := twoBackends()
	policy.Seats = map[string]string{"design": "second", "build": "first"}

	if got := policy.firstBackend(itemRow{ItemID: "item_042", Seat: "design"}); got != "second" {
		t.Fatalf("the design seat's backend is %q, want second", got)
	}
	if got := policy.firstBackend(itemRow{ItemID: "item_042", Seat: "build"}); got != "first" {
		t.Fatalf("the build seat's backend is %q, want first", got)
	}
	if got := policy.firstBackend(itemRow{ItemID: "item_042"}); got != "first" {
		t.Fatalf("an item asking for no seat routes to %q, want the default", got)
	}

	policy.Overrides = map[string]string{"item_042": "first"}
	if got := policy.firstBackend(itemRow{ItemID: "item_042", Seat: "design"}); got != "first" {
		t.Fatalf("the overridden item's seat routed it to %q, want the override's first", got)
	}
}

// A policy that maps a seat it cannot route is refused before the store is read: an unknown
// backend would make a dispatch's request name a backend the policy does not hold, and a
// seat name outside the item model's vocabulary is a typo that would silently route nothing.
func TestPolicyCheckRefusesSeatsItCannotRoute(t *testing.T) {
	cases := map[string]struct {
		seats map[string]string
		names string
	}{
		"seat mapped to an unknown backend": {
			seats: map[string]string{"design": "third"},
			names: `seat "design" to backend "third"`,
		},
		"seat outside the vocabulary": {
			seats: map[string]string{"desgn": "first"},
			names: `seat "desgn", which is not one of the item model's seats`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			policy := twoBackends()
			policy.Seats = c.seats
			err := policy.Check()
			if err == nil {
				t.Fatalf("the policy was accepted, want a refusal naming %q", c.names)
			}
			if !strings.Contains(err.Error(), c.names) {
				t.Fatalf("the refusal %q does not name %q", err, c.names)
			}
			if code := ExitCode(err); code != 2 {
				t.Fatalf("the refusal exits %d, want 2", code)
			}
		})
	}
}

// seatListPolicy is the shape docs/design/tillr-claude-backend.md §4 ships: each seat names
// its own ordered fallback, decoded from the document's array form, beside a seat that
// still names one backend as a plain string.
func seatListPolicy(t *testing.T) Policy {
	t.Helper()
	var policy Policy
	doc := `{
	  "default_backend": "builder-a",
	  "preference": ["builder-a", "builder-b"],
	  "seats": {
	    "design": ["designer-a", "designer-b"],
	    "build": ["builder-a", "builder-b"],
	    "review": ["builder-a"],
	    "scout": "builder-b"
	  },
	  "backends": {
	    "builder-a": {"adapter": "/adapter/a", "model": "model-x"},
	    "builder-b": {"adapter": "/adapter/b", "model": "model-y"},
	    "designer-a": {"adapter": "/adapter/a", "model": "model-z"},
	    "designer-b": {"adapter": "/adapter/b", "model": "model-w"}
	  }
	}`
	if err := json.Unmarshal([]byte(doc), &policy); err != nil {
		t.Fatalf("decoding a policy with seat lists failed: %v", err)
	}
	if err := policy.Check(); err != nil {
		t.Fatalf("a policy with seat lists fails its check: %v", err)
	}
	return policy
}

// A seat's value decodes in both shapes: a string is today's one backend, an array is an
// ordered list whose first name is where the seat dispatches. Seats keeps the head either
// way, so code that reads Seats sees the same answer it did before lists existed.
func TestASeatDecodesFromAStringOrAList(t *testing.T) {
	policy := seatListPolicy(t)
	for seat, want := range map[string]string{"design": "designer-a", "build": "builder-a", "review": "builder-a", "scout": "builder-b"} {
		if got := policy.Seats[seat]; got != want {
			t.Fatalf("seat %q maps to %q, want its first backend %q", seat, got, want)
		}
		if got := policy.firstBackend(itemRow{ItemID: "item_042", Seat: seat}); got != want {
			t.Fatalf("an item on seat %q first dispatches to %q, want %q", seat, got, want)
		}
	}
	if got := strings.Join(policy.seatList("design"), ","); got != "designer-a,designer-b" {
		t.Fatalf("the design seat's list is %q", got)
	}
	if _, ok := policy.SeatLists["scout"]; ok {
		t.Fatalf("a seat written as a string grew a list: %v", policy.SeatLists)
	}
}

// A failover for an item on a listed seat walks that seat's own list forward and stops at
// its end, and a step along the list is not a degradation: every backend on it was named for
// that seat. A one-element seat and an item with no seat walk the preference as they always
// did, including naming the seat a walk leaves.
func TestAFailoverWalksTheItemsOwnSeatList(t *testing.T) {
	policy := seatListPolicy(t)
	cases := []struct {
		name, failed, seat string
		next, degraded     string
		ok                 bool
	}{
		{"the design list's next backend", "designer-a", "design", "designer-b", "", true},
		{"the end of the design list", "designer-b", "design", "", "", false},
		{"the build list's next backend", "builder-a", "build", "builder-b", "", true},
		{"the end of the build list", "builder-b", "build", "", "", false},
		{"a backend off the list starts at its head", "builder-b", "design", "designer-a", "", true},
		{"a one-element seat walks the preference and names the seat", "builder-a", "review", "builder-b", "review", true},
		{"no seat walks the preference", "builder-a", "", "builder-b", "", true},
		{"no seat stops at the preference's end", "builder-b", "", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next, degraded, ok := policy.nextBackend(c.failed, c.seat)
			if next != c.next || degraded != c.degraded || ok != c.ok {
				t.Fatalf("after %q on seat %q the walk is %q / %q / %v, want %q / %q / %v",
					c.failed, c.seat, next, degraded, ok, c.next, c.degraded, c.ok)
			}
		})
	}

	// NextRequest is the walk the loop performs inside a tick, so it must follow the same
	// list and stop at the same end.
	request, degraded, ok := policy.NextRequest("item_042", "lane-1", "run-2", "designer-a", "design")
	if !ok || request.Backend != "designer-b" || degraded != "" {
		t.Fatalf("the loop's failover off designer-a is %+v / %q / %v, want designer-b, not degraded", request, degraded, ok)
	}
	if _, _, ok := policy.NextRequest("item_042", "lane-1", "run-3", "designer-b", "design"); ok {
		t.Fatalf("the loop's failover walked past the end of the design seat's list")
	}
}

// Every name in a seat's list must be one of the policy's backends, and the refusal names
// the seat so the operator knows which list to fix. An empty list, a name listed twice, and
// a value that is neither a string nor a list of strings are refused too.
func TestPolicyCheckRefusesASeatListItCannotWalk(t *testing.T) {
	base := func(seats string) string {
		return `{"default_backend": "builder-a", "preference": ["builder-a"], "seats": ` + seats + `,
		  "backends": {"builder-a": {"adapter": "/a", "model": "model-x"}, "builder-b": {"adapter": "/b", "model": "model-y"}}}`
	}
	refused := map[string]struct {
		seats string
		names string
	}{
		"an undeclared backend": {`{"design": ["builder-a", "nobody"]}`, `seat "design" to backend "nobody", which is not one of its backends`},
		"an empty list":         {`{"build": []}`, `seat "build" to an empty list`},
		"a backend twice":       {`{"build": ["builder-a", "builder-b", "builder-a"]}`, `seat "build" to backend "builder-a" twice`},
		"an unknown seat":       {`{"desgn": ["builder-a"]}`, `seat "desgn", which is not one of the item model's seats`},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			var policy Policy
			if err := json.Unmarshal([]byte(base(c.seats)), &policy); err != nil {
				t.Fatalf("decoding failed before the check could refuse: %v", err)
			}
			err := policy.Check()
			if err == nil {
				t.Fatalf("the policy was accepted, want a refusal naming %q", c.names)
			}
			if !strings.Contains(err.Error(), c.names) {
				t.Fatalf("the refusal %q does not name %q", err, c.names)
			}
			if code := ExitCode(err); code != 2 {
				t.Fatalf("the refusal exits %d, want 2", code)
			}
		})
	}

	for _, seats := range []string{`{"build": 7}`, `{"build": ["builder-a", 7]}`} {
		var policy Policy
		if err := json.Unmarshal([]byte(base(seats)), &policy); err == nil {
			t.Fatalf("the seats value %s decoded, want a refusal", seats)
		}
	}

	// A list built in code whose head disagrees with Seats would dispatch to one backend
	// and fall back as though it had started at another.
	policy := seatListPolicy(t)
	policy.Seats["design"] = "builder-a"
	if err := policy.Check(); err == nil || !strings.Contains(err.Error(), "read two ways") {
		t.Fatalf("a seat whose head disagrees with its list was not refused: %v", err)
	}
}
