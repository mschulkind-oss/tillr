package main

import "testing"

// TestParseFrontmatterReadsTheMetadataCard pins the card the pane draws and, as
// importantly, what it refuses to draw: a nested block is not a glance, an unclosed fence
// is a horizontal rule rather than a metadata block, and a document with no block answers
// nil instead of five blanks.
func TestParseFrontmatterReadsTheMetadataCard(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     *documentFrontmatter
	}{
		{
			name: "the five keys, quoted and bare, with the blocks around them ignored",
			document: "---\n" +
				"title: \"A design worth reading\"\n" +
				"status: in-review\n" +
				"stage: DECIDED\n" +
				"next: \"rule OQ-1, then land\"\n" +
				"updated: 2026-11-13\n" +
				"tags:\n  - tillr\n  - board\n" +
				"depends-on:\n  other: 1\n" +
				"---\n\n# A design\n",
			want: &documentFrontmatter{
				Title:   "A design worth reading",
				Status:  "in-review",
				Stage:   "DECIDED",
				Next:    "rule OQ-1, then land",
				Updated: "2026-11-13",
			},
		},
		{
			name:     "a document that opens with no block at all",
			document: "# A design\n\ntitle: not frontmatter\n",
			want:     nil,
		},
		{
			name:     "an opening fence with no closing one is a rule, not a metadata block",
			document: "---\ntitle: not a card\n\n# A design\n",
			want:     nil,
		},
		{
			name:     "a block carrying none of the five keys",
			document: "---\ntags:\n  - tillr\n---\n\n# A design\n",
			want:     &documentFrontmatter{},
		},
		{
			name:     "a key with nothing after the colon states no value",
			document: "---\ntitle:\nstage: DESIGN\n---\n",
			want:     &documentFrontmatter{Stage: "DESIGN"},
		},
		{
			name:     "a quoted hash is text, an unquoted one starts a comment",
			document: "---\ntitle: \"a # b\"\nstatus: accepted # ruled\n---\n",
			want:     &documentFrontmatter{Title: "a # b", Status: "accepted"},
		},
		{
			name:     "an indented key belongs to a nested block",
			document: "---\n  title: nested\nsingle: 'quoted'\n---\n",
			want:     &documentFrontmatter{},
		},
		{
			name:     "a carriage return is not part of the value",
			document: "---\r\ntitle: A design\r\nstage: DESIGN\r\n---\r\n",
			want:     &documentFrontmatter{Title: "A design", Stage: "DESIGN"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseFrontmatter(c.document)
			if c.want == nil {
				if got != nil {
					t.Fatalf("answered %+v, want nil (no metadata block)", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("answered nil, want %+v", *c.want)
			}
			if *got != *c.want {
				t.Fatalf("answered %+v, want %+v", *got, *c.want)
			}
		})
	}
}
