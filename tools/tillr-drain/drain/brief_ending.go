package drain

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
)

const endingCapabilityFile = "ending-capability"

func stagedEndingCapability(dir string) (string, error) {
	path := filepath.Join(dir, endingCapabilityFile)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return run.EndingFooter, nil // Sessions staged before capability pinning used footers.
	}
	if err != nil {
		return "", Externalf("reading %s: %v", path, err)
	}
	switch capability := strings.TrimSpace(string(raw)); capability {
	case run.EndingFooter, run.EndingCallSuspends:
		return capability, nil
	default:
		return "", Externalf("invalid staged ending capability %q in %s", capability, path)
	}
}

const callEndingRequired = `This run ends with one
tool call, and a message without it is not an ending: you will be asked to continue.`

const finishing = `## Finishing

You are running unattended. Nobody reads a message from you. ` + callEndingRequired + `

Call ` + "`" + `land` + "`" + ` with no arguments when your change is committed on this branch and its verification has run.

The commit at the head of this branch when you call is what you offer. Uncommitted work
is not offered, and the call returns at once, naming the paths, if there is any.

The call does not return while your change is checked and landed. If it lands and holds,
you will never see a result: this session ends there, and that is success. If a result
arrives, it is one of these, and none is counted against the item:

  refused        a check said no. The result names the check and carries its output.
  conflict       your commit does not apply to the trunk. The result names the paths
                 and the trunk commit. Reconcile by merge or rebase, your choice.
  reverted       your change landed, the full suite then failed on a check tied to it,
                 and it was taken back out. The result carries that check's output.
  design-answer  the design seat changed the item on this branch. Continue from there.

Every result states the branch head. Check it before you change anything. Fix what the
result names, commit, and call ` + "`" + `land` + "`" + ` again.

If a fact you cannot change stops you, call ` + "`" + `obstacle` + "`" + ` instead, with ` + "`" + `kind` + "`" + ` and its required fields.
`

func callEndingBrief(body string) (string, error) {
	prefix, report, ok := strings.Cut(body, "\n## Report shape\n")
	_, obstacle, table := strings.Cut(report, "The only other ending is an **obstacle**:")
	obstacle, _, end := strings.Cut(obstacle, "\nYour final message ")
	if !ok || !table || !end {
		return "", Externalf("the brief template needs Report shape and its obstacle table to render Finishing")
	}
	prefix = strings.Replace(prefix, `Your
first message without a tool call ends the run, so keep
working with tools until the goal in **Report shape** is met or one of the obstacles it
names holds.`, "See **Finishing** for how to end this run.", 1)
	prefix = strings.ReplaceAll(prefix, "**Report shape**", "**Finishing**")
	return prefix + "\n" + finishing + "\nThe only other ending is an **obstacle**:" + obstacle + "\nCall `obstacle` with `kind` and that kind's fields in lowercase (`criteria`, `evidence`, `paths`, `checks`, `base`, `command`, `tried`, `missing`, `question`) instead of writing a `RESULT: blocked` footer.\n", nil
}

const noChangeMarkerLine = "A reproduced no-change finding may finish with no_change."

const noChangeFinishingNote = `If your reproductions establish that nothing in the repository needs changing, leave the
worktree clean at the staged base and call ` + "`" + `no_change` + "`" + ` with ` + "`" + `cause` + "`" + `, ` + "`" + `evidence` + "`" + `, and
` + "`" + `why_no_change` + "`" + ` instead of ` + "`" + `land` + "`" + ` or ` + "`" + `obstacle` + "`" + `.
`

func addNoChangeToFinishing(ending string) string {
	if strings.Contains(ending, noChangeMarkerLine) {
		return ending
	}
	return ending + "\n" + noChangeMarkerLine + "\n" + noChangeFinishingNote
}
