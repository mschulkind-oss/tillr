package drain

import "testing"

func TestHeavyLegacyFailureRecoversWhileALaterDeclarationRuns(t *testing.T) {
	testHeavyEarlyProcess(t, true)
}
