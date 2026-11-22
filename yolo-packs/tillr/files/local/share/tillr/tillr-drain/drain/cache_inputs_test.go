package drain

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/internal/testinputs"
)

func declareSiblingInputs(t *testing.T) {
	t.Helper()
	if err := testinputs.Track(".."); err != nil {
		t.Fatal(err)
	}
}

func TestSiblingSourcesAreCacheInputs(t *testing.T) {
	declareSiblingInputs(t)
}

// Discover the child builds independently of the input list across all packages of
// tools/tillr-drain. A newly used sibling must not quietly escape the cache inputs.
func siblingBuilds(t *testing.T) map[string]bool {
	t.Helper()
	used := map[string]bool{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != ".." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "sibling" && len(call.Args) == 2 {
					if literal, ok := call.Args[1].(*ast.BasicLit); ok {
						name, _ := strconv.Unquote(literal.Value)
						used["tillr-"+name] = true
					}
				}
			}
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				value, _ := strconv.Unquote(literal.Value)
				for _, prefix := range []string{"../tillr-", "../../tillr-"} {
					if strings.HasPrefix(value, prefix) {
						rest := value[len(prefix)-len("tillr-"):]
						if rest != "tillr-" && !strings.Contains(rest, "/") && rest != "tillr-drain" {
							used[rest] = true
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func missingBuildInput(used map[string]bool, listed []string) error {
	for _, name := range listed {
		delete(used, name)
	}
	for name := range used {
		return fmt.Errorf("sibling program package directory %s is missing from siblings.txt", name)
	}
	return nil
}

func TestSiblingBuildInputsCoverEveryChildProgram(t *testing.T) {
	if err := testinputs.Track(".."); err != nil {
		t.Fatal(err)
	}
	if err := missingBuildInput(siblingBuilds(t), testinputs.Siblings()); err != nil {
		t.Fatal(err)
	}
	for _, name := range testinputs.Siblings() {
		if _, err := os.Stat(filepath.Join("..", "..", name, "main.go")); err != nil {
			t.Fatalf("listed sibling %s has no main package: %v", name, err)
		}
	}
}

func TestMissingSiblingBuildInputIsRejected(t *testing.T) {
	for _, omitted := range testinputs.Siblings() {
		t.Run(omitted, func(t *testing.T) {
			var listed []string
			for _, name := range testinputs.Siblings() {
				if name != omitted {
					listed = append(listed, name)
				}
			}
			if err := missingBuildInput(siblingBuilds(t), listed); err == nil || !strings.Contains(err.Error(), omitted) {
				t.Fatalf("omitting %s did not report the missing package directory: %v", omitted, err)
			}
		})
	}
}
