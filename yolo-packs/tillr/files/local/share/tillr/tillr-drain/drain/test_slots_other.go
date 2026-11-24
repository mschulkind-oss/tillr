//go:build !linux

package drain

func inspectTestSlots(_ string, _ TestSlotsLimits) (testSlotsSnapshot, error) {
	return testSlotsSnapshot{}, nil
}
