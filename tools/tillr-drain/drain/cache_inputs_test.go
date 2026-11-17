package drain

import (
	"testing"

	"github.com/mschulkind-oss/tillr-drain/internal/testinputs"
)

func declareSiblingInputs(t *testing.T) {
	t.Helper()
	if err := testinputs.Track(".."); err != nil {
		t.Fatal(err)
	}
}

func TestSiblingSourcesAreCacheInputs(t *testing.T) {
	declareSiblingInputs(t)
}
