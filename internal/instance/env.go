package instance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// localeVars are the environment variables that decide a process's character
// encoding, in the order the C library resolves them.
var localeVars = []string{"LC_ALL", "LC_CTYPE", "LANG"}

// fallbackLocale is the one UTF-8 locale that needs no locale data generated
// for it. On a system old enough not to have it, setlocale fails and the JVM
// lands back on ASCII — no worse than doing nothing.
const fallbackLocale = "C.UTF-8"

// launchEnv builds the environment for a server process, forcing a UTF-8
// locale when the panel's own environment does not name one.
//
// This is not a nicety. The JVM decodes file paths with sun.jnu.encoding,
// which comes from the locale, and a service started by systemd or a minimal
// container image usually has no locale at all — so sun.jnu.encoding lands on
// ANSI_X3.4-1968 (plain ASCII). Instance directories are named after the
// instance, and this panel goes out of its way to keep names like 生存服
// intact, so on such a host the server dies at startup with:
//
//	Error: An unexpected error occurred while trying to open file paper.jar
//
// which says nothing whatsoever about locales. An operator with a Chinese
// server name has no way to guess that from the message.
//
// An explicitly chosen non-UTF-8 locale is left alone: zh_CN.GBK encodes those
// names perfectly well, and overriding a deliberate setting is not our call.
// Only "unset", "C" and "POSIX" — the three ways of saying "nobody decided" —
// are replaced.
func launchEnv() []string {
	base := os.Environ()

	effective := ""
	for _, key := range localeVars {
		if value := envValue(base, key); value != "" {
			effective = value
			break
		}
	}
	if !isUnsetLocale(effective) {
		return base
	}

	env := slices.Clone(base)
	// LANG is the weakest of the three, so anything above it that is also
	// unconfigured has to be replaced too, or it would keep winning.
	for _, key := range localeVars {
		if key != "LANG" && envValue(base, key) == "" {
			continue
		}
		env = setEnv(env, key, fallbackLocale)
	}
	return env
}

// withJavaEnv points a child process at the Java the operator picked, for the
// case where the panel is not the one spelling out the path: a start.sh or
// Forge's run.sh calls a bare "java" and gets whatever PATH hands it.
//
// This is why the Java page means anything to a script-launched server. The
// panel can download a JDK 21 and pin an instance to it, and without these two
// variables the script would still start on the host's JDK 8 and fail with a
// class-file-version error that names no Java version anyone would recognise.
//
// Both variables are set, because scripts disagree about which one they read:
// Forge's run.sh takes "java" off PATH, while a hand-written start.sh often
// says "$JAVA_HOME/bin/java". Setting one and not the other would work on half
// the scripts in the wild.
//
// "java" means "whatever the host resolves", so there is nothing to point at
// and the environment is left exactly as it was.
func withJavaEnv(env []string, javaPath string) []string {
	javaPath = strings.TrimSpace(javaPath)
	if javaPath == "" || javaPath == "java" || !filepath.IsAbs(javaPath) {
		return env
	}
	bin := filepath.Dir(javaPath)
	if filepath.Base(bin) != "bin" {
		// Not a JDK layout — someone's wrapper script, or a binary moved out
		// of its tree. Prepending its directory to PATH would shadow the real
		// java with something that is not one.
		return env
	}

	env = setEnv(env, "JAVA_HOME", filepath.Dir(bin))
	// Prepended, not appended: an appended entry loses to the host's own java,
	// which is the one this exists to override.
	if current := envValue(env, "PATH"); current != "" {
		env = setEnv(env, "PATH", bin+string(os.PathListSeparator)+current)
	} else {
		env = setEnv(env, "PATH", bin)
	}
	return env
}

// terminalType is what a server on a pseudo-terminal is told it is talking to.
//
// The other end really is an xterm — xterm.js, driving the same escape
// sequences — so claiming anything less would only make JLine and ncurses
// programs hold back features the browser can render perfectly well. An
// inherited TERM is overridden rather than passed through: it describes the
// panel's own terminal, which nothing here is attached to.
const terminalType = "xterm-256color"

// withTerminalEnv adds the variables that only make sense once the process has
// a terminal. Without TERM, JLine decides it is on a dumb terminal and gives up
// on exactly the completion and line editing this mode exists to provide.
func withTerminalEnv(env []string) []string {
	env = setEnv(env, "TERM", terminalType)
	// jansi and JLine both check COLORTERM before emitting 24-bit colour.
	env = setEnv(env, "COLORTERM", "truecolor")
	return env
}

// isUnsetLocale reports whether a locale value means "nobody chose one".
func isUnsetLocale(value string) bool {
	return value == "" || strings.EqualFold(value, "C") || strings.EqualFold(value, "POSIX")
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
