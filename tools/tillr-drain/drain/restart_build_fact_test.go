package drain

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDrainBuildStartedRecordsCarriedRestartRequest(t *testing.T) {
	ctx := context.Background()
	h := newRestartHarness(t, []byte("drain build"), nil)
	t.Setenv(restart.RequestEnv, "")
	assertBuild := func(want string) {
		t.Helper()
		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		if h.l.stopInheritedReaper != nil {
			t.Cleanup(h.l.stopInheritedReaper)
		}
		s := openFixture(t, h.f.storePath)
		defer s.Close()
		var value, kind sql.NullString
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT json_extract(payload, '$.restart_request'), json_type(payload, '$.restart_request')
FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`, restart.BuildKind).Scan(&value, &kind)
		}); err != nil {
			t.Fatal(err)
		}
		if want == "" {
			if kind.Valid {
				t.Fatalf("ordinary build-started must omit restart_request, got %s %q", kind.String, value.String)
			}
		} else if kind.String != "text" || value.String != want {
			t.Fatalf("replacement build-started restart_request = %q, want %q", value.String, want)
		}
	}

	assertBuild("")
	req := h.request(t, 1)[0]
	t.Setenv(restart.RequestEnv, req.ID)
	assertBuild(req.ID)
}
