package plugin

import (
	"strings"
	"testing"
)

// The log excerpts below are the shapes a Paper server actually prints. They
// are here verbatim rather than paraphrased because the patterns are anchored
// on the exact wording, and a paraphrase would test the paraphrase.

func TestMissingDependencyIsNamedWithThePluginsThatAreMissing(t *testing.T) {
	lines := []string{
		"[19:32:01 INFO]: Starting minecraft server version 1.20.4",
		"[19:32:04 ERROR]: Could not load 'plugins/MyEconomy-3.1.0.jar' in folder 'plugins'",
		"org.bukkit.plugin.UnknownDependencyException: Unknown/missing dependency plugins: [Vault, PlaceholderAPI]. Please download and install these plugins to run 'MyEconomy'.",
		"\tat org.bukkit.plugin.SimplePluginManager.loadPlugins(SimplePluginManager.java:243)",
		"[19:32:05 INFO]: Done (8.201s)! For help, type \"help\"",
	}

	failures := ScanFailures(lines)
	if len(failures) != 1 {
		t.Fatalf("expected one failure, got %d: %+v", len(failures), failures)
	}
	failure := failures[0]
	if failure.Kind != FailureDependency {
		t.Errorf("kind = %q, want %q", failure.Kind, FailureDependency)
	}
	if got := strings.Join(failure.Missing, ","); got != "Vault,PlaceholderAPI" {
		t.Errorf("missing = %q", got)
	}
	// Either name has to find the row: the panel's listing may know this
	// plugin by its jar or by what plugin.yml calls it.
	if MatchFailure(failures, "MyEconomy") == nil {
		t.Error("the failure should match the plugin name")
	}
	if MatchFailure(failures, "MyEconomy-3.1.0") == nil {
		t.Error("the failure should match the jar name")
	}
}

func TestApiBreakIsReportedAsAVersionMismatchRatherThanAGenericError(t *testing.T) {
	lines := []string{
		"[19:32:04 ERROR]: Error occurred while enabling OldChat v1.4.2 (Is it up to date?)",
		"java.lang.NoSuchMethodError: 'void org.bukkit.entity.Player.sendMessage(net.kyori.adventure.text.Component)'",
		"\tat com.example.oldchat.Main.onEnable(Main.java:41)",
	}

	failures := ScanFailures(lines)
	if len(failures) != 1 {
		t.Fatalf("expected one failure, got %d", len(failures))
	}
	if failures[0].Kind != FailureIncompatible {
		t.Errorf("kind = %q, want %q", failures[0].Kind, FailureIncompatible)
	}
	if failures[0].Plugin != "OldChat" {
		t.Errorf("plugin = %q", failures[0].Plugin)
	}
	if !strings.Contains(failures[0].Reason, "NoSuchMethodError") {
		t.Errorf("reason should name the exception, got %q", failures[0].Reason)
	}
}

func TestJavaVersionFailureIsItsOwnKind(t *testing.T) {
	lines := []string{
		"[19:32:04 ERROR]: Could not load 'plugins/Modern-2.0.jar' in folder 'plugins'",
		"java.lang.UnsupportedClassVersionError: com/example/modern/Main has been compiled by a more recent version of the Java Runtime (class file version 65.0)",
	}

	failures := ScanFailures(lines)
	if len(failures) != 1 || failures[0].Kind != FailureJava {
		t.Fatalf("expected one java failure, got %+v", failures)
	}
}

// A plugin whose trace runs into the next plugin's must not inherit its cause.
func TestOnePluginsCauseIsNotAttributedToTheNext(t *testing.T) {
	lines := []string{
		"[19:32:04 ERROR]: Could not load 'plugins/First-1.0.jar' in folder 'plugins'",
		"\tat org.bukkit.plugin.java.JavaPluginLoader.loadPlugin(JavaPluginLoader.java:141)",
		"[19:32:04 ERROR]: Could not load 'plugins/Second-1.0.jar' in folder 'plugins'",
		"org.bukkit.plugin.UnknownDependencyException: Unknown/missing dependency plugins: [Vault]. Please download and install these plugins to run 'Second'.",
	}

	failures := ScanFailures(lines)
	if len(failures) != 2 {
		t.Fatalf("expected two failures, got %d: %+v", len(failures), failures)
	}
	first := MatchFailure(failures, "First-1.0")
	if first == nil {
		t.Fatal("First should be reported")
	}
	if first.Kind == FailureDependency {
		t.Errorf("First borrowed Second's cause: %+v", first)
	}
	second := MatchFailure(failures, "Second")
	if second == nil || second.Kind != FailureDependency {
		t.Errorf("Second = %+v", second)
	}
}

// Colour is on by default on a lot of servers, and the escape sequences land
// inside the very words the patterns match on.
func TestColouredOutputIsStillRead(t *testing.T) {
	lines := []string{
		"\x1b[m\x1b[31;1m[19:32:04 ERROR]: Could not load 'plugins/Coloured-1.0.jar' in folder 'plugins'\x1b[m",
		"\x1b[m\x1b[31;1morg.bukkit.plugin.UnknownDependencyException: Unknown/missing dependency plugins: [Vault]. Please download and install these plugins to run 'Coloured'.\x1b[m",
	}

	failures := ScanFailures(lines)
	if len(failures) != 1 || failures[0].Kind != FailureDependency {
		t.Fatalf("coloured output was not read: %+v", failures)
	}
}

// The cost of a false positive is that nobody trusts a red row again, so a
// clean startup — including one with warnings in it — has to come back empty.
func TestACleanStartupReportsNothing(t *testing.T) {
	lines := []string{
		"[19:32:01 INFO]: Starting minecraft server version 1.20.4",
		"[19:32:02 WARN]: The server will make no attempt to authenticate usernames.",
		"[19:32:03 INFO]: [EssentialsX] Enabling EssentialsX v2.20.1",
		"[19:32:03 WARN]: [EssentialsX] Could not find a Vault installation, economy features are off",
		"[19:32:05 INFO]: Done (8.201s)! For help, type \"help\"",
	}

	if failures := ScanFailures(lines); len(failures) != 0 {
		t.Errorf("a clean startup produced %d failures: %+v", len(failures), failures)
	}
}
