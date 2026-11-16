package main

import "strings"

// documentFrontmatter is the metadata a design document's frontmatter declares, as the
// item pane's card shows it: the one glance that separates a draft from a ruling
// (design §8.1). Every key is optional because the repository's documents do not all
// carry all five — a document with no `stage:` is a draft, and a card that printed an
// empty stage would be a value the document never stated.
type documentFrontmatter struct {
	Title   string `json:"title,omitempty"`
	Status  string `json:"status,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Next    string `json:"next,omitempty"`
	Updated string `json:"updated,omitempty"`
}

// parseFrontmatter reads the metadata block a document opens with, or answers nil when the
// document opens with no block at all — which is a different answer from a block that
// carries none of these five keys, and the pane renders the two differently.
//
// It is not a YAML parser and does not pretend to be one. It reads the top-level
// `key: value` lines of the block between the first pair of `---` fences, which is the
// shape every design document in this repository writes, and it ignores everything that
// is not one: a nested block (`tags:`, `depends-on:`), a list item, a comment, a blank
// line. A nested block is not a glance, and the card is a glance.
func parseFrontmatter(document string) *documentFrontmatter {
	lines := strings.Split(document, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		// An opening fence with no closing one is a horizontal rule at the top of a
		// document, not a metadata block. Reading the rest of the file for keys would
		// invent a card out of prose.
		return nil
	}
	out := &documentFrontmatter{}
	fields := map[string]*string{
		"title":   &out.Title,
		"status":  &out.Status,
		"stage":   &out.Stage,
		"next":    &out.Next,
		"updated": &out.Updated,
	}
	for _, line := range lines[1:end] {
		key, value, ok := splitFrontmatterLine(line)
		if !ok {
			continue
		}
		if field, wanted := fields[key]; wanted {
			*field = value
		}
	}
	return out
}

// splitFrontmatterLine reads one top-level `key: value` line. An indented line, a list
// item, a comment or a blank is not a top-level key and answers false; so does a key with
// nothing after the colon, because a block is not a scalar and the card shows scalars.
func splitFrontmatterLine(line string) (string, string, bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '-' {
		return "", "", false
	}
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	quoted := len(value) >= 2 &&
		((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\''))
	if quoted {
		// A quoted value is its own text: a `#` inside it is a character, not a comment.
		value = value[1 : len(value)-1]
	} else if comment := strings.Index(value, " #"); comment >= 0 {
		// YAML reads an unquoted ` #` as the start of a comment; so does this, so a
		// document that annotates a value answers the value rather than the annotation.
		value = strings.TrimSpace(value[:comment])
	}
	if value == "" {
		return "", "", false
	}
	return strings.TrimSpace(key), value, true
}
