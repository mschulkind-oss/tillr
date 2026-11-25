package store

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
)

// Per-owner registration is how a utility contributes to the store without editing a
// central list: it registers its DDL and its record→row mapping from its own package,
// in an init(), and the store composes what it was given. The event-kind registry
// (event/kind.go) is the model; the DDL list is not a list any more.
//
// Registration is global and happens at package init, so it is complete before any
// store is opened. A registration made after a store has migrated is not applied to
// that store, which is why the pattern is init() and not a call a face remembers.

// ownerDDL is one owner's contribution to the schema.
type ownerDDL struct {
	owner string
	ddl   string
}

// Materializer is one utility's record→row mapping: it validates the record, writes
// the rows the record describes, appends the transition's event in the same
// transaction, and answers the record as stored.
//
// It is the *one* mapping a utility owns, shared by its write face and the seed
// materializer (lanes §1, OQ-L2). A second mapping drifts, and a fixture that can
// construct a state the write face would reject — or cannot construct one it allows —
// makes every test built on it suspect. The mapping lives in the utility's own
// package; the substrate holds only the registration.
type Materializer func(ctx context.Context, tx *Tx, env record.Envelope) (record.Envelope, error)

var (
	registryMu                sync.RWMutex
	registeredDDL             []ownerDDL
	registeredColumns         []ownerColumn
	registeredColumnBackfills []ownerColumnBackfill
	materializers             = map[string]materializerEntry{}
)

// RegisterDDL adds one owner's DDL to the schema the store migrates to. The defect
// the registry exists to catch is two writers of one table set, so an owner already
// registered is refused rather than overwritten. The same owner registering the same
// DDL again is a no-op: a package linked twice, or a test run with -count=2, is not a
// defect and must not become an import-time panic.
func RegisterDDL(owner, ddl string) error {
	if owner == "" || ddl == "" {
		return fmt.Errorf("store: a DDL registration needs an owner and a statement")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, existing := range registeredDDL {
		if existing.owner != owner {
			continue
		}
		if existing.ddl == ddl {
			return nil
		}
		return fmt.Errorf("store: owner %q is already registered with a different DDL", owner)
	}
	registeredDDL = append(registeredDDL, ownerDDL{owner: owner, ddl: ddl})
	return nil
}

// ownerColumn is one column an owner added to a table that already existed when the
// column was added. The declaration is the DDL fragment the column is added with, so
// a store that gains the column through the migration has the shape a fresh store's
// CREATE TABLE declares.
type ownerColumn struct {
	owner  string
	table  string
	column string
	decl   string
}

// RegisterColumn records one column an owner added to a table that already existed
// when the column was added. It is the hook CREATE TABLE IF NOT EXISTS cannot be: that
// statement is a no-op against a store that already has the table, so a column added
// to an existing table reaches every *new* store and none of the old ones, and the
// first write naming the column fails on them. migrate() adds the registered columns a
// store lacks (addMissingColumns).
//
// The column's table is the owner's, so the same (table, column) registered again by
// the same owner is a no-op — a package linked twice, or -count=2, is not a defect —
// and any other registration naming it is refused: two writers of one column would
// make the store's shape depend on import order.
func RegisterColumn(owner, table, column, decl string) error {
	if owner == "" || table == "" || column == "" || decl == "" {
		return fmt.Errorf("store: a column registration needs an owner, a table, a column and a declaration")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, existing := range registeredColumns {
		if existing.table != table || existing.column != column {
			continue
		}
		if existing.owner == owner && existing.decl == decl {
			return nil
		}
		return fmt.Errorf("store: column %s.%s is already registered by owner %q", table, column, existing.owner)
	}
	registeredColumns = append(registeredColumns, ownerColumn{owner: owner, table: table, column: column, decl: decl})
	return nil
}

// ownerColumnBackfill is a one-time SQL statement executed immediately after
// addMissingColumns adds the named column to an existing table.
type ownerColumnBackfill struct {
	owner  string
	table  string
	column string
	stmt   string
}

// RegisterColumnBackfill registers a one-time SQL statement to run in the same
// migration transaction immediately after ALTER TABLE adds (table, column).
// If the column already exists on the table, the backfill does not run.
func RegisterColumnBackfill(owner, table, column, stmt string) error {
	if owner == "" || table == "" || column == "" || stmt == "" {
		return fmt.Errorf("store: a column backfill registration needs an owner, a table, a column and a statement")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, existing := range registeredColumnBackfills {
		if existing.table != table || existing.column != column {
			continue
		}
		if existing.owner == owner && existing.stmt == stmt {
			return nil
		}
		return fmt.Errorf("store: column backfill for %s.%s is already registered by owner %q", table, column, existing.owner)
	}
	registeredColumnBackfills = append(registeredColumnBackfills, ownerColumnBackfill{
		owner:  owner,
		table:  table,
		column: column,
		stmt:   stmt,
	})
	return nil
}

func columnBackfillFor(table, column string) (string, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, entry := range registeredColumnBackfills {
		if entry.table == table && entry.column == column {
			return entry.stmt, true
		}
	}
	return "", false
}

// RegisterMaterializer registers one utility's record→row mapping for a record kind.
// Two owners for one kind is the two-writers defect, so it is refused; the same owner
// registering again is a no-op, for the reason RegisterDDL gives.
func RegisterMaterializer(kind, owner string, fn Materializer) error {
	if kind == "" || owner == "" || fn == nil {
		return fmt.Errorf("store: a materializer registration needs a kind, an owner and a function")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if existing, ok := materializers[kind]; ok {
		if existing.owner == owner {
			return nil
		}
		return fmt.Errorf("store: record kind %q already has a materializer owned by %q", kind, existing.owner)
	}
	materializers[kind] = materializerEntry{owner: owner, fn: fn}
	return nil
}

// materializerEntry is one registered mapping and the utility that owns it.
type materializerEntry struct {
	owner string
	fn    Materializer
}

// materializerFor answers the mapping registered for a record kind, and false for a
// kind that materializes no rows. A kind with no mapping is not an error: an event
// that describes no rows has nothing to materialize.
func materializerFor(kind string) (Materializer, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	entry, ok := materializers[kind]
	return entry.fn, ok
}

// migrations is the DDL list in application order: the store's own administration
// first, then the events table, then every registered owner in owner order so the
// order does not depend on import order.
func migrations() []string {
	registryMu.RLock()
	owners := make([]ownerDDL, len(registeredDDL))
	copy(owners, registeredDDL)
	registryMu.RUnlock()
	sort.Slice(owners, func(i, j int) bool { return owners[i].owner < owners[j].owner })
	list := make([]string, 0, len(owners)+2)
	list = append(list, MetaDDL, event.EventsDDL)
	for _, owner := range owners {
		list = append(list, owner.ddl)
	}
	return list
}

// columns is the column registrations in application order: owner order, then table,
// then column, for the reason migrations() sorts.
func columns() []ownerColumn {
	registryMu.RLock()
	registered := make([]ownerColumn, len(registeredColumns))
	copy(registered, registeredColumns)
	registryMu.RUnlock()
	sort.Slice(registered, func(i, j int) bool {
		if registered[i].owner != registered[j].owner {
			return registered[i].owner < registered[j].owner
		}
		if registered[i].table != registered[j].table {
			return registered[i].table < registered[j].table
		}
		return registered[i].column < registered[j].column
	})
	return registered
}
