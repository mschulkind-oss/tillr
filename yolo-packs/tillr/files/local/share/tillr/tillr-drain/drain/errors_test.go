package drain

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
