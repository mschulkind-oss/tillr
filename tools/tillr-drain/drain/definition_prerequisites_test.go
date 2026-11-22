package drain_test

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/definition"
)

func TestPlannerPrerequisitesAreSelectedIndependently(t *testing.T) {
	for _, tc := range []struct{ name, doc, prerequisite, want string }{
		{"missing document unselected", "docs/missing.md", "", drain.ReasonReady},
		{"missing document selected", "docs/missing.md", definition.PrerequisiteDesignDocument, drain.ReasonDesignDocMissing},
		{"open questions unselected", "docs/open.md", "", drain.ReasonReady},
		{"open questions selected", "docs/open.md", definition.PrerequisiteDesignQuestions, drain.ReasonQuestionOpen},
		{"document check ignores questions", "docs/open.md", definition.PrerequisiteDesignDocument, drain.ReasonReady},
		{"document check ignores unavailable index", "docs/open.md", definition.PrerequisiteDesignDocument, drain.ReasonReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixtureRepo(t, "docs/open.md")
			path := fixture(t, itemRecordWithDoc("ready", "in-queue", tc.doc))
			d, err := definition.GitLanding()
			if err != nil {
				t.Fatal(err)
			}
			d.Prerequisites = nil
			if tc.prerequisite != "" {
				d.Prerequisites = []string{tc.prerequisite}
			}
			setPlannerDefinition(t, path, d)
			index := indexStub(t, map[string]any{"path": "docs/open.md", "questions": []map[string]any{{"id": "OQ-1", "state": "open"}}})
			if tc.name == "document check ignores unavailable index" {
				index = "/does-not-exist/questions-index"
			}
			code, out := invoke(t, "why", "--store", path, "--root", root, "--policy", policy(t, "true"), "--lanes", "lane-1", "--questions-cmd", index, "--json")
			if code != 0 {
				t.Fatalf("why: %d %s", code, out)
			}
			var answer drain.WhyAnswer
			if err := json.Unmarshal(out, &answer); err != nil {
				t.Fatal(err)
			}
			if len(answer.Rows) != 1 || answer.Rows[0].Reason != tc.want {
				t.Fatalf("selected check: %s", out)
			}
		})
	}
}

func TestPlannerFenceCheckIsOptionalUnderLanding(t *testing.T) {
	path := fixture(t, itemRecordFenced("ready", "in-queue", "a", nil))
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Prerequisites = nil
	setPlannerDefinition(t, path, d)
	code, out := invoke(t, "why", "--store", path, "--policy", policy(t, "true"), "--lanes", "lane-1", "--json")
	var answer drain.WhyAnswer
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Rows[0].Decision.Action != drain.ActionDispatch {
		t.Fatalf("unselected fence held: %s", out)
	}
}
