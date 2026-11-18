package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func sharesPolicy(t *testing.T, seat string) Policy {
	t.Helper()
	p := seatListPolicy(t)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["seats"] = json.RawMessage(`{"build":` + seat + `}`)
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSeatSharesValidation(t *testing.T) {
	for _, seat := range []string{
		`[{"backend":"builder-a","share":0}]`,
		`[{"backend":"builder-a","share":-1}]`,
		`[{"backend":"builder-a","share":"one"}]`,
		`[{"backend":"builder-a","share":null}]`,
		`[{"backend":"builder-a","share":1},"builder-b"]`,
		`{"backends":["builder-a"],"pick":"typo"}`,
	} {
		t.Run(seat, func(t *testing.T) {
			p := seatListPolicy(t)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			doc["seats"] = json.RawMessage(`{"build":` + seat + `}`)
			raw, err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			err = json.Unmarshal(raw, &p)
			if err == nil {
				err = p.Check()
			}
			if err == nil || !strings.Contains(err.Error(), "build") {
				t.Fatalf("seat-named refusal: %v", err)
			}
		})
	}
}

func TestSeatSharesPersistedDispatches(t *testing.T) {
	for _, tc := range []struct {
		seat  string
		total int
		wantA int
	}{
		{`[{"backend":"builder-a","share":1},{"backend":"builder-b","share":1}]`, 40, 20},
		{`[{"backend":"builder-a","share":3},{"backend":"builder-b","share":1}]`, 40, 30},
		{`{"backends":["builder-a","builder-b"],"pick":"random"}`, 300, -1},
		{`{"backends":[{"backend":"builder-a","share":3},{"backend":"builder-b","share":1}],"pick":"random"}`, 300, -2},
	} {
		t.Run(tc.seat, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(scratchUnderDurable(t), "shares.sqlite3")
			s, err := store.InitFixture(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			p := sharesPolicy(t, tc.seat)
			if err := p.Check(); err != nil {
				t.Fatal(err)
			}
			count := map[string]int{}
			draws := rand.New(rand.NewPCG(42, 73))
			for i := 0; i < tc.total; i++ {
				if i == 20 {
					s.Close()
					s, err = store.Open(ctx, path, true)
					if err != nil {
						t.Fatal(err)
					}
					p = sharesPolicy(t, tc.seat)
				}
				// Independent policy instances model two loops using the same transaction history.
				other := sharesPolicy(t, tc.seat)
				if i%2 == 0 {
					p = other
				}
				p.seatDraw = draws.Float64
				err = s.WriteTx(ctx, func(tx *store.Tx) error {
					d := Decision{DecisionID: fmt.Sprintf("d-%d", i), ItemID: fmt.Sprintf("item-%d", i), Seat: "build", Action: ActionDispatch, Reason: ReasonReady, SelectSeat: true, RunRequest: p.request(fmt.Sprintf("item-%d", i), "lane", fmt.Sprintf("run-%d", i), "builder-a")}
					ok, err := selectSeatDispatch(tx, &d, &p, map[string]bool{}, time.Now())
					if err != nil {
						return err
					}
					if !ok {
						t.Fatal("no open backend")
					}
					count[d.RunRequest.Backend]++
					if tc.wantA == 20 && d.RunRequest.Backend != []string{"builder-a", "builder-b"}[i%2] {
						t.Fatalf("dispatch %d: %s", i, d.RunRequest.Backend)
					}
					return recordPassDecision(tx, d, time.Now())
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			defer s.Close()
			if tc.wantA >= 0 && count["builder-a"] != tc.wantA {
				t.Fatalf("counts: %v", count)
			}
			if tc.wantA == -1 && (count["builder-a"] < 120 || count["builder-a"] > 180) {
				t.Fatalf("equal random counts: %v", count)
			}
			if tc.wantA == -2 && (count["builder-a"] < 190 || count["builder-a"] > 260) {
				t.Fatalf("weighted random counts: %v", count)
			}
			t.Logf("%d dispatches: %v", tc.total, count)
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				var n int
				if err := r.QueryRow(`SELECT count(*) FROM dispatches WHERE backend IS NOT NULL`).Scan(&n); err != nil {
					return err
				}
				if n != tc.total {
					t.Fatalf("recorded draws %d", n)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
