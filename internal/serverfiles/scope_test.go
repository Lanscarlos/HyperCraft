package serverfiles

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func scoped(t *testing.T, prefixes ...string) *Browser {
	t.Helper()

	dir := t.TempDir()
	for _, name := range []string{
		"server.properties",
		"plugins/MyPlugin/config.yml",
		"plugins/MyPlugin/data/store.db",
		"plugins/Other/config.yml",
		"world/level.dat",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("x: 1\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return New(dir).Restrict(prefixes)
}

func names(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

// The point of the feature: inside the prefix everything works, outside it
// nothing does — and "outside" includes the files the account could read a
// moment ago with no rule set.
func TestRestrictConfinesReadsAndWrites(t *testing.T) {
	b := scoped(t, "plugins/MyPlugin")

	if _, err := b.ReadText("plugins/MyPlugin/config.yml"); err != nil {
		t.Errorf("reading inside the scope failed: %v", err)
	}
	if err := b.WriteText("plugins/MyPlugin/config.yml", "x: 2\n"); err != nil {
		t.Errorf("writing inside the scope failed: %v", err)
	}
	// Deeper is still inside.
	if _, err := b.ReadText("plugins/MyPlugin/data/store.db"); err != nil {
		t.Errorf("reading below the scope root failed: %v", err)
	}

	for _, outside := range []string{
		"server.properties",
		"plugins/Other/config.yml",
		"world/level.dat",
	} {
		if _, err := b.ReadText(outside); err == nil {
			t.Errorf("read %q outside the scope", outside)
		}
		if err := b.WriteText(outside, "pwned"); err == nil {
			t.Errorf("wrote %q outside the scope", outside)
		}
		if err := b.Remove(outside); err == nil {
			t.Errorf("removed %q outside the scope", outside)
		}
	}
}

// A prefix and a name that merely starts with it are different directories.
func TestRestrictDoesNotMatchOnStringPrefixAlone(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"plugins/My/config.yml", "plugins/MyOther/config.yml"} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	b := New(dir).Restrict([]string{"plugins/My"})

	if _, err := b.ReadText("plugins/My/config.yml"); err != nil {
		t.Errorf("reading the scope itself failed: %v", err)
	}
	if _, err := b.ReadText("plugins/MyOther/config.yml"); err == nil {
		t.Error("plugins/MyOther was reachable from a scope of plugins/My")
	}
}

// The file manager has to be able to walk down to its one directory, so the
// ancestors are listable — showing only the way down.
func TestRestrictLeavesAPathDownToTheScope(t *testing.T) {
	b := scoped(t, "plugins/MyPlugin")

	top, err := b.List("/")
	if err != nil {
		t.Fatalf("listing the root failed: %v", err)
	}
	if got := names(top); !slices.Equal(got, []string{"plugins"}) {
		t.Errorf("root listing is %v, want only the way down", got)
	}

	inPlugins, err := b.List("plugins")
	if err != nil {
		t.Fatalf("listing plugins failed: %v", err)
	}
	if got := names(inPlugins); !slices.Equal(got, []string{"MyPlugin"}) {
		t.Errorf("plugins listing is %v, want only the granted one", got)
	}

	// And inside, everything shows.
	inside, err := b.List("plugins/MyPlugin")
	if err != nil {
		t.Fatalf("listing the scope failed: %v", err)
	}
	if got := names(inside); !slices.Equal(got, []string{"data", "config.yml"}) {
		t.Errorf("scope listing is %v, want its real contents", got)
	}

	// A sibling directory is not listable at all.
	if _, err := b.List("world"); err == nil {
		t.Error("a directory outside the scope was listable")
	}
}

// An entry only visible because it leads to the scope is not writable, and the
// listing has to say so — otherwise the UI offers a delete that is refused.
func TestListingMarksEntriesOnTheWayDownAsReadOnly(t *testing.T) {
	b := scoped(t, "plugins/MyPlugin")

	top, err := b.List("/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(top) != 1 || top[0].Name != "plugins" {
		t.Fatalf("root listing is %v", names(top))
	}
	if top[0].Writable {
		t.Error("plugins/ is writable, but it is only on the way to the scope")
	}

	inPlugins, err := b.List("plugins")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !inPlugins[0].Writable {
		t.Error("the scope root itself is not writable")
	}

	// And with no rule, everything is.
	open, err := scoped(t).List("/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, entry := range open {
		if !entry.Writable {
			t.Errorf("%s is not writable on an unrestricted browser", entry.Name)
		}
	}
}

func TestRestrictWithNoPrefixesChangesNothing(t *testing.T) {
	b := scoped(t)

	top, err := b.List("/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := names(top); !slices.Equal(got, []string{"plugins", "world", "server.properties"}) {
		t.Errorf("an unrestricted browser listed %v, want everything at the top level", got)
	}
	if _, err := b.ReadText("server.properties"); err != nil {
		t.Errorf("an unrestricted browser could not read: %v", err)
	}
}

// A rule that cleans down to the whole instance is not a rule. Dropping it
// rather than honouring it keeps "restricted" from silently meaning
// "unrestricted" — and keeps the other prefixes in the list working.
func TestRestrictIgnoresPrefixesThatMeanEverything(t *testing.T) {
	b := scoped(t, "/", ".", "  ", "plugins/MyPlugin")

	if got := b.Scope(); !slices.Equal(got, []string{"plugins/MyPlugin"}) {
		t.Fatalf("scope is %v, want the one real prefix", got)
	}
	if _, err := b.ReadText("server.properties"); err == nil {
		t.Error("a prefix meaning the whole instance was honoured, defeating the rule")
	}
}

// Renaming is two paths, and both have to be inside: renaming a file out of
// the scope is a write outside it, and renaming one in is how you would
// overwrite something you cannot see.
func TestRestrictChecksBothEndsOfARename(t *testing.T) {
	b := scoped(t, "plugins/MyPlugin")

	if err := b.Rename("plugins/MyPlugin/config.yml", "server.properties"); err == nil {
		t.Error("renamed a file out of the scope")
	}
	if err := b.Rename("plugins/Other/config.yml", "plugins/MyPlugin/stolen.yml"); err == nil {
		t.Error("renamed a file into the scope from outside it")
	}
	if err := b.Rename("plugins/MyPlugin/config.yml", "plugins/MyPlugin/renamed.yml"); err != nil {
		t.Errorf("a rename entirely inside the scope failed: %v", err)
	}
}

// TestEveryMethodResolvesThroughScope is what keeps the rule from being
// bypassed by the next method somebody adds.
//
// The confinement works because every path a caller supplies goes through
// resolve or resolveDir. A new method calling clean directly would be
// unrestricted, would pass every test above, and would look exactly like the
// method next to it.
func TestEveryMethodResolvesThroughScope(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "browser.go", nil, 0)
	if err != nil {
		t.Fatalf("parse browser.go: %v", err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "clean" {
				return true
			}
			t.Errorf("%s calls clean() directly. Paths from a caller go through "+
				"b.resolve (or b.resolveDir for the two that walk down to the scope), "+
				"or the directory restriction does not apply to this method — see scope.go",
				fn.Name.Name)
			return true
		})
	}
}
