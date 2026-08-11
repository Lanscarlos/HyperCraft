package plugin

// Taking a jar the operator already has.
//
// The four catalogues plus GitHub reach almost everything, and "almost" is the
// problem: a plugin bought on a marketplace, one compiled from a fork, one a
// friend sent over, one whose author only ever posts it in a Discord. Those
// jars had exactly one way into the panel — dropped into a server's plugins
// directory by hand, where the panel could see a file and say nothing else
// about it, once per server, with no checksum and nothing to compare against
// the copy on the other four.
//
// Imported into the library they are ordinary plugins: one file, one checksum,
// the same 装到实例 and the same cross-instance version view as everything
// downloaded. They do not get update checking, because there is nowhere to
// check — and that is the whole of what makes them different.
//
// The jar is asked what it is. Every server plugin ships a descriptor naming
// itself, its version and the platform it is written for, and jarinfo.go
// already reads all five formats for the installed-plugins page. So an import
// is a file picker and nothing else: the name, the version number and the
// loader come out of the file, and what the operator would otherwise have had
// to type is exactly what the panel would have had to trust them about.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrTooLarge rejects an upload past the panel's limit. Its own error so the
// handler can answer 413 rather than a generic failure.
var ErrTooLarge = errors.New("文件超过了面板的上传大小限制")

// ErrNotAJar rejects something that is not a readable archive. Checked by
// opening it rather than by looking at the name: a .jar that will not unzip is
// a truncated download, and finding that out here beats finding it out from a
// server that would not boot.
var ErrNotAJar = errors.New("这不是一个能打开的 jar")

// Imported is what one uploaded jar turned out to be.
type Imported struct {
	Plugin  Plugin  `json:"plugin"`
	Version Version `json:"version"`
	// Info is the jar's own description of itself, so the UI can show what was
	// read out rather than only what was stored.
	Info JarInfo `json:"info"`
	// Replaced is true when this exact jar — same checksum — was already in the
	// library. Nothing is duplicated; the operator is told rather than left to
	// wonder why the version count did not move.
	Replaced bool `json:"replaced"`
}

// ImportJar files an uploaded jar as a version of a library plugin.
//
// `id` may be empty, in which case the entry is found or created from what the
// jar says its name is — which is what the panel-wide 导入 jar does. Given an
// id, the jar joins that plugin instead, which is how a build made from a fork
// ends up beside the releases it was forked from.
func (l *Library) ImportJar(id, fileName string, src io.Reader, limit int64) (Imported, error) {
	fileName = filepath.Base(strings.ReplaceAll(strings.TrimSpace(fileName), "\\", "/"))
	if fileName == "" || fileName == "." || fileName == ".." {
		return Imported{}, fmt.Errorf("%w: %q", ErrInvalidID, fileName)
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".jar") {
		return Imported{}, fmt.Errorf("%w: %s", ErrNotAJar, fileName)
	}

	// Staged outside any plugin's directory, because until the jar has been
	// read there is no telling which plugin it belongs to.
	staged, size, digest, err := l.stage(src, limit)
	if err != nil {
		return Imported{}, err
	}
	defer os.Remove(staged)

	info, err := readJarFile(staged, size)
	if err != nil {
		return Imported{}, err
	}

	item, err := l.entryFor(id, info, fileName)
	if err != nil {
		return Imported{}, err
	}

	// Content-addressed: the same jar uploaded twice is the same version, so a
	// re-upload repairs a corrupt file instead of growing a second entry that
	// differs from the first in nothing an operator could see.
	artifact := Artifact{
		SHA256:   digest,
		FileName: fileName,
		Size:     size,
		AddedAt:  time.Now(),
	}
	artifact.applyJarInfo(info)
	// What the descriptor declared, in the same two fields a download fills
	// from its registry — so Judge treats an imported jar exactly like any
	// other, and the install dialog can say "this one is for Velocity".
	if loader := normaliseLoader(info.Platform); loader != "" {
		artifact.Loaders = []string{loader}
	}
	if info.APIVersion != "" {
		artifact.GameVersions = []string{info.APIVersion}
	}

	version := Version{
		Tag:         "local-" + digest[:12],
		Version:     importedVersion(info, fileName),
		Artifacts:   []Artifact{artifact},
		PublishedAt: time.Now(),
		AddedAt:     time.Now(),
	}.normalise()

	replaced := item.HasVersion(version.Tag)
	final := l.versionFile(item.ID, version.Tag, version.FileName)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return Imported{}, err
	}
	// Rename rather than copy: the staging file is under the same root, so this
	// is atomic and the jar is never half-visible to an install running beside
	// it.
	if err := os.Rename(staged, final); err != nil {
		return Imported{}, err
	}
	if err := l.record(item.ID, version); err != nil {
		return Imported{}, fmt.Errorf("文件已存入，但记录插件版本失败：%w", err)
	}

	stored, err := l.Get(item.ID)
	if err != nil {
		return Imported{}, err
	}
	return Imported{Plugin: stored, Version: version, Info: info, Replaced: replaced}, nil
}

// stage writes the upload to a temporary file under the library root and
// returns its path, size and checksum.
//
// To disk rather than to memory, and hashed on the way past: a plugin jar is
// routinely tens of megabytes, several can be uploaded at once, and the panel
// is expected to run on the same small box as the servers.
func (l *Library) stage(src io.Reader, limit int64) (path string, size int64, digest string, err error) {
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return "", 0, "", err
	}
	temp, err := os.CreateTemp(l.root, ".import-*"+partSuffix)
	if err != nil {
		return "", 0, "", err
	}
	defer temp.Close()

	hash := sha256.New()
	// One byte past the limit, so hitting it is distinguishable from a file
	// that happens to be exactly the maximum size.
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(src, limit+1))
	if err != nil {
		os.Remove(temp.Name())
		return "", 0, "", err
	}
	if written > limit {
		os.Remove(temp.Name())
		return "", 0, "", ErrTooLarge
	}
	if err := temp.Sync(); err != nil {
		os.Remove(temp.Name())
		return "", 0, "", err
	}
	return temp.Name(), written, hex.EncodeToString(hash.Sum(nil)), nil
}

// readJar opens a jar on disk and asks it what it is, reporting its size along
// the way. Used wherever a jar has just landed — a finished download, an
// adopted file — and the panel needs the identity out of it.
func readJar(path string) (JarInfo, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return JarInfo{}, 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return JarInfo{}, 0, err
	}
	info, ok := ReadJarInfo(file, stat.Size())
	if !ok {
		return JarInfo{}, stat.Size(), ErrNotAJar
	}
	return info, stat.Size(), nil
}

func readJarFile(path string, size int64) (JarInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return JarInfo{}, err
	}
	defer file.Close()

	info, ok := ReadJarInfo(file, size)
	if !ok {
		// Not fatal on its own — a Forge mod's descriptor is TOML and jarinfo
		// deliberately does not parse it — so this only fails when the archive
		// itself would not open.
		if _, zipErr := zip.NewReader(file, size); zipErr != nil {
			return JarInfo{}, fmt.Errorf("%w: %v", ErrNotAJar, zipErr)
		}
	}
	return info, nil
}

// entryFor finds the library entry an uploaded jar belongs to, creating one
// when the operator did not name it.
func (l *Library) entryFor(id string, info JarInfo, fileName string) (Plugin, error) {
	if strings.TrimSpace(id) != "" {
		return l.Get(id)
	}

	name := strings.TrimSpace(info.Name)
	if name == "" {
		// No descriptor to read. The file name is what the operator called it
		// and is better than anything the panel could invent.
		name = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}
	repo := importSlug(name)

	// A second upload of the same plugin joins the entry that already exists.
	// Matched on the slug rather than the display name so "EssentialsX" and
	// "essentialsx" are one plugin, which is what they are.
	for _, existing := range l.List() {
		if existing.Source.Kind == SourceLocal && strings.EqualFold(existing.Source.Repo, repo) {
			return existing, nil
		}
	}

	dir := DefaultTargetDir
	if loader := normaliseLoader(info.Platform); loader != "" {
		dir = TargetDirFor(loader)
	}
	return l.Add(name, Source{Kind: SourceLocal, Repo: repo}, dir, "")
}

// importedVersion is what the version column reads. The descriptor's own
// version number where there is one, and otherwise the file name — which is
// where a plugin with no descriptor keeps its version anyway.
func importedVersion(info JarInfo, fileName string) string {
	if version := strings.TrimSpace(info.Version); version != "" {
		return version
	}
	return strings.TrimSuffix(fileName, filepath.Ext(fileName))
}

// importSlug reduces a plugin name to the identity two uploads are matched on.
func importSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '.':
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "imported"
	}
	return slug
}
