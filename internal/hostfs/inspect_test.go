package hostfs

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// A Velocity directory imported as a server would be launched with --nogui,
// stopped with a command it does not have, and given a config page about a file
// it does not own. The one signal that survives a renamed jar is its own config
// file, so that is the one checked first.
func TestInspectRecognisesAProxy(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("server.jar", "renamed by hand")
	write("velocity.toml", "bind = \"0.0.0.0:25577\"\n")

	got, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !got.Proxy {
		t.Error("a directory with a velocity.toml was not read as a proxy")
	}

	// And by the jar's name, for a proxy that has never been started and so has
	// no velocity.toml yet.
	fresh := t.TempDir()
	if err := os.WriteFile(filepath.Join(fresh, "velocity-3.4.0-462.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Inspect(fresh); err != nil || !got.Proxy {
		t.Errorf("a velocity jar was not read as a proxy: %+v (%v)", got, err)
	}

	if got, err := Inspect(server(t)); err != nil || got.Proxy {
		t.Errorf("a paper server was read as a proxy: %+v (%v)", got, err)
	}
}

// Forge from 1.17 on installs no runnable jar: the installer leaves a
// libraries tree, a run.sh and user_jvm_args.txt. Every other way the panel
// works out what a directory holds reads a jar name, so without this such a
// directory imports as "not a server" and starts as nothing.
func TestAForgeDirectoryIsRecognisedWithoutAJar(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(
		filepath.Join(dir, "libraries", "net", "minecraftforge", "forge", "1.20.1-47.2.20"), 0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\nexec java @user_jvm_args.txt -jar x\n"), 0o755,
	); err != nil {
		t.Fatal(err)
	}

	out, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if out.Loader != "forge" {
		t.Errorf("loader = %q, want forge", out.Loader)
	}
	if out.GameVersion != "1.20.1" {
		t.Errorf("gameVersion = %q, want the part before the Forge build number", out.GameVersion)
	}
	if out.LaunchScript != "run.sh" {
		t.Errorf("launchScript = %q", out.LaunchScript)
	}
	if !out.Server {
		t.Error("a Forge install was not recognised as a server — it has no jar to go by")
	}
	if out.Jar != "" {
		t.Errorf("jar = %q, want none", out.Jar)
	}
}

// NeoForge versions its own artifacts (21.1.72) and says nothing about the
// game version, so inventing one would be worse than leaving it blank.
func TestNeoForgeIsNamedButItsVersionIsNotGuessedAt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(
		filepath.Join(dir, "libraries", "net", "neoforged", "neoforge", "21.1.72"), 0o755,
	); err != nil {
		t.Fatal(err)
	}

	out, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if out.Loader != "neoforge" {
		t.Errorf("loader = %q, want neoforge", out.Loader)
	}
	if out.GameVersion != "" {
		t.Errorf("gameVersion = %q, want blank rather than a guess", out.GameVersion)
	}
}

// scriptDir writes a directory holding only the named files, which is all the
// candidate search looks at.
func scriptDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\njava -jar s.jar\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLaunchScriptsFindNonEnglishNames(t *testing.T) {
	// The reason this search exists: run.sh is what an installer writes, but a
	// server someone set up by hand is as likely to have 启动.sh next to it.
	got, err := Inspect(scriptDir(t, "启动.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.LaunchScripts, "启动.sh") {
		t.Errorf("LaunchScripts = %q, want 启动.sh among them", got.LaunchScripts)
	}
	if got.LaunchScript != "启动.sh" {
		t.Errorf("LaunchScript = %q", got.LaunchScript)
	}
}

func TestLaunchScriptsAcceptTheCommonSpellings(t *testing.T) {
	for _, name := range []string{
		"run.sh", "start.sh", "startup.sh", "start_server.sh", "launch.sh",
		"server.sh", "开服.sh", "启动服务器.bat", "start.cmd",
	} {
		got, err := Inspect(scriptDir(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(got.LaunchScripts, name) {
			t.Errorf("%s was not offered as a launch script", name)
		}
	}
}

func TestLaunchScriptsSkipTheOnesThatAreNotLaunches(t *testing.T) {
	// Offering one of these is worse than offering nothing: the operator picks
	// it, and the panel parses an installer or a backup job as their server.
	for _, name := range []string{
		"install.sh", "安装.sh", "setup.sh", "update.sh", "更新.sh",
		"stop.sh", "停止.sh", "restart.sh", "backup.sh", "uninstall.sh",
		"server-backup.sh", "启动备份.sh", "eula.txt", "server.properties",
	} {
		got, err := Inspect(scriptDir(t, name))
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(got.LaunchScripts, name) {
			t.Errorf("%s was offered as a launch script", name)
		}
	}
}

func TestRunScriptSortsFirst(t *testing.T) {
	// run.sh is an installer's own artefact, so it outranks whatever else in
	// the directory also looks like a launch.
	got, err := Inspect(scriptDir(t, "start.sh", "启动.sh", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LaunchScripts) != 3 {
		t.Fatalf("LaunchScripts = %q, want all three", got.LaunchScripts)
	}
	if got.LaunchScripts[0] != "run.sh" {
		t.Errorf("LaunchScripts = %q, want run.sh first", got.LaunchScripts)
	}
}

func TestThisPlatformsScriptsComeFirst(t *testing.T) {
	// Both are worth offering — the panel reads them, it no longer runs them —
	// but the one written for this host is the likelier answer.
	got, err := Inspect(scriptDir(t, "run.bat", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	want := "run.sh"
	if runtime.GOOS == "windows" {
		want = "run.bat"
	}
	if got.LaunchScript != want {
		t.Errorf("LaunchScript = %q, want %q on %s", got.LaunchScript, want, runtime.GOOS)
	}
	if len(got.LaunchScripts) != 2 {
		t.Errorf("LaunchScripts = %q, want both offered", got.LaunchScripts)
	}
}

func TestNoLaunchScriptsIsNotAnError(t *testing.T) {
	got, err := Inspect(scriptDir(t, "server.properties"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LaunchScripts) != 0 || got.LaunchScript != "" {
		t.Errorf("LaunchScripts = %q, LaunchScript = %q, want neither", got.LaunchScripts, got.LaunchScript)
	}
}

func TestLaunchScriptsIgnoreDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LaunchScripts) != 0 {
		t.Errorf("LaunchScripts = %q, want none: a directory is not a script", got.LaunchScripts)
	}
}
