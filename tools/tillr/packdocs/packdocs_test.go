package packdocs

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclaredAndUndecidedRows(t *testing.T) {
	index := strings.Join([]string{
		"| Document | Status | Pack |",
		"| :--- | :--- | :--- |",
		"| [Item graphs](../reference/tillr-item-graphs.md) | built | shipped |",
		"| [Seams](../design/autoqa-tillr-seams.md#top) | superseded | not shipped |",
		"| [Gate](../../tools/tillr-gate/README.md) | built | shipped |",
		"| [Unsure](../design/unsure.md) | ? | maybe |",
		"| not a link | built | shipped |",
		"```",
		"| [Specimen](../design/specimen.md) | x | shipped |",
		"```",
	}, "\n")

	got := Declared(index, Index)
	want := []string{"docs/reference/tillr-item-graphs.md", "tools/tillr-gate/README.md"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Declared = %v, want %v", got, want)
	}
	undecided := UndecidedRows(index)
	if len(undecided) != 1 || !strings.HasPrefix(undecided[0], "6: ") {
		t.Fatalf("UndecidedRows = %v, want 1 row at line 6", undecided)
	}
}

func TestDestinationAndSourceOf(t *testing.T) {
	cases := []struct {
		source string
		dest   string
	}{
		{"docs/design/a.md", "docs/design/a.md"},
		{"docs/tillr/design-status.md", "docs/tillr/design-status.md"},
		{"tools/tillr-loop/README.md", "tillr-loop/README.md"},
		{"tools/tillr-view/README.md", "tillr-view/README.md"},
		{"tools/tillr-run/backends/pi/run-pi", "tillr-run/backends/pi/run-pi"},
	}
	for _, tc := range cases {
		if got := Destination(tc.source); got != tc.dest {
			t.Fatalf("Destination(%q) = %q, want %q", tc.source, got, tc.dest)
		}
		if got := SourceOf(DeliveredPrefix + tc.dest); got != tc.source {
			t.Fatalf("SourceOf(%q) = %q, want %q", DeliveredPrefix+tc.dest, got, tc.source)
		}
	}
	for _, outside := range []string{"docs/design/a.md", "tools/tillr-view/README.md", "yolo-packs/matt/README.md"} {
		if got := SourceOf(outside); got != outside {
			t.Fatalf("SourceOf(%q) = %q, want %q", outside, got, outside)
		}
	}
}

func TestTransformAndLiveDirectives(t *testing.T) {
	source := "docs/design/a.md"
	delivered := map[string]bool{
		"docs/design/a.md":            true,
		"docs/design/b.md":            true,
		"docs/tillr/design-status.md": true,
	}
	text := strings.Join([]string{
		"---",
		"depends-on: [b.md]",
		"---",
		"",
		"# A",
		"",
		"See [b](./b.md#2-the-part), [index](../tillr/design-status.md), [gone](./c.md#x),",
		"[stage](../../tools/tillr-drain/drain/stage.go), [loop](../../tools/tillr-loop/README.md),",
		"[here](#local), [web](https://example.com/x.md) and [dir](../../tools/tillr-gate/).",
		"[ref]: ../roadmap.md",
		`<!-- tillr: item id="item-x" paths="docs/" blocked_by="" -->`,
		`   <!-- vantage: question id=OQ-A1 leaning="A" -->`,
		"```text",
		`<!-- tillr: item id="item-specimen" paths="docs/" blocked_by="" -->`,
		"[kept](./c.md)",
		"```",
		"",
	}, "\n")

	out := Transform(text, source, delivered, "0123456789abcdef", "2026-11-16")
	lines := strings.Split(out, "\n")
	if lines[0] != "---" || lines[1] != "depends-on: [b.md]" || lines[2] != "---" {
		t.Fatalf("frontmatter modified: %v", lines[:3])
	}
	if lines[5] != "" || !strings.HasPrefix(lines[6], "> **Delivered copy.** This is `docs/design/a.md`") {
		t.Fatalf("banner missing after first heading: %v", lines[4:8])
	}
	if strings.Count(out, "> **Delivered copy.**") != 1 {
		t.Fatalf("expected exactly 1 banner")
	}
	for _, want := range []string{
		"[b](b.md#2-the-part)",
		"[index](../tillr/design-status.md)",
		"[gone](" + Repository + "docs/design/c.md#x)",
		"[stage](../../tillr-drain/drain/stage.go)",
		"[loop](" + Repository + "tools/tillr-loop/README.md)",
		"[here](#local)",
		"[web](https://example.com/x.md)",
		"[dir](../../tillr-gate/)",
		"[ref]: " + Repository + "docs/roadmap.md",
		`<!-- inert copy of a tillr directive: item id="item-x"`,
		`<!-- tillr: item id="item-specimen"`,
		"[kept](./c.md)",
		`   <!-- vantage: question id=OQ-A1 leaning="A" -->`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Transform output missing %q:\n%s", want, out)
		}
	}
	if got := LiveDirectives(out); len(got) != 0 {
		t.Fatalf("expected no live directives in output, got %v", got)
	}
	if got := LiveDirectives(text); len(got) == 0 {
		t.Fatalf("expected live directive in input")
	}
}

func TestSyncAndSnapshotSchema(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")

	if err := os.MkdirAll(filepath.Join(repo, "docs", "tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs", "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	indexContent := "# Status\n\n| Doc | Pack |\n| :--- | :--- |\n| [A](../design/a.md) | shipped |\n"
	aContent := "# Doc A\n\n<!-- tillr: item id=\"x\" -->\n"
	if err := os.WriteFile(filepath.Join(repo, Index), []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "design", "a.md"), []byte(aContent), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")

	var out, errBuf bytes.Buffer
	code := Sync(repo, "HEAD", &out, &errBuf)
	if code != 0 {
		t.Fatalf("Sync exit = %d, stderr = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "pack-docs: delivered 2 document(s),") {
		t.Fatalf("unexpected stdout: %s", out.String())
	}

	snapPath := filepath.Join(DeliveredRoot(repo), filepath.FromSlash(Snapshot))
	raw, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("snapshot.json missing trailing newline")
	}
	var snap SnapshotData
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Format != "tillr-pack-docs-1" || snap.Index != Index || snap.JailRoot != JailRoot {
		t.Fatalf("unexpected snapshot metadata: %+v", snap)
	}
	if len(SnapshotEntries(snap)) != 2 || len(DeliveredSet(snap)) != 2 {
		t.Fatalf("unexpected snapshot entries: %+v", snap.Documents)
	}
}

func TestMainExitCodes(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Main([]string{"bad"}, &out, &errBuf); code != 2 {
		t.Fatalf("Main(bad) = %d, want 2", code)
	}
	out.Reset()
	errBuf.Reset()
	if code := Main([]string{"sync", "--extra"}, &out, &errBuf); code != 2 {
		t.Fatalf("Main(sync --extra) = %d, want 2", code)
	}
}
