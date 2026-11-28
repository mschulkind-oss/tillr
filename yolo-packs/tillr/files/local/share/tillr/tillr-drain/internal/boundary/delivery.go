package boundary

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

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr-drain/workflow"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const DeliveryDecisionKind = "drain.delivery-decision"
const DeliveryPartialKind = "drain.delivery-partial"
const DeliveryOutput = "delivery"
const deliverySeat = "classify"

func init() {
	for _, kind := range []string{DeliveryDecisionKind, DeliveryPartialKind} {
		if err := event.Register(kind, drainmodel.Utility); err != nil {
			panic(err)
		}
	}
}

const DeliveryShape = `{"kind": "repo"}, {"kind": "fork", "fork": "<name>"}, or {"kind": "both", "fork": "<name>"}`

const DeliveryPrompt = `Classify delivery from the item's words and repository. Write the required delivery JSON output as a JSON object (not a bare string): ` + DeliveryShape + `. For fork or both, name exactly one fork from bin/pi-forks.list in the "fork" field (omit "fork" for "repo"). A both decision delivers the fork first. Never infer the target from fence entries. For fork or both, do not use do-it-now and do not propose repository pieces before fork delivery.`

type DeliveryParseError struct{ Err error }

func (e DeliveryParseError) Error() string { return e.Err.Error() }
func (e DeliveryParseError) Unwrap() error { return e.Err }

type DeliveryArtifact struct {
	Name     string
	Type     string
	Location string
}

func ClassificationNode(stage workflow.WorkflowStage) bool {
	// Planned-build planners and previously recorded single-worker graphs
	// can still carry the delivery output on the plan seat.
	return (stage.Seat == deliverySeat || stage.Seat == "plan") && stage.Access == "read_only" && stage.Outputs[DeliveryOutput] == "json"
}

func ClassifyDelivery(root string, artifacts []DeliveryArtifact, quickWay bool) (*DeliveryDecision, error) {
	for _, a := range artifacts {
		if a.Name != DeliveryOutput {
			continue
		}
		raw, err := os.ReadFile(a.Location)
		if err != nil {
			return nil, err
		}
		var d DeliveryDecision
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			return nil, DeliveryParseError{err}
		}
		if d.ForkLanded != "" || d.ForkRun != "" {
			return nil, fmt.Errorf("classification cannot claim publication")
		}
		known, err := DeliveryCatalog(root)
		if err != nil {
			return nil, err
		}
		if _, err := d.Pieces(known); err != nil {
			return nil, DeliveryParseError{err}
		}
		if d.ActiveFork() != "" {
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

func PlanDelivery(
	w *workflow.Workflow,
	selected, itemID, seat string,
	delivery DeliveryDecision,
	known map[string]bool,
	isGeneratedRepair func(string) bool,
	singleWorkerWorkflow string,
	evaluatorSlot func(*workflow.Workflow) string,
) *workflow.Workflow {
	if known == nil || (isGeneratedRepair != nil && isGeneratedRepair(itemID)) {
		return w
	}
	if w == nil && selected == singleWorkerWorkflow && delivery.Kind == "" {
		return &workflow.Workflow{Name: "single-worker", Version: 1, Stages: []workflow.WorkflowStage{
			{ID: "classify", Kind: "agent", Access: "read_only", Seat: deliverySeat, Prompt: DeliveryPrompt, Outputs: map[string]string{DeliveryOutput: "json"}},
			{ID: "work", Kind: "agent", Access: "write", Seat: seat},
		}, Edges: []workflow.WorkflowEdge{{From: "classify", To: "work"}}}
	}
	if w != nil && len(w.Stages) > 0 && delivery.Kind != "" && ClassificationNode(w.Stages[0]) {
		if selected == "planned-build@v1" && delivery.Kind == "repo" {
			// A repository retry still has a planner (including plan-wrong
			// returns); it need not classify the already recorded target.
			copy := *w
			copy.Stages = append([]workflow.WorkflowStage(nil), w.Stages...)
			stage := &copy.Stages[0]
			stage.Prompt = strings.TrimPrefix(stage.Prompt, DeliveryPrompt+"\n\n")
			outputs := map[string]string{}
			for name, typ := range stage.Outputs {
				if name != DeliveryOutput {
					outputs[name] = typ
				}
			}
			stage.Outputs = outputs
			return &copy
		}
		// A new attempt starts in the recorded piece. The classifier is kept
		// in the original graph and accounts for its original attempt only.
		w = withoutDeliveryStage(w, w.Stages[0].ID)
		return WorkflowForDelivery(w, delivery, evaluatorSlot)
	}
	if w != nil && len(w.Stages) > 0 && delivery.Kind == "" && selected == "planned-build@v1" && !ClassificationNode(w.Stages[0]) {
		copy := *w
		copy.Stages = append([]workflow.WorkflowStage(nil), w.Stages...)
		stage := &copy.Stages[0]
		outputs := map[string]string{DeliveryOutput: "json"}
		for k, v := range stage.Outputs {
			outputs[k] = v
		}
		stage.Outputs = outputs
		stage.Prompt = DeliveryPrompt + "\n\n" + stage.Prompt
		return &copy
	}
	return WorkflowForDelivery(w, delivery, evaluatorSlot)
}

// The fork gate judges the first piece; the final repository evaluator judges
// completion of the item. Running the latter before the second piece exists
// would ask it to pass criteria whose work has not been dispatched yet.
func WorkflowForDelivery(w *workflow.Workflow, d DeliveryDecision, evaluatorSlot func(*workflow.Workflow) string) *workflow.Workflow {
	if evaluatorSlot == nil {
		return w
	}
	if slot := evaluatorSlot(w); slot != "" && d.Kind == "both" && d.ActiveFork() != "" {
		return withoutDeliveryStage(w, slot)
	}
	return w
}

func withoutDeliveryStage(w *workflow.Workflow, id string) *workflow.Workflow {
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
func DeliveryCatalog(root string) (map[string]bool, error) {
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

func RecordedDelivery(ctx context.Context, storePath, id string) (DeliveryDecision, error) {
	var d DeliveryDecision
	db, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return d, err
	}
	defer db.Close()
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		d, err = ReadDelivery(r, id)
		return err
	})
	return d, err
}

func ReadDelivery(r *store.Reader, id string) (DeliveryDecision, error) {
	var d DeliveryDecision
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE object_type='item' AND object_id=?
		AND kind IN (?, ?) ORDER BY seq DESC LIMIT 1`, id, DeliveryDecisionKind, DeliveryPartialKind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(raw, &d)
	return d, err
}

func ReadDeliveries(r *store.Reader) (map[string]DeliveryDecision, error) {
	events, err := r.EventsOfKind(0, DeliveryDecisionKind, DeliveryPartialKind)
	if err != nil {
		return nil, err
	}
	out := map[string]DeliveryDecision{}
	for _, e := range events {
		switch e.Kind {
		case DeliveryDecisionKind, DeliveryPartialKind:
			// A partial receipt includes the full decision. Events arrive in
			// sequence order, so the latest record of either kind wins.
			if e.ObjectID == nil {
				return nil, fmt.Errorf("delivery record has no item")
			}
			var d DeliveryDecision
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				return nil, fmt.Errorf("reading delivery for %s: %w", *e.ObjectID, err)
			}
			out[*e.ObjectID] = d
		}
	}
	return out, nil
}

func ReadDeliveriesForItems(r *store.Reader, itemIDs []string) (map[string]DeliveryDecision, error) {
	out := map[string]DeliveryDecision{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	events, err := r.EventsForObjects(0, itemIDs, DeliveryDecisionKind, DeliveryPartialKind)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		switch e.Kind {
		case DeliveryDecisionKind, DeliveryPartialKind:
			if e.ObjectID == nil {
				return nil, fmt.Errorf("delivery record has no item")
			}
			var d DeliveryDecision
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				return nil, fmt.Errorf("reading delivery for %s: %w", *e.ObjectID, err)
			}
			out[*e.ObjectID] = d
		}
	}
	return out, nil
}

// DeliveryDecision describes the planner's delivery target independently of path hints.
type DeliveryDecision struct {
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
func RefuseOutOfOrderDelivery(
	ctx context.Context,
	storePath, itemID, runID, baseRef string,
	refuse func(ctx context.Context, detail string) error,
) (bool, error) {
	err := CheckDeliveryOrder(ctx, storePath, itemID, runID, baseRef)
	var early *repositoryBeforeFork
	if !errors.As(err, &early) {
		return false, err
	}
	return true, refuse(ctx, early.Error())
}

func CheckDeliveryOrder(ctx context.Context, storePath, itemID, runID, baseRef string) error {
	d, err := RecordedDelivery(ctx, storePath, itemID)
	if err != nil {
		return err
	}
	if d.Kind != "both" {
		return nil
	}
	patchRef := "refs/heads/patch/" + d.Fork
	if d.ForkLanded == "" && baseRef == "" {
		return &repositoryBeforeFork{fork: d.Fork}
	}
	if d.ForkLanded == "" && baseRef == patchRef ||
		d.ForkLanded != "" && (baseRef == "" || baseRef == patchRef && runID == d.ForkRun) {
		return nil
	}
	return drainmodel.Internalf("mixed delivery candidate %s targets %q out of order", runID, baseRef)
}

func SettlePartialDelivery(
	ctx context.Context,
	env drainmodel.Env,
	itemID, runID, baseRef, sha string,
	appendEffect func(tx *store.Tx, kind, objectType, id string, value any, at time.Time) error,
) (bool, error) {
	if !strings.HasPrefix(baseRef, "refs/heads/patch/") {
		return false, nil
	}
	db, err := store.Open(ctx, env.StorePath, true)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var partial bool
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		d, err := ReadDelivery(tx.Reader(), itemID)
		if err != nil {
			return err
		}
		if d.Kind != "both" || baseRef != "refs/heads/patch/"+d.Fork {
			return nil
		}
		partial = true
		if sha == "" {
			return fmt.Errorf("partial fork delivery has no landing commit")
		}
		if d.ForkLanded != "" {
			if d.ForkLanded == sha && d.ForkRun == runID {
				return nil
			}
			return fmt.Errorf("partial fork delivery receipt changed for %s", itemID)
		}
		d.ForkLanded, d.ForkRun = sha, runID
		if appendEffect != nil {
			return appendEffect(tx, DeliveryPartialKind, "item", itemID, d, now().UTC())
		}
		body, err := json.Marshal(d)
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{Kind: DeliveryPartialKind, ObjectType: "item", ObjectID: &itemID, Payload: body, At: now().UTC()})
		return err
	})
	return partial, err
}

func (d DeliveryDecision) ActiveFork() string {
	if d.Kind == "fork" || (d.Kind == "both" && d.ForkLanded == "") {
		return d.Fork
	}
	return ""
}

func CheckoutForDelivery(root, lane string, d DeliveryDecision) remotesync.ForkTarget {
	fork := d.ActiveFork()
	if fork == "" {
		return remotesync.ForkTarget{}
	}
	path := ".forks/lanes/" + lane + "/" + fork
	return remotesync.ForkTarget{Checkout: filepath.Join(root, filepath.FromSlash(path)), Ref: "refs/heads/patch/" + fork, Path: path, Fork: fork}
}

// ForkFence applies containment only after the target has been decided. A
// repository fence does not select a fork, nor does it restrict the fork piece.
func ForkFence(fork remotesync.ForkTarget, paths []string) []string {
	var scoped []string
	for _, p := range paths {
		if _, name, suffix, ok := item.ForkCheckoutPath(p); ok && name == fork.Fork {
			scoped = append(scoped, fork.Path+suffix)
		}
	}
	return scoped
}

func ItemDeliveryFence(fork remotesync.ForkTarget, paths []string) []string {
	if narrowed := ForkFence(fork, paths); len(narrowed) > 0 {
		return narrowed
	}
	return []string{fork.Path + "/"}
}

// Pieces returns delivery targets in landing order. An empty target denotes the repository.
func (d DeliveryDecision) Pieces(known map[string]bool) ([]string, error) {
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
