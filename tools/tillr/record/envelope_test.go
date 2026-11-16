package record_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

const good = `{"record_version":1,"kind":"gate.verdict","id":"v-1",
"producer":{"system":"tillr","utility":"gate","version":"0.1.0"},
"created_at":"2026-11-06T00:00:00Z","payload":{"result":"pass"}}`

func TestARecordRoundTrips(t *testing.T) {
	env, err := record.Decode([]byte(good))
	if err != nil {
		t.Fatalf("a well-formed record was refused: %v", err)
	}
	if env.Kind != "gate.verdict" || env.Producer.Utility != "gate" {
		t.Fatalf("decoded %q from %q", env.Kind, env.Producer.Utility)
	}
	if !env.CreatedAt.Equal(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("created_at decoded as %v", env.CreatedAt)
	}
	again, err := record.Encode(env)
	if err != nil {
		t.Fatalf("re-encoding failed: %v", err)
	}
	if _, err := record.Decode(again); err != nil {
		t.Fatalf("the re-encoded record does not decode: %v", err)
	}
}

// A consumer that guesses at a version it does not know is the failure the
// envelope exists to prevent, so the refusal is a named error and not a parse
// failure the caller might retry.
func TestARecordRefusesAnUnsupportedVersion(t *testing.T) {
	raw := []byte(`{"record_version":99,"kind":"gate.verdict","id":"v-1",
"producer":{"system":"tillr","utility":"gate","version":"0.1.0"},
"created_at":"2026-11-06T00:00:00Z","payload":{}}`)
	_, err := record.Decode(raw)
	if !errors.Is(err, record.ErrUnsupportedVersion) {
		t.Fatalf("want ErrUnsupportedVersion, got %v", err)
	}
}

// The system name is not a free field: a record from another system is not this
// system's record, and accepting it would let a second consumer's records be read
// as board state.
func TestARecordFromAnotherSystemIsRefused(t *testing.T) {
	raw := []byte(`{"record_version":1,"kind":"gate.verdict","id":"v-1",
"producer":{"system":"other","utility":"gate","version":"0.1.0"},
"created_at":"2026-11-06T00:00:00Z","payload":{}}`)
	_, err := record.Decode(raw)
	if !errors.Is(err, record.ErrForeignSystem) {
		t.Fatalf("want ErrForeignSystem, got %v", err)
	}
}

func TestTheExitCodesAreTheDesigns(t *testing.T) {
	for want, got := range map[int]int{
		0: record.ExitOK, 1: record.ExitNegative, 2: record.ExitUsage,
		3: record.ExitIncompatible, 4: record.ExitExternal, 5: record.ExitInternal,
	} {
		if got != want {
			t.Errorf("exit %d is %d", want, got)
		}
	}
}
