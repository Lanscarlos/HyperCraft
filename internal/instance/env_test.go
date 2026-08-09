package instance

import (
	"slices"
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
