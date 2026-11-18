package drain

import (
	"os"
	"os/exec"
	"testing"
)

// Newly added tests have no recorded duration. A large fallback must not leave
// five processes idle while a single process executes all of those tests.
func TestShardRunnerBalancesUnrecordedTests(t *testing.T) {
	// The installed pack ships the module but not the repository-only runner.
	if _, err := os.Stat("../../go-test-shards.py"); os.IsNotExist(err) {
		t.Skip("repository-only shard runner is not installed")
	} else if err != nil {
		t.Fatal(err)
	}
	// The Python child is invisible to Go's result cache. Open the runner through
	// the persistent module-local alias used for sibling source inputs.
	declareSiblingInputs(t)
	if _, err := os.ReadFile("../.sibling-inputs/go-test-shards.py"); err != nil {
		t.Fatal(err)
	}
	program := `
import importlib.util
spec = importlib.util.spec_from_file_location('shards', '../../go-test-shards.py')
s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s)
for known in ([], ['TestKnown%d' % i for i in range(6)]):
    unknown = ['TestNew%d' % i for i in range(120)]
    names = known + unknown
    groups = s.assign(names, {name: 1 for name in known}, 6)
    assert sorted(sum(groups, [])) == sorted(names), groups
    assert all(18 <= len(group) <= 22 for group in groups), [len(group) for group in groups]
`
	if out, err := exec.Command("python3", "-c", program).CombinedOutput(); err != nil {
		t.Fatalf("shard assignment: %v\n%s", err, out)
	}
}
