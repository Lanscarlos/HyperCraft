package api

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestInstanceRequestFieldsAreClassified pins the field split that
// CapInstanceLaunch is checked against.
//
// PUT /api/instances/{id} carries both harmless edits and the three ways to run
// arbitrary code on this machine, so the capability is decided per field. A new
// field that nobody classified would fall through whichever way the check is
// written — silently allowed, or silently refused — and both are worse than
// this test failing.
func TestInstanceRequestFieldsAreClassified(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(instanceRequest{}))
	seen := make(map[string]bool, len(fields))

	for _, field := range fields {
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			t.Errorf("%s has no json tag, so it cannot be classified", field.Name)
			continue
		}
		seen[tag] = true

		inLaunch := slices.Contains(launchFields, tag)
		inSettings := slices.Contains(settingsFields, tag)
		switch {
		case inLaunch && inSettings:
			t.Errorf("%q is in both lists", tag)
		case !inLaunch && !inSettings:
			t.Errorf("%q is in neither launchFields nor settingsFields. Adding a field to "+
				"instanceRequest means answering one question: does setting it decide what "+
				"code the server runs? If yes it belongs in launchFields, which requires "+
				"CapInstanceLaunch; if no, in settingsFields.", tag)
		}
	}

	// And nothing left behind by a field that was removed or renamed.
	for _, tag := range slices.Concat(launchFields, settingsFields) {
		if !seen[tag] {
			t.Errorf("%q is classified but is not a field of instanceRequest any more", tag)
		}
	}
}

// TestArgFilesReachTheConfig covers the other launch target. Forge and
// NeoForge from 1.17 are started from @argfiles rather than a jar, so a
// request that names them has to arrive intact — and, like every other way of
// deciding what runs, behind CapInstanceLaunch.
func TestArgFilesReachTheConfig(t *testing.T) {
	req := instanceRequest{
		Name:      "forge",
		Kind:      "server",
		Directory: "/srv/forge",
		ArgFiles:  []string{"user_jvm_args.txt", "  ", "libraries/unix_args.txt"},
	}

	cfg := req.toConfig()
	want := []string{"user_jvm_args.txt", "libraries/unix_args.txt"}
	if !slices.Equal(cfg.ArgFiles, want) {
		t.Errorf("ArgFiles = %q, want %q with the blank line dropped", cfg.ArgFiles, want)
	}
	if !slices.Contains(launchFields, "argFiles") {
		t.Error("argFiles decides what the JVM runs, so it belongs in launchFields")
	}
}
