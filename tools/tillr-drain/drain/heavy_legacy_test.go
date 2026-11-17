package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHeavyLegacyAttributionUsesTheRealReplaySelectorVocabulary(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	bin := heavyBuildGate(t, work)
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checks, "fast.json"), `{"id":"fast","label":"fast","scope":"tree","tier":"fast","budget_ms":1,"command":["true"]}`)
	base := heavyCommit(t, root, "good")
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "bad")
	heavyCommit(t, root, "worse")
	source, err := filepath.Abs(filepath.Join("..", "..", "fork-check.py"))
	if err != nil {
		t.Fatal(err)
	}
	replay := filepath.Join(work, "replay.py")
	writeForkFile(t, replay, `import ast,pathlib,sys
module=ast.parse(pathlib.Path(sys.argv[1]).read_text())
function=next(node for node in module.body if isinstance(node,ast.FunctionDef) and node.name=='preflight_names')
namespace={}
exec(compile(ast.Module(body=[function],type_ignores=[]),'<real selector>','exec'),namespace)
if not namespace['preflight_names'](sys.argv[2],'go-tool','go tools/tillr-drain'): sys.exit(4)
sys.exit(0 if pathlib.Path('value').read_text()=='good' else 1)
`)
	legacy := executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"go-tool/go-tools-tillr-drain","kind":"go-tool","label":"go tools/tillr-drain","status":"FAIL","attributes_json":"{\"writes\":[\"value\"]}"}]}
JSON
exit 1`)
	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: bin, RollingProgram: legacy, ReplayProgram: "python3 " + replay + " " + source + " {only}"}
	runner := heavyFixture(t, root, steps.JudgeHeavy)
	fixes := 0
	runner.Fix = func(_ context.Context, sha string, _ []HeavyCheck) error {
		if sha != red {
			t.Errorf("red=%s", sha)
		}
		fixes++
		return nil
	}
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fixes != 1 || out.Runs[0].Checks[0].FirstRedLanding != red {
		t.Fatalf("out=%+v fixes=%d", out, fixes)
	}
}
