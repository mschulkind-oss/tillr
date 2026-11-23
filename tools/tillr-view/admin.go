package main

// The admin browser: a read-only, Django-admin-shaped view of the store's own tables.
//
// The safety argument is structural, not a filter. The client never sends SQL: it names a
// table, columns, a sort and filter values, and every table and column name is looked up
// in the store's own schema (sqlite_master and the table_xinfo pragma) before it is quoted
// into a statement. Every value travels as a bound parameter. The handle these routes read
// through is the server's read-only one (store.OpenRead), so even a defect here could not
// write.
//
// The cost argument is the other half. The log holds over a million events, so no route
// counts, decodes or scans a whole table:
//   - Row counts are bounded: a table whose rowid span is small is counted exactly, a
//     larger one answers its span as an estimate (two index probes).
//   - Pages use keyset pagination on (sort column, rowid), never OFFSET, and a sort is
//     offered only on a column an index leads with.
//   - A filter no index serves is scanned in rowid windows (adminScanWindow rowids per
//     request) rather than all at once: the page can come back short with a cursor to
//     continue from, and says how far the scan got.

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/store"
)

const (
	adminDefaultLimit = 50
	adminMaxLimit     = 200
	// adminExactCount is the largest rowid span counted exactly. Counting is a scan of at
	// most this many rows; a larger table answers its span as an estimate.
	adminExactCount = 50_000
	// adminScanWindow is how many rowids one request examines when no index serves the
	// filter, so an unindexed filter on the whole log costs a window per request.
	adminScanWindow = 100_000
	// adminCellBytes is the longest text a table page carries whole; the row page carries
	// adminDetailBytes. Both mark the cut and say the true length.
	adminCellBytes   = 2_000
	adminDetailBytes = 1 << 20
	// adminRelatedCap bounds a related-rows count, and adminRelatedProbes how many are asked.
	adminRelatedCap    = 1_000
	adminRelatedProbes = 48
)

type adminColumn struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"not_null"`
	PK      int    `json:"pk"`
	// Indexed is true when a lookup by this column is served by an index: it is the
	// rowid alias or the leading column of some index.
	Indexed bool `json:"indexed"`
	// Sortable columns are the ones a page can be ordered by without a sort of the table.
	Sortable bool `json:"sortable"`
}

type adminIndex struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Partial bool     `json:"partial"`
	Columns []string `json:"columns"`
}

type adminTable struct {
	Name         string        `json:"name"`
	Columns      []adminColumn `json:"columns"`
	Indexes      []adminIndex  `json:"indexes"`
	WithoutRowID bool          `json:"without_rowid"`
	Virtual      bool          `json:"virtual"`
	// RowidAlias is the column that is the rowid ("seq" for events), or "".
	RowidAlias string `json:"rowid_alias"`
	// Browsable is false for a table this page cannot key by rowid.
	Browsable bool `json:"browsable"`
}

func (t *adminTable) column(name string) *adminColumn {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

type adminSchema struct {
	tables map[string]*adminTable
	order  []string
}

type adminCache struct {
	mu      sync.Mutex
	version int64
	schema  *adminSchema
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// loadAdminSchema reads every table's shape. The cache key is the database's own schema
// version, which changes when a table or index does, so a shape is never served stale.
func (s *server) loadAdminSchema(r *store.Reader) (*adminSchema, error) {
	var version int64
	if err := r.QueryRow(`SELECT schema_version FROM pragma_schema_version`).Scan(&version); err != nil {
		return nil, err
	}
	s.admin.mu.Lock()
	defer s.admin.mu.Unlock()
	if s.admin.schema != nil && s.admin.version == version {
		return s.admin.schema, nil
	}
	schema, err := readAdminSchema(r)
	if err != nil {
		return nil, err
	}
	s.admin.schema, s.admin.version = schema, version
	return schema, nil
}

func readAdminSchema(r *store.Reader) (*adminSchema, error) {
	rows, err := r.Query(`SELECT name, COALESCE(sql, '') FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := &adminSchema{tables: map[string]*adminTable{}}
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			rows.Close()
			return nil, err
		}
		upper := strings.ToUpper(ddl)
		out.tables[name] = &adminTable{
			Name:         name,
			WithoutRowID: strings.Contains(upper, "WITHOUT ROWID"),
			Virtual:      strings.HasPrefix(upper, "CREATE VIRTUAL"),
		}
		out.order = append(out.order, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, name := range out.order {
		if err := fillAdminTable(r, out.tables[name]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func fillAdminTable(r *store.Reader, t *adminTable) error {
	cols, err := r.Query(`SELECT name, COALESCE(type, ''), "notnull", pk FROM pragma_table_xinfo(?) WHERE hidden IN (0, 2, 3) ORDER BY cid`, t.Name)
	if err != nil {
		return err
	}
	pkCols := 0
	for cols.Next() {
		var c adminColumn
		var notnull int
		if err := cols.Scan(&c.Name, &c.Type, &notnull, &c.PK); err != nil {
			cols.Close()
			return err
		}
		c.NotNull = notnull != 0
		if c.PK > 0 {
			pkCols++
		}
		t.Columns = append(t.Columns, c)
	}
	if err := cols.Err(); err != nil {
		cols.Close()
		return err
	}
	cols.Close()
	if pkCols == 1 && !t.WithoutRowID {
		for i := range t.Columns {
			if t.Columns[i].PK > 0 && strings.EqualFold(t.Columns[i].Type, "INTEGER") {
				t.RowidAlias = t.Columns[i].Name
			}
		}
	}
	idx, err := r.Query(`SELECT name, "unique", partial FROM pragma_index_list(?)`, t.Name)
	if err != nil {
		return err
	}
	for idx.Next() {
		var i adminIndex
		var unique, partial int
		if err := idx.Scan(&i.Name, &unique, &partial); err != nil {
			idx.Close()
			return err
		}
		i.Unique, i.Partial = unique != 0, partial != 0
		t.Indexes = append(t.Indexes, i)
	}
	if err := idx.Err(); err != nil {
		idx.Close()
		return err
	}
	idx.Close()
	for n := range t.Indexes {
		info, err := r.Query(`SELECT COALESCE(name, '') FROM pragma_index_info(?) ORDER BY seqno`, t.Indexes[n].Name)
		if err != nil {
			return err
		}
		for info.Next() {
			var col string
			if err := info.Scan(&col); err != nil {
				info.Close()
				return err
			}
			t.Indexes[n].Columns = append(t.Indexes[n].Columns, col)
		}
		info.Close()
	}
	for i := range t.Columns {
		c := &t.Columns[i]
		c.Indexed = c.Name == t.RowidAlias
		for _, ix := range t.Indexes {
			if !ix.Partial && len(ix.Columns) > 0 && ix.Columns[0] == c.Name {
				c.Indexed = true
			}
		}
		c.Sortable = c.Indexed
	}
	t.Browsable = !t.WithoutRowID && !t.Virtual
	return nil
}

// ---- table index ----

type adminTableSummary struct {
	Name      string `json:"name"`
	Rows      int64  `json:"rows"`
	Estimated bool   `json:"estimated"`
	Columns   int    `json:"columns"`
	Indexes   int    `json:"indexes"`
	Browsable bool   `json:"browsable"`
}

// adminSpan is the first and last rowid. They are two scalar subqueries on purpose: SQLite
// answers a lone min(rowid) or max(rowid) from one end of the b-tree, but a query holding
// both scans the table (measured on the live log, 1.38M events: about 50 ms each way).
func adminSpan(r *store.Reader, t *adminTable) (lo, hi sql.NullInt64, err error) {
	name := quoteIdent(t.Name)
	err = r.QueryRow(fmt.Sprintf(`SELECT (SELECT min(rowid) FROM %s), (SELECT max(rowid) FROM %s)`, name, name)).Scan(&lo, &hi)
	return lo, hi, err
}

// adminCount answers a table's row count at bounded cost. A rowid table's span is two
// index probes; it is exact when small, and an upper-bound estimate when large (the log is
// append-only, so its span is its count unless rows were removed).
func adminCount(r *store.Reader, t *adminTable) (count int64, estimated bool, err error) {
	if !t.Browsable {
		return 0, true, nil
	}
	lo, hi, err := adminSpan(r, t)
	if err != nil {
		return 0, false, err
	}
	if !lo.Valid {
		return 0, false, nil
	}
	span := hi.Int64 - lo.Int64 + 1
	if span > adminExactCount {
		return span, true, nil
	}
	err = r.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s`, quoteIdent(t.Name))).Scan(&count)
	return count, false, err
}

type adminTablesData struct {
	Tables []adminTableSummary `json:"tables"`
}

func (s *server) adminTablesPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	if len(req.URL.Query()) > 0 {
		return nil, failure(codeBadQuery, "the table index takes no parameters")
	}
	schema, err := s.loadAdminSchema(r)
	if err != nil {
		return nil, unreadable(err)
	}
	out := adminTablesData{Tables: []adminTableSummary{}}
	for _, name := range schema.order {
		t := schema.tables[name]
		n, est, err := adminCount(r, t)
		if err != nil {
			return nil, unreadable(err)
		}
		out.Tables = append(out.Tables, adminTableSummary{Name: name, Rows: n, Estimated: est,
			Columns: len(t.Columns), Indexes: len(t.Indexes), Browsable: t.Browsable})
	}
	return out, nil
}

// ---- table page ----

// adminCell is a text value cut for a page: the head, and the length it was cut from.
type adminCell struct {
	Text      string `json:"text"`
	Length    int    `json:"length"`
	Truncated bool   `json:"truncated"`
}

type adminBlob struct {
	Bytes int `json:"blob_bytes"`
}

func adminValue(v any, limit int) any {
	switch x := v.(type) {
	case nil:
		return nil
	case int64, bool:
		return x
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return strconv.FormatFloat(x, 'g', -1, 64)
		}
		return x
	case string:
		return adminText(x, limit)
	case []byte:
		return adminBlob{Bytes: len(x)}
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(x)
	}
}

func adminText(s string, limit int) any {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return adminCell{Text: s[:cut], Length: len(s), Truncated: true}
}

type adminCursor struct {
	K int64     `json:"k"`
	S *adminVal `json:"s,omitempty"`
}

// adminVal is a sort value with its storage class, so a cursor binds the same type the
// row held rather than whatever JSON guesses.
type adminVal struct {
	T string `json:"t"`
	V string `json:"v"`
}

func encodeCursor(c adminCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(text string) (adminCursor, error) {
	var c adminCursor
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return c, fmt.Errorf("after is not a cursor this server issued")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("after is not a cursor this server issued")
	}
	return c, nil
}

func (v *adminVal) bind() (any, error) {
	switch v.T {
	case "i":
		return strconv.ParseInt(v.V, 10, 64)
	case "f":
		return strconv.ParseFloat(v.V, 64)
	case "s":
		return v.V, nil
	}
	return nil, fmt.Errorf("after is not a cursor this server issued")
}

func sortValOf(v any) *adminVal {
	switch x := v.(type) {
	case nil:
		return nil
	case int64:
		return &adminVal{"i", strconv.FormatInt(x, 10)}
	case float64:
		return &adminVal{"f", strconv.FormatFloat(x, 'g', -1, 64)}
	case []byte:
		return &adminVal{"s", string(x)}
	case time.Time:
		return &adminVal{"s", x.UTC().Format(time.RFC3339Nano)}
	default:
		return &adminVal{"s", fmt.Sprint(x)}
	}
}

type adminFilter struct {
	col   string
	op    string // "=", ">=", "<=", "null"
	value string
}

type adminQuery struct {
	limit   int
	desc    bool
	sortCol string // "" is rowid order
	after   *adminCursor
	filters []adminFilter
}

func parseAdminQuery(t *adminTable, req *http.Request) (adminQuery, *apiFailure) {
	q := adminQuery{limit: adminDefaultLimit}
	for key, vals := range req.URL.Query() {
		if len(vals) != 1 {
			return q, failure(codeBadQuery, "%s must appear once", key)
		}
		val := vals[0]
		switch {
		case key == "limit":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 || n > adminMaxLimit {
				return q, failure(codeBadQuery, "limit must be a whole number from 1 to %d", adminMaxLimit)
			}
			q.limit = n
		case key == "order":
			if val != "asc" && val != "desc" {
				return q, failure(codeBadQuery, "order must be asc or desc")
			}
			q.desc = val == "desc"
		case key == "sort":
			if val == "" || val == "rowid" {
				break
			}
			c := t.column(val)
			if c == nil {
				return q, failure(codeBadQuery, "table %q has no column %q to sort by", t.Name, val)
			}
			if !c.Sortable {
				return q, failure(codeBadQuery, "column %q of %q has no index to sort by; sortable columns are those an index leads with", val, t.Name)
			}
			if val != t.RowidAlias {
				q.sortCol = val
			}
		case key == "after":
			c, err := decodeCursor(val)
			if err != nil {
				return q, failure(codeBadQuery, "%v", err)
			}
			q.after = &c
		case strings.HasPrefix(key, "f."), strings.HasPrefix(key, "min."), strings.HasPrefix(key, "max."), strings.HasPrefix(key, "null."):
			prefix, col, _ := strings.Cut(key, ".")
			if t.column(col) == nil {
				return q, failure(codeBadQuery, "table %q has no column %q to filter by", t.Name, col)
			}
			op := map[string]string{"f": "=", "min": ">=", "max": "<=", "null": "null"}[prefix]
			q.filters = append(q.filters, adminFilter{col: col, op: op, value: val})
		default:
			return q, failure(codeBadQuery, "unknown parameter %q: use limit, order, sort, after, f.<column>, min.<column>, max.<column>, null.<column>", key)
		}
	}
	// A stable order, so a statement and its plan do not depend on map iteration.
	sort.Slice(q.filters, func(i, j int) bool {
		if q.filters[i].col != q.filters[j].col {
			return q.filters[i].col < q.filters[j].col
		}
		return q.filters[i].op < q.filters[j].op
	})
	return q, nil
}

type adminScan struct {
	// Through is the rowid the scan has examined up to, Max the table's end in scan
	// direction. A page can be short while Through is short of Max.
	Through int64 `json:"through"`
	Max     int64 `json:"max"`
}

type adminPage struct {
	Table   string        `json:"table"`
	Columns []adminColumn `json:"columns"`
	Indexes []adminIndex  `json:"indexes"`
	Key     string        `json:"key"`
	Rowids  []int64       `json:"rowids"`
	Rows    [][]any       `json:"rows"`
	Next    string        `json:"next,omitempty"`
	More    bool          `json:"more"`
	Sort    string        `json:"sort"`
	Order   string        `json:"order"`
	Scan    *adminScan    `json:"scan,omitempty"`
}

func (s *server) lookupAdminTable(r *store.Reader, req *http.Request) (*adminTable, *apiFailure) {
	schema, err := s.loadAdminSchema(r)
	if err != nil {
		return nil, unreadable(err)
	}
	name := req.PathValue("table")
	t, ok := schema.tables[name]
	if !ok {
		return nil, failure(codeUnknownTable, "the store has no table %q: read /api/v1/admin/tables for the tables it has", name)
	}
	if !t.Browsable {
		return nil, failure(codeBadQuery, "table %q cannot be browsed here: it is a virtual or WITHOUT ROWID table", name)
	}
	return t, nil
}

func (s *server) adminTablePayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	t, fail := s.lookupAdminTable(r, req)
	if fail != nil {
		return nil, fail
	}
	q, fail := parseAdminQuery(t, req)
	if fail != nil {
		return nil, fail
	}
	return adminBrowse(r, t, q)
}

// adminSkippedIndexes picks, when several exact-match filters could each drive the query
// through its own index, the most selective one and answers the columns whose index must
// not be chosen. Without statistics the planner cannot tell kind = 'run.child-session' (1.28M
// events) from object_id = 'item-x' (a few dozen) and walked the wide one: 1.25 s on the live
// log for a lookup the object index answers in 5 ms. Selectivity is measured, not assumed,
// by a count capped at adminRelatedCap+1 index entries per candidate.
func adminSkippedIndexes(r *store.Reader, t *adminTable, filters []adminFilter) (map[string]bool, error) {
	var candidates []adminFilter
	for _, f := range filters {
		if c := t.column(f.col); f.op == "=" && c.Indexed && f.col != t.RowidAlias {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) < 2 {
		return nil, nil
	}
	best, bestCount := "", -1
	for _, f := range candidates {
		var n int
		err := r.QueryRow(fmt.Sprintf(`SELECT count(*) FROM (SELECT 1 FROM %s WHERE %s = ? LIMIT %d)`,
			quoteIdent(t.Name), quoteIdent(f.col), adminRelatedCap+1), f.value).Scan(&n)
		if err != nil {
			return nil, err
		}
		if bestCount < 0 || n < bestCount {
			best, bestCount = f.col, n
		}
	}
	skip := map[string]bool{}
	for _, f := range candidates {
		if f.col != best {
			skip[f.col] = true
		}
	}
	return skip, nil
}

func adminBrowse(r *store.Reader, t *adminTable, q adminQuery) (any, *apiFailure) {
	quoted := quoteIdent(t.Name)
	var where []string
	var args []any
	served := false // some filter is answered by an index
	skipIndex, err := adminSkippedIndexes(r, t, q.filters)
	if err != nil {
		return nil, unreadable(err)
	}
	for _, f := range q.filters {
		col := quoteIdent(f.col)
		c := t.column(f.col)
		if c.Indexed {
			served = true
		}
		if skipIndex[f.col] {
			// A unary plus makes the term an expression, so the planner cannot choose
			// this column's index and must drive the query from the more selective one.
			col = "+" + col
		}
		switch f.op {
		case "null":
			where = append(where, col+" IS NULL")
		default:
			where = append(where, col+" "+f.op+" ?")
			args = append(args, f.value)
		}
	}
	lo, hi, err := adminSpan(r, t)
	if err != nil {
		return nil, unreadable(err)
	}
	page := adminPage{Table: t.Name, Columns: t.Columns, Indexes: t.Indexes, Key: "rowid",
		Rowids: []int64{}, Rows: [][]any{}, Sort: q.sortCol, Order: "asc"}
	if q.sortCol == "" && t.RowidAlias != "" {
		page.Sort = t.RowidAlias
	}
	if q.desc {
		page.Order = "desc"
	}
	if !lo.Valid {
		return page, nil
	}
	span := hi.Int64 - lo.Int64 + 1
	// Scan mode: filters exist, no index serves any of them, and the table is large
	// enough that one request must not read it all. Then rowid order is the only order
	// that can be windowed.
	scanning := len(q.filters) > 0 && !served && span > adminScanWindow
	if scanning && q.sortCol != "" {
		return nil, failure(codeBadQuery, "no index serves the filter on %q, so this large table can only be read in rowid order: drop sort, or filter by an indexed column", t.Name)
	}
	dir, cmp := "ASC", ">"
	if q.desc {
		dir, cmp = "DESC", "<"
	}
	// The keyset condition: strictly after the cursor in the page's order.
	if q.after != nil {
		if q.sortCol == "" {
			where = append(where, "rowid "+cmp+" ?")
			args = append(args, q.after.K)
		} else {
			col := quoteIdent(q.sortCol)
			if q.after.S != nil {
				v, err := q.after.S.bind()
				if err != nil {
					return nil, failure(codeBadQuery, "%v", err)
				}
				if q.desc {
					where = append(where, fmt.Sprintf("((%s, rowid) < (?, ?) OR %s IS NULL)", col, col))
				} else {
					where = append(where, fmt.Sprintf("(%s, rowid) > (?, ?)", col))
				}
				args = append(args, v, q.after.K)
			} else if q.desc {
				where = append(where, fmt.Sprintf("(%s IS NULL AND rowid < ?)", col))
				args = append(args, q.after.K)
			} else {
				where = append(where, fmt.Sprintf("((%s IS NULL AND rowid > ?) OR %s IS NOT NULL)", col, col))
				args = append(args, q.after.K)
			}
		}
	}
	var through, edge int64
	if scanning {
		start := lo.Int64 - 1
		edge = hi.Int64
		if q.desc {
			start, edge = hi.Int64+1, lo.Int64
		}
		if q.after != nil {
			start = q.after.K
		}
		if q.desc {
			through = start - adminScanWindow
			where = append(where, "rowid >= ?")
		} else {
			through = start + adminScanWindow
			where = append(where, "rowid <= ?")
		}
		args = append(args, through)
	}
	cols := make([]string, 0, len(t.Columns)+1)
	cols = append(cols, "rowid")
	for _, c := range t.Columns {
		cols = append(cols, quoteIdent(c.Name))
	}
	stmt := "SELECT " + strings.Join(cols, ", ") + " FROM " + quoted
	if len(where) > 0 {
		stmt += " WHERE " + strings.Join(where, " AND ")
	}
	order := "rowid " + dir
	if q.sortCol != "" {
		order = quoteIdent(q.sortCol) + " " + dir + ", rowid " + dir
	}
	stmt += " ORDER BY " + order + " LIMIT ?"
	args = append(args, q.limit+1)

	rows, err := r.Query(stmt, args...)
	if err != nil {
		return nil, unreadable(err)
	}
	defer rows.Close()
	sortIdx := -1
	if q.sortCol != "" {
		for i, c := range t.Columns {
			if c.Name == q.sortCol {
				sortIdx = i + 1
			}
		}
	}
	var lastRowid int64
	var lastSort any
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, unreadable(err)
		}
		if len(page.Rows) == q.limit {
			page.More = true
			break
		}
		id, _ := raw[0].(int64)
		cells := make([]any, len(t.Columns))
		for i := range cells {
			cells[i] = adminValue(raw[i+1], adminCellBytes)
		}
		page.Rowids = append(page.Rowids, id)
		page.Rows = append(page.Rows, cells)
		lastRowid = id
		if sortIdx >= 0 {
			lastSort = raw[sortIdx]
		}
	}
	if err := rows.Err(); err != nil {
		return nil, unreadable(err)
	}
	switch {
	case page.More:
		c := adminCursor{K: lastRowid}
		if sortIdx >= 0 {
			c.S = sortValOf(lastSort)
		}
		page.Next = encodeCursor(c)
	case scanning && through != edge && ((!q.desc && through < edge) || (q.desc && through > edge)):
		// The window ended before the table did: the page is short, and the way on is
		// the window's edge.
		page.More = true
		page.Next = encodeCursor(adminCursor{K: through})
	}
	if scanning {
		page.Scan = &adminScan{Through: through, Max: edge}
		if !page.More {
			page.Scan.Through = edge
		}
	}
	return page, nil
}

// ---- row page ----

type adminRelated struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Value  string `json:"value"`
	Count  int    `json:"count"`
	// Capped is true when Count stopped at the cap.
	Capped bool `json:"capped"`
}

type adminPayloadRef struct {
	Key string `json:"key"`
	ID  string `json:"id"`
}

type adminRowData struct {
	Table       string            `json:"table"`
	Rowid       int64             `json:"rowid"`
	Columns     []adminColumn     `json:"columns"`
	Values      []any             `json:"values"`
	Related     []adminRelated    `json:"related"`
	PayloadRefs []adminPayloadRef `json:"payload_refs"`
}

// payloadRefKeys are the payload fields whose values name an object other pages know.
var payloadRefKeys = []string{"item_id", "run_id", "lane_id", "candidate_id", "attempt_id"}

func (s *server) adminRowPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	t, fail := s.lookupAdminTable(r, req)
	if fail != nil {
		return nil, fail
	}
	if len(req.URL.Query()) > 0 {
		return nil, failure(codeBadQuery, "a row takes no parameters")
	}
	rowid, err := strconv.ParseInt(req.PathValue("rowid"), 10, 64)
	if err != nil {
		return nil, failure(codeBadQuery, "rowid must be a whole number")
	}
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = quoteIdent(c.Name)
	}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	err = r.QueryRow(fmt.Sprintf(`SELECT %s FROM %s WHERE rowid = ?`, strings.Join(cols, ", "), quoteIdent(t.Name)), rowid).Scan(ptrs...)
	if err == sql.ErrNoRows {
		return nil, failure(codeRowNotFound, "table %q has no row with rowid %d", t.Name, rowid)
	}
	if err != nil {
		return nil, unreadable(err)
	}
	out := adminRowData{Table: t.Name, Rowid: rowid, Columns: t.Columns, Values: make([]any, len(raw)),
		Related: []adminRelated{}, PayloadRefs: []adminPayloadRef{}}
	for i, v := range raw {
		out.Values[i] = adminValue(v, adminDetailBytes)
	}
	schema, err := s.loadAdminSchema(r)
	if err != nil {
		return nil, unreadable(err)
	}
	probes := 0
	seen := map[string]bool{}
	probe := func(table *adminTable, col, value string) *apiFailure {
		key := table.Name + "\x00" + col + "\x00" + value
		if seen[key] || probes >= adminRelatedProbes {
			return nil
		}
		seen[key] = true
		probes++
		var n int
		err := r.QueryRow(fmt.Sprintf(`SELECT count(*) FROM (SELECT 1 FROM %s WHERE %s = ? LIMIT %d)`,
			quoteIdent(table.Name), quoteIdent(col), adminRelatedCap+1), value).Scan(&n)
		if err != nil {
			return unreadable(err)
		}
		// The row itself is not a relation of itself.
		if table.Name == t.Name && n <= 1 {
			return nil
		}
		if n > 0 {
			capped := n > adminRelatedCap
			if capped {
				n = adminRelatedCap
			}
			out.Related = append(out.Related, adminRelated{Table: table.Name, Column: col, Value: value, Count: n, Capped: capped})
		}
		return nil
	}
	events := schema.tables["events"]
	for i, c := range t.Columns {
		text, ok := scalarText(raw[i])
		if !ok || text == "" || c.Name == t.RowidAlias {
			continue
		}
		for _, name := range schema.order {
			u := schema.tables[name]
			if !u.Browsable {
				continue
			}
			if uc := u.column(c.Name); uc != nil && uc.Indexed {
				if f := probe(u, c.Name, text); f != nil {
					return nil, f
				}
			}
		}
		// An event names its object by type and id: object_type "item" is the item_id
		// column of whichever tables carry one.
		if t.Name == "events" && c.Name == "object_id" {
			objectType := ""
			for j, tc := range t.Columns {
				if tc.Name == "object_type" {
					objectType, _ = scalarText(raw[j])
				}
			}
			if objectType != "" {
				for _, name := range schema.order {
					u := schema.tables[name]
					if uc := u.column(objectType + "_id"); u.Browsable && uc != nil && uc.Indexed {
						if f := probe(u, objectType+"_id", text); f != nil {
							return nil, f
						}
					}
				}
			}
		}
		// An object id read in any table is also the object an event names.
		if events != nil && strings.HasSuffix(c.Name, "_id") && c.Name != "object_id" {
			if oc := events.column("object_id"); oc != nil && oc.Indexed {
				if f := probe(events, "object_id", text); f != nil {
					return nil, f
				}
			}
		}
	}
	if t.Name == "events" {
		for i, c := range t.Columns {
			payload, ok := raw[i].(string)
			if c.Name != "payload" || !ok || len(payload) > adminDetailBytes {
				continue
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal([]byte(payload), &fields) != nil {
				continue
			}
			for _, key := range payloadRefKeys {
				var id string
				if v, ok := fields[key]; ok && json.Unmarshal(v, &id) == nil && id != "" {
					out.PayloadRefs = append(out.PayloadRefs, adminPayloadRef{Key: key, ID: id})
				}
			}
		}
	}
	return out, nil
}

func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10), true
	case string:
		if len(x) > 256 {
			return "", false
		}
		return x, true
	}
	return "", false
}
