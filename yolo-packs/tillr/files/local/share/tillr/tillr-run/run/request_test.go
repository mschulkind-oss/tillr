package run_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
)

// rotation is a backend whose adapter is handed one of three client ids, which is how one
// wire model reached through several routes stays attributable per run.
func rotation() run.Backend {
	return run.Backend{
		Adapter: "/adapter/first", Models: []string{"route-a", "route-b", "route-c"},
		Effort: "high", TimeoutSeconds: 1800,
	}
}

// routeIn answers the route a request drew, by reading it back out of the argv — which is
// where the design puts a model id, and therefore the only place a reader has.
func routeIn(t *testing.T, request run.Request) string {
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

// The route is drawn from the backend's rotation, is one of its ids, and is not fixed to one
// id across runs: the wire id collapses the routes to one, so this draw is the only
// per-request attribution an eval has. The draw is keyed by the run id, so two derivations of
// one run produce the same bytes.
func TestTheRouteIsDrawnFromTheBackendsRotation(t *testing.T) {
	backend := rotation()
	drawn := map[string]int{}
	for i := 0; i < 200; i++ {
		route := routeIn(t, backend.Request("item_042", "lane-1", fmt.Sprintf("run-item_%d-20261110T000000Z", i), "first"))
		found := false
		for _, model := range backend.Models {
			if model == route {
				found = true
			}
		}
		if !found {
			t.Fatalf("the drawn route %q is not one of the backend's models %v", route, backend.Models)
		}
		drawn[route]++
	}
	if len(drawn) != len(backend.Models) {
		t.Fatalf("200 runs drew %d of the backend's %d routes: %v", len(drawn), len(backend.Models), drawn)
	}
	first := routeIn(t, backend.Request("item_042", "lane-1", "run-item_042-20261110T000000Z", "first"))
	second := routeIn(t, backend.Request("item_042", "lane-1", "run-item_042-20261110T000000Z", "first"))
	if first != second {
		t.Fatalf("one run id drew %q then %q", first, second)
	}
	// A backend with one model has nothing to draw.
	single := run.Backend{Adapter: "/adapter/second", Model: "route-z", TimeoutSeconds: 60}
	if got := routeIn(t, single.Request("item_042", "lane-1", "run-1", "second")); got != "route-z" {
		t.Fatalf("a single-model backend drew %q, want its model", got)
	}
}

// A backend that passes Missing builds a complete request: the bound in the unit run reads,
// the lane's worktree as the working directory, and the adapter's argv with the backend name
// it must be told to be.
func TestABackendDerivesACompleteRequest(t *testing.T) {
	request := rotation().Request("item_042", "lane-1", "run-item_042-20261110T000000Z", "first")
	if request.TimeoutMS != 1800*1000 {
		t.Fatalf("the request's timeout_ms is %d, want 1800000 from timeout_seconds 1800", request.TimeoutMS)
	}
	if request.RunID != "run-item_042-20261110T000000Z" || request.ItemID != "item_042" ||
		request.LaneID != "lane-1" || request.Backend != "first" {
		t.Fatalf("the request names run %q / item %q / lane %q / backend %q",
			request.RunID, request.ItemID, request.LaneID, request.Backend)
	}
	if request.WorkingDir != run.WorktreeDir || request.EnvRefs == nil {
		t.Fatalf("the request's working_dir %q / env_refs %v are not the defaults", request.WorkingDir, request.EnvRefs)
	}
	// The wall is on the argv as well as in the request, so the runner's bound and the
	// adapter's cannot silently diverge.
	want := []string{"/adapter/first", "--backend", "first", "--model", routeIn(t, request), "--effort", "high", "--timeout", "1800"}
	if strings.Join(request.Command, " ") != strings.Join(want, " ") {
		t.Fatalf("the request's command is %v, want %v", request.Command, want)
	}
}

// A backend naming a working directory of its own keeps it: the default is a default, and a
// policy that names a value has named one.
func TestABackendKeepsTheWorkingDirItNames(t *testing.T) {
	backend := rotation()
	backend.WorkingDir = "elsewhere"
	if got := backend.Request("item_042", "lane-1", "run-1", "first").WorkingDir; got != "elsewhere" {
		t.Fatalf("the request's working_dir is %q, want the backend's", got)
	}
}

// A row's harness arguments reach the adapter after a `--` and in their own order, decoded
// straight from the policy's JSON, so a tool list or a quoted prompt arrives as one argument
// each. A row with none, or an empty list, adds no separator: the adapters that take no
// harness arguments refuse a `--`, so an argv that grew one would break every such row.
func TestHarnessArgsFollowASeparatorAtTheEndOfTheArgv(t *testing.T) {
	var backend run.Backend
	if err := json.Unmarshal([]byte(`{"adapter":"/adapter/first","model":"model-x","effort":"medium",
		"harness_args":["--tools","Read,Edit,Bash","--append-system-prompt","two words"]}`), &backend); err != nil {
		t.Fatalf("decoding a row with harness_args failed: %v", err)
	}
	got := backend.Request("item_042", "lane-1", "run-1", "first").Command
	want := []string{"/adapter/first", "--backend", "first", "--model", "model-x", "--effort", "medium",
		"--", "--tools", "Read,Edit,Bash", "--append-system-prompt", "two words"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("the request's command is %q, want %q", got, want)
	}

	for _, row := range []string{
		`{"adapter":"/adapter/first","model":"model-x"}`,
		`{"adapter":"/adapter/first","model":"model-x","harness_args":[]}`,
	} {
		var plain run.Backend
		if err := json.Unmarshal([]byte(row), &plain); err != nil {
			t.Fatalf("decoding %s failed: %v", row, err)
		}
		for _, element := range plain.Request("item_042", "lane-1", "run-1", "first").Command {
			if element == "--" {
				t.Fatalf("the row %s staged a separator with nothing after it", row)
			}
		}
	}

	// The field is typed: a string where a list belongs is a policy the decoder refuses,
	// rather than one argument split by a guess.
	var wrong run.Backend
	if err := json.Unmarshal([]byte(`{"adapter":"/a","model":"m","harness_args":"--tools Read"}`), &wrong); err == nil {
		t.Fatalf("a harness_args string was accepted, want a decode refusal")
	}
}

// Missing names the first field that makes a request unrunnable, so the policy that refuses
// the document and the face that would emit the record both say which field is wrong.
func TestMissingNamesTheFieldARequestNeeds(t *testing.T) {
	cases := map[string]struct {
		mutate func(*run.Backend)
		names  string
	}{
		"no adapter":       {func(b *run.Backend) { b.Adapter = "" }, "names no adapter"},
		"negative timeout": {func(b *run.Backend) { b.TimeoutSeconds = -1 }, "names timeout_seconds -1"},
		"no model":         {func(b *run.Backend) { b.Model, b.Models = "", nil }, "names no model"},
		"empty route":      {func(b *run.Backend) { b.Models = []string{"route-a", ""} }, "empty route at models[1]"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			backend := rotation()
			c.mutate(&backend)
			if got := backend.Missing(); !strings.Contains(got, c.names) {
				t.Fatalf("Missing answered %q, want it to name %q", got, c.names)
			}
		})
	}
	if got := rotation().Missing(); got != "" {
		t.Fatalf("a complete backend is missing %q", got)
	}
	// A backend with no wall is complete: 0 means unbounded, not missing (design D-RW1).
	unbounded := rotation()
	unbounded.TimeoutSeconds = 0
	if got := unbounded.Missing(); got != "" {
		t.Fatalf("a backend with no wall is missing %q", got)
	}
	// ...and its request carries no wall and no `--timeout`, so neither the runner nor the
	// adapter invents one.
	request := unbounded.Request("item_042", "lane-1", "run-1", "first")
	if request.TimeoutMS != 0 {
		t.Fatalf("an unbounded request carries timeout_ms %d, want 0", request.TimeoutMS)
	}
	for _, element := range request.Command {
		if element == "--timeout" {
			t.Fatalf("an unbounded request passes a wall to the adapter: %v", request.Command)
		}
	}
}

// RequestRecord is design §5.1's envelope around the request, produced by the caller that
// emitted it: the loop and the standalone face emit one record shape, and the producer names
// which one made it.
func TestRequestRecordCarriesTheRequestInItsEnvelope(t *testing.T) {
	at := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	producer := record.Producer{System: record.System, Utility: "run", Version: run.Version}
	env, err := run.RequestRecord(rotation().Request("item_042", "lane-1", "run-1", "first"), producer, at)
	if err != nil {
		t.Fatalf("building the record failed: %v", err)
	}
	if env.Kind != run.RequestKind || env.ID != "run-request-run-1" {
		t.Fatalf("the record is %q %q, want a %s", env.Kind, env.ID, run.RequestKind)
	}
	if env.Subject == nil || env.Subject.Kind != "run" || env.Subject.ID != "run-1" {
		t.Fatalf("the record's subject is %+v", env.Subject)
	}
	if !env.CreatedAt.Equal(at) {
		t.Fatalf("the record is stamped %s, want the injected clock", env.CreatedAt)
	}
	if env.Producer.Utility != "run" {
		t.Fatalf("the record's producer is %+v", env.Producer)
	}
	var payload run.Request
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("the payload is not a run.request: %v", err)
	}
	if payload.RunID != "run-1" || payload.LaneID != "lane-1" || payload.Backend != "first" {
		t.Fatalf("the payload names run %q / lane %q / backend %q", payload.RunID, payload.LaneID, payload.Backend)
	}
}

// Route answers the model id the adapter was handed, read out of the argv rather than
// drawn a second time: the draw already happened at staging, and a second one could name a
// route the process never received. A request whose argv names no model has no route.
func TestRouteReadsTheModelTheAdapterWasHanded(t *testing.T) {
	request := rotation().Request("item_042", "lane-1", "run-1", "first")
	if got, want := request.Route(), routeIn(t, request); got != want {
		t.Fatalf("Route answered %q, want the argv's %q", got, want)
	}
	handed := request
	handed.Command = append([]string{}, request.Command...)
	for i, element := range handed.Command {
		if element == "--model" {
			handed.Command[i+1] = "route-handed"
		}
	}
	if got := handed.Route(); got != "route-handed" {
		t.Fatalf("Route answered %q, want the argv's %q", got, "route-handed")
	}
	for _, command := range [][]string{
		nil,
		{"/adapter/first"},
		{"/adapter/first", "--model"},
	} {
		if got := (run.Request{Command: command}).Route(); got != "" {
			t.Fatalf("the argv %v names route %q, want none", command, got)
		}
	}
}
