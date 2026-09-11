package authz

import "testing"

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
