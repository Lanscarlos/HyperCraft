package instance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLaunchEnvForcesUTF8WhenNoLocaleIsSet(t *testing.T) {
	// A systemd unit or a slim container image: no locale variables at all.
	// Without this the JVM decodes paths as ASCII and cannot open a jar that
	// lives in a directory called 生存服.
	for _, key := range localeVars {
		t.Setenv(key, "")
	}

	env := launchEnv()
	if got := envValue(env, "LANG"); got != fallbackLocale {
		t.Errorf("LANG = %q, want %q", got, fallbackLocale)
	}
	if got := envValue(env, "LC_ALL"); got != "" {
		t.Errorf("LC_ALL should be left alone, got %q", got)
	}
}

func TestLaunchEnvReplacesTheCLocale(t *testing.T) {
	// LANG=C is "nobody decided", spelled out — and it is still ASCII.
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "C")

	if got := envValue(launchEnv(), "LANG"); got != fallbackLocale {
		t.Errorf("LANG = %q, want %q", got, fallbackLocale)
	}
}

// LC_ALL outranks LANG, so leaving LC_ALL=POSIX in place would undo the fix.
func TestLaunchEnvReplacesAnOverridingPOSIXLocale(t *testing.T) {
	t.Setenv("LC_ALL", "POSIX")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")

	env := launchEnv()
	if got := envValue(env, "LC_ALL"); got != fallbackLocale {
		t.Errorf("LC_ALL = %q, want %q", got, fallbackLocale)
	}
	if got := envValue(env, "LANG"); got != fallbackLocale {
		t.Errorf("LANG = %q, want %q", got, fallbackLocale)
	}
}

func TestLaunchEnvLeavesARealLocaleAlone(t *testing.T) {
	cases := map[string]string{
		"utf-8 locale":     "en_US.UTF-8",
		"lowercase utf8":   "zh_CN.utf8",
		"non-utf8 locale":  "zh_CN.GBK", // encodes Chinese names fine; not ours to change
		"unusual but real": "de_DE@euro",
	}
	for name, locale := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("LC_ALL", "")
			t.Setenv("LC_CTYPE", "")
			t.Setenv("LANG", locale)

			if got := envValue(launchEnv(), "LANG"); got != locale {
				t.Errorf("LANG = %q, want it untouched (%q)", got, locale)
			}
		})
	}
}

func TestLaunchEnvKeepsTheRestOfTheEnvironment(t *testing.T) {
	t.Setenv("LANG", "")
	t.Setenv("HYPERCRAFT_TEST_MARKER", "kept")

	env := launchEnv()
	if !slices.Contains(env, "HYPERCRAFT_TEST_MARKER=kept") {
		t.Errorf("unrelated variables must survive; got %d entries without the marker", len(env))
	}
}

func TestAScriptGetsTheJavaTheOperatorPicked(t *testing.T) {
	// The whole point: run.sh calls a bare "java", so the choice has to
	// arrive as PATH and JAVA_HOME or it does not arrive at all.
	java := filepath.Join("/opt", "jdk-21", "bin", "java")
	env := withJavaEnv([]string{"PATH=/usr/bin:/bin"}, java)

	if got := envValue(env, "JAVA_HOME"); got != filepath.Join("/opt", "jdk-21") {
		t.Errorf("JAVA_HOME = %q, want the JDK root", got)
	}
	path := envValue(env, "PATH")
	if !strings.HasPrefix(path, filepath.Join("/opt", "jdk-21", "bin")+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q, want the chosen JDK first — appended, the host's java wins", path)
	}
	if !strings.HasSuffix(path, "/usr/bin:/bin") {
		t.Errorf("PATH = %q, want the inherited entries kept", path)
	}
}

func TestNothingToPointAtLeavesTheEnvironmentAlone(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	for _, java := range []string{"", "java", "relative/bin/java"} {
		env := withJavaEnv(slices.Clone(base), java)
		if envValue(env, "JAVA_HOME") != "" {
			t.Errorf("java=%q invented a JAVA_HOME", java)
		}
		if got := envValue(env, "PATH"); got != "/usr/bin" {
			t.Errorf("java=%q rewrote PATH to %q", java, got)
		}
	}
}

func TestABinaryOutsideAJDKLayoutIsNotPutOnPATH(t *testing.T) {
	// Prepending this directory would shadow the real java with a wrapper.
	env := withJavaEnv([]string{"PATH=/usr/bin"}, "/home/op/scripts/java")
	if envValue(env, "PATH") != "/usr/bin" || envValue(env, "JAVA_HOME") != "" {
		t.Errorf("a non-JDK path was treated as one: %v", env)
	}
}

func TestTheScriptCanStillOverrideWhatThePanelInjects(t *testing.T) {
	// JAVA_TOOL_OPTIONS is applied before the command line, so the script's
	// own flags win — the same precedence consoleJVMArgs has in jar mode.
	env := withJavaToolOptions([]string{"JAVA_TOOL_OPTIONS=-Dpre=1"}, []string{"-Dfile.encoding=UTF-8"})
	if got := envValue(env, "JAVA_TOOL_OPTIONS"); got != "-Dfile.encoding=UTF-8 -Dpre=1" {
		t.Errorf("JAVA_TOOL_OPTIONS = %q, want ours first and the inherited value kept", got)
	}
	if got := withJavaToolOptions([]string{}, nil); len(got) != 0 {
		t.Errorf("no flags should set no variable, got %v", got)
	}
}
