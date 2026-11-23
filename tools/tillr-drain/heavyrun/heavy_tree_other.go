//go:build !linux

package heavyrun

func PrepareHeavyTree() error {
	return Externalf("heavy lifetime supervision requires Linux child adoption and pidfd signaling")
}
func FinishHeavyTree() error                           { return PrepareHeavyTree() }
func HeavySupervisorIdentity() (int, string, error)    { return 0, "", PrepareHeavyTree() }
func HeavySupervisorRunning(int, string) (bool, error) { return false, PrepareHeavyTree() }
func ReapWhileSupervising(<-chan int) func()           { return func() {} }
