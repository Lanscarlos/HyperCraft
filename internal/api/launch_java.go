package api

import (
	"fmt"
	"sort"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// Which Java a Minecraft version needs, as a floor.
//
// These are not predictions. A released version's Java requirement is a fact
// that was settled when it shipped and cannot change afterwards, which is what
// makes a table safe here where a table of, say, plugin compatibility would
// not be. What it must never do is answer for a version it has not heard of:
// the ceiling below is the highest entry, and past that the check says nothing
// rather than inventing a requirement. A wrong "your Java is too old" costs
// more than a missing line, because the reader acts on it.
//
// Each entry is "from this version onwards", newest first.
var javaFloors = []struct {
	from  string
	major int
}{
	{"1.20.5", 21},
	{"1.18", 17},
	{"1.17", 16},
	{"1.0", 8},
}

// The newest version this table is prepared to speak about. Raise it — with
// its floor — when a Minecraft release settles a new requirement, and not
// before.
const javaFloorCeiling = "1.21.99"

// javaFloorFor reports the minimum Java major for a declared game version,
// and whether it knows at all.
func javaFloorFor(gameVersion string) (int, bool) {
	if !plugin.GameVersionReadable(gameVersion) {
		return 0, false
	}
	if plugin.CompareGameVersions(gameVersion, javaFloorCeiling) > 0 {
		return 0, false
	}
	for _, entry := range javaFloors {
		if plugin.CompareGameVersions(gameVersion, entry.from) >= 0 {
			return entry.major, true
		}
	}
	return 0, false
}

// Servers built before this stopped being reliable on a modern JVM: 1.16.5 and
// earlier reach into internals that Java 17 sealed, and the failure is not a
// clean refusal at startup — it boots, then dies in reflection somewhere into
// world load, which is the worst shape a failure can have.
//
// One interval rather than a per-version ceiling, because a ceiling is not a
// fact the way a floor is: whether a given old server survives a given new JVM
// depends on the server implementation and on which plugins are loaded. This
// is the one boundary with a real cause behind it — the rest would be guessing
// with a number attached.
const (
	legacyCeiling   = "1.16.5"
	legacyJavaLimit = 16
)

// javaChoice is one registered runtime, reduced to what this check needs.
type javaChoice struct {
	path  string
	major int
}

// javaVersionIssues reports the selected Java against what the declared game
// version needs.
//
// Silent unless it has both halves: a blank gameVersion is "nobody said",
// which config.go is explicit is not the same as "none", and a major of 0 is a
// runtime the panel could not probe. Guessing from either would put a red line
// on a page for a server that is fine.
func javaVersionIssues(gameVersion string, major int, choices []javaChoice) []launchIssue {
	floor, known := javaFloorFor(gameVersion)
	if !known || major <= 0 {
		return nil
	}

	if major < floor {
		issue := launchIssue{
			Level: launchLevelFatal,
			Code:  "java-version",
			Message: fmt.Sprintf(
				"Minecraft %s 需要 Java %d 或更高，现在选的是 Java %d。服务端会在加载类的时候直接退出。",
				gameVersion, floor, major),
		}
		if pick, ok := bestJava(choices, floor, 0); ok {
			issue.Fix = &launchFix{
				Label: fmt.Sprintf("改用 Java %d", pick.major),
				Patch: map[string]any{"java": pick.path},
			}
		} else {
			issue.Message += fmt.Sprintf("面板里还没有 Java %d，到「资源库 → Java 环境」装一个。", floor)
		}
		return []launchIssue{issue}
	}

	if plugin.CompareGameVersions(gameVersion, legacyCeiling) <= 0 && major > legacyJavaLimit {
		issue := launchIssue{
			Level: launchLevelWarn,
			Code:  "java-version",
			Message: fmt.Sprintf(
				"Minecraft %s 属于 Java %d 出现之前的那一代服务端，跑在 Java %d 上通常能启动，然后在加载世界时死于反射。建议换回 Java %d 或更低。",
				gameVersion, legacyJavaLimit+1, major, legacyJavaLimit),
		}
		if pick, ok := bestJava(choices, floor, legacyJavaLimit); ok {
			issue.Fix = &launchFix{
				Label: fmt.Sprintf("改用 Java %d", pick.major),
				Patch: map[string]any{"java": pick.path},
			}
		}
		return []launchIssue{issue}
	}

	return []launchIssue{{
		Level:   launchLevelOK,
		Code:    "java-version",
		Message: fmt.Sprintf("Java %d 满足 Minecraft %s（要求 %d 或更高）。", major, gameVersion, floor),
	}}
}

// bestJava picks a registered runtime in [floor, ceiling], preferring the
// lowest that qualifies — the conservative choice for a server that is
// unhappy about new JVMs, and no worse for one that only has a floor.
// A ceiling of 0 means no upper bound.
func bestJava(choices []javaChoice, floor, ceiling int) (javaChoice, bool) {
	fits := make([]javaChoice, 0, len(choices))
	for _, one := range choices {
		if one.major < floor {
			continue
		}
		if ceiling > 0 && one.major > ceiling {
			continue
		}
		fits = append(fits, one)
	}
	if len(fits) == 0 {
		return javaChoice{}, false
	}
	sort.Slice(fits, func(a, b int) bool { return fits[a].major < fits[b].major })
	return fits[0], true
}

// javaVersionIssue is the handler-side half: it reads the registry, finds what
// this config's java path resolves to, and asks the question above.
func (s *Server) javaVersionIssue(cfg instance.Config) []launchIssue {
	if s.java == nil {
		return nil
	}
	available, err := javaruntime.AvailableList(s.java.Store(), s.java.Registry())
	if err != nil {
		// The registry being unreadable is a problem for the Java page to
		// report, not a reason to put a launch finding on this one.
		return nil
	}
	choices := make([]javaChoice, 0, len(available))
	major := 0
	for _, entry := range available {
		if entry.Major > 0 && entry.Valid {
			choices = append(choices, javaChoice{path: entry.JavaPath, major: entry.Major})
		}
		if entry.JavaPath == cfg.Java {
			major = entry.Major
		}
	}
	return javaVersionIssues(cfg.GameVersion, major, choices)
}
