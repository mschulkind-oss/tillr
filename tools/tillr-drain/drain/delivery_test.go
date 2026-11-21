package drain

import (
	"reflect"
	"testing"
)

func TestDeliveryPieces(t *testing.T) {
	for _, tc := range []struct {
		decision deliveryDecision
		want     []string
	}{
		{deliveryDecision{}, nil},
		{deliveryDecision{Kind: "repo"}, []string{""}},
		{deliveryDecision{Kind: "repo", Fork: "pi"}, nil},
		{deliveryDecision{Kind: "fork", Fork: "pi"}, []string{"pi"}},
		{deliveryDecision{Kind: "fork", Fork: "unknown"}, nil},
		{deliveryDecision{Kind: "both", Fork: "pi"}, []string{"pi", ""}},
	} {
		got, err := tc.decision.pieces(map[string]bool{"pi": true})
		if (err != nil) != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v: got %q, %v; want %q", tc.decision, got, err, tc.want)
		}
	}
}
