package instance

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// scripted builds a config still carrying the retired script mode, with the
// script it points at written into the instance directory.
func scripted(t *testing.T, name, body string, command ...string) Config {
	t.Helper()
	cfg := launchConfig(t)
	cfg.ID = "test"
	if name != "" {
		write(t, filepath.Join(cfg.Directory, name), body)
	}
	cfg.Command = command
	return cfg
}

func TestMigrationConvertsAScriptedInstance(t *testing.T) {
	cfg := scripted(t, "启动.sh", "#!/bin/sh\njava -Xms2G -Xmx4G -XX:+UseG1GC -jar paper.jar --nogui\n", "./启动.sh")

	got, changed := migrateRetiredCommand(cfg)
	if !changed {
		t.Fatal("want the config to be migrated")
	}
	if len(got.Command) != 0 {
		t.Errorf("Command = %q, want it retired", got.Command)
	}
	if got.NeedsLaunchSetup {
		t.Error("NeedsLaunchSetup is set on a config that converted cleanly")
	}
	if got.Jar != "paper.jar" || got.MinMemoryMB != 2048 || got.MaxMemoryMB != 4096 {
		t.Errorf("got %+v", got)
	}
	if !slices.Equal(got.JVMArgs, []string{"-XX:+UseG1GC"}) {
		t.Errorf("JVMArgs = %q", got.JVMArgs)
	}
	if !slices.Equal(got.ServerArgs, []string{"--nogui"}) {
		t.Errorf("ServerArgs = %q", got.ServerArgs)
	}
}

func TestMigrationConvertsAForgeScript(t *testing.T) {
	body := "#!/usr/bin/env sh\njava @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt \"$@\"\n"
	cfg := scripted(t, "run.sh", body, "./run.sh")

	got, changed := migrateRetiredCommand(cfg)
	if !changed || got.NeedsLaunchSetup {
		t.Fatalf("want a clean migration, got changed=%v needs=%v", changed, got.NeedsLaunchSetup)
	}
	if got.Jar != "" {
		t.Errorf("Jar = %q, want empty for an argfile launch", got.Jar)
	}
	want := []string{"user_jvm_args.txt", "libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt"}
	if !slices.Equal(got.ArgFiles, want) {
		t.Errorf("ArgFiles = %q", got.ArgFiles)
	}
}

func TestMigrationConvertsAHandTypedCommand(t *testing.T) {
	// Not every script-mode instance points at a script: the launch settings
	// page let people type an argv straight in.
	cfg := scripted(t, "", "", "java", "-Xmx6G", "-jar", "fabric.jar", "--nogui")

	got, changed := migrateRetiredCommand(cfg)
	if !changed || got.NeedsLaunchSetup {
		t.Fatalf("want a clean migration, got changed=%v needs=%v", changed, got.NeedsLaunchSetup)
	}
	if got.Jar != "fabric.jar" || got.MaxMemoryMB != 6144 {
		t.Errorf("got %+v", got)
	}
}

func TestMigrationFlagsWhatItCannotConvert(t *testing.T) {
	// A Bedrock server has no JVM and no launch settings to migrate. Losing
	// what it used to run would leave the operator with nothing to go on, so
	// the old argv is kept where the UI can show it — never to be executed.
	cfg := scripted(t, "", "", "./bedrock_server")

	got, changed := migrateRetiredCommand(cfg)
	if !changed {
		t.Fatal("want the config to be changed: the retired field has to be cleared either way")
	}
	if !got.NeedsLaunchSetup {
		t.Error("NeedsLaunchSetup is not set on a config that could not be converted")
	}
	if !slices.Equal(got.LegacyCommand, []string{"./bedrock_server"}) {
		t.Errorf("LegacyCommand = %q, want the original argv kept for the operator", got.LegacyCommand)
	}
	if len(got.Command) != 0 {
		t.Errorf("Command = %q, want it retired even when the migration failed", got.Command)
	}
}

func TestMigrationWillNotGuessAJavaTheScriptDidNotName(t *testing.T) {
	// $JAVA_HOME is right on the machine that exports it and silently wrong
	// everywhere else, so this is a case for the operator, not a default.
	cfg := scripted(t, "start.sh", "#!/bin/sh\n$JAVA_HOME/bin/java -Xmx4G -jar s.jar\n", "./start.sh")

	got, _ := migrateRetiredCommand(cfg)
	if !got.NeedsLaunchSetup {
		t.Error("want the instance flagged: nobody here knows which JVM it used")
	}
}

func TestMigrationLeavesAJarInstanceAlone(t *testing.T) {
	cfg := launchConfig(t)
	cfg.Jar = "paper.jar"

	got, changed := migrateRetiredCommand(cfg)
	if changed {
		t.Error("a config that never used script mode was rewritten")
	}
	if got.Jar != "paper.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	cfg := scripted(t, "run.sh", "#!/bin/sh\njava -Xmx4G -jar s.jar\n", "./run.sh")

	once, _ := migrateRetiredCommand(cfg)
	twice, changed := migrateRetiredCommand(once)
	if changed {
		t.Error("the second pass changed something; migration has to be a one-off")
	}
	if twice.Jar != "s.jar" {
		t.Errorf("Jar = %q", twice.Jar)
	}
}

func TestAnInstanceNeedingSetupRefusesToStart(t *testing.T) {
	// The failure this prevents: a server that answers the start button with a
	// stack trace about a missing jar, when the real answer is "the panel
	// stopped running your script and nobody has said what to run instead".
	cfg := launchConfig(t)
	cfg.ID = "test"
	cfg.NeedsLaunchSetup = true
	cfg.LegacyCommand = []string{"./start.sh"}

	inst, err := New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = inst.Start()
	if err == nil {
		t.Fatal("want an error, the instance has no launch settings")
	}
	if !strings.Contains(err.Error(), "启动设置") {
		t.Errorf("error = %q, want it to say what the operator has to do", err)
	}
}

func TestStartRejectsAMissingArgFile(t *testing.T) {
	// The jar form checks the jar is there; the argfile form has to check its
	// own target, or a deleted argfile reaches the JVM as a bare "@file" it
	// cannot read.
	cfg := launchConfig(t)
	cfg.ID = "test"
	cfg.ArgFiles = []string{"user_jvm_args.txt"}

	inst, err := New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := inst.Start(); err == nil {
		t.Fatal("want an error naming the missing argfile")
	} else if !strings.Contains(err.Error(), "user_jvm_args.txt") {
		t.Errorf("error = %q, want the missing file named", err)
	}

	// And starts looking for the right thing once it exists.
	write(t, filepath.Join(cfg.Directory, "user_jvm_args.txt"), "-Xmx1G\n")
	if err := inst.Start(); err != nil && strings.Contains(err.Error(), "user_jvm_args.txt") {
		t.Errorf("error = %q, want the argfile check to pass now", err)
	}
	_ = inst.Kill()
	_ = os.Remove(filepath.Join(cfg.Directory, "user_jvm_args.txt"))
}

func TestLoadMigratesScriptedInstancesAndSavesThem(t *testing.T) {
	// The migration has to happen once, at load, and be written back: leaving
	// it in memory would mean every boot re-reads scripts the panel has
	// already stopped using, and would lose the flag on the ones that failed.
	store := &memStore{}
	m := NewManager(store, t.TempDir(), slog.New(slog.DiscardHandler))

	scriptable := scripted(t, "run.sh", "#!/bin/sh\njava -Xmx4G -jar paper.jar --nogui\n", "./run.sh")
	scriptable.ID, scriptable.Name = "a", "生存服"
	stubborn := scripted(t, "", "", "./bedrock_server")
	stubborn.ID, stubborn.Name = "b", "基岩服"

	m.Load([]Config{scriptable, stubborn})

	got := map[string]Config{}
	for _, cfg := range m.Configs() {
		got[cfg.ID] = cfg
	}
	if got["a"].Jar != "paper.jar" || got["a"].MaxMemoryMB != 4096 {
		t.Errorf("a = %+v, want it converted", got["a"])
	}
	if !got["b"].NeedsLaunchSetup {
		t.Error("b was not flagged as needing launch settings")
	}
	for id, cfg := range got {
		if len(cfg.Command) != 0 {
			t.Errorf("%s still carries a command: %q", id, cfg.Command)
		}
	}
	if len(store.saved) != 2 {
		t.Errorf("saved %d configs, want the migration written back", len(store.saved))
	}
}

func TestLoadDoesNotSaveWhenNothingMigrated(t *testing.T) {
	store := &memStore{}
	m := NewManager(store, t.TempDir(), slog.New(slog.DiscardHandler))

	cfg := launchConfig(t)
	cfg.ID, cfg.Jar = "a", "paper.jar"
	m.Load([]Config{cfg})

	if store.saved != nil {
		t.Error("a load with nothing to migrate wrote the instance file anyway")
	}
}

// flagged builds an instance left needing launch settings by the migration.
func flagged(t *testing.T) *Instance {
	t.Helper()
	cfg := launchConfig(t)
	cfg.ID = "test"
	cfg.NeedsLaunchSetup = true
	cfg.LegacyCommand = []string{"./start.sh"}

	inst, err := New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return inst
}

func TestSayingWhatToLaunchClearsTheFlag(t *testing.T) {
	inst := flagged(t)

	next := inst.Config()
	next.Jar = "paper.jar"
	if err := inst.UpdateConfig(next); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	got := inst.Config()
	if got.NeedsLaunchSetup {
		t.Error("the instance has a jar now; the flag should be gone")
	}
	if len(got.LegacyCommand) != 0 {
		t.Errorf("LegacyCommand = %q, want it dropped once it has been acted on", got.LegacyCommand)
	}
}

func TestAnUnrelatedEditDoesNotClearTheFlag(t *testing.T) {
	// Renaming an instance is not the same as saying what it runs. Clearing
	// the flag here would give back the failure it exists to prevent: a start
	// button that produces a stack trace instead of an explanation.
	inst := flagged(t)

	// Built fresh, the way the API builds one from a request body: the flag is
	// server-side state and never arrives in the body at all.
	next := Config{
		Name:      "改个名字",
		Kind:      KindServer,
		Directory: inst.Config().Directory,
	}
	if err := inst.UpdateConfig(next); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	got := inst.Config()
	if !got.NeedsLaunchSetup {
		t.Error("a rename cleared the flag")
	}
	if len(got.LegacyCommand) != 1 {
		t.Errorf("LegacyCommand = %q, want the operator's only record kept", got.LegacyCommand)
	}
}

func TestTheFlagCannotBeClearedByAskingNicely(t *testing.T) {
	// It is derived from the config, never taken from the request: a client
	// that sends needsLaunchSetup=false must not thereby get a server that
	// starts with empty launch settings.
	inst := flagged(t)

	next := inst.Config()
	next.NeedsLaunchSetup = false
	next.LegacyCommand = nil
	if err := inst.UpdateConfig(next); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	if !inst.Config().NeedsLaunchSetup {
		t.Error("the flag was cleared without anything being launched")
	}
}
