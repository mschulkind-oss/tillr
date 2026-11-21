package drain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnsharedMultiBackendSeatIsRefused(t *testing.T) {
	for _, seat := range []string{`["first","second"]`, `{"backends":["first","second"],"pick":"round-robin"}`} {
		var p Policy
		if err := json.Unmarshal([]byte(`{"default_backend":"first","seats":{"build":`+seat+`},"backends":{"first":{"adapter":"true","model":"a","effort":"medium"},"second":{"adapter":"true","model":"b","effort":"medium"}}}`), &p); err != nil {
			t.Fatal(err)
		}
		if err := p.Check(); err == nil || !strings.Contains(err.Error(), `seat "build" must name one backend`) {
			t.Errorf("seat %s: expected a refusal naming build, got %v", seat, err)
		}
	}
}
