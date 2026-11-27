package drain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A run paused by a quota window before its harness took a first turn has a staged
// session id but no conversation behind it, so resuming it fails with the harness's
// "No conversation found" diagnostic. That ending says the conversation is gone, not
// that the backend is broken: the next dispatch starts a new session at once, with no
// resumed attempt in between, and the backend stays open.
func TestQuotaResumeWithMissingConversationStartsNewSession(t *testing.T) {
	f, l, steps, d, _ := quotaContinuationFixture(t, true)
	const session = "33b5f2d2-0cd4-4a49-b293-c58f5635561f"
	alpha := filepath.Join(steps.backends, "alpha")
	writeForkFile(t, filepath.Join(alpha, "exec"), "#!/bin/sh\nif [ \"$TILLR_SESSION_MODE\" = resume ]; then\n"+
		"echo '{\"type\":\"result\",\"subtype\":\"error_during_execution\",\"is_error\":true,\"num_turns\":0,\"session_id\":\""+session+"\",\"errors\":[\"No conversation found with session ID: "+session+"\"]}'\n"+
		"echo 'No conversation found with session ID: "+session+"' >&2\nexit 1\nfi\n"+
		"test -f one.txt && test -f two.txt || exit 9\ngit add one.txt two.txt; git commit -qm candidate\necho 'RESULT: done'\n")
	writeForkFile(t, filepath.Join(alpha, "extract-report"), "#!/bin/sh\nprev=; out=; code=\nfor arg in \"$@\"; do [ \"$prev\" != --out ] || out=$arg; [ \"$prev\" != --exit-code ] || code=$arg; prev=$arg; done\nif [ \"$code\" = 0 ]; then printf '# run report\\n---\\nRESULT: done\\n' > \"$out\"; else printf '# run report\\n---\\n' > \"$out\"; fi\n")
	for _, name := range []string{"exec", "extract-report"} {
		if err := os.Chmod(filepath.Join(alpha, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	var result LoopResult
	settleContinuation(t, l, &result)
	steps.at = steps.at.Add(2 * time.Hour)
	for i := 0; i < 4; i++ {
		settleContinuation(t, l, &result)
	}
	if len(steps.requests) != 3 {
		t.Fatalf("want first run, failed resume, new session; got %d requests: %+v", len(steps.requests), steps.requests)
	}
	resumed, fresh := steps.requests[1], steps.requests[2]
	if resumed.RunID != d.RunRequest.RunID || !resumed.Resume {
		t.Fatalf("quota continuation did not resume the paused run: %+v", resumed)
	}
	if fresh.RunID != attemptRunID(d.ItemID, 2) || fresh.Resume || fresh.Backend != "alpha" {
		t.Fatalf("next dispatch is not a new session on the same backend: %+v", fresh)
	}
	if steps.gates != 1 || steps.lands != 1 || result.InfraFailures != 0 {
		t.Fatalf("gates=%d lands=%d infra=%d", steps.gates, steps.lands, result.InfraFailures)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind=?`, backendOutcomeKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var o struct {
				Closes bool   `json:"closes"`
				Cause  string `json:"cause"`
			}
			if err := json.Unmarshal(raw, &o); err != nil {
				return err
			}
			if o.Closes {
				t.Errorf("missing conversation closed the backend: %s", raw)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, infrastructureFailureKind).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("missing conversation recorded %d infrastructure failures", n)
		}
		return nil
	})
}
