package instance

import (
	"os"
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
