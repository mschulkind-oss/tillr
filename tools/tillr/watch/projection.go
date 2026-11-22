package watch

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// CharterFacts are the store charter values exposed to watch rules.
type CharterFacts struct {
	Name, Ready, Blocked, Closed string
	States, Finished             []string
}

// A read projection fact can be unavailable; that error is itself cached.
type projectionFact[T any] struct {
	value T
	err   error
}

// storeFacts live for one snapshot, including negative lookups. A rule never
// sees different values for the same subject, even across event kinds.
type storeFacts struct {
	rd                         *store.Reader
	charter                    func() (CharterFacts, error)
	checkItems, checkWorkflows func() error
	items                      map[string]projectionFact[itemFacts]
	workflows                  map[string]projectionFact[sql.NullString]
}

const itemColumns = `SELECT state,seat,waiting_on FROM items`
const workflowColumn = `SELECT workflow FROM workflow_selections`

type itemFacts struct{ state, seat, waitingOn sql.NullString }

func readStoreFacts(rd *store.Reader) *storeFacts {
	// Check the projection even when no event supplies a subject.
	probe := func(query string) func() error {
		return sync.OnceValue(func() error {
			rows, err := rd.Query(query + " WHERE item_id IS NULL LIMIT 0")
			if err != nil {
				return err
			}
			return rows.Close()
		})
	}
	charter := sync.OnceValues(func() (CharterFacts, error) {
		rec, err := definition.ForReader(rd)
		if err != nil {
			return CharterFacts{}, err
		}
		d := rec.Parsed
		return CharterFacts{d.Name, d.Ready, d.Blocked, d.Closed, d.States, d.Finished}, nil
	})
	return &storeFacts{
		rd: rd, charter: charter,
		checkItems: probe(itemColumns), checkWorkflows: probe(workflowColumn),
		items: map[string]projectionFact[itemFacts]{}, workflows: map[string]projectionFact[sql.NullString]{},
	}
}

func (p *storeFacts) item(subject string) (itemFacts, error) {
	fact, ok := p.items[subject]
	if !ok {
		fact.err = p.rd.QueryRow(itemColumns+` WHERE item_id=?`, subject).Scan(&fact.value.state, &fact.value.seat, &fact.value.waitingOn)
		if errors.Is(fact.err, sql.ErrNoRows) {
			fact = projectionFact[itemFacts]{}
		}
		p.items[subject] = fact
	}
	return fact.value, fact.err
}

func (p *storeFacts) workflow(subject string) (sql.NullString, error) {
	fact, ok := p.workflows[subject]
	if !ok {
		fact.err = p.rd.QueryRow(workflowColumn+` WHERE item_id=?`, subject).Scan(&fact.value)
		if errors.Is(fact.err, sql.ErrNoRows) {
			fact = projectionFact[sql.NullString]{}
		}
		p.workflows[subject] = fact
	}
	return fact.value, fact.err
}

type unreadableProjection struct {
	name string
	err  error
}

func (e unreadableProjection) Error() string { return fmt.Sprintf("%s: %v", e.name, e.err) }

type ruleFacts struct {
	store    *storeFacts
	git      gitSnapshot
	charter  map[string]any
	workflow string // empty for scopes that don't filter by item workflow
	items    bool
}

func namedScope(scope, prefix string) string {
	name, ok := strings.CutPrefix(scope, prefix)
	if !ok || name == "*" {
		return ""
	}
	return name
}

// forRule checks the charter scope first. An out-of-scope rule does not read
// store projections. Git was acquired outside the store transaction.
func (p *storeFacts) forRule(r Rule, g gitSnapshot) (facts ruleFacts, outOfScope string, err error) {
	facts = ruleFacts{store: p, git: g, workflow: namedScope(r.Scope, "workflow:"), items: slices.Contains(r.Reads.Projections, "items")}
	name := namedScope(r.Scope, "charter:")
	readsCharter := slices.Contains(r.Reads.Projections, "charter")
	if name != "" || readsCharter {
		d, err := p.charter()
		if err != nil {
			return facts, "", unreadableProjection{"charter", err}
		}
		if name != "" && name != d.Name {
			return facts, d.Name, nil
		}
		if readsCharter {
			facts.charter = map[string]any{
				"charter.name": d.Name, "charter.ready": d.Ready, "charter.blocked": d.Blocked,
				"charter.closed": d.Closed, "charter.states": textList(d.States), "charter.finished": textList(d.Finished),
			}
		}
	}
	if err := g.ruleError(r); err != nil {
		return facts, "", err
	}
	if facts.items {
		if err := p.checkItems(); err != nil {
			return facts, "", unreadableProjection{"items", err}
		}
	}
	if facts.workflow != "" {
		if err := p.checkWorkflows(); err != nil {
			return facts, "", unreadableProjection{"workflow_selections", err}
		}
	}
	return facts, "", nil
}

func textList(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

func (p ruleFacts) forSubject(subject string) (lookup func(projectionOperand) any, inScope bool, err error) {
	if p.workflow != "" {
		workflow, err := p.store.workflow(subject)
		if err != nil {
			return nil, false, unreadableProjection{"workflow_selections", err}
		}
		if !workflow.Valid || workflow.String != p.workflow {
			return nil, false, nil
		}
	}
	var item itemFacts
	if p.items {
		item, err = p.store.item(subject)
		if err != nil {
			return nil, false, unreadableProjection{"items", err}
		}
	}
	return func(op projectionOperand) any {
		var column sql.NullString
		switch op.name {
		case "item.state":
			column = item.state
		case "item.seat":
			column = item.seat
		case "item.waiting_on":
			column = item.waitingOn
		case "git.ref":
			return p.git.refs[op.ref].value
		default:
			return p.charter[op.name]
		}
		if !column.Valid {
			return nil
		}
		return column.String
	}, true, nil
}
