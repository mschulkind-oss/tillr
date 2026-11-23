package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// seedAdminEvents writes n events: kinds alternate between two values, objects cycle over
// five items, and every payload names its item. One CTE statement, so n costs one commit.
func (h *serveHarness) seedAdminEvents(n int) {
	h.t.Helper()
	h.exec(fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < %d)
		INSERT INTO events(ts, kind, object_type, object_id, payload)
		SELECT 1000+i, CASE WHEN i%%2=0 THEN 'run.child-session' ELSE 'item.moved' END, 'item',
		       'item-seed-0' || (i%%5), '{"item_id":"item-seed-0' || (i%%5) || '","n":' || i || '}' FROM n`, n))
}

type adminPageBody struct {
	Rowids  []float64
	Rows    [][]any
	Next    string
	More    bool
	Scan    map[string]any
	Columns []map[string]any
}

func (h *serveHarness) adminPage(path string) (int, adminPageBody, map[string]any) {
	h.t.Helper()
	status, body, raw := h.get(path)
	var out adminPageBody
	data, _ := body["data"].(map[string]any)
	if status != http.StatusOK {
		return status, out, body
	}
	for _, v := range data["rowids"].([]any) {
		out.Rowids = append(out.Rowids, v.(float64))
	}
	for _, row := range data["rows"].([]any) {
		out.Rows = append(out.Rows, row.([]any))
	}
	out.Next, _ = data["next"].(string)
	out.More, _ = data["more"].(bool)
	out.Scan, _ = data["scan"].(map[string]any)
	for _, c := range data["columns"].([]any) {
		out.Columns = append(out.Columns, c.(map[string]any))
	}
	_ = raw
	return status, out, body
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// walk follows next cursors until the listing ends and answers every rowid in order.
func (h *serveHarness) adminWalk(path string) []float64 {
	h.t.Helper()
	var all []float64
	after := ""
	for pages := 0; pages < 1000; pages++ {
		p := path
		if after != "" {
			p += "&after=" + url.QueryEscape(after)
		}
		status, page, body := h.adminPage(p)
		if status != http.StatusOK {
			h.t.Fatalf("%s: %d %v", p, status, body)
		}
		all = append(all, page.Rowids...)
		if !page.More {
			return all
		}
		after = page.Next
	}
	h.t.Fatalf("%s did not end", path)
	return nil
}

func TestAdminTableIndexListsEveryTableWithCounts(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(30)
	status, body, raw := h.get("/api/v1/admin/tables")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	counts := map[string]float64{}
	for _, v := range body["data"].(map[string]any)["tables"].([]any) {
		row := v.(map[string]any)
		counts[row["name"].(string)] = row["rows"].(float64)
		if row["estimated"] == true {
			t.Fatalf("a small table must be counted exactly: %v", row)
		}
	}
	if counts["events"] != 30 || counts["items"] != 3 {
		t.Fatalf("counts: %v", counts)
	}
	for _, name := range []string{"runs", "lanes", "item_criteria", "meta"} {
		if _, ok := counts[name]; !ok {
			t.Fatalf("table %s missing from %v", name, counts)
		}
	}
	if status, _, _ := h.get("/api/v1/admin/tables?x=1"); status != http.StatusBadRequest {
		t.Fatalf("the index takes no parameters, got %d", status)
	}
}

func TestAdminLargeTableCountIsAnEstimate(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(adminExactCount + 10)
	_, body, _ := h.get("/api/v1/admin/tables")
	for _, v := range body["data"].(map[string]any)["tables"].([]any) {
		row := v.(map[string]any)
		if row["name"] == "events" {
			if row["estimated"] != true || row["rows"].(float64) != float64(adminExactCount+10) {
				t.Fatalf("events: %v", row)
			}
			return
		}
	}
	t.Fatal("events missing")
}

func TestAdminTablePagesByKeysetWithoutRepeatsOrGaps(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(120)
	asc := h.adminWalk("/api/v1/admin/tables/events?limit=50")
	if len(asc) != 120 {
		t.Fatalf("ascending walk saw %d rows", len(asc))
	}
	for i := 1; i < len(asc); i++ {
		if asc[i] != asc[i-1]+1 {
			t.Fatalf("not contiguous ascending at %d: %v", i, asc[i-1:i+1])
		}
	}
	desc := h.adminWalk("/api/v1/admin/tables/events?limit=50&order=desc")
	if len(desc) != 120 || desc[0] != 120 || desc[119] != 1 {
		t.Fatalf("descending walk: %d rows, %v..%v", len(desc), desc[0], desc[len(desc)-1])
	}
	_, first, _ := h.adminPage("/api/v1/admin/tables/events?limit=50")
	if len(first.Rows) != 50 || !first.More || first.Next == "" {
		t.Fatalf("first page: %d rows more=%v", len(first.Rows), first.More)
	}
	_, last, _ := h.adminPage("/api/v1/admin/tables/events?limit=50&after=" + url.QueryEscape(first.Next) + "&order=asc")
	if last.Rowids[0] != 51 {
		t.Fatalf("second page starts at %v", last.Rowids[0])
	}
}

func TestAdminSortByIndexedColumnPagesAcrossTies(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(100)
	for _, order := range []string{"asc", "desc"} {
		got := h.adminWalk("/api/v1/admin/tables/events?sort=kind&limit=30&order=" + order)
		if len(got) != 100 {
			t.Fatalf("%s: saw %d rows", order, len(got))
		}
		seen := map[float64]bool{}
		for _, id := range got {
			if seen[id] {
				t.Fatalf("%s: rowid %v repeated", order, id)
			}
			seen[id] = true
		}
		// 'item.moved' sorts before 'run.child-session'.
		firstKind := map[string]string{"asc": "item.moved", "desc": "run.child-session"}[order]
		_, page, _ := h.adminPage("/api/v1/admin/tables/events?sort=kind&limit=5&order=" + order)
		kindAt := -1
		for i, c := range page.Columns {
			if c["name"] == "kind" {
				kindAt = i
			}
		}
		if page.Rows[0][kindAt] != firstKind {
			t.Fatalf("%s: first kind %v", order, page.Rows[0][kindAt])
		}
	}
}

func TestAdminSortAcrossNullsKeepsKeysetOrder(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(10)
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (1,'a',NULL,NULL,'{}'),(2,'a',NULL,NULL,'{}'),(3,'a',NULL,NULL,'{}')`)
	for _, order := range []string{"asc", "desc"} {
		got := h.adminWalk("/api/v1/admin/tables/events?sort=object_id&limit=4&order=" + order)
		if len(got) != 13 {
			t.Fatalf("%s: saw %d of 13 rows: %v", order, len(got), got)
		}
	}
}

func TestAdminFilters(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(100)
	got := h.adminWalk("/api/v1/admin/tables/events?f.kind=run.child-session&limit=20")
	if len(got) != 50 {
		t.Fatalf("kind filter saw %d rows", len(got))
	}
	got = h.adminWalk("/api/v1/admin/tables/events?f.object_id=item-seed-03")
	if len(got) != 20 {
		t.Fatalf("object filter saw %d rows", len(got))
	}
	got = h.adminWalk("/api/v1/admin/tables/events?f.kind=item.moved&min.seq=10&max.seq=29")
	if len(got) != 10 {
		t.Fatalf("kind + seq range saw %d rows: %v", len(got), got)
	}
	if got = h.adminWalk("/api/v1/admin/tables/events?f.kind=nothing"); len(got) != 0 {
		t.Fatalf("absent kind answered rows: %v", got)
	}
	h.exec(`INSERT INTO events(ts, kind, payload) VALUES (5,'orphan','{}')`)
	if got = h.adminWalk("/api/v1/admin/tables/events?null.object_id=1"); len(got) != 1 {
		t.Fatalf("null filter saw %v", got)
	}
	// An exact match on a column no index serves still answers, from a short table.
	if got = h.adminWalk("/api/v1/admin/tables/events?f.ts=1005"); len(got) != 1 {
		t.Fatalf("unindexed filter saw %v", got)
	}
}

func TestAdminUnindexedFilterOnALargeTableScansInWindows(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(adminScanWindow*2 + 500)
	// ts = 1000+i, so ts 1000+150000 is the single row at rowid 150000, two windows in.
	status, page, body := h.adminPage("/api/v1/admin/tables/events?f.ts=" + fmt.Sprint(1000+150000))
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, body)
	}
	if len(page.Rows) != 0 || !page.More || page.Next == "" || page.Scan == nil {
		t.Fatalf("the first window holds no match and must say the scan goes on: %+v", page)
	}
	if page.Scan["through"].(float64) != float64(adminScanWindow) {
		t.Fatalf("scan: %v", page.Scan)
	}
	got := h.adminWalk("/api/v1/admin/tables/events?f.ts=" + fmt.Sprint(1000+150000))
	if len(got) != 1 || got[0] != 150000 {
		t.Fatalf("walk found %v", got)
	}
	status, _, body = h.adminPage("/api/v1/admin/tables/events?f.ts=5&sort=kind")
	if status != http.StatusBadRequest || errCode(body) != codeBadQuery {
		t.Fatalf("a window cannot be sorted by another column: %d %v", status, body)
	}
}

func TestAdminRejectsWhatTheSchemaDoesNotName(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(5)
	cases := []struct {
		path string
		want int
		code string
	}{
		{"/api/v1/admin/tables/nope", 404, codeUnknownTable},
		{"/api/v1/admin/tables/" + url.PathEscape(`events"; DROP TABLE events; --`), 404, codeUnknownTable},
		{"/api/v1/admin/tables/sqlite_master", 404, codeUnknownTable},
		{"/api/v1/admin/tables/nope/rows/1", 404, codeUnknownTable},
		{"/api/v1/admin/tables/events?f.nope=1", 400, codeBadQuery},
		{"/api/v1/admin/tables/events?min.nope=1", 400, codeBadQuery},
		{"/api/v1/admin/tables/events?sort=nope", 400, codeBadQuery},
		{"/api/v1/admin/tables/events?sort=payload", 400, codeBadQuery}, // exists, but no index
		{"/api/v1/admin/tables/events?" + url.Values{"f.kind\" OR 1=1 --": {"x"}}.Encode(), 400, codeBadQuery},
		{"/api/v1/admin/tables/events?" + url.Values{"sort": {"kind; DROP TABLE events"}}.Encode(), 400, codeBadQuery},
		{"/api/v1/admin/tables/events?sql=SELECT+1", 400, codeBadQuery},
		{"/api/v1/admin/tables/events?limit=0", 400, codeBadQuery},
		{"/api/v1/admin/tables/events?limit=10000", 400, codeBadQuery},
		{"/api/v1/admin/tables/events?order=sideways", 400, codeBadQuery},
		{"/api/v1/admin/tables/events?after=%25%25", 400, codeBadQuery},
		{"/api/v1/admin/tables/events/rows/abc", 400, codeBadQuery},
		{"/api/v1/admin/tables/events/rows/999", 404, codeRowNotFound},
	}
	for _, c := range cases {
		status, body, raw := h.get(c.path)
		if status != c.want || errCode(body) != c.code {
			t.Errorf("%s: got %d %s, want %d %s: %s", c.path, status, errCode(body), c.want, c.code, raw)
		}
	}
	// None of that touched the table.
	if got := h.adminWalk("/api/v1/admin/tables/events?limit=50"); len(got) != 5 {
		t.Fatalf("events after the refusals: %v", got)
	}
}

func TestAdminValuesAreBoundNotSpliced(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(5)
	for _, v := range []string{`x' OR '1'='1`, `x'; DELETE FROM events; --`, `%`} {
		got := h.adminWalk("/api/v1/admin/tables/events?f.kind=" + url.QueryEscape(v))
		if len(got) != 0 {
			t.Fatalf("value %q matched rows %v", v, got)
		}
	}
	if got := h.adminWalk("/api/v1/admin/tables/events"); len(got) != 5 {
		t.Fatalf("events after the hostile values: %v", got)
	}
}

func TestAdminRoutesAreReadOnly(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(5)
	for _, path := range []string{"/api/v1/admin/tables", "/api/v1/admin/tables/events", "/api/v1/admin/tables/events/rows/1"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			req, _ := http.NewRequest(method, h.http.URL+path, strings.NewReader(`{"sql":"DELETE FROM events"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Fatalf("%s %s answered %d", method, path, resp.StatusCode)
			}
		}
	}
	// The handle the routes read through refuses a write outright.
	err := h.srv.store.ReadTx(t.Context(), func(r *store.Reader) error {
		rows, err := r.Query(`DELETE FROM items`)
		if err == nil {
			rows.Close()
		}
		return err
	})
	if err == nil {
		t.Fatal("the read handle accepted a DELETE")
	}
	_, body, _ := h.get("/api/v1/admin/tables")
	for _, v := range body["data"].(map[string]any)["tables"].([]any) {
		row := v.(map[string]any)
		if row["name"] == "items" && row["rows"].(float64) != 3 {
			t.Fatalf("items changed: %v", row)
		}
	}
}

func TestAdminRowCarriesAllColumnsAndRelations(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(20)
	status, body, raw := h.get("/api/v1/admin/tables/events/rows/7")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	data := body["data"].(map[string]any)
	if len(data["values"].([]any)) != len(data["columns"].([]any)) {
		t.Fatalf("a row carries one value per column: %s", raw)
	}
	// rowid 7 is item-seed-02 (7 % 5), which has 4 events (2, 7, 12, 17).
	related := map[string]float64{}
	for _, v := range data["related"].([]any) {
		r := v.(map[string]any)
		related[r["table"].(string)+"."+r["column"].(string)] = r["count"].(float64)
	}
	if related["events.object_id"] != 4 {
		t.Fatalf("events for the object come from events_by_object: %v", related)
	}
	if related["events.kind"] != 10 {
		t.Fatalf("events of the kind: %v", related)
	}
	if related["items.item_id"] != 1 {
		t.Fatalf("the item row an event's object names: %v", related)
	}
	refs := data["payload_refs"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["id"] != "item-seed-02" {
		t.Fatalf("payload refs: %v", refs)
	}
	// An item's row relates to its criteria and events: relations are looked up through
	// indexes only, so runs (no item_id index) is not probed.
	_, body, _ = h.get("/api/v1/admin/tables/items/rows/1")
	rel := map[string]float64{}
	for _, v := range body["data"].(map[string]any)["related"].([]any) {
		r := v.(map[string]any)
		rel[r["table"].(string)+"."+r["column"].(string)] = r["count"].(float64)
	}
	if rel["item_criteria.item_id"] != 2 || rel["events.object_id"] != 4 {
		t.Fatalf("item relations: %v", rel)
	}
}

func TestAdminLongTextIsCutOnAPageAndWholeOnTheRow(t *testing.T) {
	h := newServeHarness(t)
	long := strings.Repeat("é", adminCellBytes)
	h.exec(`INSERT INTO events(ts, kind, payload) VALUES (1, 'big', ?)`, long)
	_, page, _ := h.adminPage("/api/v1/admin/tables/events")
	payloadAt := -1
	for i, c := range page.Columns {
		if c["name"] == "payload" {
			payloadAt = i
		}
	}
	cell, ok := page.Rows[0][payloadAt].(map[string]any)
	if !ok || cell["truncated"] != true || cell["length"].(float64) != float64(len(long)) {
		t.Fatalf("page cell: %v", page.Rows[0][payloadAt])
	}
	if text := cell["text"].(string); !strings.HasPrefix(long, text) || len(text) > adminCellBytes {
		t.Fatalf("cut mid-rune or too long: %d bytes", len(text))
	}
	_, body, _ := h.get("/api/v1/admin/tables/events/rows/1")
	values := body["data"].(map[string]any)["values"].([]any)
	if values[payloadAt] != long {
		t.Fatalf("the row must carry the whole text")
	}
}

func TestAdminSchemaFollowsANewTable(t *testing.T) {
	h := newServeHarness(t)
	h.get("/api/v1/admin/tables") // fills the cache
	h.exec(`CREATE TABLE admin_later(id INTEGER PRIMARY KEY, label TEXT)`)
	h.exec(`INSERT INTO admin_later(label) VALUES ('x')`)
	status, page, body := h.adminPage("/api/v1/admin/tables/admin_later")
	if status != http.StatusOK || len(page.Rows) != 1 {
		t.Fatalf("%d %v", status, body)
	}
}

func TestAdminTwoExactFiltersDriveFromTheMoreSelectiveIndex(t *testing.T) {
	h := newServeHarness(t)
	h.seedAdminEvents(3000)
	// kind alone matches 1500 rows and object_id 600: a planner with no statistics may walk
	// the wide index. The chosen driver is the narrower one, measured per request.
	err := h.srv.store.ReadTx(t.Context(), func(r *store.Reader) error {
		schema, err := h.srv.loadAdminSchema(r)
		if err != nil {
			return err
		}
		skip, err := adminSkippedIndexes(r, schema.tables["events"], []adminFilter{
			{col: "kind", op: "=", value: "run.child-session"}, {col: "object_id", op: "=", value: "item-seed-02"}})
		if err != nil {
			return err
		}
		if !skip["kind"] || skip["object_id"] {
			t.Errorf("skip = %v, want only kind skipped", skip)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := h.adminWalk("/api/v1/admin/tables/events?f.kind=run.child-session&f.object_id=item-seed-02&limit=200")
	if len(got) != 300 { // i even and i%5 == 2: i = 2 mod 10, 3000/10 each
		t.Fatalf("combined filter saw %d rows, want 300", len(got))
	}
}
