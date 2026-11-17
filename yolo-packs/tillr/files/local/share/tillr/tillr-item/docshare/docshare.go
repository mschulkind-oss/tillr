// Package docshare compares document declarations and stored item states without
// importing a writer or registering database migrations.
package docshare

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Document struct {
	Path    string
	Content []byte
}

var Start = regexp.MustCompile(`^<!--\s*tillr:\s*item(?:\s|-->|$)`)

// AttributePattern is the directive attribute grammar, also used by block retirement.
var AttributePattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
var supported = map[string]bool{"id": true, "paths": true, "blocked_by": true, "seat": true, "prose": true, "workflow": true, "claims": true}
var commentID = regexp.MustCompile(`^item-comments-[A-Za-z0-9._-]+-[1-9][0-9]*$`)

func IsCommentItem(id string) bool { return commentID.MatchString(id) }

// Attributes is shared with the full sync parser, including partial malformed
// directives and first-wins duplicate attributes. Notes carry rule and message.
func Attributes(body string) (map[string]string, [][2]string) {
	attrs := map[string]string{}
	notes := [][2]string{}
	for rest := strings.TrimSpace(body); rest != ""; rest = strings.TrimSpace(rest) {
		m := AttributePattern.FindStringSubmatchIndex(rest)
		if m == nil {
			word := rest
			if words := strings.Fields(rest); len(words) > 0 {
				word = words[0]
			}
			notes = append(notes, [2]string{"malformed-directive", fmt.Sprintf("cannot read an attribute at %q: an attribute is name=\"value\"", word)})
			break
		}
		name := rest[m[2]:m[3]]
		value := ""
		for g := 4; g <= 8; g += 2 {
			if m[g] >= 0 {
				value = rest[m[g]:m[g+1]]
				break
			}
		}
		rest = rest[m[1]:]
		if !supported[name] {
			notes = append(notes, [2]string{"unknown-attribute", fmt.Sprintf("attribute %q is not one of id, paths, blocked_by, seat, prose, workflow or claims: a block carries no state, rank or tombstone", name)})
			continue
		}
		if _, twice := attrs[name]; twice {
			notes = append(notes, [2]string{"duplicate-attribute", fmt.Sprintf("attribute %q appears twice", name)})
			continue
		}
		attrs[name] = value
	}
	return attrs, notes
}

type Fence struct {
	char byte
	n    int
}

func (f *Fence) Step(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	run := func(c byte) int {
		i := 0
		for i < len(trimmed) && trimmed[i] == c {
			i++
		}
		return i
	}
	if f.n > 0 {
		if n := run(f.char); n >= f.n && strings.TrimSpace(trimmed[n:]) == "" {
			f.n = 0
		}
		return true
	}
	for _, c := range []byte{'`', '~'} {
		if n := run(c); n >= 3 {
			f.char, f.n = c, n
			return true
		}
	}
	return false
}

func Declared(docs []Document) map[string]bool {
	ids := map[string]bool{}
	for _, doc := range docs {
		lines := strings.Split(strings.ReplaceAll(string(doc.Content), "\r\n", "\n"), "\n")
		fence := Fence{}
		fenced := make([]bool, len(lines))
		for i, line := range lines {
			fenced[i] = fence.Step(line)
		}
		for i := 0; i < len(lines); i++ {
			if fenced[i] || !Start.MatchString(strings.TrimLeft(lines[i], " \t")) {
				continue
			}
			var text strings.Builder
			end := -1
			for j := i; j < len(lines); j++ {
				text.WriteString(lines[j])
				text.WriteByte('\n')
				if strings.Contains(lines[j], "-->") {
					end = j
					break
				}
			}
			if end < 0 {
				break
			}
			body := text.String()
			body = body[strings.Index(body, "<!--")+len("<!--"):]
			body = body[:strings.Index(body, "-->")]
			body = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), "tillr:"))
			body = strings.TrimPrefix(body, "item")
			attrs, _ := Attributes(body)
			if id := strings.TrimSpace(attrs["id"]); id != "" {
				ids[id] = true
			}
			i = end
		}
	}
	return ids
}

// Sharing is the comparison used by sync, planning, and store status. A terminal
// declaration still makes a store document-fed; synthetic comment batches are not residue.
func Sharing(docs []Document, states map[string]string) (fed bool, missing []string) {
	declared := Declared(docs)
	missing = []string{}
	for id, state := range states {
		if declared[id] {
			fed = true
			continue
		}
		if state == "closed" || state == "landed" || state == "resolved-elsewhere" || state == "abandoned" || state == "deleted" || IsCommentItem(id) {
			continue
		}
		missing = append(missing, id)
	}
	sort.Strings(missing)
	return
}

type SubdirectoryError struct{ Root, Prefix string }

func (e SubdirectoryError) Error() string {
	return fmt.Sprintf("--root %s is the subdirectory %s of a repository, not its top level", e.Root, e.Prefix)
}

func ReadTracked(root string) ([]Document, error) {
	probe := exec.Command("git", "-C", root, "rev-parse", "--show-prefix")
	var stderr bytes.Buffer
	probe.Stderr = &stderr
	prefix, err := probe.Output()
	if err != nil {
		return nil, fmt.Errorf("asking git whether %s is a repository: %v: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	if p := strings.TrimSpace(string(prefix)); p != "" {
		return nil, SubdirectoryError{root, p}
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--", "docs")
	stderr.Reset()
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing the tracked documents of %s with git: %v: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	docs := []Document{}
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" || !strings.HasSuffix(name, ".md") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		docs = append(docs, Document{Path: name, Content: content})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

// ReadQueue does not inherit declarations from a containing repository when the
// caller names a standalone directory or a subdirectory instead of a repository root.
func ReadQueue(root string) ([]Document, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return ReadTracked(root)
}
