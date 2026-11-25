package tillrdocs

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func repoRootForTest(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestRollingGuardFixtures(t *testing.T) {
	unrelated := "#!/usr/bin/env python3\n" +
		"subprocess.run([\"tillr-drain\", \"loop\"])\n" +
		"subprocess.run([\"other-tool\", \"--rolling\", \"\"])\n"
	if errs := RollingPolicyErrors(map[string]string{"bin/keeper": unrelated}, map[string]string{}); len(errs) > 0 {
		t.Fatalf("rolling guard associated another tool's options with the drain: %v", errs)
	}

	scoped := "#!/usr/bin/env python3\ncommand = [\"tillr-drain\", \"loop\"]\n" +
		"def unused():\n    command.append(\"--rolling=\")\n" +
		"subprocess.run(command)\n"
	if errs := RollingPolicyErrors(map[string]string{"bin/keeper": scoped}, map[string]string{}); len(errs) > 0 {
		t.Fatalf("rolling guard leaked mutations from another function scope: %v", errs)
	}

	disablingCommands := []string{
		"tillr-drain loop --rolling ''",
		`exec /opt/bin/tillr-drain loop --rolling=""`,
		"tillr-drain loop \\\n  --rolling \"\"",
		"tillr drain loop --rolling=",
		`tillr-drain loop '--rol'"ling" ''`,
		`tillr-drain loop -\-rolling=""`,
		"#!/usr/bin/env python3\ndef loop_command():\n    return [\"tillr-drain\", \"loop\", \"--rolling\", \"\"]",
		"#!/usr/bin/env python3\ncommand = [\"tillr-drain\", \"loop\"]\ncommand.extend([\"--rolling\", \"\"])\nsubprocess.run(command)",
		"#!/usr/bin/env python3\nsubprocess.run(\"tillr-drain loop --rolling ''\", shell=True)",
		"#!/usr/bin/env python3\ncommand = [\"tillr-drain\", \"loop\"]\ncommand.append(\"--rolling=\")\nsubprocess.run(command)",
		"#!/usr/bin/env python3\ncommand = \"tillr-drain loop --rolling ''\"\nsubprocess.run(command, shell=True)",
		"#!/usr/bin/env python3\nsubprocess.run(args=[\"tillr-drain\", \"loop\", \"--rolling\", \"\"])",
		"#!/usr/bin/env python3\nif True:\n    command = [\"tillr-drain\", \"loop\", \"--rolling\", \"\"]\nelse:\n    command = [\"tillr-drain\", \"loop\"]\nsubprocess.run(command)",
		"#!/usr/bin/env python3\nif unknown:\n    command = [\"tillr-drain\", \"loop\", \"--rolling\", \"\"]\nelse:\n    command = [\"tillr-drain\", \"loop\"]\nsubprocess.run(command)",
		"#!/usr/bin/env python3\nif unknown:\n    rolling = \"\"\nelse:\n    rolling = \"python3 gate.py\"\nsubprocess.run([\"tillr-drain\", \"loop\", \"--rolling\", rolling])",
	}
	for _, command := range disablingCommands {
		errs := RollingPolicyErrors(map[string]string{"bin/keeper": command}, map[string]string{})
		if len(errs) == 0 || !strings.Contains(errs[0], "bin/keeper") {
			t.Fatalf("rolling guard missed the disabling keeper command: %q (errs=%v)", command, errs)
		}
	}

	overwritten := "#!/usr/bin/env python3\n" +
		"command = [\"tillr-drain\", \"loop\", \"--rolling\", \"\"]\n" +
		"command = [\"tillr-drain\", \"loop\"]\nsubprocess.run(command)\n"
	if errs := RollingPolicyErrors(map[string]string{"bin/keeper": overwritten}, map[string]string{}); len(errs) > 0 {
		t.Fatalf("rolling guard treated a superseded assignment as an invocation: %v", errs)
	}

	if errs := RollingPolicyErrors(map[string]string{"docs/example.md": "tillr-drain loop --rolling ''"}, map[string]string{}); len(errs) == 0 {
		t.Fatalf("rolling guard missed an unfenced document command")
	}
	if errs := RollingPolicyErrors(map[string]string{"bin/keeper": "tillr-drain loop"}, map[string]string{}); len(errs) > 0 {
		t.Fatalf("rolling guard rejected the default enabled check: %v", errs)
	}
	if errs := RollingPolicyErrors(map[string]string{"bin/keeper": "tillr-drain loop --rolling 'python3 gate.py'"}, map[string]string{}); len(errs) > 0 {
		t.Fatalf("rolling guard rejected an explicit nonempty check: %v", errs)
	}

	historical := "docs/design/past.md"
	body := "```sh\ntillr-drain loop --rolling ''\n```"
	if errs := RollingPolicyErrors(map[string]string{historical: body}, map[string]string{historical: "An already executed run."}); len(errs) > 0 {
		t.Fatalf("rolling guard rejected a named historical run: %v", errs)
	}
	if errs := RollingPolicyErrors(map[string]string{historical: "tillr-drain loop"}, map[string]string{historical: "Past run."}); len(errs) == 0 {
		t.Fatalf("rolling guard accepted a stale historical exception")
	}

	copyOf := "yolo-packs/tillr/files/local/share/tillr/" + historical
	if errs := RollingPolicyErrors(map[string]string{historical: body, copyOf: body}, map[string]string{historical: "An already executed run."}); len(errs) > 0 {
		t.Fatalf("rolling guard refused the delivered copy of a named historical run: %v", errs)
	}
	otherCopy := "yolo-packs/tillr/files/local/share/tillr/docs/design/other.md"
	if errs := RollingPolicyErrors(map[string]string{historical: body, otherCopy: body}, map[string]string{historical: "An already executed run."}); len(errs) == 0 {
		t.Fatalf("rolling guard excused the delivered copy of a document with no exception")
	}
	for _, name := range []string{"bin/keeper", "yolo-packs/tillr/skills/keeper/SKILL.md", ".justfile"} {
		if errs := RollingPolicyErrors(map[string]string{name: body}, map[string]string{name: "Not a historical record."}); len(errs) == 0 {
			t.Fatalf("rolling guard allowed an instruction exception: %s", name)
		}
	}
}

func TestRecoveryFixtures(t *testing.T) {
	specimen := "## Recovery\nRetry under its own id with `tillr drain retry`.\n"
	if errs := RecoveryErrors(specimen); len(errs) > 0 {
		t.Fatalf("same-id recovery fixture was refused: %v", errs)
	}
	if errs := RecoveryErrors(specimen + "Use `item-failed-v2`.\n"); len(errs) == 0 {
		t.Fatalf("recovery guard accepted a cloned -v2 item id")
	}
	for _, instruction := range []string{
		"Run a recovery script.",
		"Use `python3 recover.py`.",
		"Edit the store directly.",
		"Run `sqlite3 state.db 'UPDATE lanes SET state=idle'`.",
		"Run `git reset --hard main` in the lane.",
	} {
		if errs := RecoveryErrors(specimen + instruction + "\n"); len(errs) == 0 {
			t.Fatalf("recovery guard accepted an unsupported hand operation: %s", instruction)
		}
	}
}

func TestCheckRollingEnabled(t *testing.T) {
	root := repoRootForTest(t)
	msg, err := CheckRollingEnabled(root)
	if err != nil {
		t.Fatalf("CheckRollingEnabled: %v", err)
	}
	if !strings.Contains(msg, "tracked loop invocations keep the after-merge check on") {
		t.Fatalf("unexpected message: %q", msg)
	}
}

func TestFacesHelpFixtureError(t *testing.T) {
	root := repoRootForTest(t)
	scratch, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "tillr-docs-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(scratch)

	if err := BuildBinariesError(scratch, root); err != nil {
		t.Fatalf("BuildBinariesError: %v", err)
	}
	if err := FacesHelpFixtureError(scratch, root); err != nil {
		t.Fatalf("FacesHelpFixtureError: %v", err)
	}
}

func TestShlexSplitAndJoin(t *testing.T) {
	got, err := ShlexSplit(`tillr-drain loop '--rol'"ling" '' && echo ok`)
	if err != nil {
		t.Fatalf("ShlexSplit error: %v", err)
	}
	want := []string{"tillr-drain", "loop", "--rolling", "", "&&", "echo", "ok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ShlexSplit = %#v, want %#v", got, want)
	}

	if _, err := ShlexSplit(`unterminated 'quote`); err == nil {
		t.Fatalf("expected error on unterminated quote")
	}

	joined := ShlexJoin([]string{"tillr-drain", "loop", "--rolling", ""})
	if joined != "tillr-drain loop --rolling ''" {
		t.Fatalf("ShlexJoin = %q, want %q", joined, "tillr-drain loop --rolling ''")
	}
}

func TestDocumentedInvocations(t *testing.T) {
	cases := []struct {
		words []string
		want  []string
	}{
		{[]string{"tillr", "item", "comments", "take", "--flag"}, []string{"tillr", "item", "comments", "take"}},
		{[]string{"tillr-gate", "shadow", "<id>"}, []string{"tillr-gate", "shadow"}},
		{[]string{"tillr", "unknown", "face"}, nil},
		{[]string{"echo", "hello"}, nil},
	}
	for _, tc := range cases {
		got := Documented(tc.words)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Documented(%v) = %v, want %v", tc.words, got, tc.want)
		}
	}
}
