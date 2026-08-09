package plugin

import (
	"archive/zip"
	"bytes"
	"testing"
)

// jarWith builds an in-memory jar carrying one descriptor file.
func jarWith(t *testing.T, name, body string) (*bytes.Reader, int64) {
	t.Helper()
	data := jarBytes(t, name, body)
	return bytes.NewReader(data), int64(len(data))
}

// jarBytes is the same jar as raw bytes, for tests that write one to disk.
func jarBytes(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	file, err := archive.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := file.Write([]byte(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Something else in the jar, so the descriptor is found rather than assumed.
	if other, err := archive.Create("me/example/Main.class"); err == nil {
		_, _ = other.Write([]byte("not a class really"))
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

func TestReadJarInfoReadsWhatTheServerWouldRead(t *testing.T) {
	reader, size := jarWith(t, "plugin.yml", `name: EssentialsX
version: '2.20.1'   # quoted so YAML keeps it a string
main: com.earth2me.essentials.Essentials
api-version: 1.20
authors: [drtshock, SupaHam]
commands:
  # nested, and none of the panel's business
  essentials:
    description: name is not this
`)

	info, ok := ReadJarInfo(reader, size)
	if !ok {
		t.Fatal("the descriptor should have been read")
	}
	if info.Name != "EssentialsX" || info.Version != "2.20.1" || info.APIVersion != "1.20" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(info.Authors) != 2 || info.Authors[0] != "drtshock" {
		t.Errorf("authors: %v", info.Authors)
	}
	if info.Platform != "bukkit" {
		t.Errorf("platform is %q", info.Platform)
	}
	// The nested "description: name is not this" must not have been read as a
	// top-level key — which is the entire reason nesting is skipped.
	if info.Name != "EssentialsX" {
		t.Errorf("a nested key leaked into the top level: %+v", info)
	}
}

func TestReadJarInfoReadsAuthorLists(t *testing.T) {
	reader, size := jarWith(t, "plugin.yml", "name: Foo\nversion: 1.0\nauthors:\n  - Ada\n  - \"Grace\"\n")
	info, ok := ReadJarInfo(reader, size)
	if !ok || len(info.Authors) != 2 || info.Authors[1] != "Grace" {
		t.Fatalf("unexpected info: %+v ok=%v", info, ok)
	}

	single, singleSize := jarWith(t, "plugin.yml", "name: Foo\nversion: 1.0\nauthor: Ada\n")
	info, _ = ReadJarInfo(single, singleSize)
	if len(info.Authors) != 1 || info.Authors[0] != "Ada" {
		t.Fatalf("unexpected authors: %+v", info)
	}
}

func TestReadJarInfoUnderstandsTheOtherPlatforms(t *testing.T) {
	velocity, size := jarWith(t, "velocity-plugin.json",
		`{"id":"myplugin","name":"My Plugin","version":"3.0.0","authors":["Ada"]}`)
	info, ok := ReadJarInfo(velocity, size)
	if !ok || info.Name != "My Plugin" || info.Version != "3.0.0" || info.Platform != "velocity" {
		t.Fatalf("velocity: %+v ok=%v", info, ok)
	}

	// Fabric allows an author to be an object rather than a name.
	fabric, size := jarWith(t, "fabric.mod.json",
		`{"id":"mymod","version":"1.2.3","authors":["Ada",{"name":"Grace"}]}`)
	info, ok = ReadJarInfo(fabric, size)
	if !ok || info.Name != "mymod" || len(info.Authors) != 2 || info.Authors[1] != "Grace" {
		t.Fatalf("fabric: %+v ok=%v", info, ok)
	}
}

func TestReadJarInfoIgnoresWhatItCannotRead(t *testing.T) {
	if _, ok := ReadJarInfo(bytes.NewReader([]byte("not a zip")), 9); ok {
		t.Error("a file that is not an archive has nothing to say")
	}

	// A jar with no descriptor at the root: a shaded dependency's plugin.yml
	// describes that dependency, not this plugin.
	nested, size := jarWith(t, "lib/plugin.yml", "name: NotThis\nversion: 9\n")
	if _, ok := ReadJarInfo(nested, size); ok {
		t.Error("a nested descriptor should not be read as this jar's own")
	}
}
