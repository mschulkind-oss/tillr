package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestPlainStagingValidatesBeforeWriting(t *testing.T) {
	for _, problem := range []string{"missing-template", "unknown-placeholder", "absolute-working-dir", "escaping-working-dir", "git-layout", "symlink-parent", "git-target"} {
		t.Run(problem, func(t *testing.T) {
			root := scratch(t, "plain-refusal-root")
			lanes := filepath.Join(scratch(t, "plain-refusal-lanes"), "lanes")
			path := fixture(t, fencedItemRecord("finding", "in-queue"))
			d := plannerFixtureDefinition(t)
			d.BriefTemplate = "template.md"
			writeFile(t, filepath.Join(root, d.BriefTemplate), "{{TASK}}\n")
			lane := filepath.Join(lanes, "lane-1")
			spec := drain.StageSpec{LaneID: "lane-1", ItemID: "finding", RunID: "plain-refusal", Backend: "fixture", WorkingDir: "work"}
			want := ""
			switch problem {
			case "missing-template":
				d.BriefTemplate = ""
				want = "brief_template"
			case "unknown-placeholder":
				writeFile(t, filepath.Join(root, d.BriefTemplate), "{{TRUNK}}\n")
				want = "TRUNK"
			case "absolute-working-dir":
				spec.WorkingDir = filepath.Join(root, "not-created")
				want = "working_dir"
			case "escaping-working-dir":
				spec.WorkingDir = "../not-created"
				want = "working_dir"
			case "git-layout":
				if err := os.MkdirAll(lane, 0755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(lane, drain.BaseSHAFile), "old-base\n")
				want = "Git layout"
			case "symlink-parent":
				if err := os.MkdirAll(lane, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(root, filepath.Join(lane, "link")); err != nil {
					t.Fatal(err)
				}
				spec.WorkingDir = "link/not-created"
				want = "non-directory component"
			case "git-target":
				if err := os.MkdirAll(filepath.Join(lane, "work"), 0755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(lane, "work", ".git"), "gitdir: elsewhere\n")
				want = "Git metadata"
			}
			setPlannerDefinition(t, path, d)
			s := &drain.LaneStager{Root: root, LaneRoot: lanes, Store: path, Template: "must-not-read"}
			err := s.Stage(context.Background(), spec)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q refusal: %v", want, err)
			}
			for _, dir := range []string{lane, drain.RunDir(root, spec.RunID)} {
				if _, err := os.Stat(filepath.Join(dir, drain.BriefFile)); !os.IsNotExist(err) {
					t.Fatalf("refusal wrote brief: %v", err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "not-created")); !os.IsNotExist(err) {
				t.Fatalf("refusal wrote outside lane: %v", err)
			}
		})
	}
}
