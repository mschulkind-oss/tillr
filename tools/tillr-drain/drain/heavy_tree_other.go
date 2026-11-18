//go:build !linux

package drain

func prepareHeavyTree() error {
	return Externalf("heavy lifetime supervision requires Linux child adoption and pidfd signaling")
}
func finishHeavyTree() error                   { return prepareHeavyTree() }
func heavySupervisorIdentity() (string, error) { return "", prepareHeavyTree() }
func reapWhileSupervising(<-chan int) func()   { return func() {} }
