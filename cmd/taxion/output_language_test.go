package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestAuthoredCLITextIsEnglish(t *testing.T) {
	hasJapanese := func(text string) bool {
		return strings.ContainsFunc(text, func(r rune) bool {
			return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana)
		})
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			// Existing input aliases remain compatible; authored output stays English.
			if path == "conversation.go" && (text == "終了" || text == "いま止める" || text == "保留" || text == "続行") {
				return true
			}
			if hasJapanese(text) {
				t.Errorf("%s: authored CLI text is not English", positions.Position(literal.Pos()))
			}
			return true
		})
	}
	if hasJapanese(readyPrompt) {
		t.Fatal("embedded ready instructions are not English")
	}
}
