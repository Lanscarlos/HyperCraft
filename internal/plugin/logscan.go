package plugin

// Why a plugin is not running, read out of the server's own output.
//
// This exists because a Minecraft server fails to load a plugin quietly. It
// prints a stack trace between two thousand other startup lines, carries on
// booting, and reports itself healthy — so the operator's first sign that
// their economy plugin never loaded is a player asking why /balance is gone.
// Nothing in the plugins directory records the failure: the jar is present,
// correctly named and enabled, and looks in every way like the ones that
// worked.
//
// So the panel reads the console. The ring buffer already holds every line the
// server printed since it started, which is exactly the window that matters —
// a plugin either loaded during this boot or it did not — and the failure
// modes are few and have been printed in the same words since Bukkit.
//
// Deliberately conservative. A false negative costs the operator what they
// have today: nothing. A false positive puts a red row against a plugin that
// is running fine, and after the second one nobody believes the red rows
// again. So every pattern here anchors on a phrase the server only prints when
// it has genuinely given up on a jar, and anything unrecognised is left alone.

import (
	"path"
	"regexp"
	"strings"
)

// Failure kinds, in the order they are worth telling apart. The kind is what
// decides which action the row offers: a missing dependency can be installed,
// a version mismatch needs a different jar, an unclassified error needs the
// log.
const (
	// FailureDependency is a plugin that needs another plugin that is not there.
	FailureDependency = "dependency"
	// FailureIncompatible is a plugin built against a different API — the
	// NoSuchMethodError and NoClassDefFoundError family, which on a Minecraft
	// server almost always means "this jar is for another game version".
	FailureIncompatible = "incompatible"
	// FailureJava is a jar compiled for a newer Java than the one launching it.
	FailureJava = "java"
	// FailureError is everything else that stopped a plugin from loading.
	FailureError = "error"
)

// Failure is one plugin the server could not load.
type Failure struct {
	// Plugin is what the server called it: the name from plugin.yml where the
	// message had one, otherwise the jar's file name without its extension.
	// Matching against the listing is by both, because which of the two the
	// server prints depends on how far the load got before it failed.
	Plugin string `json:"plugin"`
	// File is the jar named in the message, when it named one.
	File string `json:"file,omitempty"`
	Kind string `json:"kind"`
	// Reason is the row's red line: one sentence, in the panel's language,
	// that says what to do about it.
	Reason string `json:"reason"`
	// Missing are the dependency names to install, for FailureDependency.
	Missing []string `json:"missing,omitempty"`
	// Line is the log line this was read from, for the "查看日志" action and so
	// an operator can check the panel's reading against the real thing.
	Line string `json:"line"`
}

// Patterns, all anchored on wording the server has printed unchanged for years.
var (
	// "Could not load 'plugins/Foo.jar' in folder 'plugins'" — Bukkit, Spigot
	// and Paper all print this, and it is the one line that appears for every
	// kind of load failure.
	couldNotLoad = regexp.MustCompile(`Could not load '([^']*?([^/'\\]+)\.jar)'`)

	// "Error occurred while enabling Foo v1.2.3 (Is it up to date?)" — the jar
	// loaded but its onEnable threw, which is the other half of the failures
	// worth reporting. The plugin is named rather than the file.
	errorEnabling = regexp.MustCompile(`Error occurred while (?:enabling|loading) (.+?) v(\S+) \(Is it up to date\?\)`)

	// "Unknown/missing dependency plugins: [Vault, PlaceholderAPI]. Please
	// download and install these plugins to run 'Foo'."
	missingDeps = regexp.MustCompile(`Unknown/missing dependency plugins?: \[([^\]]*)\]`)
	// The same message names the dependent plugin at its end.
	dependentOf = regexp.MustCompile(`to run '([^']+)'`)

	// Velocity says it its own way.
	velocityFailed = regexp.MustCompile(`Can't load plugin ([^\s:]+)`)

	// A jar built for a newer Java than the one running it. Worth its own kind
	// because the fix is on the 实例设置 page, not in the plugin list.
	unsupportedClass = regexp.MustCompile(`UnsupportedClassVersionError:\s*(\S+)`)
)

// apiBreak are the exception names a server prints when a plugin was compiled
// against a different version of the server API. On a Minecraft server these
// essentially never mean anything else — the classpath is fixed and the plugin
// is the only thing that changed.
var apiBreak = []string{
	"NoSuchMethodError",
	"NoSuchFieldError",
	"NoClassDefFoundError",
	"ClassNotFoundException",
	"IncompatibleClassChangeError",
	"AbstractMethodError",
}

// causeWindow is how many lines after an anchor are read for the reason.
//
// The exception that explains a failure is on the next line or a few below it,
// under "Caused by:". Past that the trace is frames, and reading further only
// risks attributing the *next* plugin's failure to this one.
const causeWindow = 12

// ScanFailures reads a server's console output and reports the plugins it
// could not load.
//
// Lines are expected oldest-first, which is what the ring buffer hands out.
// Only the most recent boot matters, and callers narrow to it by passing the
// lines since the process started; a buffer that spans two boots would
// otherwise keep reporting a failure the operator has already fixed.
func ScanFailures(lines []string) []Failure {
	var out []Failure
	seen := map[string]int{}

	record := func(failure Failure) {
		key := strings.ToLower(failure.Plugin)
		if at, ok := seen[key]; ok {
			// A later, better-classified reading of the same plugin replaces an
			// earlier vague one: the "Could not load" anchor comes first and the
			// cause that explains it comes after.
			if rank(failure.Kind) > rank(out[at].Kind) {
				out[at] = failure
			}
			return
		}
		seen[key] = len(out)
		out = append(out, failure)
	}

	for i, line := range lines {
		clean := stripANSI(line)

		// A dependency message under a "Could not load" line has already been
		// read by that anchor's classify, and recording it again would put two
		// rows on the page for one broken plugin — one under the jar's name and
		// one under the plugin's. Only a message with no anchor above it is
		// handled here, which is how some server versions print it.
		if match := missingDeps.FindStringSubmatch(clean); match != nil {
			if _, anchored := anchorAbove(lines, i); anchored {
				continue
			}
			owner := dependentOf.FindStringSubmatch(clean)
			if owner == nil {
				continue
			}
			names := splitNames(match[1])
			record(Failure{
				Plugin:  owner[1],
				Kind:    FailureDependency,
				Reason:  "缺少前置插件 " + strings.Join(names, "、"),
				Missing: names,
				Line:    clean,
			})
			continue
		}

		if match := couldNotLoad.FindStringSubmatch(clean); match != nil {
			failure := Failure{
				Plugin: match[2],
				File:   match[1],
				Kind:   FailureError,
				Reason: "服务端启动时加载失败",
				Line:   clean,
			}
			classify(&failure, lines, i)
			record(failure)
			continue
		}

		if match := errorEnabling.FindStringSubmatch(clean); match != nil {
			failure := Failure{
				Plugin: strings.TrimSpace(match[1]),
				Kind:   FailureError,
				Reason: "启用时抛出异常（" + match[2] + "）",
				Line:   clean,
			}
			classify(&failure, lines, i)
			record(failure)
			continue
		}

		if match := velocityFailed.FindStringSubmatch(clean); match != nil {
			failure := Failure{
				Plugin: strings.TrimSpace(match[1]),
				Kind:   FailureError,
				Reason: "代理端加载失败",
				Line:   clean,
			}
			classify(&failure, lines, i)
			record(failure)
		}
	}
	return out
}

// rank orders how specific a classification is, so a second reading of the
// same plugin only replaces the first when it says more.
func rank(kind string) int {
	switch kind {
	case FailureDependency:
		return 3
	case FailureJava:
		return 2
	case FailureIncompatible:
		return 2
	default:
		return 1
	}
}

// classify reads the lines under an anchor for the exception that explains it.
func classify(failure *Failure, lines []string, from int) {
	end := min(from+1+causeWindow, len(lines))
	for _, raw := range lines[from+1 : end] {
		line := stripANSI(raw)

		if match := missingDeps.FindStringSubmatch(line); match != nil {
			names := splitNames(match[1])
			failure.Kind = FailureDependency
			failure.Missing = names
			failure.Reason = "缺少前置插件 " + strings.Join(names, "、")
			// This message names the plugin as plugin.yml declares it, which is
			// what the listing shows and rarely what the jar is called. The jar
			// stays in File, and MatchFailure tries both.
			if owner := dependentOf.FindStringSubmatch(line); owner != nil {
				failure.Plugin = owner[1]
			}
			return
		}
		if match := unsupportedClass.FindStringSubmatch(line); match != nil {
			failure.Kind = FailureJava
			failure.Reason = "需要更高版本的 Java（" + trimClass(match[1]) + "）"
			return
		}
		for _, name := range apiBreak {
			if !strings.Contains(line, name) {
				continue
			}
			failure.Kind = FailureIncompatible
			failure.Reason = name + " · 与当前服务端版本不兼容"
			return
		}
		// A second anchor means this plugin's trace is over and the next
		// plugin's failure has started; reading on would attribute the wrong
		// cause to this one.
		if couldNotLoad.MatchString(line) || errorEnabling.MatchString(line) {
			return
		}
	}
}

// anchorAbove reports whether this line is part of a trace that a "Could not
// load" line already opened — in which case that anchor's classify has read
// it, and reading it a second time here would double the row.
//
// Only a short way back, for the same reason classify only reads a short way
// forward: past that, the line above belongs to a different plugin.
func anchorAbove(lines []string, from int) (string, bool) {
	start := max(from-causeWindow, 0)
	for i := from - 1; i >= start; i-- {
		if match := couldNotLoad.FindStringSubmatch(stripANSI(lines[i])); match != nil {
			return match[2], true
		}
	}
	return "", false
}

func splitNames(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// trimClass shortens a fully qualified class name to what it is called, which
// is the part anyone reads.
func trimClass(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

// ansiPattern matches the escape sequences a colourised server prints. They
// land in the middle of the words the patterns above are anchored on, which is
// enough to make every match fail on a server with colour turned on.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(line string) string {
	if !strings.ContainsRune(line, '\x1b') {
		return line
	}
	return ansiPattern.ReplaceAllString(line, "")
}

// MatchFailure finds the failure that belongs to one listed plugin.
//
// Two names to try, because which one the server printed depends on where the
// load failed: the jar's file name if it never got as far as reading
// plugin.yml, and the declared plugin name if it did. Comparing both against
// both is what makes the red row land on the right plugin whichever way it
// broke.
func MatchFailure(failures []Failure, names ...string) *Failure {
	for i := range failures {
		stem := strings.TrimSuffix(path.Base(failures[i].File), ".jar")
		for _, name := range names {
			if name == "" {
				continue
			}
			name = strings.TrimSuffix(name, ".jar")
			if strings.EqualFold(failures[i].Plugin, name) ||
				(stem != "" && strings.EqualFold(stem, name)) {
				return &failures[i]
			}
		}
	}
	return nil
}
