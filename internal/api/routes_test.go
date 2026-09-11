package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/authz"
)

// TestProtectedRoutesDeclareCapabilities is the test the whole route table
// exists for. A route that reaches the panel without saying what it requires is
// a permission hole that looks like working code, so the table is walked rather
// than reviewed.
func TestProtectedRoutesDeclareCapabilities(t *testing.T) {
	env := newTestEnv(t)

	seen := make(map[string]bool)
	for _, rt := range env.api.protectedRoutes() {
		if rt.handler == nil {
			t.Errorf("%s: no handler", rt.pattern)
		}
		if seen[rt.pattern] {
			t.Errorf("%s: registered twice; the second one would panic at startup", rt.pattern)
		}
		seen[rt.pattern] = true

		method, path, ok := strings.Cut(rt.pattern, " ")
		switch {
		case !ok || method != strings.ToUpper(method):
			t.Errorf("%s: pattern needs a method prefix, e.g. \"GET /api/…\"", rt.pattern)
		case !strings.HasPrefix(path, "/api/"):
			t.Errorf("%s: every protected route lives under /api/", rt.pattern)
		}

		if len(rt.need) == 0 {
			t.Errorf("%s: declares no capability. Name the ones it requires, or "+
				"authz.CapSignedIn if it is open to every signed-in user", rt.pattern)
			continue
		}
		for _, cap := range rt.need {
			if cap == authz.CapSignedIn {
				if len(rt.need) != 1 {
					t.Errorf("%s: CapSignedIn means everybody, so it cannot be combined with %v", rt.pattern, rt.need)
				}
				continue
			}
			if !authz.Valid(cap) {
				t.Errorf("%s: requires %q, which is not in the vocabulary (internal/authz)", rt.pattern, cap)
			}
		}
	}
}

// TestPublicRoutesArePinned makes reaching outside the door a deliberate act.
// "It has to work before login" is a true statement about three routes and a
// tempting one about many more, so the set is written down here and a fourth
// has to be argued for in a diff that touches this test.
func TestPublicRoutesArePinned(t *testing.T) {
	env := newTestEnv(t)

	want := []string{
		"POST /api/auth/login",
		"GET /api/health",
		"POST /api/auth/devices",
	}

	var got []string
	for _, rt := range env.api.publicRoutes() {
		got = append(got, rt.pattern)
		if len(rt.need) != 0 {
			t.Errorf("%s: a public route cannot require a capability", rt.pattern)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("public routes changed\n got: %v\nwant: %v", got, want)
	}
}

// TestEveryCapabilityIsReachable catches the other direction: a capability in
// the vocabulary that no route requires grants nothing, which means either the
// enforcement was forgotten or the capability should not exist.
func TestEveryCapabilityIsReachable(t *testing.T) {
	env := newTestEnv(t)

	// Capabilities whose routes have not been written yet. Empty, and meant to
	// stay that way: it is not a place to park a capability nobody got round to.
	var pending []authz.Cap

	used := make(map[authz.Cap]bool)
	for _, rt := range env.api.protectedRoutes() {
		for _, cap := range rt.need {
			used[cap] = true
		}
	}
	for _, info := range authz.All() {
		if used[info.Cap] || slices.Contains(pending, info.Cap) {
			continue
		}
		t.Errorf("no route requires %q, so granting it does nothing", info.Cap)
	}
}

// TestRoutesRegistersOnlyFromTheTables guards the one thing the tables cannot
// guard themselves against: a route added straight onto the mux inside
// routes(), which would be reachable and invisible to every test above.
//
// It reads the source rather than the mux because net/http gives no way to
// enumerate what a ServeMux holds. Any registration needs a pattern literal, so
// the check is that routes() contains no string constant but the two mounts.
func TestRoutesRegistersOnlyFromTheTables(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}

	fn := findFunc(file, "routes")
	if fn == nil {
		t.Fatal("no routes() in server.go; if it moved, move this test with it")
	}

	// The two mount points the loop hangs its muxes on.
	allowed := []string{"/api/", "/"}
	ast.Inspect(fn, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || slices.Contains(allowed, value) {
			return true
		}
		t.Errorf("routes() contains the literal %q. Routes belong in the tables in "+
			"routes.go, where a capability has to be declared alongside them", value)
		return true
	})
}

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
