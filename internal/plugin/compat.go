package plugin

// Will this plugin run on this server?
//
// Two questions, not one. A plugin has to be built for the loader the server
// runs — a Fabric mod in a Paper plugins directory is not a version mismatch,
// it is a file the server will never look at — and it has to support the game
// version. Checking only the game version is the mistake that makes a panel
// confidently offer a Velocity plugin to a survival server.
//
// The third answer matters as much as the two obvious ones: unknown. A source
// that publishes no compatibility metadata is common — every Hangar search
// result before its versions are read, every GitHub release ever — and the
// only safe reading of "it did not say" is that nobody knows. Treating silence
// as compatible is how an operator ends up restarting into a server that will
// not boot, having been told the plugin was fine.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Compatibility verdicts. Three, deliberately: see the file comment.
const (
	CompatOK      = "ok"
	CompatBad     = "bad"
	CompatUnknown = "unknown"
)

// Compat is one compatibility verdict, ready for a badge.
type Compat struct {
	State string `json:"state"`
	// Label is what the badge reads: "兼容 1.20.4", "最高支持 1.16.5",
	// "不支持 Paper", "未知兼容性". Written here rather than in the browser
	// because the reason for the verdict is only fully known at the point the
	// verdict is made.
	Label string `json:"label"`
	// Detail expands on the label for a tooltip or the drawer, where there is
	// room for the whole supported range rather than its top end.
	Detail string `json:"detail,omitempty"`
}

// Target is the server a plugin would be installed into.
//
// Both fields are optional, and an empty one is honest rather than defaulted:
// a panel that assumed "probably Paper, probably the newest version" would
// produce green badges for a server it knows nothing about.
type Target struct {
	// MCVersion is the game version, like "1.20.4".
	MCVersion string `json:"mcVersion,omitempty"`
	// Loader is the server software family: paper, spigot, velocity, fabric,
	// forge. Not the exact fork — Purpur is judged as Paper, because a Paper
	// plugin runs on it.
	Loader string `json:"loader,omitempty"`
	// Source says where the panel learned this, so the UI can explain a target
	// it guessed from a file name differently from one the server itself wrote
	// down. Empty when nothing was found.
	Source string `json:"source,omitempty"`
}

// Known reports whether enough was detected to judge anything.
func (t Target) Known() bool { return t.MCVersion != "" || t.Loader != "" }

// loaderFamilies maps a server's loader onto every loader name a plugin might
// declare that still runs on it.
//
// Paper is the interesting one: it loads Bukkit and Spigot plugins unchanged,
// which is most of why it won, so a plugin that only claims "spigot" is
// compatible with a Paper server and must not be greyed out. The reverse is
// not true and is not encoded — a Spigot server cannot load a Paper-API
// plugin, and pretending otherwise would hide a real failure.
var loaderFamilies = map[string][]string{
	"paper":      {"paper", "spigot", "bukkit", "purpur", "folia"},
	"purpur":     {"purpur", "paper", "spigot", "bukkit"},
	"folia":      {"folia", "paper"},
	"spigot":     {"spigot", "bukkit"},
	"bukkit":     {"bukkit"},
	"velocity":   {"velocity"},
	"bungeecord": {"bungeecord", "waterfall"},
	"waterfall":  {"waterfall", "bungeecord"},
	"fabric":     {"fabric", "quilt"},
	"quilt":      {"quilt", "fabric"},
	"forge":      {"forge"},
	"neoforge":   {"neoforge", "forge"},
}

// loaderFamily is every loader name that runs on this server, lowercased.
// An unrecognised loader is its own family, which is the right answer for a
// fork nobody here has heard of.
func loaderFamily(loader string) []string {
	loader = strings.ToLower(strings.TrimSpace(loader))
	if loader == "" {
		return nil
	}
	if family, ok := loaderFamilies[loader]; ok {
		return family
	}
	return []string{loader}
}

// loaderLabel is how a loader is written in the UI. Nothing depends on it
// beyond the badge text.
func loaderLabel(loader string) string {
	switch strings.ToLower(loader) {
	case "paper":
		return "Paper"
	case "purpur":
		return "Purpur"
	case "folia":
		return "Folia"
	case "spigot":
		return "Spigot"
	case "bukkit":
		return "Bukkit"
	case "velocity":
		return "Velocity"
	case "bungeecord":
		return "BungeeCord"
	case "waterfall":
		return "Waterfall"
	case "fabric":
		return "Fabric"
	case "quilt":
		return "Quilt"
	case "forge":
		return "Forge"
	case "neoforge":
		return "NeoForge"
	default:
		return loader
	}
}

// Judge decides whether something supporting these loaders and game versions
// runs on this target.
func Judge(target Target, loaders, gameVersions []string) Compat {
	if !target.Known() {
		return Compat{
			State:  CompatUnknown,
			Label:  "未知兼容性",
			Detail: "还不知道这台服务器的核心和版本，无法判断兼容性",
		}
	}

	// Loader first. A plugin for the wrong loader is not "an old version" —
	// no version of it will ever load — so saying which end is wrong matters.
	if target.Loader != "" && len(loaders) > 0 {
		if !supportsLoader(target.Loader, loaders) {
			return Compat{
				State:  CompatBad,
				Label:  "不支持 " + loaderLabel(target.Loader),
				Detail: "只支持 " + strings.Join(labelAll(loaders), "、"),
			}
		}
	}

	if target.MCVersion == "" {
		// The loader matched and there is nothing else to check against.
		return Compat{
			State:  CompatUnknown,
			Label:  "未知兼容性",
			Detail: "核心对得上，但还不知道这台服务器的游戏版本",
		}
	}
	if len(gameVersions) == 0 {
		return Compat{
			State:  CompatUnknown,
			Label:  "未知兼容性",
			Detail: "来源没有说明支持哪些游戏版本，装之前请自行确认",
		}
	}

	if supportsVersion(target.MCVersion, gameVersions) {
		return Compat{
			State:  CompatOK,
			Label:  "兼容 " + target.MCVersion,
			Detail: "支持 " + summariseVersions(gameVersions),
		}
	}

	highest := highestVersion(gameVersions)
	if highest == "" {
		return Compat{
			State:  CompatUnknown,
			Label:  "未知兼容性",
			Detail: "来源给出的版本号读不出来：" + summariseVersions(gameVersions),
		}
	}
	if compareVersions(highest, target.MCVersion) < 0 {
		return Compat{
			State:  CompatBad,
			Label:  "最高支持 " + highest,
			Detail: "这台服务器是 " + target.MCVersion + "，支持 " + summariseVersions(gameVersions),
		}
	}
	// Supported versions bracket the target without including it — a plugin
	// that skipped this release, or one that only lists a much newer line.
	return Compat{
		State:  CompatBad,
		Label:  "不支持 " + target.MCVersion,
		Detail: "支持 " + summariseVersions(gameVersions),
	}
}

// AssetClaims is what one jar of an upstream release says it supports.
//
// The registry-side twin of Claims, which does the same job for a jar the
// library already holds, and it exists for the same reason: a release's own
// Loaders field is the union across its assets, so it is true of the release
// and false of every file under it. Judging a velocity build by "paper,
// velocity" is how a proxy jar gets a green badge on a Paper server — and on
// the market page that badge is the last thing an operator sees before the
// download.
//
// The jar's own claim first, its platform next — a build Hangar labelled
// "velocity" and said nothing else about is still known to be a Velocity jar —
// and the release's only as a fallback, which is the honest answer for a
// source that never broke its metadata down per file.
func AssetClaims(release Release, asset Asset) (loaders, gameVersions []string) {
	loaders = asset.Loaders
	if len(loaders) == 0 && asset.Platform != "" {
		loaders = []string{asset.Platform}
	}
	if len(loaders) == 0 {
		loaders = release.Loaders
	}
	gameVersions = asset.GameVersions
	if len(gameVersions) == 0 {
		gameVersions = release.GameVersions
	}
	return loaders, gameVersions
}

// NamedTarget is one server a badge is measured against, carrying the name the
// verdict has to be able to blame.
type NamedTarget struct {
	Name   string
	Target Target
}

// JudgeAcross folds one verdict per selected server into the single badge a
// row has room for.
//
// Nil when nothing was selected, and that is the interesting case: with no
// reference server there is no verdict, and the row must show no badge at all
// rather than a grey 未知兼容性. A column where every cell reads the same thing
// is not information — it is the most prominent position on the row, spent on
// saying nothing. Not answering is the honest form of not knowing.
//
// With servers selected the fold is pessimistic on purpose. 兼容 over a fleet
// has to mean every one of them, because what happens next is one jar copied
// onto all of them; one bad server makes the row bad, and the detail names
// which one so the operator can go and look.
func JudgeAcross(targets []NamedTarget, loaders, gameVersions []string) *Compat {
	if len(targets) == 0 {
		return nil
	}
	if len(targets) == 1 {
		verdict := Judge(targets[0].Target, loaders, gameVersions)
		return &verdict
	}

	var bad, unknown, ok []string
	for _, entry := range targets {
		verdict := Judge(entry.Target, loaders, gameVersions)
		switch verdict.State {
		case CompatBad:
			bad = append(bad, entry.Name+"："+verdict.Label)
		case CompatUnknown:
			unknown = append(unknown, entry.Name+"："+verdict.Label)
		default:
			ok = append(ok, entry.Name)
		}
	}

	switch {
	case len(bad) > 0:
		return &Compat{
			State:  CompatBad,
			Label:  strconv.Itoa(len(bad)) + "/" + strconv.Itoa(len(targets)) + " 台不兼容",
			Detail: strings.Join(bad, "；"),
		}
	case len(unknown) > 0:
		return &Compat{
			State:  CompatUnknown,
			Label:  "未知兼容性",
			Detail: strings.Join(unknown, "；"),
		}
	default:
		return &Compat{
			State:  CompatOK,
			Label:  "兼容全部 " + strconv.Itoa(len(targets)) + " 台",
			Detail: strings.Join(ok, "、") + " 都支持",
		}
	}
}

// PickFor chooses which jar of a release goes onto one server.
//
// The question only has a wrong answer once releases are read correctly: a
// release that ships a paper build and a velocity build is one version, and
// "install 5.5.71 here" has to resolve to a different file on a proxy than on
// a game server. Best fit wins — a jar that fits beats one nothing is known
// about, which beats one that does not fit — and ties go to the earlier jar,
// which is the release's primary.
//
// A jar that does not fit is still returned when none of them do: refusing the
// install would be the panel overruling an operator who can read their own
// server's logs, and the dialogs say so loudly before it gets this far.
func PickFor(version Version, target Target) Artifact {
	if len(version.Artifacts) == 0 {
		return version.Primary()
	}
	best, rank := version.Artifacts[0], -1
	for _, artifact := range version.Artifacts {
		loaders, gameVersions := Claims(version, artifact)
		next := fitRank(Judge(target, loaders, gameVersions).State)
		if rank < 0 || next < rank {
			best, rank = artifact, next
		}
	}
	return best
}

// Offer is a newer version of a plugin that one server can actually take.
type Offer struct {
	Tag     string `json:"tag"`
	Version string `json:"version"`
	// SHA256 and FileName name the jar within that release, because a release
	// is not a file: on this server it is the paper build, on the proxy next to
	// it, the velocity one.
	SHA256   string `json:"sha256,omitempty"`
	FileName string `json:"fileName,omitempty"`
	// Platform is that jar's, for a row that wants to say which build it is
	// offering.
	Platform string `json:"platform,omitempty"`
}

// UpdateFor is the newest release the library holds that this server can take.
//
// Not simply "the newest release the library holds", which is what the plugin
// page used to offer, and which is wrong in a way that only became visible
// once releases were read correctly: LuckPerms publishes a Velocity build and
// a Fabric build under numbers newer than the Bukkit one a Paper server is
// running, and a panel that sorts by date and stops there tells that server to
// update to a jar it cannot load. So a release is only an offer if it holds a
// jar this server could run.
//
// Unknown counts as runnable. Every GitHub release is unknown — nobody
// publishes loader metadata there — and refusing to offer updates for those
// would be the panel withholding what it does know because of what it does
// not. Only a jar the metadata positively rules out is skipped.
//
// Nil when there is nothing newer, when the plugin is pinned, or when
// everything newer is for another platform.
func UpdateFor(item Plugin, current string, target Target) *Offer {
	if item.Policy.Pin != "" {
		return nil
	}
	for _, version := range item.Versions {
		// Versions are newest first, so reaching the installed one means
		// everything above it was for something else.
		if version.Tag == current {
			return nil
		}
		artifact, ok := runnable(version, target)
		if !ok {
			continue
		}
		return &Offer{
			Tag:      version.Tag,
			Version:  version.Version,
			SHA256:   artifact.SHA256,
			FileName: artifact.FileName,
			Platform: artifact.Platform,
		}
	}
	return nil
}

// runnable is the jar of this release this server could load, if any.
func runnable(version Version, target Target) (Artifact, bool) {
	best := PickFor(version, target)
	loaders, gameVersions := Claims(version, best)
	if Judge(target, loaders, gameVersions).State == CompatBad {
		return Artifact{}, false
	}
	return best, true
}

func fitRank(state string) int {
	switch state {
	case CompatOK:
		return 0
	case CompatUnknown:
		return 1
	default:
		return 2
	}
}

func labelAll(loaders []string) []string {
	out := make([]string, 0, len(loaders))
	for _, loader := range loaders {
		out = append(out, loaderLabel(loader))
	}
	return out
}

func supportsLoader(target string, loaders []string) bool {
	family := loaderFamily(target)
	for _, declared := range loaders {
		declared = strings.ToLower(strings.TrimSpace(declared))
		for _, accepted := range family {
			if declared == accepted {
				return true
			}
		}
	}
	return false
}

// supportsVersion matches a game version against a declared list.
//
// The match is on the minor line — 1.20.x — not on the exact patch, and that
// is the whole subtlety of this function. Plugin authors overwhelmingly list
// one entry per line rather than every patch: EssentialsX declares 1.19.4,
// 1.20.6, 1.21.11 and means "1.19, 1.20 and 1.21". Comparing patches exactly
// would tell an operator on 1.20.4 that EssentialsX does not support their
// server, which is both wrong and the single most visible thing this panel
// could get wrong — a badge that cries wolf once is a badge nobody reads again.
//
// The looseness is bounded to the line, so it never spans a real API break: a
// plugin that stopped at 1.16.5 is still reported as stopping there, which is
// the case the yellow badge exists for. Minecraft's compatibility breaks land
// between minor versions, not inside them.
func supportsVersion(target string, declared []string) bool {
	wanted := parseVersion(target)
	if len(wanted) < 2 {
		return false
	}
	for _, entry := range declared {
		got := parseVersion(entry)
		if len(got) < 2 {
			continue
		}
		if got[0] == wanted[0] && got[1] == wanted[1] {
			return true
		}
	}
	return false
}

func highestVersion(declared []string) string {
	best := ""
	var bestParts []int
	for _, entry := range declared {
		parts := parseVersion(entry)
		if len(parts) == 0 {
			continue
		}
		if bestParts == nil || compareParts(parts, bestParts) > 0 {
			best, bestParts = strings.TrimSpace(entry), parts
		}
	}
	return best
}

func compareVersions(a, b string) int {
	return compareParts(parseVersion(a), parseVersion(b))
}

func compareParts(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var left, right int
		if i < len(a) {
			left = a[i]
		}
		if i < len(b) {
			right = b[i]
		}
		if left != right {
			if left < right {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionPattern pulls "1.20.4" out of whatever it is embedded in. Sources
// write game versions as bare numbers, as ranges ("1.20-1.20.4"), and with
// prefixes ("MC 1.20.4"); snapshots and pre-releases have no numeric form and
// are deliberately not matched.
var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

func parseVersion(raw string) []int {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return nil
	}
	out := make([]int, 0, 3)
	for _, part := range match[1:] {
		if part == "" {
			continue
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out = append(out, number)
	}
	return out
}

// summariseVersions compresses a version list to something that fits in a
// tooltip. Plugins routinely declare thirty of them.
func summariseVersions(versions []string) string {
	if len(versions) == 0 {
		return "未知"
	}
	if len(versions) <= 4 {
		return strings.Join(versions, "、")
	}
	lowest, highest := versions[0], versions[0]
	for _, entry := range versions {
		if compareVersions(entry, lowest) < 0 {
			lowest = entry
		}
		if compareVersions(entry, highest) > 0 {
			highest = entry
		}
	}
	return lowest + " – " + highest + "（共 " + strconv.Itoa(len(versions)) + " 个版本）"
}

// ----------------------------------------------------------------- target

// versionHistory is what Paper, Purpur and Folia write into the server
// directory on every boot. It is the only place a running server records which
// game version it actually is, which makes it the one detection that cannot be
// wrong about a renamed jar.
type versionHistory struct {
	CurrentVersion string `json:"currentVersion"`
}

// historyPattern reads "git-Paper-196 (MC: 1.20.4)".
var historyPattern = regexp.MustCompile(`(?i)^(?:git-)?([A-Za-z]+)[-\s]?\S*\s*\(MC:\s*([0-9][0-9.]*)\)`)

// jarPattern reads a core's file name: paper-1.20.4-496.jar, velocity-3.3.0.jar,
// fabric-server-mc.1.20.4-loader.0.15.6.jar.
var jarPattern = regexp.MustCompile(`(?i)^([a-z]+)[-_.]?(?:server[-_.]?)?(?:mc\.)?(\d+\.\d+(?:\.\d+)?)`)

// DetectTarget works out what game version and loader an instance is running.
//
// Three sources, most trustworthy first. version_history.json is written by the
// server itself and names both. The core library's index knows what it
// downloaded, which is right unless the jar was replaced by hand. The jar's
// file name is a guess, and is marked as one — an operator who renamed
// server.jar gets 未知 rather than a confident wrong answer.
func DetectTarget(directory, jarName string, core func(fileName string) (project, version string, ok bool)) Target {
	if target, ok := readVersionHistory(directory); ok {
		return target
	}
	if core != nil && jarName != "" {
		if project, version, ok := core(filepath.Base(jarName)); ok && (project != "" || version != "") {
			return Target{
				MCVersion: version,
				Loader:    normaliseLoader(project),
				Source:    "core-library",
			}
		}
	}
	if jarName != "" {
		if match := jarPattern.FindStringSubmatch(filepath.Base(jarName)); match != nil {
			loader := normaliseLoader(match[1])
			if loader != "" {
				return Target{MCVersion: match[2], Loader: loader, Source: "jar-name"}
			}
		}
	}
	return Target{}
}

func readVersionHistory(directory string) (Target, bool) {
	if directory == "" {
		return Target{}, false
	}
	data, err := os.ReadFile(filepath.Join(directory, "version_history.json"))
	if err != nil {
		return Target{}, false
	}
	var history versionHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return Target{}, false
	}
	match := historyPattern.FindStringSubmatch(strings.TrimSpace(history.CurrentVersion))
	if match == nil {
		return Target{}, false
	}
	loader := normaliseLoader(match[1])
	if loader == "" {
		return Target{}, false
	}
	return Target{MCVersion: match[2], Loader: loader, Source: "version-history"}, true
}

// normaliseLoader maps whatever a jar or an index calls the server onto the
// loader names Judge understands. An unknown word is not a loader.
func normaliseLoader(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "paper", "papermc", "paperspigot":
		return "paper"
	case "purpur":
		return "purpur"
	case "folia":
		return "folia"
	case "spigot":
		return "spigot"
	case "craftbukkit", "bukkit":
		return "bukkit"
	case "velocity":
		return "velocity"
	case "waterfall":
		return "waterfall"
	case "bungeecord", "bungee":
		return "bungeecord"
	case "fabric":
		return "fabric"
	case "quilt":
		return "quilt"
	case "forge":
		return "forge"
	case "neoforge":
		return "neoforge"
	default:
		return ""
	}
}

// TargetDirFor is where a plugin belongs inside an instance running this
// loader. Fabric, Forge and their forks read "mods"; everything else reads
// "plugins".
func TargetDirFor(loader string) string {
	switch strings.ToLower(loader) {
	case "fabric", "quilt", "forge", "neoforge":
		return "mods"
	default:
		return DefaultTargetDir
	}
}
