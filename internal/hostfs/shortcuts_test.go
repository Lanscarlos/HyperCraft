package hostfs

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCleanShortcutPathRejectsRelative(t *testing.T) {
	for _, bad := range []string{"", "  ", "servers", "./servers"} {
		if _, err := CleanShortcutPath(bad); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("CleanShortcutPath(%q) = %v, want ErrInvalidPath", bad, err)
		}
	}
	if _, err := CleanShortcutPath("/opt/mc\x00"); !errors.Is(err, ErrInvalidPath) {
		t.Fatal("a null byte in a path has to be refused")
	}
}

func TestCleanShortcutPathCleans(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix path shapes")
	}
	got, err := CleanShortcutPath("/opt/minecraft/../minecraft/survival/")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Clean("/opt/minecraft/survival"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The id has to follow the path and nothing else: renaming a shortcut must not
// move it, and adding the same path twice must collide rather than make a
// second row saying the same thing.
func TestShortcutIDFollowsPathOnly(t *testing.T) {
	a := ShortcutID("/opt/mc")
	if a != ShortcutID("/opt/mc") {
		t.Fatal("the same path has to give the same id")
	}
	if a == ShortcutID("/opt/mc2") {
		t.Fatal("two paths must not share an id")
	}
}

// A custom shortcut keeps its id and its Custom flag through the merge, which
// is what lets the picker offer to rename that one and not the built-ins.
func TestShortcutsKeepsCustomIdentity(t *testing.T) {
	root := string(filepath.Separator)
	mine := filepath.Join(root, "opt", "mc")
	out := Shortcuts([]Shortcut{
		{Label: "面板服务器目录", Path: root},
		{ID: "fav-abc", Label: "我的服务端", Path: mine, Custom: true},
	})

	var found *Shortcut
	for i := range out {
		if out[i].Path == mine {
			found = &out[i]
		}
	}
	if found == nil {
		t.Fatalf("the custom shortcut is missing from %+v", out)
	}
	if found.ID != "fav-abc" || !found.Custom || found.Label != "我的服务端" {
		t.Fatalf("the merge dropped the custom identity: %+v", *found)
	}
	// The built-ins that follow must not come back marked custom.
	for _, entry := range out {
		if entry.Path == root && entry.Custom {
			t.Fatal("a built-in shortcut must not be marked custom")
		}
	}
}
