package drain_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyLandedCostsLegacyStore(t *testing.T) {
	path := fixture(t, itemRecord("legacy", "landed"))
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('legacy-lane','free');
 INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at) VALUES('legacy-run','legacy','legacy-lane','pi','exited',0,0);
 INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES('legacy-landing','legacy','legacy-run','v','landed','b','h','x','y',1);
 DROP TABLE node_runs; DROP TABLE workflow_selections; DROP TABLE seat_dispatches; ALTER TABLE runs DROP COLUMN usage_cost_usd`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	code, out := invoke(t, "why", "--store", path)
	if code != 0 || !strings.Contains(string(out), "cost per landed item: legacy unknown") || !strings.Contains(string(out), "cost per seat: build unknown") {
		t.Fatalf("legacy why: %d %s", code, out)
	}
}

func TestWhyLandedCosts(t *testing.T) {
	path := fixture(t)
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('cost-lane','free')`); err != nil {
			return err
		}
		for i := 1; i <= 21; i++ {
			id := fmt.Sprintf("cost-%02d", i)
			if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES(?,?,'landed',0,'')`, id, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_cost_usd) VALUES(?,?,'cost-lane','pi','exited',0,0,?)`, id, id, float64(i)); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'v','landed','b','h','x','y',?)`, id, id, id, i); err != nil {
				return err
			}
			if i <= 10 {
				if _, err := tx.Exec(`INSERT INTO workflow_selections(item_id,workflow,source,graph) VALUES(?,'planned-build@v1','item','{"name":"planned-build","version":1,"stages":[{"id":"n","kind":"agent","seat":"plan","access":"read_only"}]}')`, id); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome,seat,model) VALUES(?,1,'n',?,'passed','plan','model')`, id, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	code, out := invoke(t, "why", "--store", path)
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	for _, want := range []string{"cost per landed item: cost-21 $21.00", "cost per seat: build $176.00 (landed: $176.00)", "cost per seat: plan $55.00 (landed: $55.00)", "median cost, last 20 landed build items: $11.50", "median cost, last 20 landed planned-build@v1 items: $5.50"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	s, err = store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE workflow_selections SET workflow='other@v1' WHERE item_id='cost-01'; UPDATE items SET seat='docs' WHERE item_id='cost-21'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	code, out = invoke(t, "why", "--store", path)
	if code != 0 || strings.Contains(string(out), "median cost, last 20 landed planned-build@v1 items:") || !strings.Contains(string(out), "cost per seat: docs $21.00 (landed: $21.00)") {
		t.Fatalf("threshold or item seat: %d %s", code, out)
	}
	s, err = store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE workflow_selections SET workflow='planned-build@v1' WHERE item_id='cost-01'; UPDATE items SET seat='' WHERE item_id='cost-21'; UPDATE runs SET usage_cost_usd=NULL WHERE run_id IN ('cost-01','cost-21')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	code, out = invoke(t, "why", "--store", path)
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	for _, want := range []string{"cost per landed item: cost-01 unknown", "cost per seat: plan unknown (landed: unknown)", "median cost, last 20 landed build items: unknown", "median cost, last 20 landed planned-build@v1 items: unknown"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestWhyOpenItemSeatCosts(t *testing.T) {
	path := fixture(t, itemRecord("landed-1", "landed"), itemRecord("open-1", "in-queue"))
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`
 INSERT INTO lanes(lane_id,state) VALUES('cost-lane','free');
 INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_cost_usd) VALUES
   ('run-landed-build','landed-1','cost-lane','pi','exited',0,0,10.0),
   ('run-open-design-1','open-1','cost-lane','pi','exited',0,0,50.0),
   ('run-open-design-2','open-1','cost-lane','pi','exited',0,0,25.0),
   ('run-open-build','open-1','cost-lane','pi','exited',0,0,25.0);
 INSERT INTO seat_dispatches(run_id,seat,backend) VALUES
   ('run-open-design-1','design','pi'),
   ('run-open-design-2','design','pi');
 INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES
   ('landing-1','landed-1','run-landed-build','v','landed','b','h','x','y',1);`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	code, out := invoke(t, "why", "--store", path)
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	text := string(out)
	for _, want := range []string{
		"cost per landed item: landed-1 $10.00",
		"cost per seat: build $35.00 (landed: $10.00)",
		"cost per seat: design $75.00 (landed: $0.00)",
		"median cost, last 20 landed build items: $10.00",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "cost per landed item: open-1") {
		t.Errorf("open item appeared in landed items: %s", text)
	}
}
