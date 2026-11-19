package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	landingOfferFile  = "landing-offer.json"
	landingAnswerFile = "landing-answer.json"
	callOffered       = "call.offered"
	callAnswered      = "call.answered"
	callLanded        = "call.landed"
	callDelivered     = "call.delivered"
	callClosed        = "call.closed"
)

type landingOffer struct {
	CallID  string `json:"call_id"`
	Commit  string `json:"commit"`
	Base    string `json:"base"`
	Summary string `json:"summary"`
}

type landingCall struct {
	landingOffer
	ItemID      string `json:"item_id"`
	RunID       string `json:"run_id"`
	TerminalSeq int64  `json:"terminal_seq"`
	NodeID      string `json:"node_id,omitempty"`
	SessionID   string `json:"session_id"`
	RunLog      string `json:"run_log"`
	Worktree    string `json:"worktree"`
	Scratch     string `json:"scratch"`
	Adapter     string `json:"adapter"`
	Capability  string `json:"ending_capability"`
	Tool        string `json:"tool"`
}

type landingAnswer struct {
	CallID   string   `json:"call_id"`
	Kind     string   `json:"kind"`
	Stage    string   `json:"stage,omitempty"`
	Text     string   `json:"text"`
	Evidence []string `json:"evidence,omitempty"`
}

// The event stream is the call record. The existence check and append share the
// store's writer transaction, so replay cannot insert a second offer or answer.
func appendLandingCallEvent(tx *store.Tx, kind, id string, payload any, at time.Time) error {
	seen, err := judgmentSeen(tx, kind, "call", id)
	if err != nil {
		return err
	}
	if seen != 0 {
		if kind == callOffered {
			return nil
		}
		return fmt.Errorf("call %q already has a %s event", id, kind)
	}
	if kind != callOffered {
		offered, err := judgmentSeen(tx, callOffered, "call", id)
		if err != nil {
			return err
		}
		if offered != 1 {
			return fmt.Errorf("call %q has no offer", id)
		}
	}
	switch kind {
	case callAnswered, callLanded, callDelivered:
		closed, err := judgmentSeen(tx, callClosed, "call", id)
		if err != nil {
			return err
		}
		if closed != 0 {
			return fmt.Errorf("call %q is closed", id)
		}
	}
	if kind == callDelivered || kind == callClosed {
		answered, err := judgmentSeen(tx, callAnswered, "call", id)
		if err != nil {
			return err
		}
		if (kind == callDelivered) != (answered == 1) {
			return fmt.Errorf("call %q cannot record %s in its current state", id, kind)
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: "call", ObjectID: &id, Payload: body, At: at})
	return err
}

type landingOfferIntake int

const (
	landingOfferAbsent landingOfferIntake = iota
	landingOfferSubmitted
	landingOfferDuplicate
	landingOfferSettled
)

func (l *Loop) intakeLandingOffer(ctx context.Context, d Decision, rec run.Record, mainSHA string) (landingOfferIntake, error) {
	if len(d.RunRequest.Command) == 0 || d.RunRequest.Backend == "command" || evaluatorNode(d) {
		return landingOfferAbsent, nil
	}
	laneDir := l.laneDirPath(d.LaneID)
	path := filepath.Join(run.DurableDir(laneDir, rec.RunID), landingOfferFile)
	// Stat only checks presence; do not read an offer while a marked child can write it.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return landingOfferAbsent, nil
		}
		return landingOfferAbsent, err
	}
	live, err := liveRunIDs(l.cfg.Store)
	if err != nil {
		return landingOfferAbsent, err
	}
	if live[rec.RunID] {
		return landingOfferAbsent, Externalf("cannot read landing offer for %s while its processes survive", rec.RunID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return landingOfferAbsent, err
	}
	var offer landingOffer
	if err := json.Unmarshal(raw, &offer); err != nil {
		return landingOfferAbsent, fmt.Errorf("reading landing offer: %w", err)
	}
	if strings.TrimSpace(offer.CallID) == "" || strings.TrimSpace(offer.Summary) == "" || offer.Commit == "" || offer.Base == "" {
		return landingOfferAbsent, Externalf("landing offer for %s is missing its call id, summary, commit, or base", rec.RunID)
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return landingOfferAbsent, err
	}
	defer db.Close()
	// The validation reads the lane's HEAD and its session file: I/O, so it runs here,
	// with no write open. Its inputs are all in hand, and the write below uses its answer
	// only where it used to call it.
	validated, validateErr := l.validateLandingOffer(ctx, d, rec, mainSHA, offer)
	var intake landingOfferIntake
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		intake = landingOfferAbsent
		var raw []byte
		err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='call' AND object_id=?`, callOffered, offer.CallID).Scan(&raw)
		if err == nil {
			var existing landingCall
			if err := json.Unmarshal(raw, &existing); err != nil {
				return err
			}
			if existing.RunID != rec.RunID {
				intake = landingOfferDuplicate
				return nil
			}
			if existing.landingOffer != offer {
				return Externalf("run %s changed its recorded landing offer %s", rec.RunID, offer.CallID)
			}
			intake = landingOfferSubmitted
			var settled int
			if err := tx.QueryRow(`SELECT count(*) FROM events WHERE (object_type='call' AND object_id=? AND kind IN (?,?,?)) OR (object_type='run' AND object_id=? AND kind=?)`, offer.CallID, callAnswered, callClosed, callLanded, rec.RunID, examinationDoneKind).Scan(&settled); err != nil {
				return err
			}
			if settled > 0 {
				intake = landingOfferSettled
				return nil
			}
			// An unfinished replay still submits the recorded commit. Never judge a
			// new lane head or base under an earlier call's immutable offer.
			return validateErr
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if validateErr != nil {
			return validateErr
		}
		intake = landingOfferSubmitted
		return appendLandingCallEvent(tx, callOffered, offer.CallID, validated, l.cfg.Now().UTC())
	})
	return intake, err
}

func (l *Loop) validateLandingOffer(ctx context.Context, d Decision, rec run.Record, mainSHA string, offer landingOffer) (landingCall, error) {
	laneDir := l.laneDirPath(d.LaneID)
	target := l.laneTarget(d.LaneID)
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return landingCall{}, err
	}
	base := l.settleBase(d.LaneID, mainSHA)
	if d.Workflow != nil && d.NodeBaseSHA != "" {
		base = d.NodeBaseSHA
	}
	if offer.Commit != head || offer.Base != base || head == base {
		return landingCall{}, Externalf("landing offer for %s does not name a call, summary and its staged base and new HEAD", rec.RunID)
	}
	artifactDir := laneDir
	if d.RunRequest.ReadOnlyGroup != "" {
		artifactDir = run.NodeDir(laneDir, rec.RunID)
	}
	session, err := ReadSessionID(artifactDir)
	if err != nil {
		return landingCall{}, err
	}
	if session == "" {
		return landingCall{}, Externalf("landing offer for %s has no staged session", rec.RunID)
	}
	adapter := d.RunRequest.Command[0]
	capability := l.endingCapabilities[adapter]
	if capability == "" {
		capability = run.EndingFooter
	}
	call := landingCall{landingOffer: offer, ItemID: d.RunRequest.ItemID, RunID: rec.RunID, TerminalSeq: rec.EventSeq, SessionID: session, RunLog: filepath.Join(artifactDir, rec.RunID+".log"), Worktree: target, Scratch: run.DurableDir(laneDir, rec.RunID), Adapter: adapter, Capability: capability, Tool: "land"}
	if d.Workflow != nil {
		call.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	}
	return call, nil
}

const endingCapabilitiesKind = "drain.ending-capabilities"

func (l *Loop) recordEndingCapabilities(ctx context.Context) error {
	capabilities := make(map[string]string)
	for _, backend := range l.cfg.Policy.Backends {
		adapter := backend.Adapter
		if _, ok := capabilities[adapter]; ok {
			continue
		}
		path := adapter
		if !filepath.IsAbs(path) {
			path = filepath.Join(l.cfg.Root, path)
		}
		capability := run.EndingFooter
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), "ending-capability")); err == nil {
			capability, err = run.ReadEndingCapability("", path)
			if err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		capabilities[adapter] = capability
	}
	body, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: endingCapabilitiesKind, ObjectType: "drain", Payload: body, At: l.cfg.Now().UTC()})
		return err
	}); err != nil {
		return err
	}
	l.endingCapabilities = capabilities
	return nil
}

// refusalEvidence is what an answer to a refused landing call quotes from files: the gate
// plan's command for each failing check, and each check's log. It depends only on the run
// and the refusal's checks, so it is read before the write that records the answer, with
// no store write open (tools/tillr/store/ioguard.go).
type refusalEvidence struct {
	commands map[string][]string
	logs     map[string]refusalLog
}

type refusalLog struct {
	body []byte
	err  error
}

// readRefusalEvidence reads the files answerLandingRefusal quotes. A refusal with no checks
// reads nothing.
func (l *Loop) readRefusalEvidence(runID string, refusal refusalPayload) refusalEvidence {
	ev := refusalEvidence{commands: map[string][]string{}, logs: map[string]refusalLog{}}
	if len(refusal.Checks) == 0 {
		return ev
	}
	planPath := filepath.Join(RunDir(l.cfg.Root, runID), GatePlanRecord)
	if raw, ok := readRecordPayload(planPath); ok {
		var plan struct {
			Checks []struct {
				CheckID string   `json:"check_id"`
				Command []string `json:"command"`
			} `json:"checks"`
		}
		if json.Unmarshal(raw, &plan) == nil {
			for _, check := range plan.Checks {
				ev.commands[check.CheckID] = check.Command
			}
		}
	}
	for _, check := range refusal.Checks {
		if check.LogRef == "" {
			continue
		}
		if _, seen := ev.logs[check.LogRef]; !seen {
			body, err := os.ReadFile(check.LogRef)
			ev.logs[check.LogRef] = refusalLog{body: body, err: err}
		}
	}
	return ev
}

// answerLandingRefusal answers the run's open landing call with the refusal, quoting ev,
// which the caller read before its write (readRefusalEvidence). It reads no file.
func (l *Loop) answerLandingRefusal(tx *store.Tx, candidate Candidate, refusal refusalPayload, ev refusalEvidence, at time.Time) error {
	outcome := ""
	switch refusal.Step {
	case RefusalFence:
		outcome = "fence-refused"
	case RefusalGate:
		outcome = "gate-refused"
	case RefusalConflict:
		outcome = "conflict"
	case RefusalReverted:
		outcome = "reverted"
	case ReasonEvalFailed:
		outcome = "judge-refused"
	case IdenticalRepeatPrefix:
		outcome = "identical-repeat"
	}
	choice := chooseLandingCallAnswer(outcome)
	if choice.Kind == "" {
		return nil
	}
	var raw []byte
	err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?`, callOffered, candidate.RunID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var call landingCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "ANSWER: %s\n", choice.Kind)
	if choice.Stage != "" {
		fmt.Fprintf(&text, "Stage: %s\n", choice.Stage)
	}
	if refusal.Detail != "" {
		fmt.Fprintln(&text, refusal.Detail)
	}
	head := candidate.HeadSHA
	if head == "" {
		head = call.Commit
	}
	base := candidate.BaseSHA
	if base == "" {
		base = call.Base
	}
	fmt.Fprintf(&text, "Offer commit: %s\nBranch head: %s\nLast base: %s\nOffer run: %s\nOffer scratch: %s\n", call.Commit, head, base, call.RunID, call.Scratch)
	commands := ev.commands
	answer := landingAnswer{CallID: call.CallID, Kind: choice.Kind, Stage: choice.Stage}
	if c := candidate.Judge; c != nil && choice.Stage == "judge" {
		var output item.JudgeOutput
		if json.Unmarshal(c.Raw, &output) == nil {
			for _, verdict := range output.Verdicts {
				if verdict.Verdict == item.VerdictNotDone {
					fmt.Fprintf(&text, "Criterion %s: %s\n", verdict.CriterionID, verdict.Reason)
					answer.Evidence = append(answer.Evidence, verdict.Evidence...)
				}
			}
		}
	}
	for _, check := range refusal.Checks {
		fmt.Fprintf(&text, "Check: %s\n", check.CheckID)
		if argv := commands[check.CheckID]; len(argv) > 0 {
			fmt.Fprintf(&text, "Command: %q\n", argv)
		}
		if check.LogRef == "" {
			continue
		}
		answer.Evidence = append(answer.Evidence, check.LogRef)
		read := ev.logs[check.LogRef]
		log, err := read.body, read.err
		if read.body == nil && read.err == nil {
			err = os.ErrNotExist // not read before the write: quoted by path only
		}
		switch {
		case err != nil:
			fmt.Fprintf(&text, "Full log: %s\n", check.LogRef)
		case utf8.RuneCount(log) <= 30000:
			fmt.Fprintln(&text, string(log))
		default:
			chars := []rune(string(log))
			fmt.Fprintf(&text, "%s\n[Full log: %s]\n%s\n", string(chars[:5000]), check.LogRef, string(chars[len(chars)-25000:]))
		}
	}
	answer.Text = text.String()
	closed, err := judgmentSeen(tx, callClosed, "call", call.CallID)
	if err != nil || closed != 0 {
		return err
	}
	return appendLandingCallEvent(tx, callAnswered, call.CallID, answer, at)
}

// writeLandingAnswer publishes a stored answer before reopening its session.
// Shipped adapters do not yet call this hand-off.
func writeLandingAnswer(laneDir, runID string, answer landingAnswer) error {
	dir := run.DurableDir(laneDir, runID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".landing-answer-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(answer)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, landingAnswerFile))
}

type landingCallDelivery struct {
	Form  string `json:"form"`
	RunID string `json:"run_id"`
}

// deliverLandingAnswer is the drain-owned file handoff. The opener reports
// success only after the harness confirms it consumed the stored result. No
// shipped adapter is wired to this opener yet.
func (l *Loop) deliverLandingAnswer(ctx context.Context, callID string, d Decision, open func(context.Context) error) error {
	req := d.RunRequest
	if req == nil || !req.SessionTurn {
		return Usagef("landing answer delivery needs a new session turn")
	}
	laneDir := l.laneDirPath(d.LaneID)
	session, err := ReadSessionID(laneDir)
	if err != nil {
		return err
	}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var answer landingAnswer
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var offerRaw, answerRaw []byte
		var delivered bool
		if err := r.QueryRow(`SELECT o.payload,a.payload,EXISTS(SELECT 1 FROM events e WHERE e.object_type='call' AND e.object_id=o.object_id AND e.kind=?)
   FROM events o JOIN events a ON a.object_type=o.object_type AND a.object_id=o.object_id AND a.kind=?
   WHERE o.object_type='call' AND o.kind=? AND o.object_id=?`, callDelivered, callAnswered, callOffered, callID).Scan(&offerRaw, &answerRaw, &delivered); err != nil {
			return fmt.Errorf("reading answer for call %s: %w", callID, err)
		}
		if delivered {
			return Usagef("landing call %s has already been delivered", callID)
		}
		var call landingCall
		if err := json.Unmarshal(offerRaw, &call); err != nil {
			return err
		}
		if req.RunID == call.RunID || req.ItemID != call.ItemID || session != call.SessionID || session == "" || l.laneTarget(d.LaneID) != call.Worktree {
			return Usagef("delivery run %s does not reopen the session for landing call %s", req.RunID, callID)
		}
		return json.Unmarshal(answerRaw, &answer)
	})
	db.Close()
	if err != nil {
		return err
	}
	if err := writeLandingAnswer(laneDir, req.RunID, answer); err != nil {
		return err
	}
	if err := open(ctx); err != nil {
		return err
	}
	db, err = store.Open(context.WithoutCancel(ctx), l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.WriteTx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		return appendLandingCallEvent(tx, callDelivered, callID, landingCallDelivery{Form: "tool-result", RunID: req.RunID}, l.cfg.Now().UTC())
	})
}

// Observe the same comparison as markExamined before persisting an irreversible
// refusal answer. The read is out of the writer transaction; only the drain
// owns the item's examination, and an existing native refusal is idempotent.
func (l *Loop) landingRefusalRepeats(ctx context.Context, c Candidate, refusal refusalPayload) (bool, error) {
	if refusal.Evidence == "" || (refusal.Step != RefusalGate && refusal.Step != RefusalFence) {
		return false, nil
	}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var repeated bool
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var offered bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?)`, callOffered, c.RunID).Scan(&offered); err != nil || !offered {
			return err
		}
		history, err := readRunHistory(r)
		if err != nil {
			return err
		}
		chain, decisions, err := readItemChain(r, history, c.ItemID)
		if err != nil {
			return err
		}
		d := decisions[c.RunID]
		if d.BlockConflict != nil {
			return nil
		}
		_, repeated = repeatedEnding(chain, chainEnding{RunID: c.RunID, Producer: producerOf(d), Cause: refusal.Step, Key: refusal.Evidence})
		return nil
	})
	return repeated, err
}
