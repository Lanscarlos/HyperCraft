package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandlersDoNotListInstancesDirectly is the guard that makes instance
// scoping hold across the whole package rather than on the routes somebody
// remembered.
//
// The leak is never in the obvious place. /api/instances/{id}/… is handled once
// by requireInstance; what gets missed is the aggregate — "which servers use
// this Java runtime", "which servers have this plugin out of date" — because
// those read like reporting rather than like access. So mgr.List() is confined
// to the two named helpers in scope.go, and calling either one is a decision
// somebody had to write down.
func TestHandlersDoNotListInstancesDirectly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			// The two helpers are where mgr.List() is allowed to live.
			if name == "scope.go" && (fn.Name.Name == "visibleInstances" || fn.Name.Name == "allInstances") {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "List" {
					return true
				}
				inner, ok := sel.X.(*ast.SelectorExpr)
				if !ok || inner.Sel.Name != "mgr" {
					return true
				}
				t.Errorf("%s: %s calls s.mgr.List() directly. Use visibleInstances(r) for a "+
					"list the caller is shown, or allInstances() for a check that would be "+
					"wrong if it ignored a server the caller cannot see — see scope.go",
					filepath.Base(fset.Position(call.Pos()).String()), fn.Name.Name)
				return true
			})
		}
	}
}

// TestFileRoutesUseTheConfinedBrowser keeps the directory rule from quietly
// not applying to a route.
//
// A handler that builds its own browser reaches the whole instance directory.
// That is correct for the ones under another capability — config editing, the
// launch check, schematic import — and wrong for the file manager, and the two
// look identical at the call site. So serverfiles.New is confined to the two
// named helpers, and picking between browserFor and unconfinedBrowser is a
// decision somebody writes down.
func TestFileRoutesUseTheConfinedBrowser(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if name == "handlers_fs.go" && (fn.Name.Name == "browserFor" || fn.Name.Name == "unconfinedBrowser") {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "New" {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "serverfiles" {
					return true
				}
				t.Errorf("%s: %s calls serverfiles.New directly. Use browserFor(r, inst) for "+
					"the file manager, which applies the caller's directory rule, or "+
					"unconfinedBrowser(dir) for a read that answers to another capability "+
					"— see handlers_fs.go", name, fn.Name.Name)
				return true
			})
		}
	}
}
