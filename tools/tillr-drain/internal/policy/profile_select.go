package policy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// ProfileSwitchedKind is the store event written when a quota closure or reopen
// changes the profile in force (design: tillr-quota-profile-switching §6.1).
const ProfileSwitchedKind = "drain.profile-switched"

// Switch causes recorded on the event.
const (
	CauseQuotaClosed    = "quota-closed"
	CauseQuotaReopened  = "quota-reopened"
	CauseAllQuotaClosed = "all-quota-closed"
)

func init() {
	if err := event.Register(ProfileSwitchedKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// ProfileReading is the quota evidence behind one profile decision.
type ProfileReading struct {
	Source     string        `json:"source"`
	ObservedAt *time.Time    `json:"observed_at,omitempty"`
	Reserve    float64       `json:"reserve"`
	Windows    []QuotaWindow `json:"windows,omitempty"`
	ResetAt    *time.Time    `json:"reset_at,omitempty"`
}

// ProfileSwitched is the payload of a drain.profile-switched event.
type ProfileSwitched struct {
	FromProfile    string          `json:"from_profile"`
	ToProfile      string          `json:"to_profile"`
	FromIndex      int             `json:"from_index"`
	ToIndex        int             `json:"to_index"`
	Profiles       []string        `json:"profiles"`
	Cause          string          `json:"cause"`
	TriggerBackend string          `json:"trigger_backend,omitempty"`
	QuotaGroup     string          `json:"quota_group,omitempty"`
	Reading        *ProfileReading `json:"reading,omitempty"`
}

// ProfileClosure says why a profile is quota-closed.
type ProfileClosure struct {
	Backend string
	Pool    string
	Reading ProfileReading
	// Window is the closing window, or "" for a runtime refusal.
	Window QuotaWindow
}

// ProfileInputs is everything selection reads: the loaded profiles in order,
// the stored quota readings, the unexpired runtime refusal windows and the clock.
type ProfileInputs struct {
	Policies []*Policy
	Names    []string
	Active   int
	Statuses []QuotaStatus
	Windows  map[string]time.Time
	// Closed is the backends an infrastructure ending holds closed in
	// backend_outcomes. A closed backend makes its profile not open as a target,
	// and never closes the profile in force (D-JL24).
	Closed map[string]bool
	At     time.Time
}

// ProfileSelection is the answer: the index to run, why it changed, and the
// evidence. Index equals the input Active when nothing changes.
type ProfileSelection struct {
	Index   int
	Changed bool
	Cause   string
	// Closure is the active profile's closure for switch-away and all-closed answers.
	Closure *ProfileClosure
	// Opened is the reading of a reopened target for switch-back and switch-away.
	Opened *ProfileClosure
	// Skipped lists, for every profile ranked above Index, the closure that bypasses it.
	Skipped []SkippedProfileQuota
	// Blocked is why an unchanged choice cannot move: every profile closed.
	AllClosed bool
}

// ActiveSeatBackends lists the backends the policy's default, seats, seat lists
// and escalation lists name, sorted and without duplicates.
func ActiveSeatBackends(p *Policy) []string {
	if p == nil {
		return nil
	}
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" {
			seen[name] = true
		}
	}
	add(p.DefaultBackend)
	for _, name := range p.Seats {
		add(name)
	}
	for _, list := range p.SeatLists {
		for _, name := range list {
			add(name)
		}
	}
	for _, list := range p.Escalate {
		for _, name := range list {
			add(name)
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (in ProfileInputs) status(name string) (QuotaStatus, bool) {
	for _, q := range in.Statuses {
		if q.Backend == name {
			return q, true
		}
	}
	return QuotaStatus{}, false
}

// profileState classifies one profile: closed by a verified quota condition,
// strictly open (every probed backend freshly read and under its reserve line,
// and no backend infrastructure-closed) or unknown (a stale, missing or failed
// reading, or an infrastructure closure, which never switches away).
func (in ProfileInputs) profileState(p *Policy) (closure *ProfileClosure, strictOpen bool) {
	strictOpen = true
	if p == nil {
		return nil, false
	}
	for _, name := range ActiveSeatBackends(p) {
		b, ok := p.Backends[name]
		if !ok {
			continue
		}
		if reset, shut := in.Windows[name]; shut && reset.After(in.At) {
			reset := reset
			return &ProfileClosure{Backend: name, Pool: QuotaPoolKey(name, b), Reading: ProfileReading{Source: "refusal", Reserve: b.Reserve, ResetAt: &reset}}, false
		}
		if in.Closed[name] {
			strictOpen = false
		}
		if len(b.Quota) == 0 {
			continue
		}
		q, have := in.status(name)
		command := QuotaCommandKey(b.Quota)
		if !have || !QuotaFresh(q, command, in.At) || q.Error != "" {
			strictOpen = false
			continue
		}
		for _, w := range q.Windows {
			if w.Fraction >= 1-b.Reserve {
				observed := q.ObservedAt
				return &ProfileClosure{
					Backend: name, Pool: QuotaPoolKey(name, b), Window: w,
					Reading: ProfileReading{Source: "probe", ObservedAt: &observed, Reserve: b.Reserve, Windows: q.Windows, ResetAt: w.ResetAt},
				}, false
			}
		}
	}
	return nil, strictOpen
}

// openedReading returns a representative fresh reading of a strictly open profile.
func (in ProfileInputs) openedReading(p *Policy) *ProfileClosure {
	for _, name := range ActiveSeatBackends(p) {
		b := p.Backends[name]
		if len(b.Quota) == 0 {
			continue
		}
		if q, ok := in.status(name); ok {
			observed := q.ObservedAt
			return &ProfileClosure{Backend: name, Pool: QuotaPoolKey(name, b), Reading: ProfileReading{Source: "probe", ObservedAt: &observed, Reserve: b.Reserve, Windows: q.Windows}}
		}
	}
	return nil
}

// SelectProfile picks the profile in force. It never reads a clock and never
// waits: the same readings always give the same answer.
//
//   - A higher-ranked profile than the active one retakes control as soon as it
//     is strictly open (every probed window under 1-reserve, fresh, error-free).
//   - The active profile stays unless a verified closure shuts it; then the
//     next strictly open profile below it takes over.
//   - A stale reading, a failed quota command and a missing reading never
//     switch anything; they leave the profile in force.
//   - An infrastructure-closed backend never switches away from the profile in
//     force, and a profile with one is not open as a switch-away or switch-back target.
//   - When every profile is verifiably closed, profile 0 stays in force.
func SelectProfile(in ProfileInputs) ProfileSelection {
	n := len(in.Policies)
	active := in.Active
	if active < 0 || active >= n {
		active = 0
	}
	sel := ProfileSelection{Index: active}
	if n < 2 {
		return sel
	}
	closures := make([]*ProfileClosure, n)
	open := make([]bool, n)
	for i, p := range in.Policies {
		closures[i], open[i] = in.profileState(p)
	}
	finish := func(index int, cause string) ProfileSelection {
		sel.Index, sel.Changed, sel.Cause = index, index != active, cause
		for i := 0; i < index; i++ {
			if c := closures[i]; c != nil {
				name := ""
				if i < len(in.Names) {
					name = in.Names[i]
				}
				skipped := SkippedProfileQuota{Profile: name, Backend: c.Backend, Windows: c.Reading.Windows, Reserve: c.Reading.Reserve, ResetAt: c.Reading.ResetAt}
				sel.Skipped = append(sel.Skipped, skipped)
			}
		}
		return sel
	}
	for i := 0; i < active; i++ {
		if open[i] {
			sel.Opened = in.openedReading(in.Policies[i])
			return finish(i, CauseQuotaReopened)
		}
	}
	if closures[active] == nil {
		return finish(active, "")
	}
	sel.Closure = closures[active]
	for i := active + 1; i < n; i++ {
		if open[i] {
			sel.Opened = in.openedReading(in.Policies[i])
			return finish(i, CauseQuotaClosed)
		}
	}
	all := true
	for _, c := range closures {
		if c == nil {
			all = false
		}
	}
	if all {
		sel.AllClosed = true
		if active != 0 {
			return finish(0, CauseAllQuotaClosed)
		}
	}
	return finish(active, "")
}

// ProfileDetail is the evidence half of a switch line: the closing reading for a
// switch-away, the reopened reading for a switch-back.
func ProfileDetail(names []string, sel ProfileSelection) string {
	name := ""
	if sel.Index >= 0 && sel.Index < len(names) {
		name = names[sel.Index]
	}
	c := sel.Closure
	switch sel.Cause {
	case CauseQuotaClosed, CauseAllQuotaClosed:
		if c == nil {
			return ""
		}
		if c.Window.Kind != "" {
			s := fmt.Sprintf("%s %s used %.2f >= %.2f (reserve %.2f", c.Backend, c.Window.Kind, c.Window.Fraction, 1-c.Reading.Reserve, c.Reading.Reserve)
			if c.Window.ResetAt != nil {
				s += "; resets " + c.Window.ResetAt.UTC().Format(time.RFC3339)
			}
			return s + ")"
		}
		s := c.Backend + " refused by the provider"
		if c.Reading.ResetAt != nil {
			s += "; resets " + c.Reading.ResetAt.UTC().Format(time.RFC3339)
		}
		return s
	case CauseQuotaReopened:
		if sel.Opened == nil {
			return name + " reopened"
		}
		var parts []string
		for _, w := range sel.Opened.Reading.Windows {
			parts = append(parts, fmt.Sprintf("%s used %.2f < %.2f", w.Kind, w.Fraction, 1-sel.Opened.Reading.Reserve))
		}
		return fmt.Sprintf("%s reopened (%s; reserve %.2f)", name, strings.Join(parts, ", "), sel.Opened.Reading.Reserve)
	}
	return ""
}

// ProfileWhy renders the human reason for the profile now in force.
func ProfileWhy(names []string, from int, sel ProfileSelection, at time.Time) string {
	name := func(i int) string {
		if i >= 0 && i < len(names) {
			return names[i]
		}
		return ""
	}
	stamp := at.UTC().Format(time.RFC3339)
	switch sel.Cause {
	case CauseQuotaClosed, CauseQuotaReopened:
		return fmt.Sprintf("switched from %s at %s: %s", name(from), stamp, ProfileDetail(names, sel))
	case CauseAllQuotaClosed:
		return fmt.Sprintf("every profile is quota-closed; %s stays in force: %s", name(0), ProfileDetail(names, sel))
	}
	if sel.Index == 0 {
		return fmt.Sprintf("preferred profile (1/%d); all quota windows open", len(names))
	}
	return fmt.Sprintf("fallback profile (%d/%d)", sel.Index+1, len(names))
}

// SwitchEvent builds the event payload for a selection that changed the profile.
func SwitchEvent(names []string, from int, sel ProfileSelection) ProfileSwitched {
	name := func(i int) string {
		if i >= 0 && i < len(names) {
			return names[i]
		}
		return ""
	}
	ev := ProfileSwitched{FromProfile: name(from), ToProfile: name(sel.Index), FromIndex: from, ToIndex: sel.Index, Profiles: append([]string(nil), names...), Cause: sel.Cause}
	trigger := sel.Closure
	if sel.Cause == CauseQuotaReopened {
		trigger = sel.Opened
	}
	if trigger != nil {
		ev.TriggerBackend, ev.QuotaGroup = trigger.Backend, trigger.Pool
		reading := trigger.Reading
		ev.Reading = &reading
	}
	return ev
}

// ReadProfileInputs reads the stored quota readings, unexpired refusal windows
// and, when readClosed is given, the infrastructure-closed backends, in one read
// transaction.
func ReadProfileInputs(ctx context.Context, storePath string, at time.Time, readClosed func(*store.Reader) (map[string]bool, error)) (ProfileInputs, error) {
	in := ProfileInputs{At: at}
	reader, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return in, err
	}
	defer reader.Close()
	err = reader.ReadTx(ctx, func(r *store.Reader) error {
		var e error
		if in.Statuses, e = ReadQuotaStatuses(r); e != nil {
			return e
		}
		if in.Windows, e = ReadBackendWindows(r, at); e != nil {
			return e
		}
		if readClosed != nil {
			in.Closed, e = readClosed(r)
		}
		return e
	})
	return in, err
}

// StartupProfile evaluates the ordered profile list on stored readings as the
// loop starts. The answer is startup state, not a switch: the caller records no
// drain.profile-switched event. Why describes the profile chosen.
func StartupProfile(in ProfileInputs) (sel ProfileSelection, why string) {
	in.Active = 0
	sel = SelectProfile(in)
	n := len(in.Policies)
	switch {
	case sel.Index > 0:
		why = fmt.Sprintf("fallback profile (%d/%d) at startup", sel.Index+1, n)
		if detail := ProfileDetail(in.Names, sel); detail != "" {
			why += ": " + detail
		}
	case sel.AllClosed:
		why = ProfileWhy(in.Names, 0, ProfileSelection{Index: 0, Cause: CauseAllQuotaClosed, Closure: sel.Closure}, in.At)
	default:
		why = fmt.Sprintf("preferred profile (1/%d); all quota windows open", n)
	}
	return sel, why
}

// RecordProfileSwitch appends the drain.profile-switched event in one write transaction.
func RecordProfileSwitch(ctx context.Context, storePath string, ev ProfileSwitched, at time.Time) error {
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return runhistory.AppendEffectOn(tx, ProfileSwitchedKind, "drain", "policy", ev, at)
	})
}

// ProfileSwitcher carries what a profile switch reads and replaces on a loop: the
// shared policy pointer and state under their mutex, the loaded profiles by index
// and the files they came from. The loop builds one per call; nothing here is
// retained between calls.
type ProfileSwitcher struct {
	Root, Store, PolicyRef string
	Refs                   []string
	Now                    func() time.Time
	Mu                     *sync.RWMutex
	Active                 **Policy
	Profiles               []*Policy
	State                  *PolicyState
	Recorded               *bool
	Reportf                func(string, ...any)
	// ReadClosed reads the infrastructure-closed backends; nil reads none.
	ReadClosed func(*store.Reader) (map[string]bool, error)
}

// ActivePolicyRef is the file the profile in force came from, so a pass-boundary
// reload re-reads that profile rather than putting the first profile back.
func ActivePolicyRef(mu *sync.RWMutex, state *PolicyState, fallback string) string {
	mu.RLock()
	defer mu.RUnlock()
	if state.Ref != "" {
		return state.Ref
	}
	return fallback
}

// Select reads the stored quota facts, applies SelectProfile and, when the
// profile changes, puts the new profile in force. It runs at the top of each
// pass after the quota refresh and after a refusal window is recorded. It never
// reads a clock beyond Now and never waits. A run already handed to a worker
// keeps the request it launched with.
func (s ProfileSwitcher) Select(ctx context.Context) error {
	s.Mu.RLock()
	names := append([]string(nil), s.State.Profiles...)
	policies, active := AllPolicies(*s.Active, s.Profiles, s.State.ProfileIndex)
	s.Mu.RUnlock()
	if len(policies) < 2 {
		return nil
	}
	at := s.Now().UTC()
	in, err := ReadProfileInputs(ctx, s.Store, at, s.ReadClosed)
	if err != nil {
		return err
	}
	in.Policies, in.Names, in.Active = policies, names, active
	sel := SelectProfile(in)
	if !sel.Changed {
		return nil
	}
	return s.apply(ctx, names, active, sel, at)
}

// Startup evaluates the ordered profile list on the stored quota readings,
// refusal windows and infrastructure closures as the loop starts (and at
// --check), and puts the chosen profile in force as startup state: it records no
// drain.profile-switched event. A missing store or ref leaves the preferred profile.
func (s ProfileSwitcher) Startup(ctx context.Context) error {
	if s.Store == "" || s.PolicyRef == "" || len(s.Profiles) < 2 {
		return nil
	}
	in, err := ReadProfileInputs(ctx, s.Store, s.Now().UTC(), s.ReadClosed)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	in.Policies, in.Names = s.Profiles, s.State.Profiles
	sel, why := StartupProfile(in)
	s.State.Why, s.State.SkippedProfiles = why, sel.Skipped
	if sel.Index == 0 || sel.Index >= len(s.Refs) {
		return nil
	}
	_, raw, hash, err := ReadPolicyContent(s.Refs[sel.Index])
	if err != nil {
		return err
	}
	*s.Active = s.Profiles[sel.Index]
	s.State.Ref, s.State.ProfileIndex = s.Refs[sel.Index], sel.Index
	if sel.Index < len(s.State.Profiles) {
		s.State.Profile = s.State.Profiles[sel.Index]
	}
	s.State.Hash, s.State.DiskHash, s.State.Content = hash, hash, string(raw)
	return nil
}

func (s ProfileSwitcher) apply(ctx context.Context, names []string, from int, sel ProfileSelection, at time.Time) error {
	target := s.Profiles[sel.Index]
	ref, refusal := "", ""
	if sel.Index < len(s.Refs) {
		ref = s.Refs[sel.Index]
	} else if target != nil {
		ref = target.Ref
	}
	var raw []byte
	var hash string
	if ref != "" {
		if p, content, h, err := ReadPolicyContent(ref); err == nil {
			p.Root, p.Profile = s.Root, target.Profile
			target, raw, hash = p, content, h
			s.Profiles[sel.Index] = p
		} else {
			refusal = err.Error()
		}
	}
	if raw == nil {
		raw, _ = target.MarshalJSON()
		hash = ContentHash(raw)
	}
	name, fromName := "", ""
	if sel.Index < len(names) {
		name = names[sel.Index]
	}
	if from < len(names) {
		fromName = names[from]
	}
	s.Mu.Lock()
	old := *s.State
	*s.Active = target
	next := old
	next.Ref, next.Profile, next.ProfileIndex, next.Why = ref, name, sel.Index, ProfileWhy(names, from, sel, at)
	next.SwitchedFrom, next.SwitchedAt, next.SkippedProfiles = fromName, &at, sel.Skipped
	next.Hash, next.DiskHash, next.Content, next.Refusal, next.At = hash, hash, string(raw), refusal, at
	*s.State = next
	s.Mu.Unlock()
	if s.PolicyRef != "" {
		change := PolicyChange{At: at, OldHash: old.DiskHash, NewHash: hash, InForceHash: hash, Refusal: refusal}
		if err := WritePolicyState(s.Root, next, change); err != nil {
			return err
		}
		*s.Recorded = true
	}
	if err := RecordProfileSwitch(ctx, s.Store, SwitchEvent(names, from, sel), at); err != nil {
		return err
	}
	s.Reportf("loop: profile switched %s -> %s (%d/%d): %s\n", fromName, name, sel.Index+1, len(names), ProfileDetail(names, sel))
	return nil
}
