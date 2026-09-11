package instance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func launchConfig(t *testing.T) Config {
	t.Helper()
	cfg := Config{
		Name:      "survival",
		Kind:      KindServer,
		Directory: t.TempDir(),
	}
	cfg.applyDefaults()
	return cfg
}

func TestCommandLineBuildsTheJarForm(t *testing.T) {
	cfg := launchConfig(t)
	cfg.Jar = "paper.jar"
	cfg.MinMemoryMB, cfg.MaxMemoryMB = 2048, 4096
	cfg.JVMArgs = []string{"-XX:+UseG1GC"}
	cfg.ServerArgs = []string{"--nogui"}

	bin, args, err := cfg.commandLine(true)
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	if bin != "java" {
		t.Errorf("bin = %q", bin)
	}
	if args[0] != "-Xms2048M" || args[1] != "-Xmx4096M" {
		t.Errorf("memory flags = %q", args[:2])
	}
	if at := slices.Index(args, "-jar"); at < 0 || args[at+1] != "paper.jar" {
		t.Errorf("args = %q, want -jar paper.jar", args)
	}
	if args[len(args)-1] != "--nogui" {
		t.Errorf("server args come last, got %q", args)
	}
}

func TestCommandLineBuildsTheArgFileForm(t *testing.T) {
	// Forge and NeoForge from 1.17 have no runnable jar: their launch is a
	// list of @argfiles the installer wrote.
	cfg := launchConfig(t)
	cfg.ArgFiles = []string{"user_jvm_args.txt", "libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt"}
	cfg.ServerArgs = []string{"--nogui"}

	bin, args, err := cfg.commandLine(true)
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	if bin != "java" {
		t.Errorf("bin = %q", bin)
	}
	if slices.Contains(args, "-jar") {
		t.Errorf("args = %q, want no -jar in argfile mode", args)
	}
	first := slices.Index(args, "@user_jvm_args.txt")
	if first < 0 {
		t.Fatalf("args = %q, want the argfiles passed with @", args)
	}
	if args[first+1] != "@libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt" {
		t.Errorf("argfiles lost their order: %q", args)
	}
	if args[len(args)-1] != "--nogui" {
		t.Errorf("server args come last, got %q", args)
	}
}

func TestArgFileModeLeavesTheHeapToTheArgFile(t *testing.T) {
	// An @file is expanded in place and the JVM lets the last -Xmx win, so a
	// -Xmx from the panel would be silently overridden by the one in
	// user_jvm_args.txt. Emitting a flag that loses is worse than emitting
	// none: the panel would report a ceiling the server never had.
	cfg := launchConfig(t)
	cfg.ArgFiles = []string{"user_jvm_args.txt"}
	cfg.MinMemoryMB, cfg.MaxMemoryMB = 2048, 4096

	_, args, err := cfg.commandLine(true)
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-Xmx") || strings.HasPrefix(arg, "-Xms") {
			t.Errorf("args = %q, want no heap flags in argfile mode", args)
		}
	}
}

func TestCommandLineNeedsATarget(t *testing.T) {
	cfg := launchConfig(t)
	if _, _, err := cfg.commandLine(true); err == nil {
		t.Fatal("want an error when neither a jar nor an argfile is configured")
	}
}

func TestConfigRejectsBothJarAndArgFiles(t *testing.T) {
	cfg := launchConfig(t)
	cfg.Jar = "paper.jar"
	cfg.ArgFiles = []string{"user_jvm_args.txt"}

	if err := cfg.validate(); err == nil {
		t.Fatal("want an error: a launch is either a jar or a list of argfiles, never both")
	}
}

func TestConfigRejectsArgFilesOutsideTheInstance(t *testing.T) {
	for _, bad := range []string{"../elsewhere/args.txt", "/etc/args.txt"} {
		cfg := launchConfig(t)
		cfg.ArgFiles = []string{bad}
		if err := cfg.validate(); err == nil {
			t.Errorf("%q was accepted; argfiles are read from inside the instance directory", bad)
		}
	}
}

func TestEffectiveMaxMemoryReadsTheArgFileInArgFileMode(t *testing.T) {
	cfg := launchConfig(t)
	cfg.ArgFiles = []string{"user_jvm_args.txt"}
	cfg.MaxMemoryMB = 4096 // never reaches the JVM, so it is not the answer
	write(t, filepath.Join(cfg.Directory, "user_jvm_args.txt"), "# 注释\n-Xmx6G\n")

	if got := cfg.EffectiveMaxMemoryMB(); got != 6144 {
		t.Errorf("EffectiveMaxMemoryMB = %d, want 6144 from user_jvm_args.txt", got)
	}
}

func TestEffectiveMaxMemoryIsUnknownWithoutAnArgFile(t *testing.T) {
	// Nobody knows the ceiling, and a chart drawing the unused config value as
	// a reference line would be worse than a chart with no line at all.
	cfg := launchConfig(t)
	cfg.ArgFiles = []string{"user_jvm_args.txt"}
	cfg.MaxMemoryMB = 4096

	if got := cfg.EffectiveMaxMemoryMB(); got != 0 {
		t.Errorf("EffectiveMaxMemoryMB = %d, want 0", got)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
