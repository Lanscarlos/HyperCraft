package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Putting a release back together that the panel had taken apart.
//
// The registries publish a cross-platform plugin as one release with a build
// per platform. The panel used to read that as several releases: a Hangar
// version became one entry per platform, tagged "1.2.3@PAPER" and
// "1.2.3@VELOCITY", and Modrinth's per-loader versions came through as
// whatever opaque ids Modrinth gave them, all carrying the same version
// number. A plugin that shipped once looked like it had shipped four times,
// the fleet looked inconsistent because the proxy was on the "other" version,
// and 批量对齐 offered to put a Paper jar on a Velocity proxy.
//
// registry.go no longer produces those, so new downloads are filed correctly.
// This is the other half: libraries that already hold the split, which have to
// be folded back or they keep reporting a disagreement that was never real.
//
// It is a one-shot at startup rather than a fix-up on read, because it moves
// files: a release's jars live in one directory named after its tag, and
// merging two releases means the jars have to end up in the surviving one.
// Per plugin it is a transaction — move every file, then rewrite the entry,
// and on any failure put the files back and leave that plugin exactly as it
// was. A plugin whose merge cannot be completed is a plugin that goes on
// working the way it did yesterday, which is the only acceptable failure for
// something that runs before anybody has asked for anything.

// Regroup folds platform-split versions back into the releases they came from.
//
// Reports how many plugins it rewrote. Errors are logged per plugin rather than
// returned: the panel must boot.
func (l *Library) Regroup(log *slog.Logger) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	registry := l.load()
	fixed := 0
	for id, item := range registry {
		item, latest := repairLatest(item)
		merged, moves, ok := planRegroup(item)
		if !ok {
			if latest {
				// Nothing on disk to move; the cached check was the only thing
				// wrong with this entry.
				registry[id] = item
				fixed++
			}
			continue
		}
		if err := l.applyMoves(id, moves); err != nil {
			log.Warn("could not merge platform builds back into one release",
				"plugin", id, "err", err)
			continue
		}
		registry[id] = merged
		fixed++
		log.Info("merged platform builds into one release",
			"plugin", id, "was", len(item.Versions), "now", len(merged.Versions))
	}
	if fixed == 0 {
		return 0
	}
	if err := l.save(registry); err != nil {
		log.Warn("could not write the merged plugin registry", "err", err)
	}
	return fixed
}

// Realign points every install record at the release its jar now belongs to.
//
// The merge above rewrites the library's tags, and an instance's record holds
// the tag it was installed at — so without this the server that is running
// LuckPerms sits on a tag that no longer exists, which reads as "this is not
// any version we know" on every page that joins the two.
//
// Matched by digest, which is the one thing about a jar that does not change:
// the record's own SHA is what the panel wrote down when it copied the file,
// and finding it in the library is proof of which release it came from — much
// better proof than the tag was. Records whose digest is in no release are
// left alone; a jar the library no longer holds is a real state, and inventing
// a tag for it would be worse than an old one.
func (m *Instances) Realign(library *Library, log *slog.Logger) int {
	items := library.List()
	byDigest := map[string]struct {
		tag, version string
		artifact     Artifact
		release      Version
	}{}
	for _, item := range items {
		for _, version := range item.Versions {
			for _, artifact := range version.Artifacts {
				if artifact.SHA256 == "" {
					continue
				}
				byDigest[strings.ToLower(item.ID+"/"+artifact.SHA256)] = struct {
					tag, version string
					artifact     Artifact
					release      Version
				}{version.Tag, version.Version, artifact, version}
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	records := m.load()

	fixed := 0
	for instanceID, book := range records {
		if book == nil {
			continue
		}
		for i := range book.Plugins {
			record := &book.Plugins[i]
			found, ok := byDigest[strings.ToLower(record.PluginID+"/"+record.SHA256)]
			if !ok || (found.tag == record.Tag && found.version == record.Version) {
				continue
			}
			log.Info("install record re-pointed at its release",
				"instance", instanceID, "plugin", record.PluginID,
				"was", record.Tag, "now", found.tag)
			record.Tag, record.Version = found.tag, found.version
			// The jar's own claims travel with it, so this server's
			// compatibility badge describes the file it actually has rather
			// than the release it belongs to.
			record.Loaders, record.GameVersions = Claims(found.release, found.artifact)
			fixed++
		}
	}
	if fixed > 0 {
		if err := m.save(records); err != nil {
			log.Warn("could not write the re-pointed install records", "err", err)
		}
	}
	return fixed
}

// repairLatest rewrites what the last update check cached.
//
// The check result is stored, not re-fetched — the anonymous GitHub API allows
// sixty calls an hour, and a page that refreshed it on every visit would spend
// that budget on nobody's behalf. Which means a check made by an older build
// keeps being displayed: 上游最新 goes on saying "v5.5.71-velocity" long after
// the reader stopped producing such a thing, because nothing has looked again.
// So the cached answer is rewritten to what a look would find now.
//
// Only the identity is repaired, not the assets — a re-check replaces the whole
// record with a properly read one, and inventing a jar list here would be the
// migration making up data it does not have.
func repairLatest(item Plugin) (Plugin, bool) {
	if item.Latest == nil {
		return item, false
	}
	tag, version := item.Latest.Tag, item.Latest.Version
	switch item.Source.Kind {
	case SourceHangar:
		if at := strings.Index(tag, "@"); at > 0 {
			tag = tag[:at]
		}
		version, _ = releaseNumber(version)
	case SourceModrinth:
		// The stored tag is a Modrinth version id, which names one build and
		// is not what a release is addressed by any more. The number is.
		number, _ := releaseNumber(version)
		if number == "" {
			return item, false
		}
		tag, version = number, number
	default:
		return item, false
	}
	if tag == item.Latest.Tag && version == item.Latest.Version {
		return item, false
	}
	// Copied rather than written through the pointer: the same Release may be
	// shared with a caller that read this entry a moment ago.
	latest := *item.Latest
	latest.Tag, latest.Version = tag, version
	item.Latest = &latest
	return item, true
}

// want is one jar and where it has to end up: which release directory it is
// in now, which it belongs under, and the name it goes by — which changes only
// when two builds of one release turn out to be called the same thing.
type want struct {
	from, tag string
	was, is   string
}

// planRegroup works out what one plugin should look like, and which jars have
// to move to get there. The bool is whether anything changes at all.
func planRegroup(item Plugin) (Plugin, []want, bool) {
	if len(item.Versions) < 2 {
		return item, nil, false
	}

	merged := make([]Version, 0, len(item.Versions))
	at := map[string]int{}
	var moves []want
	changed := false

	for _, version := range item.Versions {
		version = version.normalise()
		tag := regroupTag(item.Source.Kind, version)
		if tag != version.Tag {
			changed = true
		}
		// The old Hangar tag said which platform this jar was built for, and
		// that is about to be thrown away. It is the only per-jar record these
		// downloads have — everything else on them describes the whole release
		// — so it is written onto the artifacts before the tag goes.
		if platform := splitPlatform(item.Source.Kind, version.Tag); platform != "" {
			for i := range version.Artifacts {
				if version.Artifacts[i].Platform == "" {
					version.Artifacts[i].Platform = platform
				}
				version.Artifacts[i].Loaders = []string{platform}
			}
		}

		// The platform suffix comes off the displayed version too. Leaving it
		// on would put "v5.5.71-bukkit" in the version column of a release
		// that also holds the velocity jar.
		if platform := versionPlatform(item.Source.Kind, version); platform != "" {
			for i := range version.Artifacts {
				if version.Artifacts[i].Platform == "" {
					version.Artifacts[i].Platform = platform
				}
			}
			version.Version = tag
			changed = true
		}

		index, seen := at[tag]
		if !seen {
			for _, artifact := range version.Artifacts {
				moves = append(moves, want{
					from: version.Tag, tag: tag,
					was: artifact.FileName, is: artifact.FileName,
				})
			}
			version.Tag = tag
			at[tag] = len(merged)
			merged = append(merged, version)
			continue
		}

		// A second build of a release already collected. Its jars join that
		// release's artifact list and move into its directory.
		changed = true
		into := merged[index]
		for _, artifact := range version.Artifacts {
			if into.Artifact(artifact.SHA256) != nil {
				continue
			}
			was := artifact.FileName
			// Two builds under one name would be one file on disk, and the
			// second would overwrite the first. The platform is what tells
			// them apart, so it goes into the name.
			if held := findByName(into.Artifacts, artifact.FileName); held != nil {
				artifact.FileName = platformName(artifact.FileName, artifactPlatform(artifact))
			}
			moves = append(moves, want{
				from: version.Tag, tag: tag,
				was: was, is: artifact.FileName,
			})
			into.Artifacts = upsertArtifact(into.Artifacts, artifact)
		}
		if into.Notes == "" {
			into.Notes = version.Notes
		}
		if version.PublishedAt.Before(into.PublishedAt) {
			into.PublishedAt = version.PublishedAt
		}
		into.Prerelease = into.Prerelease && version.Prerelease
		into.GameVersions = union(into.GameVersions, version.GameVersions)
		into.Loaders = union(into.Loaders, version.Loaders)
		merged[index] = into.normalise()
	}

	if !changed {
		return item, nil, false
	}
	item.Versions = merged
	return item, moves, true
}

func findByName(list []Artifact, name string) *Artifact {
	for i := range list {
		if strings.EqualFold(list[i].FileName, name) {
			return &list[i]
		}
	}
	return nil
}

// artifactPlatform is the best name this jar has for what it runs on: what its
// own descriptor declared, or failing that what its source said.
func artifactPlatform(artifact Artifact) string {
	if artifact.Platform != "" {
		return artifact.Platform
	}
	if len(artifact.Loaders) > 0 {
		return strings.ToLower(artifact.Loaders[0])
	}
	return ""
}

// regroupTag is what a stored version's tag should have been.
//
//   - Hangar addressed a version by name and the panel appended the platform.
//     Everything before the "@" is the release.
//   - Modrinth files one version per loader, each with its own opaque id and
//     the release's version number — with the platform written into it, in the
//     case that matters: LuckPerms publishes "v5.5.71-bukkit" beside
//     "v5.5.71-velocity". The number without that suffix is the release, and
//     it is what registry.go tags them with now.
//
// Everything else — GitHub, SpigotMC, an imported local jar — publishes one
// release per tag and is left alone. Guessing at those would merge two
// genuinely different releases that happen to share a version string, which is
// a worse mistake than the one being fixed.
func regroupTag(kind string, version Version) string {
	switch kind {
	case SourceHangar:
		if at := strings.Index(version.Tag, "@"); at > 0 {
			return version.Tag[:at]
		}
	case SourceModrinth:
		if number, _ := releaseNumber(version.Version); number != "" {
			return number
		}
	}
	return version.Tag
}

// move is one jar changing place on disk, as absolute paths.
type move struct {
	from, to string
}

// versionPlatform is the platform a stored version's *number* named, for the
// registry that writes it there. Empty when the number named none.
func versionPlatform(kind string, version Version) string {
	if kind != SourceModrinth {
		return ""
	}
	_, platform := releaseNumber(version.Version)
	return platform
}

// splitPlatform is the platform an old Hangar tag carried, lowercased, or
// empty for a tag that carried none.
func splitPlatform(kind, tag string) string {
	if kind != SourceHangar {
		return ""
	}
	if at := strings.Index(tag, "@"); at > 0 && at < len(tag)-1 {
		return strings.ToLower(tag[at+1:])
	}
	return ""
}

// applyMoves puts every jar under its release's directory, and puts them all
// back if any one of them will not go.
func (l *Library) applyMoves(id string, moves []want) error {
	planned := make([]move, 0, len(moves))
	for _, wanted := range moves {
		to := l.versionFile(id, wanted.tag, wanted.is)
		if _, err := os.Stat(to); err == nil {
			// Already where it belongs — the release kept its tag, or a
			// previous run got this far.
			continue
		}
		from := l.versionFile(id, wanted.from, wanted.was)
		if info, err := os.Stat(from); err != nil || info.IsDir() {
			// The file is gone. Not an error: describe() already drops
			// artifacts with no file, and refusing the whole merge over a jar
			// somebody deleted by hand would strand the rest.
			continue
		}
		planned = append(planned, move{from: from, to: to})
	}

	done := make([]move, 0, len(planned))
	for _, one := range planned {
		err := os.MkdirAll(filepath.Dir(one.to), 0o755)
		if err == nil {
			err = os.Rename(one.from, one.to)
		}
		if err != nil {
			rollback(done)
			return fmt.Errorf("moving %s: %w", filepath.Base(one.from), err)
		}
		done = append(done, one)
	}

	// The directories the jars came out of are named after tags that no longer
	// exist. Removed only when empty, so nothing unaccounted for is deleted.
	for _, one := range done {
		_ = os.Remove(filepath.Dir(one.from))
	}
	return nil
}

func rollback(done []move) {
	for i := len(done) - 1; i >= 0; i-- {
		_ = os.Rename(done[i].to, done[i].from)
	}
}
