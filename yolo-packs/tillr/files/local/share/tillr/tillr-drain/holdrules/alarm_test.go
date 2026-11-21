package holdrules

import (
	"strings"
	"testing"
)

func TestBackendAlarmRulesSortFirstAndNameOperator(t *testing.T) {
	if Classes[0] != "alarm" {
		t.Fatalf("first class = %q", Classes[0])
	}
	for _, reason := range []string{"backend-closed", "backend-window", "judge-backend-unavailable"} {
		r := For(reason)
		if r.Class != "alarm" || !strings.HasPrefix(r.Mover, "operator") {
			t.Errorf("%s: %+v", reason, r)
		}
	}
	if _, active := Rules["judge-grant-absent"]; active {
		t.Fatal("the legacy grant refusal must not have an active hold rule")
	}
}
