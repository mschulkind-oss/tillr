package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestDesignerPolicyAdapterPauseKindPaths(t *testing.T) {
	for _, tc := range []struct {
		name     string
		absolute bool
		want     string
	}{
		{"explicit-relative", false, "resumed-warm"},
		{"installed-name", true, "cannot-resume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, _ := commentFixture(t, "")
			dir := filepath.Join(f.root, "designer-adapter-fixture")
			if tc.absolute {
				root := filepath.Join(f.root, "install")
				t.Setenv("TILLR_INSTALL_ROOT", root)
				dir = filepath.Join(root, "tillr-run", "backends", "pi")
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(dir, "pause-kind"), "cannot-resume\n")
			transcript := filepath.Join(f.root, "session.jsonl")
			writeForkFile(t, transcript, "transcript")
			adapter := "./designer-adapter-fixture/run"
			if tc.absolute {
				writeForkFile(t, filepath.Join(dir, "run-pi"), "#!/bin/sh\n")
				if err := os.Chmod(filepath.Join(dir, "run-pi"), 0755); err != nil {
					t.Fatal(err)
				}
				adapter = "run-pi"
			}
			l.cfg.Policy.Backends["fixture"] = Backend{Adapter: adapter, Model: "fixture-model"}
			raw, err := json.Marshal(l.cfg.Policy)
			if err != nil {
				t.Fatal(err)
			}
			l.cfg.Policy, err = decodePolicy("fixture", raw)
			if err != nil {
				t.Fatal(err)
			}
			l.cfg.Policy.Root = f.root
			lifetime := 3600
			designer := lastDesigner{Document: "docs/design/example.md", SessionID: "old", Backend: "fixture", Model: "fixture-model", Transcript: transcript, LastRequest: l.cfg.Now().Add(-time.Minute).Format(time.RFC3339Nano), CacheLifetimeSeconds: &lifetime}
			putDesigner(t, f, designer)
			number := 1
			server := stubReview(t, &number)
			defer server.Close()
			id := takeFixtureComment(t, f, server.URL)
			var d Decision
			for _, planned := range mustPlan(t, l) {
				if planned.decision.ItemID == id {
					d = planned.decision
				}
			}
			if d.RunRequest == nil || d.DesignerReason != tc.want {
				t.Fatalf("planning: %+v, want %s", d, tc.want)
			}
			d.Designer = &designer // The plan's public representation omits this internal field.
			p := l.cfg.Policy
			p.SeatLists = map[string][]string{d.Seat: {"fixture"}}
			p.SeatShares = map[string][]float64{d.Seat: {1}}
			d.SelectSeat = true
			ctx := context.Background()
			s, err := store.Open(ctx, f.storePath, true)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			err = s.WriteTx(ctx, func(tx *store.Tx) error {
				ok, err := selectSeatDispatch(tx, &d, p, nil, l.cfg.Now())
				if err == nil && !ok {
					t.Fatal("seat selection refused")
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if d.DesignerReason != tc.want {
				t.Fatalf("seat selection: %+v, want %s", d, tc.want)
			}
		})
	}
}
