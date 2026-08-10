package instance

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

type memStore struct{ saved []Config }

func (m *memStore) SaveInstances(configs []Config) error {
	m.saved = configs
	return nil
}

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	return NewManager(&memStore{}, root, slog.New(slog.NewTextHandler(io.Discard, nil))), root
}

func TestSlugifyKeepsNonASCIINames(t *testing.T) {
	cases := map[string]string{
		"生存服":         "生存服",
		"My Server":   "My-Server",
		"a/b":         "ab",
		"  spaced  ":  "spaced",
		"..":          "",
		"bad:name?":   "badname",
		"tabs\tmixed": "tabs-mixed",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// A Chinese instance name should get a directory named after it, not a
// generic fallback that makes every server look alike on disk.
func TestCreateUsesTheNameForTheDefaultDirectory(t *testing.T) {
	mgr, root := newTestManager(t)

	inst, err := mgr.Create(Config{Name: "生存服", Jar: "server.jar"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, want := inst.Config().Directory, filepath.Join(root, "生存服"); got != want {
		t.Errorf("directory = %q, want %q", got, want)
	}
}

func TestCreateAvoidsDirectoryCollisions(t *testing.T) {
	mgr, root := newTestManager(t)

	first, err := mgr.Create(Config{Name: "survival"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	second, err := mgr.Create(Config{Name: "survival"})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	if first.Config().Directory == second.Config().Directory {
		t.Fatalf("both instances share %q", first.Config().Directory)
	}
	if got, want := second.Config().Directory, filepath.Join(root, "survival-1"); got != want {
		t.Errorf("second directory = %q, want %q", got, want)
	}
}

func TestCreatePersistsAndListsSorted(t *testing.T) {
	mgr, _ := newTestManager(t)

	for _, name := range []string{"zulu", "alpha", "Mike"} {
		if _, err := mgr.Create(Config{Name: name}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	got := make([]string, 0, 3)
	for _, inst := range mgr.List() {
		got = append(got, inst.Config().Name)
	}
	want := []string{"alpha", "Mike", "zulu"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List() = %v, want %v", got, want)
		}
	}
}
