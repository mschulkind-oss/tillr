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

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const deliveryDecisionKind = "drain.delivery-decision"
const deliveryPartialKind = "drain.delivery-partial"
const deliveryOutput = "delivery"
const deliverySeat = "classify"

func init() {
	for _, kind := range []string{deliveryDecisionKind, deliveryPartialKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

const deliveryShape = `{"kind": "repo"}, {"kind": "fork", "fork": "<name>"}, or {"kind": "both", "fork": "<name>"}`

const deliveryPrompt = `Classify delivery from the item's words and repository. Write the required delivery JSON output as a JSON object (not a bare string): ` + deliveryShape + `. For fork or both, name exactly one fork from bin/pi-forks.list in the "fork" field (omit "fork" for "repo"). A both decision delivers the fork first. Never infer the target from fence entries. For fork or both, do not use do-it-now and do not propose repository pieces before fork delivery.`

type deliveryParseError struct{ err error }

func (e deliveryParseError) Error() string { return e.err.Error() }
func (e deliveryParseError) Unwrap() error { return e.err }

func classificationNode(stage WorkflowStage) bool {
	// Planned-build planners and previously recorded single-worker graphs
	// can still carry the delivery output on the plan seat.
	return (stage.Seat == deliverySeat || stage.Seat == "plan") && stage.Access == "read_only" && stage.Outputs[deliveryOutput] == "json"
}

func classifyDelivery(root string, artifacts []nodeArtifact, quickWay bool) (*deliveryDecision, error) {
	for _, a := range artifacts {
		if a.Name != deliveryOutput {
			continue
		}
		raw, err := os.ReadFile(a.Location)
		if err != nil {
			return nil, err
		}
		var d deliveryDecision
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			return nil, deliveryParseError{err}
		}
		if d.ForkLanded != "" || d.ForkRun != "" {
			return nil, fmt.Errorf("classification cannot claim publication")
		}
		known, err := deliveryCatalog(root)
		if err != nil {
			return nil, err
		}
		if _, err := d.pieces(known); err != nil {
			return nil, deliveryParseError{err}
		}
		if d.activeFork() != "" {
			if quickWay {
				return nil, fmt.Errorf("fork delivery must run on its staged checkout")
			}
			for _, artifact := range artifacts {
				if artifact.Type == "graph.proposal" {
					return nil, fmt.Errorf("cannot expand repository work before fork delivery")
				}
			}
		}
		return &d, nil
	}
	return nil, fmt.Errorf("classification has no delivery output")
}

func planDelivery(w *Workflow, selected string, row itemRow, known map[string]bool) *Workflow {
	if known == nil || IsGeneratedRepair(row.ItemID) {
		return w
	}
	if w == nil && selected == singleWorkerWorkflow && row.Delivery.Kind == "" {
		return &Workflow{Name: "single-worker", Version: 1, Stages: []WorkflowStage{
			{ID: "classify", Kind: "agent", Access: "read_only", Seat: deliverySeat, Prompt: deliveryPrompt, Outputs: map[string]string{deliveryOutput: "json"}},
			{ID: "work", Kind: "agent", Access: "write", Seat: row.Seat},
		}, Edges: []WorkflowEdge{{From: "classify", To: "work"}}}
	}
	if w != nil && len(w.Stages) > 0 && row.Delivery.Kind != "" && classificationNode(w.Stages[0]) {
		if selected == "planned-build@v1" && row.Delivery.Kind == "repo" {
			// A repository retry still has a planner (including plan-wrong
			// returns); it need not classify the already recorded target.
			copy := *w
			copy.Stages = append([]WorkflowStage(nil), w.Stages...)
			stage := &copy.Stages[0]
			stage.Prompt = strings.TrimPrefix(stage.Prompt, deliveryPrompt+"\n\n")
			outputs := map[string]string{}
			for name, typ := range stage.Outputs {
				if name != deliveryOutput {
					outputs[name] = typ
				}
			}
			stage.Outputs = outputs
			return &copy
		}
		// A new attempt starts in the recorded piece. The classifier is kept
		// in the original graph and accounts for its original attempt only.
		w = withoutDeliveryStage(w, w.Stages[0].ID)
		return workflowForDelivery(w, row.Delivery)
	}
	if w != nil && len(w.Stages) > 0 && row.Delivery.Kind == "" && selected == "planned-build@v1" && !classificationNode(w.Stages[0]) {
		copy := *w
		copy.Stages = append([]WorkflowStage(nil), w.Stages...)
		stage := &copy.Stages[0]
		outputs := map[string]string{deliveryOutput: "json"}
		for k, v := range stage.Outputs {
			outputs[k] = v
		}
		stage.Outputs = outputs
		stage.Prompt = deliveryPrompt + "\n\n" + stage.Prompt
		return &copy
	}
	return workflowForDelivery(w, row.Delivery)
}

// The fork gate judges the first piece; the final repository evaluator judges
// completion of the item. Running the latter before the second piece exists
// would ask it to pass criteria whose work has not been dispatched yet.
func workflowForDelivery(w *Workflow, d deliveryDecision) *Workflow {
	if slot := evaluatorSlot(w); slot != "" && d.Kind == "both" && d.activeFork() != "" {
		return withoutDeliveryStage(w, slot)
	}
	return w
}

func withoutDeliveryStage(w *Workflow, id string) *Workflow {
	copy := *w
	copy.Stages, copy.Edges = nil, nil
	for _, stage := range w.Stages {
		if stage.ID != id {
			copy.Stages = append(copy.Stages, stage)
		}
	}
	for _, e := range w.Edges {
		if e.From != id && e.To != id {
			copy.Edges = append(copy.Edges, e)
		}
	}
	if w.Exit != nil {
		exit := *w.Exit
		exit.RequiresNodes = nil
		for _, n := range w.Exit.RequiresNodes {
			if n != id {
				exit.RequiresNodes = append(exit.RequiresNodes, n)
			}
		}
		if exit.EvaluatorSlot == id {
			exit.EvaluatorSlot = ""
		}
		copy.Exit = &exit
	}
	return &copy
}

// Forks are available only where the repository declares a fork catalog. The
// catalog determines valid names, never an item's checkout paths.
func deliveryCatalog(root string) (map[string]bool, error) {
	if root == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(root, "bin", "pi-forks.list"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		name := filepath.Base(fields[0])
		if len(fields) < 2 || name == "." || name == ".." || strings.ContainsAny(name, `\:`) {
			return nil, fmt.Errorf("invalid fork catalog entry: %s", line)
		}
		known[name] = true
	}
	return known, nil
}

func (l *Loop) recordedDelivery(ctx context.Context, id string) (deliveryDecision, error) {
	var d deliveryDecision
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return d, err
	}
	defer db.Close()
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		d, err = readDelivery(r, id)
		return err
	})
	return d, err
}

func readDelivery(r *store.Reader, id string) (deliveryDecision, error) {
	var d deliveryDecision
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE object_type='item' AND object_id=?
		AND kind IN (?, ?) ORDER BY seq DESC LIMIT 1`, id, deliveryDecisionKind, deliveryPartialKind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(raw, &d)
	return d, err
}

func readDeliveries(r *store.Reader) (map[string]deliveryDecision, error) {
	events, err := r.EventsOfKind(0, deliveryDecisionKind, deliveryPartialKind)
	if err != nil {
		return nil, err
	}
	out := map[string]deliveryDecision{}
	for _, e := range events {
		switch e.Kind {
		case deliveryDecisionKind, deliveryPartialKind:
			// A partial receipt includes the full decision. Events arrive in
			// sequence order, so the latest record of either kind wins.
			if e.ObjectID == nil {
				return nil, fmt.Errorf("delivery record has no item")
			}
			var d deliveryDecision
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				return nil, fmt.Errorf("reading delivery for %s: %w", *e.ObjectID, err)
			}
			out[*e.ObjectID] = d
		}
	}
	return out, nil
}

// deliveryDecision describes the planner's delivery target independently of path hints.
type deliveryDecision struct {
	Kind string `json:"kind"`
	Fork string `json:"fork,omitempty"`
	// ForkLanded is the durable receipt of the first piece, not planner input.
	ForkLanded string `json:"fork_landed,omitempty"`
	ForkRun    string `json:"fork_run,omitempty"`
}

type repositoryBeforeFork struct{ fork string }

func (e *repositoryBeforeFork) Error() string {
	return fmt.Sprintf("land fork %s first, then offer the repository candidate", e.fork)
}

// A repository offer before the fork receipt is worker feedback. Any other
// target mismatch is an examination fault, not instructions to change targets.
func (l *Loop) refuseOutOfOrderDelivery(ctx context.Context, c Candidate, result *LoopResult) (bool, error) {
	err := l.checkDeliveryOrder(ctx, c)
	var early *repositoryBeforeFork
	if !errors.As(err, &early) {
		return false, err
	}
	c.RefusalDetail = early.Error()
	return true, l.refuse(ctx, c, result, RefusalDeliveryOrder)
}

func (l *Loop) checkDeliveryOrder(ctx context.Context, c Candidate) error {
	d, err := l.recordedDelivery(ctx, c.ItemID)
	if err != nil {
		return err
	}
	if d.Kind != "both" {
		return nil
	}
	patchRef := "refs/heads/patch/" + d.Fork
	if d.ForkLanded == "" && c.BaseRef == "" {
		return &repositoryBeforeFork{fork: d.Fork}
	}
	if d.ForkLanded == "" && c.BaseRef == patchRef ||
		d.ForkLanded != "" && (c.BaseRef == "" || c.BaseRef == patchRef && c.RunID == d.ForkRun) {
		return nil
	}
	return Internalf("mixed delivery candidate %s targets %q out of order", c.RunID, c.BaseRef)
}

func (l *Loop) settlePartialDelivery(ctx context.Context, c Candidate, sha string) (bool, error) {
	if !strings.HasPrefix(c.BaseRef, "refs/heads/patch/") {
		return false, nil
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var partial bool
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		d, err := readDelivery(tx.Reader(), c.ItemID)
		if err != nil {
			return err
		}
		if d.Kind != "both" || c.BaseRef != "refs/heads/patch/"+d.Fork {
			return nil
		}
		partial = true
		if sha == "" {
			return fmt.Errorf("partial fork delivery has no landing commit")
		}
		if d.ForkLanded != "" {
			if d.ForkLanded == sha && d.ForkRun == c.RunID {
				return nil
			}
			return fmt.Errorf("partial fork delivery receipt changed for %s", c.ItemID)
		}
		d.ForkLanded, d.ForkRun = sha, c.RunID
		return appendEffectOn(tx, deliveryPartialKind, "item", c.ItemID, d, l.cfg.Now().UTC())
	})
	return partial, err
}

func (d deliveryDecision) activeFork() string {
	if d.Kind == "fork" || (d.Kind == "both" && d.ForkLanded == "") {
		return d.Fork
	}
	return ""
}

func checkoutForDelivery(root, lane string, d deliveryDecision) ForkTarget {
	fork := d.activeFork()
	if fork == "" {
		return ForkTarget{}
	}
	path := ".forks/lanes/" + lane + "/" + fork
	return ForkTarget{Checkout: filepath.Join(root, filepath.FromSlash(path)), Ref: "refs/heads/patch/" + fork, Path: path, Fork: fork}
}

// forkFence applies containment only after the target has been decided. A
// repository fence does not select a fork, nor does it restrict the fork piece.
func forkFence(fork ForkTarget, paths []string) []string {
	var scoped []string
	for _, p := range paths {
		if _, name, suffix, ok := item.ForkCheckoutPath(p); ok && name == fork.Fork {
			scoped = append(scoped, fork.Path+suffix)
		}
	}
	return scoped
}

func itemDeliveryFence(fork ForkTarget, paths []string) []string {
	if narrowed := forkFence(fork, paths); len(narrowed) > 0 {
		return narrowed
	}
	return []string{fork.Path + "/"}
}

// pieces returns delivery targets in landing order. An empty target denotes the repository.
func (d deliveryDecision) pieces(known map[string]bool) ([]string, error) {
	switch d.Kind {
	case "repo":
		if d.Fork != "" {
			return nil, fmt.Errorf("repository delivery cannot name a fork")
		}
		return []string{""}, nil
	case "fork", "both":
		if d.Fork == "" || !known[d.Fork] {
			return nil, fmt.Errorf("unknown delivery fork %q", d.Fork)
		}
		if d.Kind == "both" {
			return []string{d.Fork, ""}, nil
		}
		return []string{d.Fork}, nil
	default:
		return nil, fmt.Errorf("invalid delivery kind %q", d.Kind)
	}
}
