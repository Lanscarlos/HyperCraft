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
		merged, moves, ok := planRegroup(item)
		if !ok {
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
//     all of them carrying the release's version number. The number is the
//     release, and it is what registry.go tags them with now.
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
		if number := strings.TrimSpace(version.Version); number != "" {
			return number
		}
	}
	return version.Tag
}

// move is one jar changing place on disk, as absolute paths.
type move struct {
	from, to string
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
