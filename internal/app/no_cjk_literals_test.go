package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// Person-facing text comes from the UI's locale files, keyed by stable codes
// (docs/conventions.md section 2). A fixed Chinese string in server code shows
// up in every non-Chinese UI, so non-test Go string literals under internal/
// may not contain CJK characters.
//
// cjkLiteralAllowed lists the files that legitimately carry per-locale text.
// Each entry needs a reason; the list only shrinks.
var cjkLiteralAllowed = map[string]string{
	// Per-locale greeting table keyed by locale tag; the text is the product copy.
	"app/onboarding.go": "per-locale onboarding greeting table",
}

func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

func TestNoCJKStringLiteralsInServerCode(t *testing.T) {
	root := ".."
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if _, ok := cjkLiteralAllowed[rel]; ok {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				v = lit.Value
			}
			if hasCJK(v) {
				offenders = append(offenders, fset.Position(lit.Pos()).String())
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("CJK in non-test string literals (use a stable error code with an English message; the UI translates it from ui/src/locales):\n%s", strings.Join(offenders, "\n"))
	}
}
