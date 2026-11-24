package store

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/notify"
)

type appendedEvent struct {
	ev  event.Event
	seq int64
}

type txNotifyState struct {
	appended []appendedEvent
	explicit []notify.Frame
	mutated  bool
}

func (n txNotifyState) hasWork() bool {
	return len(n.appended) > 0 || len(n.explicit) > 0 || n.mutated
}

func (n txNotifyState) frames() []notify.Frame {
	var out []notify.Frame
	for _, a := range n.appended {
		for _, f := range framesForEvent(a.ev, a.seq) {
			out = mergeFrame(out, f)
		}
	}
	for _, f := range n.explicit {
		out = mergeFrame(out, f)
	}
	if len(out) == 0 && n.mutated {
		return []notify.Frame{{
			Topic: notify.TopicQueueChanged,
			AtMS:  time.Now().UnixMilli(),
		}}
	}
	return out
}

func mergeFrame(pending []notify.Frame, f notify.Frame) []notify.Frame {
	for i := range pending {
		cur := &pending[i]
		if cur.Topic == f.Topic && cur.ItemID == f.ItemID && cur.RunID == f.RunID {
			if f.Seq > cur.Seq {
				cur.Seq = f.Seq
			}
			if f.AtMS > cur.AtMS {
				cur.AtMS = f.AtMS
			}
			return pending
		}
	}
	return append(pending, f)
}

func (s *Store) publishNotify(n txNotifyState) {
	if !n.hasWork() {
		return
	}
	sockPath, ok := notify.ActiveSocketPath(s.path)
	if !ok {
		return
	}
	frames := n.frames()
	_ = notify.PublishToSocket(sockPath, frames...)
}

// Notify queues an explicit wake-hint topic on this transaction, to be published
// only if and after Commit succeeds. Optional ids supply item_id (ids[0]) and
// run_id (ids[1]).
func (t *Tx) Notify(topic string, ids ...string) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return
	}
	f := notify.Frame{
		Topic: topic,
		AtMS:  time.Now().UnixMilli(),
	}
	if len(ids) > 0 {
		f.ItemID = ids[0]
	}
	if len(ids) > 1 {
		f.RunID = ids[1]
	}
	t.notify.explicit = append(t.notify.explicit, f)
}

// NotifyFrame queues f on this transaction, to be published only if and after
// Commit succeeds.
func (t *Tx) NotifyFrame(f notify.Frame) {
	if strings.TrimSpace(f.Topic) == "" {
		return
	}
	if f.AtMS == 0 {
		f.AtMS = time.Now().UnixMilli()
	}
	t.notify.explicit = append(t.notify.explicit, f)
}

type eventPayloadPeek struct {
	ItemID      string `json:"item_id"`
	RunID       string `json:"run_id"`
	State       string `json:"state"`
	Status      string `json:"status"`
	Disposition string `json:"disposition"`
	To          string `json:"to"`
	Result      string `json:"result"`
	Outcome     string `json:"outcome"`
	Passed      *bool  `json:"passed"`
}

func framesForEvent(e event.Event, seq int64) []notify.Frame {
	atMS := time.Now().UnixMilli()
	if !e.At.IsZero() {
		atMS = e.At.UnixMilli()
	}

	var itemID, runID string
	if e.ObjectID != nil {
		switch e.ObjectType {
		case "item":
			itemID = *e.ObjectID
		case "run":
			runID = *e.ObjectID
		}
	}

	var peek eventPayloadPeek
	if len(e.Payload) > 0 {
		_ = json.Unmarshal(e.Payload, &peek)
		if itemID == "" && peek.ItemID != "" {
			itemID = peek.ItemID
		}
		if runID == "" && peek.RunID != "" {
			runID = peek.RunID
		}
	}

	topics := topicsForEvent(e.Kind, peek)
	out := make([]notify.Frame, 0, len(topics))
	for _, topic := range topics {
		out = append(out, notify.Frame{
			Topic:  topic,
			ItemID: itemID,
			RunID:  runID,
			Seq:    seq,
			AtMS:   atMS,
		})
	}
	return out
}

func topicsForEvent(kind string, peek eventPayloadPeek) []string {
	switch kind {
	case "item.landed", "item.abandoned", "item.blocked", "item.cancelled",
		"drain.item-already-terminal", "drain.run-ended-item-closed":
		return []string{notify.TopicQueueChanged, notify.TopicItemEnded}

	case "item.add", "item.update", "item.record", "item.idea", "item.place",
		"item.verdict", "item.grant", "item.verdicts-withdrawn", "item.wait-released",
		"item.conflict", "item.sync", "decision.record", "decision.revoke",
		notify.TopicQueueChanged:
		if isTerminalItemState(peek.State) || isTerminalItemState(peek.Status) ||
			isTerminalItemState(peek.Disposition) || isTerminalItemState(peek.To) {
			return []string{notify.TopicQueueChanged, notify.TopicItemEnded}
		}
		return []string{notify.TopicQueueChanged}

	case notify.TopicItemEnded:
		return []string{notify.TopicItemEnded}

	case "drain.started", "drain.dispatch-started", "drain.candidate-assigned", notify.TopicRunAssigned:
		return []string{notify.TopicRunAssigned}

	case "drain.staged", "drain.candidate-staged", notify.TopicRunStaged:
		return []string{notify.TopicRunStaged}

	case "run.allocate", "run.resume", notify.TopicRunStarted:
		return []string{notify.TopicRunStarted}

	case "run.finish", notify.TopicRunExited:
		return []string{notify.TopicRunExited, notify.TopicAgentSlotFreed}

	case "drain.examined", "drain.examination-done", "run.release", "drain.lane-released", notify.TopicAgentSlotFreed:
		return []string{notify.TopicAgentSlotFreed}

	case "drain.node-advanced", "drain.graph-changed":
		return []string{notify.TopicQueueChanged, notify.TopicAgentSlotFreed}

	case "run.record", "run.heartbeat":
		switch strings.ToLower(peek.State) {
		case "assigned":
			return []string{notify.TopicRunAssigned}
		case "staged":
			return []string{notify.TopicRunStaged}
		case "running":
			return []string{notify.TopicRunStarted}
		case "exited", "finished", "failed", "cancelled", "killed":
			return []string{notify.TopicRunExited, notify.TopicAgentSlotFreed}
		default:
			return []string{notify.TopicRunStarted}
		}

	case "drain.backend-outcome", "backend.outcome", "drain.backend-probe",
		"drain.quota-updated", "drain.backend-quota-reopened", notify.TopicBackendChanged:
		return []string{notify.TopicBackendChanged}

	case "drain.candidate-ready", "drain.candidate", "drain.prepared-candidate", notify.TopicCandidateReady:
		return []string{notify.TopicCandidateReady}

	case "gate.verdict":
		green := isPassingVerdict(peek.Result) ||
			isPassingVerdict(peek.Outcome) ||
			(peek.Passed != nil && *peek.Passed)
		if green {
			return []string{notify.TopicGateFinished, notify.TopicGatePassed}
		}
		return []string{notify.TopicGateFinished}

	case notify.TopicGatePassed:
		return []string{notify.TopicGatePassed}

	case notify.TopicGateFinished:
		return []string{notify.TopicGateFinished}

	case "land.landing", "land.landed":
		return []string{notify.TopicTrunkMoved, notify.TopicQueueChanged}

	case notify.TopicTrunkMoved:
		return []string{notify.TopicTrunkMoved}

	case notify.TopicProofGreen:
		return []string{notify.TopicProofGreen}

	case notify.TopicDocsChanged:
		return []string{notify.TopicDocsChanged}

	default:
		return []string{notify.TopicQueueChanged}
	}
}

func isPassingVerdict(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pass", "pass-inherited":
		return true
	default:
		return false
	}
}

func isTerminalItemState(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "done", "landed", "resolved-elsewhere", "abandoned", "blocked", "cancelled", "canceled", "closed":
		return true
	default:
		return false
	}
}
