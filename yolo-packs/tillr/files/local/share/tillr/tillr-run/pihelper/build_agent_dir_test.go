package pihelper

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBuildAgentDirStripJSONC(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]any
	}{
		{
			name: "line and block comments",
			in: `{
  // leading comment
  "a": 1, /* inline block */
  /* multi
     line */
  "b": "two"
}`,
			want: map[string]any{"a": float64(1), "b": "two"},
		},
		{
			name: "comment markers and escaped quotes inside strings",
			in: `{
  "url": "https://example.com//path/*not-comment*/",
  "escaped": "quote\"//still-in-string",
  // real comment
  "ok": true
}`,
			want: map[string]any{
				"url":     "https://example.com//path/*not-comment*/",
				"escaped": `quote"//still-in-string`,
				"ok":      true,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stripped := stripJSONC(tc.in)
			var got map[string]any
			if err := json.Unmarshal([]byte(stripped), &got); err != nil {
				t.Fatalf("invalid JSON after stripJSONC: %v\n%s", err, stripped)
			}
			for k, wantVal := range tc.want {
				if got[k] != wantVal {
					t.Fatalf("key %q = %#v, want %#v", k, got[k], wantVal)
				}
			}
		})
	}
}

func TestBuildAgentDirCanonicalJSONStability(t *testing.T) {
	input := map[string]any{
		"z": "last",
		"a": map[string]any{
			"y": "<tag>",
			"x": "a&b>c",
		},
		"m": []any{1, "two"},
	}
	got, err := canonicalJSON(input)
	if err != nil {
		t.Fatalf("canonicalJSON failed: %v", err)
	}
	want := `{"a":{"x":"a&b>c","y":"<tag>"},"m":[1,"two"],"z":"last"}`
	if string(got) != want {
		t.Fatalf("canonicalJSON = %s, want %s", got, want)
	}
}

func TestBuildAgentDirFlagValidation(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := RunBuildAgentDir([]string{"--help"}, &out, &errBuf); code != 0 {
		t.Fatalf("expected exit 0 for --help, got %d (stderr=%q)", code, errBuf.String())
	}

	out.Reset()
	errBuf.Reset()
	if code := RunBuildAgentDir([]string{"--unknown-flag"}, &out, &errBuf); code != 2 {
		t.Fatalf("expected exit 2 for unknown flag, got %d", code)
	}

	out.Reset()
	errBuf.Reset()
	if code := RunBuildAgentDir([]string{"positional"}, &out, &errBuf); code != 2 {
		t.Fatalf("expected exit 2 for unexpected positional arg, got %d", code)
	}
}
