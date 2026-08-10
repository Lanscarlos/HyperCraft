package hostfs

import (
	"os"
	"path/filepath"
	"testing"
)

// server writes a directory that looks like a Minecraft server someone has
// been running by hand: a couple of jars, a world, some plugins.
func server(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write := func(name string, size int, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := []byte(content)
		if size > 0 {
			body = make([]byte, size)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("paper-1.21.4-232.jar", 40_000_000, "")
	// A plugin jar sitting beside the server jar, which is the case the name
	// ranking exists for: it is a jar in the right directory and it is not the
	// server.
	write("vault.jar", 200_000, "")
	write("server.properties", 0, "motd=A Test Server\nserver-port=25566\nlevel-name=world\nmax-players=40\n")
	write("eula.txt", 0, "#By changing the setting below to TRUE\neula=true\n")
	write("world/level.dat", 0, "not really nbt")
	write("plugins/EssentialsX.jar", 0, "x")
	write("plugins/LuckPerms.jar", 0, "x")
	return dir
}

func TestInspectReadsAnExistingServer(t *testing.T) {
	got, err := Inspect(server(t))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	if !got.Exists || !got.Server {
		t.Fatalf("Exists=%v Server=%v, want a server directory", got.Exists, got.Server)
	}
	if got.Jar != "paper-1.21.4-232.jar" {
		t.Errorf("Jar = %q, want the paper jar rather than the plugin beside it", got.Jar)
	}
	if got.EULA != EULAAccepted {
		t.Errorf("EULA = %q, want accepted", got.EULA)
	}
	if got.Properties == nil {
		t.Fatal("Properties = nil, want server.properties read")
	}
	if got.Properties.Port != "25566" || got.Properties.MaxPlayers != "40" {
		t.Errorf("Properties = %+v, want port 25566 and 40 players", got.Properties)
	}
	if len(got.Worlds) != 1 || got.Worlds[0] != "world" {
		t.Errorf("Worlds = %v, want [world]", got.Worlds)
	}
	if got.Plugins != 2 {
		t.Errorf("Plugins = %d, want 2", got.Plugins)
	}
	if got.Name != filepath.Base(got.Path) {
		t.Errorf("Name = %q, want the directory's own name", got.Name)
	}
}

func TestInspectDoesNotWriteAnything(t *testing.T) {
	dir := server(t)
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(dir); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Errorf("directory has %d entries after an inspection, had %d", len(after), len(before))
	}
	// mcprops.Load parses a missing file as empty; the one thing that must not
	// happen is it being created.
	if _, err := os.Stat(filepath.Join(dir, "server.properties.tmp")); err == nil {
		t.Error("inspection left a temporary file behind")
	}
}

func TestInspectOnAnEmptyDirectoryIsNotAServer(t *testing.T) {
	got, err := Inspect(t.TempDir())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !got.Exists {
		t.Error("Exists = false for a directory that is there")
	}
	if got.Server {
		t.Error("Server = true for an empty directory")
	}
	if got.Jar != "" || got.Properties != nil || got.EULA != EULAMissing {
		t.Errorf("got %+v, want nothing found", got)
	}
}

func TestInspectOnAMissingDirectoryAnswersRatherThanFailing(t *testing.T) {
	got, err := Inspect(filepath.Join(t.TempDir(), "not-there"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Exists {
		t.Error("Exists = true for a path that does not exist")
	}
}

func TestInspectRejectsARelativePath(t *testing.T) {
	if _, err := Inspect("servers/survival"); err == nil {
		t.Fatal("Inspect accepted a relative path")
	}
}

func TestInspectFallsBackToTheLargestJar(t *testing.T) {
	dir := t.TempDir()
	for name, size := range map[string]int{
		"custom-modpack-launcher.jar": 30_000_000,
		"authlib.jar":                 100_000,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got.Jar != "custom-modpack-launcher.jar" {
		t.Errorf("Jar = %q, want the largest when no name is recognised", got.Jar)
	}
}
