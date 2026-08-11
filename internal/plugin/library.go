package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned for a plugin or version that is not in the library.
	ErrNotFound = errors.New("plugin not found")
	// ErrInvalidID rejects an id that does not name something inside the
	// library directory.
	ErrInvalidID = errors.New("invalid plugin id")
	// ErrExists is returned when adding a plugin that is already tracked.
	ErrExists = errors.New("this plugin is already in the library")
	// ErrInvalidTarget rejects a target directory that is not a plain relative
	// directory inside the instance.
	ErrInvalidTarget = errors.New("invalid target directory")
)

// registryName is the library's index. Unlike the core library, where the jars
// on disk are the source of truth, a plugin is only meaningful together with
// its source and version history — a bare jar in a directory says nothing
// about where updates come from. So the registry owns the list, and the files
// beside it are what the entries point at.
const registryName = "registry.json"

// partSuffix marks a download still in flight. Nothing with this suffix is
// ever listed or installed.
const partSuffix = ".hypercraft-part"

// DefaultTargetDir is where a plugin lands inside an instance. Bukkit, Spigot,
// Paper, Velocity and BungeeCord all read "plugins"; Fabric and Forge read
// "mods", which is why this is a per-plugin field rather than a constant the
// installer hard-codes.
const DefaultTargetDir = "plugins"

// Artifact is one jar held under a release.
//
// A release is not a file. LuckPerms publishes bukkit, velocity, fabric and
// forge builds under one release number; treating each as a version of its own
// is what made a panel report "2 versions, inconsistent" about a plugin that
// had shipped one. So a Version is what upstream published, and the jars under
// it are these.
//
// The primary key is the digest, and it is the only thing here that identifies
// the file. FileName is whatever the author happened to call it — the same jar
// arrives as LuckPerms-Bukkit-5.5.71.jar, luckperms-bukkit.jar and LuckPerms.jar
// depending on the source, and operators rename it again on the way in — so it
// is carried for display and for landing the file on disk, and no decision is
// ever made from it.
//
// PluginName and PluginVer come out of the jar's own descriptor, which is the
// identity the server itself uses: a Bukkit server refuses to load two jars
// declaring the same name, no matter what the files are called. That is why
// they are stored rather than derived on demand — the upgrade sweep in
// instances.go has to know what to delete before it can safely add anything.
type Artifact struct {
	SHA256   string `json:"sha256"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`

	// The descriptor's own account of the jar: plugin.yml for Bukkit and its
	// descendants, velocity-plugin.json, fabric.mod.json. Empty for a jar whose
	// descriptor the panel cannot read — a Forge mod's is TOML — which is
	// honest rather than guessed.
	PluginName string   `json:"pluginName,omitempty"`
	PluginVer  string   `json:"pluginVer,omitempty"`
	Platform   string   `json:"platform,omitempty"`
	APIVersion string   `json:"apiVersion,omitempty"`
	Depend     []string `json:"depend,omitempty"`
	SoftDepend []string `json:"softDepend,omitempty"`
	// Description and Authors are the rest of what the descriptor says, and
	// they are here for a plainer reason than the fields above: the library
	// page could name a plugin and count its versions but could not say what
	// the thing *does*. The registries answer that for a plugin that came from
	// one; a GitHub source and an uploaded jar have no listing to read, and for
	// those the descriptor is the only account of the plugin that exists.
	//
	// Descriptor, not listing. They are separate claims and the panel keeps
	// them apart everywhere else, so it does here too — see DescriptorFacts.
	Description string   `json:"description,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	// Scanned marks a jar the panel has opened and read, whatever it found.
	// Without it there is no way to tell "this jar declares nothing" from "this
	// record predates the panel reading jars at all", and the backfill would
	// re-open every Forge mod in the library on every start.
	Scanned bool `json:"scanned,omitempty"`

	// GameVersions and Loaders are what the *source* said this jar supports,
	// which is a different claim from the descriptor's api-version and is kept
	// apart from it. See Version for why they are stored at download time.
	GameVersions []string  `json:"gameVersions,omitempty"`
	Loaders      []string  `json:"loaders,omitempty"`
	AddedAt      time.Time `json:"addedAt,omitempty"`
}

// Describes reports whether this artifact is the jar with that digest.
func (a Artifact) Describes(sha string) bool {
	return sha != "" && strings.EqualFold(a.SHA256, sha)
}

// applyJarInfo copies a descriptor onto the artifact.
//
// One place rather than a field list repeated at every call site: a download, a
// local import and the backfill all read the same descriptor and every one of
// them used to copy its own subset, which is how the library ended up holding
// dependency lists for downloaded jars and nothing at all for imported ones.
//
// Platform is the exception and is only filled in when the descriptor names
// one, because the caller may know better: a release's asset can carry a
// platform for a jar whose descriptor does not.
func (a *Artifact) applyJarInfo(info JarInfo) {
	a.Scanned = true
	a.PluginName = info.Name
	a.PluginVer = info.Version
	a.APIVersion = info.APIVersion
	a.Depend = info.Depend
	a.SoftDepend = info.SoftDepend
	a.Description = info.Description
	a.Authors = info.Authors
	if info.Platform != "" {
		a.Platform = info.Platform
	}
}

// DescriptorFacts is what a plugin's jars declare about themselves, folded
// across every version the library holds.
//
// Folded rather than taken from the newest, because the newest is not always
// the one that answers: a plugin whose author dropped the description in one
// release still has it in the one before, and an operator asking "what is this"
// is asking about the plugin rather than about a build of it. Newest first
// wherever there is a choice, since a description that was rewritten was
// rewritten for a reason.
type DescriptorFacts struct {
	// Name is what the jars call themselves, which is the name the *server*
	// files the plugin under and is regularly not what the source calls it —
	// the EssentialsX repository ships a jar declaring "Essentials".
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	APIVersion  string   `json:"apiVersion,omitempty"`
	Depend      []string `json:"depend,omitempty"`
	SoftDepend  []string `json:"softDepend,omitempty"`
	// Scanned is false when no jar of this plugin has ever been read — an
	// empty descriptor block then means "not looked at yet" rather than "this
	// jar declares nothing", and the page says so instead of showing a gap.
	Scanned bool `json:"scanned,omitempty"`
}

// Descriptor folds a plugin's jars into one account of what it is.
func (p Plugin) Descriptor() DescriptorFacts {
	var out DescriptorFacts
	depend, soft := map[string]bool{}, map[string]bool{}

	// Newest first: Versions is held newest first, so the first jar that says
	// anything is the most recent one that does.
	for _, version := range p.Versions {
		for _, artifact := range version.Artifacts {
			if artifact.Scanned {
				out.Scanned = true
			}
			if out.Name == "" {
				out.Name = artifact.PluginName
			}
			if out.Description == "" {
				out.Description = artifact.Description
			}
			if len(out.Authors) == 0 {
				out.Authors = artifact.Authors
			}
			if out.APIVersion == "" {
				out.APIVersion = artifact.APIVersion
			}
			for _, name := range artifact.Depend {
				depend[name] = true
			}
			for _, name := range artifact.SoftDepend {
				soft[name] = true
			}
		}
	}

	// A hard dependency outranks a soft one: a plugin that made an optional
	// dependency required is a plugin whose server will not start without it,
	// and listing that name under 软前置 would be the friendlier of two answers
	// and the wrong one.
	for name := range depend {
		delete(soft, name)
	}
	out.Depend, out.SoftDepend = sortedKeys(depend), sortedKeys(soft)
	return out
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Version is one release of a plugin, and the jars held under it.
type Version struct {
	// Tag is the GitHub tag, and the id of this version everywhere else. It is
	// what the source names, so it is what a re-download can be matched against.
	Tag     string `json:"tag"`
	Version string `json:"version"`
	// Artifacts are the jars held under this release, primary first. Never
	// empty for a version the library actually holds.
	Artifacts []Artifact `json:"artifacts,omitempty"`

	// The four fields below mirror the primary artifact. They are what every
	// registry written before the artifact list said, so they are still read
	// on load and still written out — a panel rolled back to an older build
	// finds its library intact rather than empty.
	FileName    string    `json:"fileName"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	Prerelease  bool      `json:"prerelease"`
	Notes       string    `json:"notes,omitempty"`
	PublishedAt time.Time `json:"publishedAt"`
	AddedAt     time.Time `json:"addedAt"`
	// GameVersions and Loaders are what the source said this jar supports,
	// copied out of the release at download time.
	//
	// Stored rather than re-read: this is what the installed-plugins page
	// judges each row's compatibility against, and it has to be able to say
	// "this jar is for 1.16.5 and the server is 1.20.4" for a server that is
	// offline, on a panel with no network, about a registry that has since
	// stopped listing the version. Empty for a GitHub release, which publishes
	// neither — and empty means unknown, not compatible. See Judge.
	GameVersions []string `json:"gameVersions,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
}

// normalise fills the artifact list from the legacy fields, and the legacy
// fields from the artifact list, so both halves of the record agree whichever
// one it was written by.
func (v Version) normalise() Version {
	if len(v.Artifacts) == 0 && v.FileName != "" {
		v.Artifacts = []Artifact{{
			SHA256:       v.SHA256,
			FileName:     v.FileName,
			Size:         v.Size,
			GameVersions: v.GameVersions,
			Loaders:      v.Loaders,
			AddedAt:      v.AddedAt,
		}}
	}
	if len(v.Artifacts) > 0 {
		primary := v.Artifacts[0]
		v.FileName, v.Size, v.SHA256 = primary.FileName, primary.Size, primary.SHA256
		if len(v.GameVersions) == 0 {
			v.GameVersions = primary.GameVersions
		}
		if len(v.Loaders) == 0 {
			v.Loaders = primary.Loaders
		}
	}
	return v
}

// Primary is the jar an install picks when nobody said which. The first one
// downloaded, which for a plugin with an asset pattern is the one the pattern
// chose and for everything else is the only one there is.
func (v Version) Primary() Artifact {
	if len(v.Artifacts) == 0 {
		return Artifact{FileName: v.FileName, Size: v.Size, SHA256: v.SHA256}
	}
	return v.Artifacts[0]
}

// ArtifactKey identifies one jar to the browser: its digest, which is its
// identity, falling back to the file name for a record written before there
// were digests.
func ArtifactKey(a Artifact) string {
	if a.SHA256 != "" {
		return a.SHA256
	}
	return a.FileName
}

// Claims is what one jar says it supports, for the compatibility check.
//
// The jar's own claim first and the release's only as a fallback, because on a
// release that ships a build per platform the release's claim is the union of
// all of them — true of the release, false of every file under it. Judging a
// velocity jar by "paper, velocity" is how a proxy build ends up on a Paper
// server with a green badge on it.
func Claims(version Version, artifact Artifact) (loaders, gameVersions []string) {
	loaders = artifact.Loaders
	if len(loaders) == 0 && artifact.Platform != "" {
		// What the jar's own descriptor declared, which beats anything a
		// registry said about it.
		loaders = []string{artifact.Platform}
	}
	if len(loaders) == 0 {
		loaders = version.Loaders
	}
	gameVersions = artifact.GameVersions
	if len(gameVersions) == 0 {
		gameVersions = version.GameVersions
	}
	return loaders, gameVersions
}

// Artifact returns the jar with a digest, or nil.
func (v Version) Artifact(sha string) *Artifact {
	for i := range v.Artifacts {
		if v.Artifacts[i].Describes(sha) {
			return &v.Artifacts[i]
		}
	}
	return nil
}

// TotalSize is what this release costs on disk, across every jar under it.
func (v Version) TotalSize() int64 {
	var total int64
	for _, artifact := range v.Artifacts {
		total += artifact.Size
	}
	return total
}

// UpdateMode is what the panel does on its own when a new release appears.
type UpdateMode string

const (
	// UpdateManual is the default: nothing happens until somebody clicks.
	UpdateManual UpdateMode = ""
	// UpdateNotify checks on a schedule and says so, and stops there.
	UpdateNotify UpdateMode = "notify"
	// UpdateFetch downloads new releases into the library. Nothing on any
	// server changes; the jar is simply there when you decide.
	UpdateFetch UpdateMode = "fetch"
	// UpdatePush also copies it to every instance that has this plugin, on
	// their next restart. The only mode that touches a running fleet.
	UpdatePush UpdateMode = "push"
)

// Policy is how one plugin is kept: what the panel may do without being asked,
// which version it is pinned to, and how much history to hold on to.
type Policy struct {
	Update UpdateMode `json:"update,omitempty"`
	// Pin locks the plugin to one release tag. A pinned plugin never reports
	// an update and is never touched by a bulk upgrade — the escape hatch for
	// "5.4.2 is the last build that works with our fork".
	Pin string `json:"pin,omitempty"`
	// Keep is how many releases to hold in the library, newest first. Zero
	// means all of them. A version any instance is running is never pruned:
	// see §8 — the reason to keep old jars is that a rollback needs them.
	Keep int `json:"keep,omitempty"`
	// AllowSelfUpdate silences the hash-mismatch alarm for this plugin.
	//
	// Some plugins genuinely rewrite their own jar — anticheats that pull
	// signature updates, Geyser's self-updating builds — and reporting that as
	// tampering every single time trains the operator to ignore the one time
	// it is not. Drift is still recorded and still shown on the plugin's own
	// page; it just stops counting as something wrong.
	AllowSelfUpdate bool `json:"allowSelfUpdate,omitempty"`
}

// Plugin is one tracked plugin: where it comes from, and which of its versions
// the panel has downloaded.
type Plugin struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Note is the operator's own description. Nothing reads it.
	Note      string    `json:"note,omitempty"`
	Source    Source    `json:"source"`
	TargetDir string    `json:"targetDir"`
	AddedAt   time.Time `json:"addedAt"`
	// IconURL is the artwork the registry publishes for this plugin, kept so
	// the library page can show the same face 获取插件 showed. Recorded when
	// the plugin is tracked rather than fetched per page load: it is a URL on
	// somebody else's CDN and looking it up again would mean a registry call
	// per row to learn something that does not change.
	IconURL string `json:"iconUrl,omitempty"`

	// Versions are the downloads held in the library, newest release first.
	Versions []Version `json:"versions"`

	// Policy is what the panel may do with this plugin unasked. Zero value is
	// "nothing, ever", which is the right default for a file that decides
	// whether somebody's server starts.
	Policy Policy `json:"policy,omitempty"`

	// Latest is what the last update check found upstream, and CheckedAt is
	// when it looked. Both are cached rather than fetched per page load: the
	// anonymous GitHub API allows 60 calls an hour, which a panel with twenty
	// plugins would burn through in three refreshes.
	Latest     *Release  `json:"latest,omitempty"`
	CheckedAt  time.Time `json:"checkedAt,omitempty"`
	CheckError string    `json:"checkError,omitempty"`
}

// HasVersion reports whether a tag is among the downloaded versions.
func (p Plugin) HasVersion(tag string) bool {
	return p.Version(tag) != nil
}

// Version returns one downloaded version by tag, or nil.
func (p Plugin) Version(tag string) *Version {
	for i := range p.Versions {
		if p.Versions[i].Tag == tag {
			return &p.Versions[i]
		}
	}
	return nil
}

// FindArtifact locates one jar anywhere in a plugin's history, by digest.
//
// This is the lookup the reconciliation runs: given a file sitting in somebody's
// plugins directory, is it one of ours, and if so which release. Nothing else
// would do — a file name match would claim a renamed jar is a different one, and
// claim a different jar with a familiar name is ours.
func (p Plugin) FindArtifact(sha string) (Version, Artifact, bool) {
	for _, version := range p.Versions {
		if artifact := version.Artifact(sha); artifact != nil {
			return version, *artifact, true
		}
	}
	return Version{}, Artifact{}, false
}

// DeclaredNames are the plugin.yml names this plugin's jars declare, lowercased.
//
// Used by the upgrade sweep, which has to delete every jar in the directory
// declaring the name the new one declares — see instances.go. Usually one name;
// more than one when a plugin renamed itself between releases, and in that case
// all of them have to go or the server loads both.
func (p Plugin) DeclaredNames() []string {
	seen := map[string]bool{}
	out := make([]string, 0, 2)
	for _, version := range p.Versions {
		for _, artifact := range version.Artifacts {
			name := strings.ToLower(strings.TrimSpace(artifact.PluginName))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// UpdateAvailable reports whether the last check found a release newer than
// everything downloaded.
//
// "Newer" is by tag identity, not by version arithmetic: plugin authors number
// releases in every scheme there is, and the only claim the panel can honestly
// make is "upstream's newest is not one you have".
// A pinned plugin never reports one: it is pinned because somebody decided the
// newer releases are wrong for this fleet, and a permanent "有更新" badge on
// that decision is a badge that teaches people to stop reading badges.
func (p Plugin) UpdateAvailable() bool {
	return p.Policy.Pin == "" && p.Latest != nil && !p.HasVersion(p.Latest.Tag)
}

// Library owns the plugin directory, normally <data>/plugins.
type Library struct {
	root string

	// mu serialises registry reads and writes. Downloads run one at a time and
	// each writes its own version directory, so the files need no lock of
	// their own.
	mu       sync.Mutex
	registry map[string]Plugin
	loaded   bool
}

func NewLibrary(root string) *Library { return &Library{root: root} }

// Root is the directory plugins are stored in.
func (l *Library) Root() string { return l.root }

// List returns every tracked plugin, newest addition first.
func (l *Library) List() []Plugin {
	l.mu.Lock()
	defer l.mu.Unlock()

	registry := l.load()
	out := make([]Plugin, 0, len(registry))
	for _, item := range registry {
		out = append(out, l.describe(item))
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].AddedAt.Equal(out[b].AddedAt) {
			return out[a].AddedAt.After(out[b].AddedAt)
		}
		return out[a].ID < out[b].ID
	})
	return out
}

// Get returns one plugin by id.
func (l *Library) Get(id string) (Plugin, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(id)
}

func (l *Library) get(id string) (Plugin, error) {
	if err := validID(id); err != nil {
		return Plugin{}, err
	}
	registry := l.load()
	item, ok := registry[id]
	if !ok {
		return Plugin{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return l.describe(item), nil
}

// describe drops jars that have gone missing, and any release left holding
// none. The registry is the list of what the panel tracks, but it cannot
// install a file that is not there, and an entry that fails on click is worse
// than one that is honestly absent.
func (l *Library) describe(item Plugin) Plugin {
	kept := make([]Version, 0, len(item.Versions))
	for _, version := range item.Versions {
		version = version.normalise()
		artifacts := make([]Artifact, 0, len(version.Artifacts))
		for _, artifact := range version.Artifacts {
			info, err := os.Stat(l.versionFile(item.ID, version.Tag, artifact.FileName))
			if err != nil || info.IsDir() {
				continue
			}
			artifact.Size = info.Size()
			artifacts = append(artifacts, artifact)
		}
		if len(artifacts) == 0 {
			continue
		}
		version.Artifacts = artifacts
		kept = append(kept, version.normalise())
	}
	sort.SliceStable(kept, func(a, b int) bool {
		if !kept[a].PublishedAt.Equal(kept[b].PublishedAt) {
			return kept[a].PublishedAt.After(kept[b].PublishedAt)
		}
		return kept[a].AddedAt.After(kept[b].AddedAt)
	})
	item.Versions = kept
	if item.TargetDir == "" {
		item.TargetDir = DefaultTargetDir
	}
	return item
}

// Add starts tracking a plugin. The source is normalised first, so a pasted
// browser URL becomes an owner/name pair before anything is stored.
func (l *Library) Add(name string, src Source, targetDir, note string) (Plugin, error) {
	src, err := src.Normalise()
	if err != nil {
		return Plugin{}, err
	}
	targetDir, err = CleanTargetDir(targetDir)
	if err != nil {
		return Plugin{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	for _, existing := range registry {
		if existing.Source.Kind == src.Kind && strings.EqualFold(existing.Source.Repo, src.Repo) {
			return Plugin{}, fmt.Errorf("%w: %s is already tracked as %q", ErrExists, src.Repo, existing.Name)
		}
	}

	if name = strings.TrimSpace(name); name == "" {
		// The repository name is what the plugin is called on its own release
		// page, so it is a better default than anything the panel could invent.
		name = repoName(src.Repo)
	}
	item := Plugin{
		ID:        l.freeID(registry, name, src.Repo),
		Name:      name,
		Note:      strings.TrimSpace(note),
		Source:    src,
		TargetDir: targetDir,
		AddedAt:   time.Now(),
		Versions:  []Version{},
	}
	registry[item.ID] = item
	if err := l.save(registry); err != nil {
		return Plugin{}, err
	}
	return item, nil
}

// SetIcon records the registry's artwork for a plugin, if it has none yet.
//
// Separate from Add and from Edit because it is neither: it is a detail the
// panel happens to learn — when the plugin is first tracked, and again every
// time its page is opened — about an entry that is already correct without it.
// Best effort by design; a plugin with no icon is a plugin with an initial in
// a square, which is what every row falls back to anyway.
func (l *Library) SetIcon(id, iconURL string) {
	if iconURL == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()
	item, ok := registry[id]
	if !ok || item.IconURL == iconURL {
		return
	}
	item.IconURL = iconURL
	registry[id] = item
	_ = l.save(registry)
}

// Edit changes what a tracked plugin is called and where it comes from. The
// downloaded versions are untouched: a corrected asset pattern or a repository
// that moved should not throw away jars that are already on disk.
func (l *Library) Edit(id, name string, src Source, targetDir, note string) (Plugin, error) {
	src, err := src.Normalise()
	if err != nil {
		return Plugin{}, err
	}
	targetDir, err = CleanTargetDir(targetDir)
	if err != nil {
		return Plugin{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return Plugin{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	for otherID, other := range registry {
		if otherID != id && other.Source.Kind == src.Kind && strings.EqualFold(other.Source.Repo, src.Repo) {
			return Plugin{}, fmt.Errorf("%w: %s is already tracked as %q", ErrExists, src.Repo, other.Name)
		}
	}

	if name = strings.TrimSpace(name); name != "" {
		item.Name = name
	}
	if item.Source != src {
		// The cached check describes the old source; keeping it would show a
		// "latest" the new source may not even publish.
		item.Latest = nil
		item.CheckedAt = time.Time{}
		item.CheckError = ""
	}
	item.Source = src
	item.TargetDir = targetDir
	item.Note = strings.TrimSpace(note)

	registry[id] = item
	if err := l.save(registry); err != nil {
		return Plugin{}, err
	}
	return l.describe(item), nil
}

// SetPrivate records a repository's visibility as GitHub reports it, and says
// whether that changed anything.
//
// It overwrites what the operator ticked rather than deferring to it: the flag
// decides how a jar is fetched, GitHub is the authority on which way works, and
// a box ticked wrongly is exactly the case this exists to repair. It is
// deliberately not the same call as Edit — nothing else about the plugin is
// touched, so a visibility check racing an operator's open edit form cannot
// revert the repository they were changing.
func (l *Library) SetPrivate(id string, private bool) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if item.Source.Private == private {
		return false, nil
	}
	item.Source.Private = private
	registry[id] = item
	return true, l.save(registry)
}

// Remove stops tracking a plugin and deletes its downloads. Copies already
// handed to instances are untouched — they are ordinary files in their own
// directories, exactly like a core.
func (l *Library) Remove(id string) error {
	if err := validID(id); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	if _, ok := registry[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(registry, id)
	if err := l.save(registry); err != nil {
		return err
	}
	// The registry goes first: an entry pointing at files that are gone is a
	// listing bug, while files with no entry are dead weight nobody can see.
	return os.RemoveAll(filepath.Join(l.root, id))
}

// RecordCheck stores the result of an update check, successful or not. A
// failed check keeps the previous "latest" — a flaky network should not make a
// known update disappear from the page.
func (l *Library) RecordCheck(id string, latest *Release, checkErr error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	item.CheckedAt = time.Now()
	if checkErr != nil {
		item.CheckError = checkErr.Error()
	} else {
		item.CheckError = ""
		item.Latest = latest
	}
	registry[id] = item
	return l.save(registry)
}

// record files a finished download under its release.
//
// A second jar from the same release joins that release rather than replacing
// it. This is the whole point of the artifact list: downloading LuckPerms'
// velocity build after its bukkit build should give one version holding two
// jars, not one version that used to be the other one.
//
// Within a release, a jar is identified by digest. Re-downloading the same file
// updates it in place — that is the repair path for a corrupt jar — while the
// same file name at a different digest is upstream having re-cut the release,
// which replaces rather than accumulating two jars the server would both load.
func (l *Library) record(id string, version Version) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	version = version.normalise()

	for i := range item.Versions {
		existing := item.Versions[i].normalise()
		if existing.Tag != version.Tag {
			continue
		}
		for _, incoming := range version.Artifacts {
			existing.Artifacts = upsertArtifact(existing.Artifacts, incoming)
		}
		// The release's own facts are refreshed from the newer read; the
		// artifact list is not, because it is cumulative.
		existing.Version = version.Version
		existing.Prerelease = version.Prerelease
		existing.PublishedAt = version.PublishedAt
		if version.Notes != "" {
			existing.Notes = version.Notes
		}
		item.Versions[i] = existing.normalise()
		registry[id] = item
		return l.save(registry)
	}

	item.Versions = append(item.Versions, version)
	registry[id] = item
	return l.save(registry)
}

// pending is one jar the rescan has to open.
type pending struct {
	id, tag, file string
	sha           string
}

// Rescan reads the descriptor of every held jar the panel has never opened,
// and records what each one says. It returns how many jars were read.
//
// This exists because the fields came later than the library did. A panel that
// has been running since before jars were read holds versions with no
// descriptor at all, and an operator has no way to ask for one — the jar is on
// disk and correct, so nothing will ever re-download it. Without a sweep those
// rows would stay blank forever while every new download filled in, which
// looks exactly like the feature being broken.
//
// The reading happens outside the lock. Every jar is a few kilobytes of zip
// central directory, but a library with a hundred of them on a slow disk is
// still long enough that holding the registry lock through it would stall the
// pages that only wanted to list what is there.
//
// Best effort throughout: a jar that will not open is marked as read with
// nothing found, because the alternative is re-opening it on every start.
func (l *Library) Rescan() int {
	l.mu.Lock()
	registry := l.load()
	work := make([]pending, 0, len(registry))
	for id, item := range registry {
		for _, version := range item.Versions {
			for _, artifact := range version.normalise().Artifacts {
				if artifact.Scanned || artifact.FileName == "" {
					continue
				}
				work = append(work, pending{id: id, tag: version.Tag, file: artifact.FileName, sha: artifact.SHA256})
			}
		}
	}
	l.mu.Unlock()

	if len(work) == 0 {
		return 0
	}

	found := make(map[pending]JarInfo, len(work))
	for _, item := range work {
		path := l.versionFile(item.id, item.tag, item.file)
		info, _, err := readJar(path)
		if err != nil && !errors.Is(err, ErrNotAJar) {
			// The file is gone or unreadable. Not marked as read: describe()
			// already hides a version whose jar is missing, and a jar that
			// comes back — a mount that was not ready at boot — should be read
			// then rather than written off now.
			continue
		}
		found[item] = info
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry = l.load()
	changed := 0
	for item, info := range found {
		plugin, ok := registry[item.id]
		if !ok {
			continue
		}
		for i := range plugin.Versions {
			if plugin.Versions[i].Tag != item.tag {
				continue
			}
			version := plugin.Versions[i].normalise()
			for j := range version.Artifacts {
				artifact := &version.Artifacts[j]
				// Matched on both, because a record written before there were
				// digests has only the file name to go on.
				if artifact.FileName != item.file || (item.sha != "" && !artifact.Describes(item.sha)) {
					continue
				}
				artifact.applyJarInfo(info)
				changed++
			}
			plugin.Versions[i] = version.normalise()
		}
		registry[item.id] = plugin
	}
	if changed == 0 {
		return 0
	}
	if err := l.save(registry); err != nil {
		return 0
	}
	return changed
}

func upsertArtifact(list []Artifact, incoming Artifact) []Artifact {
	for i := range list {
		if list[i].Describes(incoming.SHA256) || list[i].FileName == incoming.FileName {
			list[i] = incoming
			return list
		}
	}
	return append(list, incoming)
}

// RemoveArtifact deletes one jar, and the release with it once it is the last
// one there.
func (l *Library) RemoveArtifact(id, tag, sha string) error {
	if err := validID(id); err != nil {
		return err
	}
	slug, err := versionSlug(tag)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	for i := range item.Versions {
		version := item.Versions[i].normalise()
		if version.Tag != tag {
			continue
		}
		gone := version.Artifact(sha)
		if gone == nil {
			return fmt.Errorf("%w: %s has no jar %s", ErrNotFound, tag, sha)
		}
		file := l.versionFile(id, tag, gone.FileName)

		kept := make([]Artifact, 0, len(version.Artifacts))
		for _, artifact := range version.Artifacts {
			if !artifact.Describes(sha) {
				kept = append(kept, artifact)
			}
		}
		if len(kept) == 0 {
			item.Versions = append(item.Versions[:i], item.Versions[i+1:]...)
		} else {
			version.Artifacts = kept
			item.Versions[i] = version.normalise()
		}
		registry[id] = item
		if err := l.save(registry); err != nil {
			return err
		}
		if len(kept) == 0 {
			return os.RemoveAll(filepath.Join(l.root, id, slug))
		}
		return os.Remove(file)
	}
	return fmt.Errorf("%w: %s has no version %s", ErrNotFound, id, tag)
}

// SetPolicy stores what the panel may do with a plugin unasked.
func (l *Library) SetPolicy(id string, policy Policy) (Plugin, error) {
	if err := validID(id); err != nil {
		return Plugin{}, err
	}
	if policy.Keep < 0 {
		policy.Keep = 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return Plugin{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	// A pin has to name a release the library actually holds, or the plugin is
	// locked to a version nothing can install.
	if policy.Pin != "" && !item.HasVersion(policy.Pin) {
		return Plugin{}, fmt.Errorf("%w: %s 里没有 %s 这个版本，锁不上去", ErrNotFound, item.Name, policy.Pin)
	}
	item.Policy = policy
	registry[id] = item
	if err := l.save(registry); err != nil {
		return Plugin{}, err
	}
	return l.describe(item), nil
}

// RemoveVersion deletes one downloaded version.
func (l *Library) RemoveVersion(id, tag string) error {
	if err := validID(id); err != nil {
		return err
	}
	slug, err := versionSlug(tag)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	registry := l.load()

	item, ok := registry[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	kept := make([]Version, 0, len(item.Versions))
	found := false
	for _, version := range item.Versions {
		if version.Tag == tag {
			found = true
			continue
		}
		kept = append(kept, version)
	}
	if !found {
		return fmt.Errorf("%w: %s has no version %s", ErrNotFound, id, tag)
	}
	item.Versions = kept
	registry[id] = item
	if err := l.save(registry); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(l.root, id, slug))
}

// Open returns a version's primary jar for copying into an instance directory.
func (l *Library) Open(id, tag string) (*os.File, Plugin, Version, error) {
	file, item, version, _, err := l.OpenArtifact(id, tag, "")
	return file, item, version, err
}

// OpenArtifact returns one specific jar of a release.
//
// An empty digest means the primary, which is what every caller that does not
// care wants. A plugin that ships one build per platform is the caller that
// does: installing LuckPerms onto a Velocity proxy and onto a Paper server are
// the same version and two different files.
func (l *Library) OpenArtifact(id, tag, sha string) (*os.File, Plugin, Version, Artifact, error) {
	item, err := l.Get(id)
	if err != nil {
		return nil, Plugin{}, Version{}, Artifact{}, err
	}
	version := item.Version(tag)
	if version == nil {
		return nil, Plugin{}, Version{}, Artifact{}, fmt.Errorf("%w: %s has no downloaded version %s", ErrNotFound, item.Name, tag)
	}
	artifact := version.Primary()
	if sha != "" {
		found := version.Artifact(sha)
		if found == nil {
			return nil, Plugin{}, Version{}, Artifact{}, fmt.Errorf("%w: %s %s has no jar %s", ErrNotFound, item.Name, version.Version, sha[:min(12, len(sha))])
		}
		artifact = *found
	}
	file, err := os.Open(l.versionFile(id, tag, artifact.FileName))
	if err != nil {
		return nil, Plugin{}, Version{}, Artifact{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return file, item, *version, artifact, nil
}

// versionFile is where one downloaded jar lives. Versions get a directory
// each because a plugin that always publishes "Foo.jar" would otherwise have
// every release overwrite the last one.
func (l *Library) versionFile(id, tag, fileName string) string {
	slug, err := versionSlug(tag)
	if err != nil {
		return filepath.Join(l.root, id, "invalid", fileName)
	}
	return filepath.Join(l.root, id, slug, fileName)
}

// load reads the registry once and keeps it in memory. The panel is the only
// writer, so re-reading per call would buy nothing.
func (l *Library) load() map[string]Plugin {
	if l.loaded {
		return l.registry
	}
	l.loaded = true
	l.registry = map[string]Plugin{}

	data, err := os.ReadFile(filepath.Join(l.root, registryName))
	if err != nil {
		return l.registry
	}
	var stored map[string]Plugin
	if err := json.Unmarshal(data, &stored); err != nil || stored == nil {
		// A corrupt registry is not worth refusing to boot over; the operator
		// can re-add their plugins, and the jars are still on disk.
		return l.registry
	}
	for id, item := range stored {
		item.ID = id
		if item.TargetDir == "" {
			item.TargetDir = DefaultTargetDir
		}
		if item.Versions == nil {
			item.Versions = []Version{}
		}
		// A registry written before the artifact list has one jar per release,
		// spelled out in the flat fields. Migrated on read rather than by a
		// rewrite pass, so a panel that is downgraded again still works.
		for i := range item.Versions {
			item.Versions[i] = item.Versions[i].normalise()
		}
		l.registry[id] = item
	}
	return l.registry
}

func (l *Library) save(registry map[string]Plugin) error {
	l.registry = registry
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}

	// Written beside the file and renamed over it, so an interrupted save
	// cannot leave a half-written registry behind.
	temp := filepath.Join(l.root, registryName+".tmp")
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(l.root, registryName)); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// freeID picks a directory-safe id that no other plugin holds.
func (l *Library) freeID(registry map[string]Plugin, name, repo string) string {
	base := slug(name)
	if base == "" {
		base = slug(repoName(repo))
	}
	if base == "" {
		base = "plugin"
	}
	if _, taken := registry[base]; !taken {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if _, taken := registry[candidate]; !taken {
			return candidate
		}
	}
}

func repoName(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// slug reduces a name to something safe as a directory and readable in a URL.
func slug(in string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped
	for _, r := range strings.ToLower(strings.TrimSpace(in)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-.")
	}
	return out
}

// versionSlug turns a tag into a directory name. Tags carry slashes often
// enough ("release/1.2.3") that this cannot be a straight pass-through.
func versionSlug(tag string) (string, error) {
	out := slug(tag)
	if out == "" {
		return "", fmt.Errorf("%w: version %q has no usable name", ErrInvalidID, tag)
	}
	return out, nil
}

// validID rejects anything that names something other than a directory
// directly inside the library.
func validID(id string) error {
	if id == "" || id == "." || id == ".." ||
		strings.ContainsAny(id, `/\`+"\x00") || strings.HasSuffix(id, partSuffix) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}

// CleanTargetDir validates the directory a plugin is installed into. It is a
// path inside the instance, so the same rules the file manager applies hold:
// relative, no escaping, no absolute paths.
func CleanTargetDir(dir string) (string, error) {
	dir = strings.TrimSpace(strings.ReplaceAll(dir, "\\", "/"))
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return DefaultTargetDir, nil
	}
	if strings.Contains(dir, "\x00") {
		return "", fmt.Errorf("%w: %q", ErrInvalidTarget, dir)
	}
	cleaned := filepath.ToSlash(filepath.Clean(dir))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%w: %q must stay inside the instance directory", ErrInvalidTarget, dir)
	}
	return cleaned, nil
}
