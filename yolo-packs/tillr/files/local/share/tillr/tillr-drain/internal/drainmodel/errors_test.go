package drainmodel

import (
	"fmt"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr/record"
)

func TestExitCodeUnwrapsSubpackageCodeErrors(t *testing.T) {
	if got := ExitCode(fmt.Errorf("wrapped: %w", &heavyrun.CodeError{Code: record.ExitUsage, Msg: "bad heavy flag"})); got != record.ExitUsage {
		t.Fatalf("ExitCode(*heavyrun.CodeError) = %d, want %d", got, record.ExitUsage)
	}
	if got := ExitCode(fmt.Errorf("wrapped: %w", &fixer.CodeError{Code: record.ExitExternal, Msg: "fixer external"})); got != record.ExitExternal {
		t.Fatalf("ExitCode(*fixer.CodeError) = %d, want %d", got, record.ExitExternal)
	}
}

type codedError int

func (e codedError) Error() string { return fmt.Sprintf("database is locked (%d)", int(e)) }
func (e codedError) Code() int     { return int(e) }

func TestExitCodeBusyStoreReturnsIncompatible(t *testing.T) {
	for _, code := range []int{5, 5 | (2 << 8), 15} {
		err := fmt.Errorf("store: beginning the write transaction: %w", codedError(code))
		if got := ExitCode(err); got != record.ExitIncompatible {
			t.Fatalf("ExitCode(code=%d) = %d, want %d (ExitIncompatible)", code, got, record.ExitIncompatible)
		}
	}
}
