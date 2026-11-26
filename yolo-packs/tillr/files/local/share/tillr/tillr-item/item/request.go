package item

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watchlinks"
)

// A filed request is an item the owner or an operator files straight into the queue from
// the command line: its words are the whole statement of the work, it names an existing
// workflow, and it never has a document block. The store holds the words
// (request_words), so the record is the item's only declaration and sync never counts it
// as unshared (see StoreDeclared in sync.go). The face is `tillr item request`.

// SingleWorkerWorkflow is the drain's built-in one-node workflow; no file defines it.
const SingleWorkerWorkflow = "single-worker@v1"

// RequestDefaultWorkflow is the workflow a request runs when the filer names none, by seat.
var RequestDefaultWorkflow = map[string]string{
	"design": "scouted-design@v1",
	"build":  "planned-build@v1",
}

// MaxRequestCriteria bounds the fixed criteria plus the filer's own.
const MaxRequestCriteria = 8

// requestCriteria are every request's criteria. They judge the work against the words and
// the repository's rules, and never ask for an item block.
var requestCriteria = []string{
	"The change does what the request's words ask, and nothing the words do not ask for is changed.",
	"The change is verified the way this repository's rules require for its kind: new or changed behavior has a test in the same commit, and each changed Markdown document passes `uvx vantage-check` on that file.",
	"No `<!-- tillr: item … -->` comment is added, edited or relied on: the item finishes with no document block.",
	"Nothing only the owner can decide is guessed: such a decision is left to the owner, with a question and its options, not settled in the change.",
}

// RequestFiling is one request to file. Words are the request's own words, byte for byte.
type RequestFiling struct {
	Words       []byte
	Title       string
	Seat        string
	Workflow    string
	Paths       []string
	Criteria    []string
	BlockedBy   []string
	Alerts      []string
	FiledBy     string
	DedupKey    string
	Ordinary    bool
	Destination Destination
	At          time.Time
	By, Via     string
}

// RequestWorkflowCheck reads the named workflow from the repository (falling back to the
// tillr installation when no file exists in the repository) and refuses one the drain could
// not run for a request: a reference of the wrong shape, a file that is not there, or a
// workflow whose top-level args a request cannot supply. It reads files, so it runs before
// the store's write transaction opens, never inside it.
func RequestWorkflowCheck(root, ref string) error {
	if !WorkflowRefValid(ref) {
		return Rejectionf("workflow-reference", "", "%q is not a workflow reference of the form <name>@v<N>", ref)
	}
	if ref == SingleWorkerWorkflow {
		return nil
	}
	file, _, err := ResolveWorkflowFile(root, ref)
	if err != nil {
		return Rejectionf("workflow-missing", "", "no workflow file config/tillr/workflows/%s.json under %s (or in tillr installation): %v", ref, root, err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return Rejectionf("workflow-missing", "", "workflow file %s is not readable: %v", file, err)
	}
	var w struct {
		Args map[string]json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Rejectionf("workflow-missing", "", "%s is not a workflow: %v", file, err)
	}
	if len(w.Args) != 0 {
		return Rejectionf("workflow-needs-args", "", "%s declares top-level args, which a request does not supply; use the face made for it (doc-triage@v1 has `tillr item doc-triage`) or another workflow", ref)
	}
	return nil
}

// requestInputHash is the identity of everything the filer chose except the place: a
// refile with the same words and the same choices is the same request.
func requestInputHash(f RequestFiling) string {
	raw, _ := json.Marshal([]any{string(f.Words), f.Title, f.Seat, f.Workflow, f.Paths, f.Criteria, f.Ordinary, f.BlockedBy, f.Alerts})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// normalizeRequest validates and cleans a filing. It reads nothing but its argument.
func normalizeRequest(f *RequestFiling) error {
	if _, err := IdeaHash(f.Words); err != nil {
		return err
	}
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" || strings.ContainsAny(f.Title, "\r\n") {
		return Rejectionf("title-required", "", "a request needs a one-line --title")
	}
	if strings.Contains(f.Title, "{{") {
		return Rejectionf("title-braces", "", "the title carries a literal `{{`, which the brief template reads as a placeholder")
	}
	if f.Seat == "" {
		f.Seat = "build"
	}
	if !ValidSeatName(f.Seat) {
		return Usagef(invalidSeatName, f.Seat)
	}
	if f.Workflow == "" {
		f.Workflow = RequestDefaultWorkflow[f.Seat]
		if f.Workflow == "" {
			return Rejectionf("workflow-required", "", "seat %q has no default workflow for a request; name one with --workflow", f.Seat)
		}
	}
	if !WorkflowRefValid(f.Workflow) {
		return Rejectionf("workflow-reference", "", "%q is not a workflow reference of the form <name>@v<N>", f.Workflow)
	}
	f.Paths = nil
	criteria := []string{}
	for _, c := range f.Criteria {
		c = strings.TrimSpace(c)
		if c == "" {
			return Rejectionf("criterion-empty", "", "a --criterion must not be empty")
		}
		if strings.Contains(c, "{{") {
			return Rejectionf("criterion-braces", "", "a --criterion carries a literal `{{`, which the brief template reads as a placeholder")
		}
		criteria = append(criteria, c)
	}
	if len(requestCriteria)+len(criteria) > MaxRequestCriteria {
		return Rejectionf("too-many-criteria", "", "a request carries %d fixed criteria and at most %d of its own", len(requestCriteria), MaxRequestCriteria-len(requestCriteria))
	}
	f.Criteria = criteria
	seenDeps := map[string]bool{}
	deps := []string{}
	for _, dep := range f.BlockedBy {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			return Rejectionf("edge-target-missing", "", "--blocked-by must name an item id")
		}
		if !seenDeps[dep] {
			seenDeps[dep] = true
			deps = append(deps, dep)
		}
	}
	slices.Sort(deps)
	f.BlockedBy = deps
	seenAlerts := map[string]bool{}
	alerts := []string{}
	for _, alertID := range f.Alerts {
		alertID = strings.TrimSpace(alertID)
		if alertID == "" {
			return Rejectionf("alert-id-empty", "", "--alert must name an alert id")
		}
		if !seenAlerts[alertID] {
			seenAlerts[alertID] = true
			alerts = append(alerts, alertID)
		}
	}
	slices.Sort(alerts)
	f.Alerts = alerts
	f.FiledBy = strings.TrimSpace(f.FiledBy)
	f.By = strings.TrimSpace(f.By)
	if f.FiledBy == "" && f.By != "" {
		f.FiledBy = f.By
	}
	if f.By == "" && f.FiledBy != "" {
		f.By = f.FiledBy
	}
	f.DedupKey = strings.TrimSpace(f.DedupKey)
	if f.DedupKey != "" {
		ruleID, causeKey, ok := strings.Cut(f.DedupKey, ":")
		if !ok || strings.TrimSpace(ruleID) == "" || strings.TrimSpace(causeKey) == "" || strings.ContainsAny(f.DedupKey, "\r\n") {
			return Rejectionf("dedup-key-invalid", "", "--dedup-key must have the form <rule_id>:<cause_key>")
		}
	}
	if !f.Destination.Valid() {
		return Usagef("a request needs at most one of --to first|last, --before <item>, --after <item>")
	}
	return nil
}

func isOpenState(d definition.Definition, state string) bool {
	return !d.IsFinished(state) && state != d.Closed && state != "abandoned" && state != "deleted"
}

func findOpenByDedupKey(r *store.Reader, d definition.Definition, dedupKey string) (string, string, bool, error) {
	rows, err := r.Query(`SELECT rw.item_id, i.state, rw.filed_by FROM request_words rw JOIN items i USING(item_id) WHERE rw.dedup_key = ? ORDER BY i.updated_at ASC, rw.item_id ASC`, dedupKey)
	if err != nil {
		return "", "", false, Internalf("checking dedup_key %q: %v", dedupKey, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, state, fb string
		if err := rows.Scan(&id, &state, &fb); err != nil {
			return "", "", false, Internalf("scanning dedup_key %q: %v", dedupKey, err)
		}
		if isOpenState(d, state) {
			return id, fb, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", false, Internalf("iterating dedup_key %q: %v", dedupKey, err)
	}
	return "", "", false, nil
}

// FileRequest files one request in the caller's transaction and places it. It does no I/O
// but the store's: the words arrive in the filing, and the workflow file was checked
// (RequestWorkflowCheck) before the transaction opened. Filing the same words with the same
// choices again answers the first item's place and writes nothing, unless the item is
// closed. The same words with different choices are refused, naming the item they already
// filed.
func FileRequest(ctx context.Context, tx *store.Tx, f RequestFiling) (record.Envelope, error) {
	if err := normalizeRequest(&f); err != nil {
		return record.Envelope{}, err
	}
	r := tx.Reader()
	if f.DedupKey != "" {
		d, err := itemDefinition(r)
		if err != nil {
			return record.Envelope{}, err
		}
		existingID, existingFiledBy, found, err := findOpenByDedupKey(r, d, f.DedupKey)
		if err != nil {
			return record.Envelope{}, err
		}
		if found {
			for _, alertID := range f.Alerts {
				if err := watchlinks.LinkAlertItemInTx(tx, alertID, existingID, watchlinks.LinkRelationFiled, f.By, "", f.At); err != nil {
					if errors.Is(err, watchlinks.ErrAlertNotFound) {
						return record.Envelope{}, Rejectionf("alert-not-found", alertID, "no alert %q in this store", alertID)
					}
					return record.Envelope{}, Internalf("linking alert %s to item %s: %v", alertID, existingID, err)
				}
			}
			pos, err := queuePosition(r, "item.request", existingID, f.At)
			if err != nil {
				return record.Envelope{}, err
			}
			var answer map[string]any
			if err := json.Unmarshal(pos.Payload, &answer); err != nil {
				return record.Envelope{}, Internalf("reading deduplicated position: %v", err)
			}
			answer["deduplicated"] = true
			answer["dedup_key"] = f.DedupKey
			if existingFiledBy != "" {
				answer["filed_by"] = existingFiledBy
			}
			linked, err := watchlinks.LinkedAlertsForItem(r, existingID)
			if err != nil {
				return record.Envelope{}, Internalf("reading linked alerts for %s: %v", existingID, err)
			}
			if len(linked) > 0 {
				ids := make([]string, 0, len(linked))
				for _, la := range linked {
					ids = append(ids, la.AlertID)
				}
				answer["linked_alerts"] = ids
			}
			return envelopeOf("item.request", existingID, f.At, answer)
		}
	}
	hash, _ := IdeaHash(f.Words)
	input := requestInputHash(f)
	var id, held, state string
	err := r.QueryRow(`SELECT item_id, input_hash, state FROM request_words JOIN items USING(item_id) WHERE words_hash=?`, hash).Scan(&id, &held, &state)
	if err == nil {
		if held != input {
			return record.Envelope{}, Rejectionf("request-differs", id, "these words already filed %s with a different title, seat, workflow, criteria, express choice or dependencies; change that item, or file different words", id)
		}
		d, err := itemDefinition(r)
		if err != nil {
			return record.Envelope{}, err
		}
		if state == d.Closed {
			return record.Envelope{}, Rejectionf("request-closed", id, "request matched closed item %s; nothing was queued. To resume it, use tillr item reopen --item %s --reason <text> --by <who>", id, id)
		}
		return queuePosition(r, "item.request", id, f.At)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return record.Envelope{}, Internalf("reading request words: %v", err)
	}
	id = ""
	for n := 10; n <= len(hash); n++ {
		candidate := "item-request-" + hash[:n]
		var count int
		if err := tx.Reader().QueryRow(`SELECT count(*) FROM items WHERE item_id=?`, candidate).Scan(&count); err != nil {
			return record.Envelope{}, Internalf("checking item %s: %v", candidate, err)
		}
		if count == 0 {
			id = candidate
			break
		}
	}
	if id == "" {
		return record.Envelope{}, Internalf("no unique request id for hash %s", hash)
	}
	return insertRequest(ctx, tx, f, id, hash, input)
}

// insertRequest writes a validated filing. Split parts have their own identity namespace;
// their request words and criteria otherwise use the ordinary request path.
func insertRequest(ctx context.Context, tx *store.Tx, f RequestFiling, id, hash, input string) (record.Envelope, error) {
	criteria := []criterion{}
	for i, text := range append(append([]string{}, requestCriteria...), f.Criteria...) {
		criteria = append(criteria, criterion{CriterionID: fmt.Sprintf("c%d", i+1), Text: text})
	}
	payload := itemPayload{
		ItemID: id, Title: f.Title, State: "in-queue", Seat: f.Seat, Workflow: f.Workflow,
		AllowedPaths: f.Paths, BlockedBy: f.BlockedBy, Express: !f.Ordinary, Criteria: criteria,
	}
	env, err := envelopeOf(RecordKind, id, f.At, payload)
	if err != nil {
		return record.Envelope{}, err
	}
	if _, err := Apply(ctx, tx, env); err != nil {
		return record.Envelope{}, err
	}
	if _, err := tx.Exec(`INSERT INTO request_words(item_id, words_hash, words, input_hash, filed_by, dedup_key) VALUES(?,?,?,?,?,?)`, id, hash, f.Words, input, f.FiledBy, f.DedupKey); err != nil {
		return record.Envelope{}, Internalf("saving the request's words: %v", err)
	}
	placed, err := Place(ctx, tx, id, f.Destination, f.At, f.By, f.Via)
	if err != nil {
		return record.Envelope{}, err
	}
	for _, alertID := range f.Alerts {
		if err := watchlinks.LinkAlertItemInTx(tx, alertID, id, watchlinks.LinkRelationFiled, f.By, "", f.At); err != nil {
			if errors.Is(err, watchlinks.ErrAlertNotFound) {
				return record.Envelope{}, Rejectionf("alert-not-found", alertID, "no alert %q in this store", alertID)
			}
			return record.Envelope{}, Internalf("linking alert %s to item %s: %v", alertID, id, err)
		}
	}
	var answer map[string]any
	if err := json.Unmarshal(placed.Payload, &answer); err != nil {
		return record.Envelope{}, Internalf("reading the placement: %v", err)
	}
	answer["seat"], answer["workflow"], answer["allowed_paths"], answer["blocked_by"], answer["words_size"] = f.Seat, f.Workflow, f.Paths, f.BlockedBy, len(f.Words)
	if f.FiledBy != "" {
		answer["filed_by"] = f.FiledBy
	}
	if f.DedupKey != "" {
		answer["dedup_key"] = f.DedupKey
	}
	if len(f.Alerts) > 0 {
		answer["linked_alerts"] = f.Alerts
	}
	return envelopeOf("item.request", id, f.At, answer)
}

// RequestWords answers a filed request's words, or nil for any other item. It also answers
// nil on a store that has never filed one.
func RequestWords(r *store.Reader, id string) ([]byte, error) {
	var present int
	if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='request_words'`).Scan(&present); err != nil {
		return nil, Internalf("checking request words: %v", err)
	}
	if present == 0 {
		return nil, nil
	}
	var words []byte
	err := r.QueryRow(`SELECT words FROM request_words WHERE item_id=?`, id).Scan(&words)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, Internalf("reading item %s's request words: %v", id, err)
	}
	return words, nil
}

// The words are kept apart from the item definition, which sync and placement may rewrite;
// the unique hash makes a concurrent refile the same item.
func init() {
	if err := store.RegisterDDL("request-words", `CREATE TABLE IF NOT EXISTS request_words (
 item_id TEXT PRIMARY KEY REFERENCES items(item_id), words_hash TEXT NOT NULL UNIQUE,
 words BLOB NOT NULL, input_hash TEXT NOT NULL,
 filed_by TEXT NOT NULL DEFAULT '', dedup_key TEXT NOT NULL DEFAULT ''
 );`); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn("request-words", "request_words", "filed_by", "TEXT NOT NULL DEFAULT ''"); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn("request-words", "request_words", "dedup_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
		panic(err)
	}
}
