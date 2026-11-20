package drain

import (
	"context"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
)

// ReasonPiExtensionsBuildFailed is recorded on drain.post-landing-action when
// rebuilding the drain's trunk pi extension directory fails; the previous
// working extension set stays active and the loop continues.
const ReasonPiExtensionsBuildFailed = "pi-extensions-build-failed"

var extensionRollPaths = []string{
	"yolo-packs/matt/files/extensions/",
	"yolo-packs/matt/patches/",
	"yolo-packs/matt/pack.json",
	"bin/pi-forks.list",
}

func isExtensionPatchBranch(baseRef string) bool {
	const prefix = "refs/heads/patch/"
	if !strings.HasPrefix(baseRef, prefix) {
		return false
	}
	name := strings.TrimPrefix(baseRef, prefix)
	return name != "" && name != "pi"
}

func matchesExtensionRollPaths(changed string) bool {
	return matchesRollPaths(changed, extensionRollPaths)
}

func (l *Loop) submitExtensionRoll(ctx context.Context, command, tip string) {
	l.extRollAction.submit(tip, func(tip string) {
		_, err := actionCommand(ctx, l.cfg.Root, "sh", "-c", command+" --pi-extensions")
		if err != nil {
			mover := holdrules.For(ReasonPiExtensionsBuildFailed).Mover
			l.reportf("loop: extension roll at %s failed (%s; mover: %s): %s\n",
				shortSHA(tip), ReasonPiExtensionsBuildFailed, mover, strings.Join(strings.Fields(err.Error()), " "))
			l.recordActionWhat(ctx, "roll", "pi-extensions", tip,
				fmt.Errorf("%s (mover: %s): %w", ReasonPiExtensionsBuildFailed, mover, err))
			return
		}
		l.recordActionWhat(ctx, "roll", "pi-extensions", tip, nil)
	})
}
