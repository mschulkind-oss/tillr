package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	heavyMissKind = "view.heavy-misses"
	landingKind   = "land.landing"
	gateKind      = "gate.verdict"
	revertKind    = "drain.revert"
	fixerAddKind  = "item.add"
	fixerLogsKind = "drain.fixer-logs"
	fixerLinkKind = "drain.fixer-attached"
	fixerDoneKind = "drain.fixer-proved"
)

type heavyMissWindow struct {
	From time.Time `json:"from_inclusive"`
	To   time.Time `json:"to_exclusive"`
}

func (w heavyMissWindow) contains(at time.Time) bool { return !at.Before(w.From) && at.Before(w.To) }

// Parse enforces the calendar; the pattern also rejects relaxed Go spellings like
// a one-digit hour or +24:00, which are not RFC3339.
var heavyMissTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func heavyMissesFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("heavy-misses")
	path := fs.String("store", "", "read-only store path (required)")
	root := fs.String("root", ".", "repository containing the recorded trunk commits")
	from := fs.String("from", "", "inclusive RFC3339 bound for landing, failure and fixer outcome event times (required)")
	to := fs.String("to", "", "exclusive RFC3339 bound for the same events; sequence decides causality (required)")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	start, fromErr := time.Parse(time.RFC3339, *from)
	end, toErr := time.Parse(time.RFC3339, *to)
	if *path == "" || *root == "" || fromErr != nil || toErr != nil ||
		!heavyMissTime.MatchString(*from) || !heavyMissTime.MatchString(*to) || !start.Before(end) {
		return record.Envelope{}, usagef("heavy-misses needs --store <path>, nonempty --root, and RFC3339 --from < --to (Z or numeric UTC offsets)")
	}
	ctx := context.Background()
	s, events, err := readHeavyMissEvents(ctx, *path)
	if err != nil {
		var mismatch schemaMismatch
		if errors.As(err, &mismatch) {
			fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
			return envelope(heavyMissKind, refusal{GeneratedAt: time.Now().UTC(), Snapshot: s}), record.ExitIncompatible
		}
		return record.Envelope{}, exitFor(err)
	}
	p, err := assembleHeavyMisses(ctx, s, events, heavyMissWindow{start.UTC(), end.UTC()}, *root)
	if err != nil {
		return record.Envelope{}, usagef("%v", err)
	}
	return envelope(heavyMissKind, p), record.ExitOK
}

func readHeavyMissEvents(ctx context.Context, path string) (snapshot, []event.Event, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return snapshot{}, nil, err
	}
	defer s.Close()
	snap := snapshot{Source: sourceStore}
	var events []event.Event
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		snap.SchemaVersion, err = r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(snap.SchemaVersion) {
			return schemaMismatch{version: snap.SchemaVersion}
		}
		snap.EventHeadSeq, err = r.EventHeadSeq()
		if err != nil {
			return err
		}
		events, err = r.EventsOfKind(0, landingKind, gateKind, revertKind, fixerAddKind, fixerLogsKind, fixerLinkKind, fixerDoneKind)
		return err
	})
	return snap, events, err
}

// Input fields mirror the ledger producers in tillr-land/land and tillr-drain/drain.
// The landing receipt carries the same identity and data as its atomically linked row.
// The subject of a gate verdict is the run; its payload adds the gate's tree binding.
type heavyMissLandingReceipt struct {
	LandingID         string `json:"landing_id"`
	ItemID            string `json:"item_id"`
	RunID             string `json:"run_id"`
	VerdictID         string `json:"verdict_id"`
	Result            string `json:"result"`
	BaseSHA           string `json:"base_sha"`
	GatedTree         string `json:"gated_tree"`
	TrunkBefore       string `json:"trunk_before"`
	TrunkAfter        string `json:"trunk_after"`
	GateSkippedReason string `json:"gate_skipped_reason,omitempty"`
}

type heavyMissGate struct {
	Seq   int64
	RunID string `json:"run_id"`
	verdict
}

func (g heavyMissGate) binds(l heavyMissLandingReceipt) bool {
	return l.RunID != "" && l.VerdictID != "" &&
		(g.Result == "pass" || g.Result == "pass-inherited") &&
		fullHeavySHA(g.BaseSHA) && fullHeavySHA(g.TreeSHA) &&
		(l.BaseSHA == "" || l.BaseSHA == g.BaseSHA) &&
		(l.GatedTree == "" || l.GatedTree == g.TreeSHA)
}

type heavyMissRevert struct {
	Landing  string   `json:"landing"`
	Checks   []string `json:"checks"`
	Result   string   `json:"result"`
	Revert   string   `json:"revert"`
	Reason   string   `json:"reason"`
	StillRed bool     `json:"still_red"`
}

// stillRedCheck identifies the replay that stopped the inverse. Older receipts
// log the check in the refusal reason, not in a separate payload field.
func (p heavyMissRevert) stillRedCheck(checks []string) string {
	if !p.StillRed {
		return ""
	}
	if len(checks) == 1 {
		return checks[0]
	}
	marker := " still fails on the tip with " + shortSHA(p.Landing) + " reverted, so "
	prefix, ok := strings.CutSuffix(p.Reason, marker+"the attribution did not explain the red")
	if !ok {
		prefix, ok = strings.CutSuffix(p.Reason, marker+"that landing does not explain the red; the fixer starts from the tip")
	}
	if ok {
		for _, id := range checks {
			if prefix == id || strings.HasSuffix(prefix, "; "+id) {
				return id
			}
		}
	}
	return ""
}

type heavyMissFixerEvent struct {
	FixerItem string            `json:"fixer_item"`
	RedSHA    string            `json:"red_sha"`
	CheckLogs map[string]string `json:"check_logs"`
	Failing   []string          `json:"failing_checks"`
	Passed    []string          `json:"checks"`
	Green     string            `json:"green"`
	ProvedAt  string            `json:"proved_at"`
}

// A new trunk fixer's criterion contains the whole failing set, whether or not
// routeTrunkFixer could keep any of the logs. Only the generated item.add form
// identifies this observation; a current or edited criterion can mix red tips.
var heavyMissFixerID = regexp.MustCompile(`^fixer-main-([0-9a-f]{12})$`)

func heavyMissCreation(e event.Event) (prefix string, checks []string, err error) {
	if e.ObjectType != "item" || e.ObjectID == nil {
		return "", nil, nil
	}
	match := heavyMissFixerID.FindStringSubmatch(*e.ObjectID)
	if match == nil {
		return "", nil, nil
	}
	var p struct {
		ItemID   string `json:"item_id"`
		Title    string `json:"title"`
		Rank     string `json:"rank"`
		Seat     string `json:"seat"`
		State    string `json:"state"`
		Criteria []struct {
			ID    string `json:"criterion_id"`
			State string `json:"state"`
			Text  string `json:"text"`
		} `json:"criteria"`
	}
	if err := heavyMissDecode(e, &p); err != nil {
		return "", nil, err
	}
	prefix = match[1]
	if !heavyMissSubject(e, p.ItemID) || p.Title != "fix the broken trunk at "+prefix ||
		p.Rank != "000" || p.Seat != "build" || p.State != "in-queue" || len(p.Criteria) != 1 ||
		p.Criteria[0].ID != "trunk-green" || p.Criteria[0].State != "not-done" {
		return "", nil, nil
	}
	text, ok := strings.CutPrefix(p.Criteria[0].Text, "the full gate is green at "+prefix+" (red: ")
	if !ok {
		return "", nil, nil
	}
	list, tail, ok := strings.Cut(text, "), and ")
	if !ok {
		return "", nil, nil
	}
	rolling := "the next rolling run advances the watermark"
	heavy := "the next heavy run advances refs/tillr/green"
	if tail != rolling && tail != heavy && !strings.HasPrefix(tail, rolling+". ") && !strings.HasPrefix(tail, heavy+". ") {
		return "", nil, nil
	}
	checks = strings.Split(list, ", ")
	for _, id := range checks {
		if id == "" || id != strings.TrimSpace(id) || strings.ContainsAny(id, ",\r\n") {
			return "", nil, nil
		}
	}
	return prefix, distinctHeavyChecks(checks), nil
}

// routeTrunkFixer appends a creation log immediately after item.add in one
// transaction. For reuse it instead appends the log immediately BEFORE the
// attachment. A later log, even for the same fixer and prefix, cannot select
// the creation's exact commit. Check adjacency in global sequence, not time.
func heavyMissCreationLog(e event.Event, following []event.Event, prefix string, checks []string) (heavyMissFixerEvent, int64, error) {
	var p heavyMissFixerEvent
	if len(following) == 0 {
		return p, 0, nil
	}
	log := following[0]
	if log.Seq != e.Seq+1 || log.Kind != fixerLogsKind || log.ObjectType != "item" || !heavyMissSubject(log, *e.ObjectID) {
		return p, 0, nil
	}
	if err := heavyMissDecode(log, &p); err != nil {
		return p, 0, err
	}
	if p.FixerItem != *e.ObjectID || !fullHeavySHA(p.RedSHA) || !strings.HasPrefix(p.RedSHA, prefix) || len(p.CheckLogs) == 0 {
		return p, 0, nil
	}
	for id := range p.CheckLogs {
		if _, ok := slices.BinarySearch(checks, id); !ok {
			return p, 0, nil
		}
	}
	if len(following) > 1 {
		next := following[1]
		if next.Seq == log.Seq+1 && next.Kind == fixerLinkKind && heavyMissSubject(next, p.FixerItem) {
			var attached heavyMissFixerEvent
			if err := heavyMissDecode(next, &attached); err != nil {
				return p, 0, err
			}
			if attached.FixerItem == p.FixerItem && attached.RedSHA == p.RedSHA {
				return p, 0, nil
			}
		}
	}
	return p, log.Seq, nil
}

type heavyMissEvidence struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	FixerItem string    `json:"fixer_item,omitempty"`
	LogRef    string    `json:"log_ref,omitempty"`
	Result    string    `json:"result,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	StillRed  bool      `json:"still_red,omitempty"`
	RevertSHA string    `json:"revert_sha,omitempty"`
	LandedSHA string    `json:"landed_sha,omitempty"`
	GreenSHA  string    `json:"green_sha,omitempty"`
	ProvedAt  string    `json:"proved_at,omitempty"`
}

func missEvidence(e event.Event) heavyMissEvidence {
	return heavyMissEvidence{Seq: e.Seq, At: e.At.UTC(), Kind: e.Kind}
}

type heavyMissFixer struct {
	ItemID         string `json:"item_id"`
	Status         string `json:"status"`
	AssociationSeq int64  `json:"association_seq"`
	LandingSeq     int64  `json:"landing_seq,omitempty"`
	LandedSHA      string `json:"landed_sha,omitempty"`
	ProofSeq       int64  `json:"proof_seq,omitempty"`
}

type heavyMissCheck struct {
	ID          string              `json:"check_id"`
	Attribution string              `json:"attribution"`
	RevertedBy  string              `json:"reverted_by,omitempty"`
	Evidence    []heavyMissEvidence `json:"evidence"`
	Fixers      []heavyMissFixer    `json:"fixers"`
	fixersByID  map[string]heavyMissFixer
}

func (c *heavyMissCheck) addRevert(e heavyMissEvidence) {
	c.Evidence = append(c.Evidence, e)
	switch {
	case e.StillRed && c.Attribution != "confirmed":
		c.Attribution = "disproved"
	case e.RevertSHA != "":
		c.Attribution, c.RevertedBy = "confirmed", e.RevertSHA
	case e.Result == "not-possible" && c.Attribution == "named":
		c.Attribution = "offered"
	}
}

type heavyMissLanding struct {
	heavyMissLandingReceipt
	EventSeq     int64             `json:"event_seq"`
	At           time.Time         `json:"at"`
	GateEventSeq int64             `json:"gate_event_seq"`
	GateResult   string            `json:"gate_result"`
	Paths        []string          `json:"changed_paths"`
	PathsError   string            `json:"changed_paths_error,omitempty"`
	Modules      []string          `json:"changed_modules"`
	Checks       []*heavyMissCheck `json:"checks"`
	checksByID   map[string]*heavyMissCheck
}

func (l *heavyMissLanding) check(id string) *heavyMissCheck {
	c := l.checksByID[id]
	if c == nil {
		if l.checksByID == nil {
			l.checksByID = map[string]*heavyMissCheck{}
		}
		c = &heavyMissCheck{ID: id, Attribution: "named", fixersByID: map[string]heavyMissFixer{}}
		l.checksByID[id] = c
	}
	return c
}

type heavyMissUnmatched struct {
	Evidence  heavyMissEvidence `json:"evidence"`
	NamedSHA  string            `json:"named_sha"`
	Checks    []string          `json:"checks"`
	CheckLogs map[string]string `json:"check_logs,omitempty"`
	Reason    string            `json:"reason"`
}

type heavyMissCoverage struct {
	Unmatched []heavyMissUnmatched `json:"unmatched"`
	Pending   int                  `json:"pending_fixer_associations"`
}

type heavyMissCount struct {
	Key   string `json:"key"`
	Count int    `json:"landings"`
}

func heavyMissCounts(counts map[string]int) []heavyMissCount {
	out := make([]heavyMissCount, 0, len(counts))
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, heavyMissCount{k, counts[k]})
	}
	return out
}

type heavyMissReport struct {
	Snapshot snapshot            `json:"snapshot"`
	Window   heavyMissWindow     `json:"window"`
	Landings []*heavyMissLanding `json:"landings"`
	ByCheck  []heavyMissCount    `json:"by_check"`
	ByModule []heavyMissCount    `json:"by_module"`
	Coverage heavyMissCoverage   `json:"coverage"`
}

func heavyMissDecode(e event.Event, payload any) error {
	if err := json.Unmarshal(e.Payload, payload); err != nil {
		return fmt.Errorf("reading %s event %d: %w", e.Kind, e.Seq, err)
	}
	return nil
}

func heavyMissSubject(e event.Event, id string) bool {
	return id != "" && e.ObjectID != nil && *e.ObjectID == id
}

func heavyMissUnmatchedReason(w heavyMissWindow, l *heavyMissLanding, sha string, checks []string) string {
	switch {
	case !fullHeavySHA(sha):
		return "not-full-sha"
	case l == nil:
		return "no-prior-landing"
	case !w.contains(l.At):
		return "landing-outside-window"
	case l.GateSkippedReason != "":
		return "gate-skipped"
	case l.GateEventSeq == 0:
		return "no-bound-pass"
	case len(checks) == 0:
		return "no-checks"
	default:
		return ""
	}
}

func distinctHeavyChecks(checks []string) []string {
	out := append([]string{}, checks...)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > 0 && out[0] == "" {
		// An empty check ID gives no check evidence.
		out = out[1:]
	}
	return out
}

func assembleHeavyMisses(ctx context.Context, snap snapshot, events []event.Event, w heavyMissWindow, root string) (heavyMissReport, error) {
	report := heavyMissReport{Snapshot: snap, Window: w, Landings: []*heavyMissLanding{}, Coverage: heavyMissCoverage{Unmatched: []heavyMissUnmatched{}}}
	type pair struct{ first, second string }
	gates := map[pair]heavyMissGate{}          // run, verdict
	landings := map[string]*heavyMissLanding{} // full trunk_after, most recent preceding by seq
	// Track all preceding exact landing SHAs for each prefix, not just ones
	// with a pass or in the window: neither filter can make a prefix unique.
	type prefixBinding struct {
		sha       string
		ambiguous bool
	}
	prefixes := map[string]prefixBinding{}
	var supportingCreationLog int64
	var orderedLandings []*heavyMissLanding
	type checkSet map[*heavyMissCheck]struct{}
	associations := map[string]map[string]checkSet{} // fixer -> check ID -> observed landing/check pairs

	// Reader returns sequence order. Wall time decides window membership only. A
	// verdict can precede the lower bound; evidence cannot borrow a future landing,
	// and an association cannot borrow a fixer outcome recorded before it.
	for i, e := range events {
		switch e.Kind {
		case gateKind:
			var g heavyMissGate
			if err := heavyMissDecode(e, &g); err != nil {
				return report, err
			}
			if e.ObjectType == "run" && e.ObjectID != nil && (g.RunID == "" || g.RunID == *e.ObjectID) && g.VerdictID != "" {
				g.Seq = e.Seq
				gates[pair{*e.ObjectID, g.VerdictID}] = g
			}
			continue
		case landingKind:
			var l heavyMissLandingReceipt
			if err := heavyMissDecode(e, &l); err != nil {
				return report, err
			}
			if e.ObjectType == "item" && heavyMissSubject(e, l.ItemID) && l.LandingID != "" && l.Result == "landed" && fullHeavySHA(l.TrunkAfter) {
				row := &heavyMissLanding{heavyMissLandingReceipt: l, EventSeq: e.Seq, At: e.At.UTC()}
				if g := gates[pair{l.RunID, l.VerdictID}]; g.binds(l) {
					row.GateEventSeq, row.GateResult = g.Seq, g.Result
				}
				landings[l.TrunkAfter] = row
				prefix := l.TrunkAfter[:12]
				if binding := prefixes[prefix]; binding.sha == "" {
					prefixes[prefix] = prefixBinding{sha: l.TrunkAfter}
				} else if binding.sha != l.TrunkAfter && !binding.ambiguous {
					prefixes[prefix] = prefixBinding{ambiguous: true, sha: binding.sha}
				}
				orderedLandings = append(orderedLandings, row)
				if w.contains(e.At) {
					ref := missEvidence(e)
					ref.FixerItem, ref.LandedSHA = l.ItemID, l.TrunkAfter
					for _, seen := range associations[l.ItemID] {
						for c := range seen {
							fixer := c.fixersByID[l.ItemID]
							fixer.Status, fixer.LandingSeq, fixer.LandedSHA = "repaired", e.Seq, l.TrunkAfter
							c.fixersByID[l.ItemID] = fixer
							c.Evidence = append(c.Evidence, ref)
						}
					}
				}
			}
			continue
		}
		if !w.contains(e.At) {
			continue
		}
		ref := missEvidence(e)
		var sha string
		var checks []string
		var logs map[string]string
		var stillRedCheck string
		switch e.Kind {
		case fixerAddKind:
			prefix, creationChecks, err := heavyMissCreation(e)
			if err != nil {
				return report, err
			}
			if prefix == "" {
				continue
			}
			checks, ref.FixerItem = creationChecks, *e.ObjectID
			log, logSeq, err := heavyMissCreationLog(e, events[i+1:], prefix, checks)
			if err != nil {
				return report, err
			}
			binding := prefixes[prefix]
			reason := ""
			switch {
			case logSeq != 0:
				sha = log.RedSHA
			case binding.ambiguous:
				reason = "ambiguous-creation-prefix"
			default:
				sha = binding.sha
			}
			if reason == "" && landings[sha] == nil {
				reason = "no-prior-landing"
			}
			if reason != "" {
				report.Coverage.Unmatched = append(report.Coverage.Unmatched, heavyMissUnmatched{Evidence: ref, NamedSHA: prefix, Checks: checks, Reason: reason})
				continue
			}
			supportingCreationLog = logSeq
		case revertKind:
			var p heavyMissRevert
			if err := heavyMissDecode(e, &p); err != nil {
				return report, err
			}
			if e.ObjectType != "landing" || !heavyMissSubject(e, p.Landing) {
				continue
			}
			sha, checks = p.Landing, p.Checks
			ref.Result, ref.Reason = p.Result, p.Reason
			stillRedCheck = p.stillRedCheck(distinctHeavyChecks(checks))
			if p.Result == "reverted" && !p.StillRed && fullHeavySHA(p.Revert) {
				ref.RevertSHA = p.Revert
			}
		default:
			var p heavyMissFixerEvent
			if err := heavyMissDecode(e, &p); err != nil {
				return report, err
			}
			// Attachments of non-trunk defects carry no fixer_item or red_sha.
			if !heavyMissSubject(e, p.FixerItem) {
				continue
			}
			ref.FixerItem = p.FixerItem
			if e.Kind == fixerDoneKind {
				if fullHeavySHA(p.Green) {
					ref.GreenSHA, ref.ProvedAt = p.Green, p.ProvedAt
					for _, id := range distinctHeavyChecks(p.Passed) {
						for c := range associations[p.FixerItem][id] {
							fixer := c.fixersByID[p.FixerItem]
							if fixer.LandingSeq == 0 {
								fixer.Status = "proved-green"
							}
							fixer.ProofSeq = e.Seq
							c.fixersByID[p.FixerItem] = fixer
							c.Evidence = append(c.Evidence, ref)
						}
					}
				}
				continue
			}
			sha, checks, logs = p.RedSHA, p.Failing, p.CheckLogs
			if e.Kind == fixerLogsKind {
				checks = slices.Sorted(maps.Keys(logs))
			}
		}
		checks = distinctHeavyChecks(checks)
		l := landings[sha]
		if reason := heavyMissUnmatchedReason(w, l, sha, checks); reason != "" {
			report.Coverage.Unmatched = append(report.Coverage.Unmatched, heavyMissUnmatched{ref, sha, checks, logs, reason})
			continue
		}
		for _, id := range checks {
			c := l.check(id)
			if e.Kind == revertKind {
				ref.StillRed = id == stillRedCheck
				c.addRevert(ref)
				continue
			}
			ref.LogRef = logs[id]
			c.Evidence = append(c.Evidence, ref)
			if e.Seq == supportingCreationLog {
				// The log supplements the creation; it isn't a second red observation.
				continue
			}
			c.fixersByID[ref.FixerItem] = heavyMissFixer{ItemID: ref.FixerItem, Status: "pending", AssociationSeq: e.Seq}
			if associations[ref.FixerItem] == nil {
				associations[ref.FixerItem] = map[string]checkSet{}
			}
			byID := associations[ref.FixerItem]
			if byID[id] == nil {
				byID[id] = checkSet{}
			}
			byID[id][c] = struct{}{}
		}
	}

	byCheck, byModule := map[string]int{}, map[string]int{}
	for _, l := range orderedLandings {
		if len(l.checksByID) == 0 {
			continue
		}
		var err error
		l.Paths, l.Modules, err = heavyMissPaths(ctx, root, l.TrunkBefore, l.TrunkAfter)
		if err != nil {
			l.PathsError = err.Error()
			l.Modules = []string{"(unknown)"}
		}
		for _, module := range l.Modules {
			byModule[module]++
		}
		for _, id := range slices.Sorted(maps.Keys(l.checksByID)) {
			c := l.checksByID[id]
			c.Fixers = make([]heavyMissFixer, 0, len(c.fixersByID))
			for _, id := range slices.Sorted(maps.Keys(c.fixersByID)) {
				f := c.fixersByID[id]
				c.Fixers = append(c.Fixers, f)
				if f.Status == "pending" {
					report.Coverage.Pending++
				}
			}
			l.Checks = append(l.Checks, c)
			byCheck[c.ID]++
		}
		report.Landings = append(report.Landings, l)
	}
	report.ByCheck, report.ByModule = heavyMissCounts(byCheck), heavyMissCounts(byModule)
	return report, nil
}
