package watch

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
	rd         *store.Reader
	charter    func() (CharterFacts, error)
	checkItems func() error
	items      map[string]projectionFact[itemFacts]
}

const itemColumns = `SELECT state,seat,waiting_on FROM items`

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
		checkItems: probe(itemColumns),
		items:      map[string]projectionFact[itemFacts]{},
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

type unreadableProjection struct {
	name string
	err  error
}

func (e unreadableProjection) Error() string { return fmt.Sprintf("%s: %v", e.name, e.err) }

type ruleFacts struct {
	store   *storeFacts
	git     gitSnapshot
	charter map[string]any
	items   bool
}

func (p *storeFacts) forRule(r Rule, g gitSnapshot) (facts ruleFacts, err error) {
	facts = ruleFacts{store: p, git: g, items: slices.Contains(r.Reads.Projections, "items")}
	readsCharter := slices.Contains(r.Reads.Projections, "charter")
	if readsCharter {
		d, err := p.charter()
		if err != nil {
			return facts, unreadableProjection{"charter", err}
		}
		if readsCharter {
			facts.charter = map[string]any{
				"charter.name": d.Name, "charter.ready": d.Ready, "charter.blocked": d.Blocked,
				"charter.closed": d.Closed, "charter.states": textList(d.States), "charter.finished": textList(d.Finished),
			}
		}
	}
	if err := g.ruleError(r); err != nil {
		return facts, err
	}
	if facts.items {
		if err := p.checkItems(); err != nil {
			return facts, unreadableProjection{"items", err}
		}
	}

	return facts, nil
}

func textList(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

func (p ruleFacts) forSubject(subject string) (lookup func(projectionOperand) any, inScope bool, err error) {

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
