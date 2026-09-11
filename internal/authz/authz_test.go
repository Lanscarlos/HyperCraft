package authz

import (
	"os"
	"regexp"
	"testing"
)

func TestCatalogueIsWellFormed(t *testing.T) {
	seen := make(map[Cap]bool, len(catalogue))
	for _, info := range catalogue {
		switch {
		case info.Cap == "":
			t.Error("a catalogue entry has no capability id")
		case seen[info.Cap]:
			t.Errorf("%q appears in the catalogue twice", info.Cap)
		case info.Title == "":
			t.Errorf("%q has no title; the role editor would show a blank row", info.Cap)
		case info.Scope != ScopeInstance && info.Scope != ScopePanel:
			t.Errorf("%q has scope %q, which is neither instance nor panel", info.Cap, info.Scope)
		}
		seen[info.Cap] = true
	}
}

// TestSignedInIsNotGrantable pins the thing that makes CapSignedIn safe: it
// must never be offered in the role editor, and a role that names it must not
// be treated as holding anything.
func TestSignedInIsNotGrantable(t *testing.T) {
	if Valid(CapSignedIn) {
		t.Fatalf("%q is in the catalogue; it would become a grantable capability", CapSignedIn)
	}
	for _, info := range All() {
		if info.Cap == CapSignedIn {
			t.Fatalf("%q is offered by All()", CapSignedIn)
		}
	}
}

func TestValidRejectsUnknown(t *testing.T) {
	for _, cap := range []Cap{"", "instance:View", "panel:root", "instance:files"} {
		if Valid(cap) {
			t.Errorf("Valid(%q) = true, want false", cap)
		}
	}
	if !Valid(CapInstanceView) {
		t.Errorf("Valid(%q) = false, want true", CapInstanceView)
	}
}

// TestAllIsACopy guards against a caller reordering or blanking the vocabulary
// for everyone else — All() hands out something a UI layer will sort.
func TestAllIsACopy(t *testing.T) {
	got := All()
	if len(got) == 0 {
		t.Fatal("All() is empty")
	}
	first := got[0]
	got[0] = Info{}
	if All()[0] != first {
		t.Fatal("All() hands out the catalogue itself, not a copy")
	}
}

// TestFrontendCapabilityIdsExist checks the capability ids the browser gates on
// against the vocabulary they are meant to name.
//
// The front end hides what an account cannot use. A typo in one of those ids
// fails closed — the page is hidden from somebody who is allowed to see it —
// which is safe and completely invisible, so nothing else would catch it. The
// ids live in one file for exactly this reason.
func TestFrontendCapabilityIdsExist(t *testing.T) {
	const source = "../../web/src/useCan.tsx"

	data, err := os.ReadFile(source)
	if err != nil {
		t.Skipf("cannot read %s: %v", source, err)
	}

	// The CAP block's entries, as `name: 'capability:id',`.
	entry := regexp.MustCompile(`(?m)^\s+\w+:\s+'([^']+)',`)
	matches := entry.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no capability ids found in %s; if the CAP block moved, move this test with it", source)
	}

	for _, match := range matches {
		if !Valid(Cap(match[1])) {
			t.Errorf("%s names %q, which is not in the vocabulary", source, match[1])
		}
	}
}
