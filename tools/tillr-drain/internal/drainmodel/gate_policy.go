package drainmodel

import "github.com/mschulkind-oss/tillr-drain/redcap"

// GatePolicy is the repository's gate settings, shared with the locked lander.
type GatePolicy = redcap.GatePolicy

// ReadGatePolicy preserves the drain's usage-error contract for invalid settings.
func ReadGatePolicy(root string) (GatePolicy, bool, error) {
	p, absent, err := redcap.ReadGatePolicy(root)
	if err != nil {
		return p, absent, Usagef("%s", err)
	}
	return p, absent, nil
}
