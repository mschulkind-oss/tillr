package policy

import (
	"strings"
	"testing"
	"time"
)

// Fake readings and a fixed clock only: nothing here probes a provider or sleeps.
var selAt = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

func selProfiles() []*Policy {
	cmd := []string{"quota-a"}
	other := []string{"quota-b"}
	return []*Policy{
		{DefaultBackend: "a1", Seats: map[string]string{"build": "a1", "design": "a2"}, Backends: map[string]Backend{
			"a1": {Quota: cmd, Reserve: 0.2}, "a2": {Quota: cmd, Reserve: 0.2}}},
		{DefaultBackend: "b1", Seats: map[string]string{"build": "b1"}, Backends: map[string]Backend{
			"b1": {Quota: other, Reserve: 0.2}}},
	}
}

func selStatus(backend string, quota []string, at time.Time, errMsg string, windows ...QuotaWindow) QuotaStatus {
	return QuotaStatus{Backend: backend, ObservedAt: at, Reserve: 0.2, Windows: windows, Error: errMsg, Command: QuotaCommandKey(quota)}
}

func selWindow(kind string, fraction float64) QuotaWindow {
	reset := selAt.Add(3 * time.Hour)
	return QuotaWindow{Kind: kind, Fraction: fraction, ResetAt: &reset}
}

func selInputs(active int, windows map[string]time.Time, statuses ...QuotaStatus) ProfileInputs {
	return ProfileInputs{Policies: selProfiles(), Names: []string{"pref", "fallback"}, Active: active, Statuses: statuses, Windows: windows, At: selAt}
}

func poolStatuses(aFraction ...float64) []QuotaStatus {
	var ws []QuotaWindow
	kinds := []string{"five_hour", "seven_day"}
	for i, f := range aFraction {
		ws = append(ws, selWindow(kinds[i], f))
	}
	return []QuotaStatus{
		selStatus("a1", []string{"quota-a"}, selAt, "", ws...),
		selStatus("a2", []string{"quota-a"}, selAt, "", ws...),
		selStatus("b1", []string{"quota-b"}, selAt, "", selWindow("five_hour", 0.1)),
	}
}

func TestSelectProfileSwitchesAwayOnReserveClosure(t *testing.T) {
	sel := SelectProfile(selInputs(0, nil, poolStatuses(0.8)...))
	if !sel.Changed || sel.Index != 1 || sel.Cause != CauseQuotaClosed {
		t.Fatalf("selection = %+v, want switch to index 1 on quota-closed", sel)
	}
	if sel.Closure == nil || sel.Closure.Backend != "a1" || sel.Closure.Window.Kind != "five_hour" {
		t.Fatalf("closure = %+v, want a1 five_hour", sel.Closure)
	}
	ev := SwitchEvent([]string{"pref", "fallback"}, 0, sel)
	if ev.FromProfile != "pref" || ev.ToProfile != "fallback" || ev.TriggerBackend != "a1" || ev.QuotaGroup != `cmd:["quota-a"]` || ev.Reading == nil || ev.Reading.Source != "probe" {
		t.Fatalf("event = %+v", ev)
	}
	if got := ProfileDetail([]string{"pref", "fallback"}, sel); !strings.Contains(got, "a1 five_hour used 0.80 >= 0.80 (reserve 0.20; resets 2026-11-28T15:00:00Z)") {
		t.Fatalf("detail = %q", got)
	}
}

func TestSelectProfileStaysBelowTheReserveLine(t *testing.T) {
	sel := SelectProfile(selInputs(0, nil, poolStatuses(0.79)...))
	if sel.Changed || sel.Index != 0 {
		t.Fatalf("selection = %+v, want to stay on the preferred profile", sel)
	}
}

func TestSelectProfileSwitchesAwayOnRefusalWindow(t *testing.T) {
	windows := map[string]time.Time{"a2": selAt.Add(time.Hour)}
	sel := SelectProfile(selInputs(0, windows, poolStatuses(0.1)...))
	if !sel.Changed || sel.Index != 1 || sel.Closure == nil || sel.Closure.Reading.Source != "refusal" {
		t.Fatalf("selection = %+v, want a switch on the runtime refusal", sel)
	}
	expired := map[string]time.Time{"a2": selAt.Add(-time.Second)}
	if sel := SelectProfile(selInputs(0, expired, poolStatuses(0.1)...)); sel.Changed {
		t.Fatalf("an expired refusal switched: %+v", sel)
	}
}

func TestSelectProfileNeverSwitchesOnAnUnreadableQuota(t *testing.T) {
	stale := selAt.Add(-time.Hour)
	cases := map[string][]QuotaStatus{
		"failed command": {selStatus("a1", []string{"quota-a"}, selAt, "quota command failed: exit 1"), selStatus("a2", []string{"quota-a"}, selAt, "quota command failed: exit 1"), poolStatuses(0.1)[2]},
		"stale reading":  {selStatus("a1", []string{"quota-a"}, stale, "", selWindow("five_hour", 0.99)), selStatus("a2", []string{"quota-a"}, stale, "", selWindow("five_hour", 0.99)), poolStatuses(0.1)[2]},
		"no reading":     nil,
	}
	for name, statuses := range cases {
		if sel := SelectProfile(selInputs(0, nil, statuses...)); sel.Changed || sel.Index != 0 {
			t.Errorf("%s: selection = %+v, want no switch", name, sel)
		}
	}
}

func TestSelectProfileDoesNotMoveToAnUnreadFallback(t *testing.T) {
	// The preferred profile is verifiably closed but the fallback's quota command failed:
	// a failure is a fault to fix, not a place to route to.
	statuses := poolStatuses(0.9)
	statuses[2] = selStatus("b1", []string{"quota-b"}, selAt, "quota command failed: exit 1")
	if sel := SelectProfile(selInputs(0, nil, statuses...)); sel.Changed {
		t.Fatalf("selection = %+v, want to stay when the fallback cannot be read", sel)
	}
}

func TestSelectProfileSwitchesBackAsSoonAsEveryWindowIsBelowItsLine(t *testing.T) {
	// Single window: below the line retakes control at once, with no dwell.
	sel := SelectProfile(selInputs(1, nil, poolStatuses(0.0)...))
	if !sel.Changed || sel.Index != 0 || sel.Cause != CauseQuotaReopened || sel.Opened == nil {
		t.Fatalf("selection = %+v, want a switch back on quota-reopened", sel)
	}
	if got := ProfileDetail([]string{"pref", "fallback"}, sel); got != "pref reopened (five_hour used 0.00 < 0.80; reserve 0.20)" {
		t.Fatalf("detail = %q", got)
	}
	// Two windows: a secondary window at the line keeps the fallback in force.
	if sel := SelectProfile(selInputs(1, nil, poolStatuses(0.0, 0.82)...)); sel.Changed {
		t.Fatalf("selection = %+v, want to stay while seven_day >= 1-reserve", sel)
	}
	if sel := SelectProfile(selInputs(1, nil, poolStatuses(0.0, 0.34)...)); !sel.Changed || sel.Index != 0 {
		t.Fatalf("selection = %+v, want a switch back once both windows are below the line", sel)
	}
}

func TestSelectProfileSwitchBackNeedsAFreshErrorFreeReadingAndNoRefusal(t *testing.T) {
	statuses := poolStatuses(0.0)
	if sel := SelectProfile(selInputs(1, map[string]time.Time{"a1": selAt.Add(time.Hour)}, statuses...)); sel.Changed {
		t.Fatalf("an open refusal window did not hold the fallback: %+v", sel)
	}
	stale := poolStatuses(0.0)
	stale[0].ObservedAt, stale[1].ObservedAt = selAt.Add(-time.Hour), selAt.Add(-time.Hour)
	if sel := SelectProfile(selInputs(1, nil, stale...)); sel.Changed {
		t.Fatalf("a stale reading switched back: %+v", sel)
	}
	failed := poolStatuses(0.0)
	failed[0].Error = "quota command failed"
	if sel := SelectProfile(selInputs(1, nil, failed...)); sel.Changed {
		t.Fatalf("a failed quota command switched back: %+v", sel)
	}
}

func TestSelectProfileKeepsIndexZeroWhenEveryProfileIsClosed(t *testing.T) {
	statuses := poolStatuses(0.9)
	statuses[2] = selStatus("b1", []string{"quota-b"}, selAt, "", selWindow("five_hour", 0.95))
	sel := SelectProfile(selInputs(0, nil, statuses...))
	if sel.Changed || sel.Index != 0 || !sel.AllClosed {
		t.Fatalf("selection = %+v, want index 0 kept with every profile closed", sel)
	}
	sel = SelectProfile(selInputs(1, nil, statuses...))
	if !sel.Changed || sel.Index != 0 || sel.Cause != CauseAllQuotaClosed {
		t.Fatalf("selection = %+v, want the loop back on index 0 when every profile closes", sel)
	}
}

func TestSelectProfileRecordsSkippedClosedProfile(t *testing.T) {
	sel := SelectProfile(selInputs(0, nil, poolStatuses(0.9)...))
	if sel.Index != 1 || len(sel.Skipped) != 1 {
		t.Fatalf("selection = %+v, want one skipped profile", sel)
	}
	sp := sel.Skipped[0]
	if sp.Profile != "pref" || sp.Backend != "a1" || sp.ResetAt == nil || !sp.ResetAt.Equal(selAt.Add(3*time.Hour)) || sp.ReopenThreshold != 0 || sp.Reserve != 0.2 {
		t.Fatalf("skipped = %+v; reopen headroom must stay unset (OQ-QPS2 option B)", sp)
	}
}
