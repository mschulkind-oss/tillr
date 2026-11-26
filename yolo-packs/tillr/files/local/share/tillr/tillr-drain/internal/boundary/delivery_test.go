package boundary

import (
	"reflect"
	"testing"
)

func TestDeliveryPieces(t *testing.T) {
	for _, tc := range []struct {
		decision DeliveryDecision
		want     []string
	}{
		{DeliveryDecision{}, nil},
		{DeliveryDecision{Kind: "repo"}, []string{""}},
		{DeliveryDecision{Kind: "repo", Fork: "pi"}, nil},
		{DeliveryDecision{Kind: "fork", Fork: "pi"}, []string{"pi"}},
		{DeliveryDecision{Kind: "fork", Fork: "unknown"}, nil},
		{DeliveryDecision{Kind: "both", Fork: "pi"}, []string{"pi", ""}},
	} {
		got, err := tc.decision.Pieces(map[string]bool{"pi": true})
		if (err != nil) != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v: got %q, %v; want %q", tc.decision, got, err, tc.want)
		}
	}
}
