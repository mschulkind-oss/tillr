package orchestrator

import (
	"context"
	"fmt"
)

// NoopSpawn is a SpawnFunc that does not actually invoke claude. It
// simulates a successful run with a synthetic completed result.
// Used by `tillr orchestrator start --dry-run` and by tests that
// don't want to depend on a real claude binary.
func NoopSpawn(_ context.Context, opts SpawnOpts) Spawned {
	return Spawned{
		ExitCode:     0,
		DurationMS:   0,
		CostUSD:      0.0,
		InputTokens:  0,
		OutputTokens: 0,
		SessionID:    "dry-run",
		Model:        "dry-run",
		Result: Result{
			Summary: fmt.Sprintf("[dry-run] persona %s would have run feature #%d (%s)",
				opts.Persona, opts.FeatureID, opts.FeatureTitle),
			ContextEntry: fmt.Sprintf("Dry-run entry for feature #%d.", opts.FeatureID),
			Result:       "completed",
		},
	}
}
