package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// providerClusterFiles is the provider/subagent runtime cluster that must
// depend on the narrow providerHost interface instead of the concrete
// *Service application type. Keeping this boundary is what breaks the old
// Service <-> ProviderRuntime bidirectional reference and keeps the cluster
// extractable into its own package.
var providerClusterFiles = []string{
	"provider_runtime.go",
	"provider_metering.go",
	"provider_vision.go",
	"context_profile.go",
	"subagent_runtime.go",
	"subagent_state.go",
	"subagent_tools.go",
}

// TestProviderClusterDoesNotReferenceService fails when any provider-cluster
// file references the app Service type directly. Host capabilities must be
// added to the providerHost interface (provider_host.go) instead.
func TestProviderClusterDoesNotReferenceService(t *testing.T) {
	for _, sourceFile := range providerClusterFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, sourceFile, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", sourceFile, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			// Qualified references such as session.Service point at other
			// packages, so walk only the X side of selector expressions and
			// skip the Sel identifier; a bare Service identifier is the app type.
			if selector, ok := node.(*ast.SelectorExpr); ok {
				walkSkippingSel(t, fset, sourceFile, selector)
				return false
			}
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "Service" {
				t.Errorf("%s references *Service at %s; extend providerHost instead", sourceFile, fset.Position(ident.Pos()))
			}
			return true
		})
	}
}

func walkSkippingSel(t *testing.T, fset *token.FileSet, sourceFile string, selector *ast.SelectorExpr) {
	t.Helper()
	ast.Inspect(selector.X, func(node ast.Node) bool {
		if nested, ok := node.(*ast.SelectorExpr); ok {
			walkSkippingSel(t, fset, sourceFile, nested)
			return false
		}
		if ident, ok := node.(*ast.Ident); ok && ident.Name == "Service" {
			t.Errorf("%s references *Service at %s; extend providerHost instead", sourceFile, fset.Position(ident.Pos()))
		}
		return true
	})
}
