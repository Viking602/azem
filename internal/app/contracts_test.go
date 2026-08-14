package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestAllActionKindsMatchesDeclaredConstants parses actions.go and verifies
// that AllActionKinds contains exactly the ActionKind constants declared
// there, so the contract list can never silently drift from the source.
func TestAllActionKindsMatchesDeclaredConstants(t *testing.T) {
	listed := make([]string, 0, len(AllActionKinds()))
	for _, kind := range AllActionKinds() {
		listed = append(listed, string(kind))
	}
	assertContractComplete(t, "actions.go", "ActionKind", "AllActionKinds", listed)
}

// TestAllEventKindsMatchesDeclaredConstants pins AllEventKinds to the
// EventKind constants declared in events.go.
func TestAllEventKindsMatchesDeclaredConstants(t *testing.T) {
	listed := make([]string, 0, len(AllEventKinds()))
	for _, kind := range AllEventKinds() {
		listed = append(listed, string(kind))
	}
	assertContractComplete(t, "events.go", "EventKind", "AllEventKinds", listed)
}

func assertContractComplete(t *testing.T, sourceFile, typeName, listName string, listed []string) {
	t.Helper()
	declared := declaredStringConstants(t, sourceFile, typeName)
	if len(declared) == 0 {
		t.Fatalf("no %s constants found in %s", typeName, sourceFile)
	}

	seen := make(map[string]int)
	for _, value := range listed {
		seen[value]++
	}
	for value, count := range seen {
		if count > 1 {
			t.Errorf("%s lists %q %d times", listName, value, count)
		}
		if !declared[value] {
			t.Errorf("%s lists %q which is not declared in %s", listName, value, sourceFile)
		}
	}
	for value := range declared {
		if seen[value] == 0 {
			t.Errorf("%s %q is declared in %s but missing from %s", typeName, value, sourceFile, listName)
		}
	}
}

func declaredStringConstants(t *testing.T, sourceFile, typeName string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, sourceFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", sourceFile, err)
	}

	values := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := valueSpec.Type.(*ast.Ident)
			if !ok || ident.Name != typeName {
				continue
			}
			for _, value := range valueSpec.Values {
				literal, ok := value.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				text, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", literal.Value, err)
				}
				values[text] = true
			}
		}
	}
	return values
}
