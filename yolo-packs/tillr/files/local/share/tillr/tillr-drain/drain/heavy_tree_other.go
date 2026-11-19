//go:build !linux

package drain

func prepareHeavyTree() error {
	return Externalf("heavy lifetime supervision requires Linux child adoption and pidfd signaling")
}
func finishHeavyTree() error                           { return prepareHeavyTree() }
func heavySupervisorIdentity() (int, string, error)    { return 0, "", prepareHeavyTree() }
func heavySupervisorRunning(int, string) (bool, error) { return false, prepareHeavyTree() }
func reapWhileSupervising(<-chan int) func()           { return func() {} }
