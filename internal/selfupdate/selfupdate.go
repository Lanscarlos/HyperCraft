// Package selfupdate replaces the running panel binary with a newer release
// published on GitHub, so an operator can update from the web UI instead of
// opening an SSH session.
//
// What this trusts: the release archive is fetched over HTTPS from GitHub and
// checked against the SHA256SUMS.txt published alongside it. That detects a
// corrupted or truncated download and a tampered mirror, but the checksum file
// is not signed — anyone able to publish a release to the configured repository
// can publish a matching checksum. The trust anchor is therefore GitHub's TLS
// and the repository's own access control, not the checksum itself.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNoAsset means the release carries no build for the running platform.
var ErrNoAsset = errors.New("this release has no build for your platform")

// ErrUpToDate means the latest release is not newer than the running binary.
var ErrUpToDate = errors.New("already running the latest version")

// Limits on what a release may make this process read. The archives are a few
// megabytes; these caps exist so a hostile or corrupt response cannot exhaust
// memory or fill the disk.
const (
	maxArchiveBytes  = 128 << 20
	maxBinaryBytes   = 256 << 20
	maxChecksumBytes = 64 << 10
	httpTimeout      = 10 * time.Minute
)

// Release is the subset of a GitHub release this package needs.
type Release struct {
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`

	// assets maps asset name to download URL.
	assets map[string]string
}

// Updater checks for and installs releases of a single GitHub repository.
type Updater struct {
	repo    string // "owner/name"
	current string
	client  *http.Client

	// apiBase points at GitHub; tests redirect it at an httptest server.
	apiBase string

	// mirror prefixes release download URLs, e.g. "https://ghfast.top/".
	// Empty fetches from GitHub directly. See SetMirror for what it may carry.
	mirror string

	// downloadPrefix is the URL prefix a mirror is allowed to front. Tests
	// point it at their own origin so the mirror/direct split can be observed
	// with both sides actually reachable.
	downloadPrefix string

	// exePath overrides the binary Prepare stages next to and Commit replaces.
	// Empty means "the running executable", which is what production wants and
	// what a test must never be allowed to overwrite.
	exePath string
}

// executable is the binary an update would replace.
func (u *Updater) executable() (string, error) {
	if u.exePath != "" {
		return u.exePath, nil
	}
	return currentExecutable()
}

// New returns an Updater for the given "owner/name" repository, comparing
// releases against the currently running version.
func New(repo, currentVersion string) *Updater {
	return &Updater{
		repo:           repo,
		current:        currentVersion,
		client:         &http.Client{Timeout: httpTimeout},
		apiBase:        "https://api.github.com",
		downloadPrefix: "https://github.com/",
	}
}

// CurrentVersion is the version of the running binary.
func (u *Updater) CurrentVersion() string { return u.current }

// SetMirror configures a proxy for release downloads, given as a prefix that a
// GitHub URL is appended to — "https://ghfast.top/" turns
// https://github.com/o/r/releases/download/v1/x.tar.gz into
// https://ghfast.top/https://github.com/o/r/releases/download/v1/x.tar.gz.
// Empty disables it.
//
// A mirror carries the release archive, which is the megabytes and therefore
// the slow part. It does not get to decide what that archive should contain:
// the checksums are fetched from GitHub first and only fall back to the mirror
// if GitHub is unreachable, so serving a doctored binary requires breaking
// GitHub's TLS too. Nor does it carry the update check — the mirrors people
// use for this do not proxy api.github.com at all.
func (u *Updater) SetMirror(prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	u.mirror = prefix
}

// Mirror is the configured download proxy, or "" for direct downloads.
func (u *Updater) Mirror() string { return u.mirror }

// mirrored rewrites a GitHub download URL to go through the mirror, or returns
// "" when no mirror applies. Only github.com URLs are rewritten: the proxies
// are GitHub-specific, and prefixing anything else would just produce a 404.
func (u *Updater) mirrored(raw string) string {
	if u.mirror == "" || !strings.HasPrefix(raw, u.downloadPrefix) {
		return ""
	}
	return u.mirror + raw
}

// bulkOrder is the URL preference for the release archive: mirror first,
// because speed is the whole point, falling back to GitHub if it fails.
func (u *Updater) bulkOrder(raw string) []string {
	if m := u.mirrored(raw); m != "" {
		return []string{m, raw}
	}
	return []string{raw}
}

// trustedOrder is the URL preference for the checksums: GitHub first, because
// they are what stops a mirror substituting its own binary, and they are small
// enough that fetching them slowly costs nothing. The mirror remains a fallback
// so a blocked GitHub still leaves the panel updatable — with the mirror
// trusted for that run, which the caller reports.
func (u *Updater) trustedOrder(raw string) []string {
	if m := u.mirrored(raw); m != "" {
		return []string{raw, m}
	}
	return []string{raw}
}

// AssetName is the release archive this platform needs. It mirrors the naming
// in the release workflow's packaging step.
func AssetName(version string) string {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("hypercraft-%s-%s-%s%s", NormalizeVersion(version), runtime.GOOS, runtime.GOARCH, ext)
}

// Check asks GitHub for the newest published release. Pre-releases and drafts
// are excluded by the endpoint itself, so an rc tag is never offered.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", u.apiBase, u.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s", resp.Status)
	}

	var payload struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	if payload.TagName == "" {
		return nil, errors.New("release has no tag")
	}

	rel := &Release{
		Tag:         payload.TagName,
		Version:     NormalizeVersion(payload.TagName),
		Notes:       payload.Body,
		URL:         payload.HTMLURL,
		PublishedAt: payload.PublishedAt,
		assets:      make(map[string]string, len(payload.Assets)),
	}
	for _, a := range payload.Assets {
		rel.assets[a.Name] = a.URL
	}
	return rel, nil
}

// IsNewerThanCurrent reports whether rel should be offered as an update.
func (u *Updater) IsNewerThanCurrent(rel *Release) bool {
	if rel == nil || !IsReleaseVersion(u.current) || !IsReleaseVersion(rel.Version) {
		return false
	}
	return CompareVersions(rel.Version, u.current) > 0
}

// HasAssetForPlatform reports whether rel ships a build this machine can run.
func (rel *Release) HasAssetForPlatform() bool {
	_, ok := rel.assets[AssetName(rel.Version)]
	return ok
}

// Staged is a verified new binary sitting next to the running one, ready to be
// moved into place.
type Staged struct {
	path string // the staged binary
	exe  string // the executable it will replace

	// checksumFromMirror records that GitHub could not be reached for the
	// checksums and the mirror supplied them instead. The binary still matched
	// what it was told to expect, but both halves came from the same party, so
	// this run trusted the mirror rather than merely using it for bandwidth.
	checksumFromMirror bool

	// archiveURL is where the archive actually came from, which is not always
	// where it was asked for: a dead mirror falls back to GitHub. Logged so
	// "the update was slow" can be answered without guessing.
	archiveURL string
}

// ArchiveURL is the URL the release archive was fetched from.
func (s *Staged) ArchiveURL() string { return s.archiveURL }

// ChecksumFromMirror reports whether this update's integrity rests on the
// mirror rather than on GitHub. Worth logging: it is the one case where a
// mirror could have substituted a binary.
func (s *Staged) ChecksumFromMirror() bool { return s.checksumFromMirror }

// Prepare downloads the release archive for this platform, checks it against
// the release's SHA256SUMS.txt, and unpacks the binary next to the running
// executable. Nothing is replaced yet, so a failure here leaves the panel — and
// any running server — completely untouched.
//
// progress, if non-nil, is called with the number of archive bytes fetched so
// far and the total when known.
func (u *Updater) Prepare(ctx context.Context, rel *Release, progress func(done, total int64)) (*Staged, error) {
	name := AssetName(rel.Version)
	assetURL, ok := rel.assets[name]
	if !ok {
		return nil, fmt.Errorf("%w (looked for %s)", ErrNoAsset, name)
	}

	want, checksumFromMirror, err := u.fetchChecksum(ctx, rel, name)
	if err != nil {
		return nil, err
	}

	exe, err := u.executable()
	if err != nil {
		return nil, err
	}

	// Staged alongside the executable: the final step is a rename, which is only
	// atomic within one filesystem. It doubles as an early check that the
	// install directory is writable, before anything is torn down.
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".hypercraft-update-*")
	if err != nil {
		return nil, fmt.Errorf("write to %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once renamed away
	}()

	archive, archiveURL, err := u.downloadVerified(ctx, u.bulkOrder(assetURL), want, progress)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archive)

	if err := extractBinary(archive, name, tmp); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return nil, err
	}

	staged := &Staged{
		path:               tmpName,
		exe:                exe,
		checksumFromMirror: checksumFromMirror,
		archiveURL:         archiveURL,
	}
	tmpName = "" // hand ownership to the caller; skip the deferred Remove
	return staged, nil
}

// fetchChecksum pulls SHA256SUMS.txt from the release and returns the digest
// recorded for asset, along with whether it had to come from the mirror
// because GitHub could not be reached.
func (u *Updater) fetchChecksum(ctx context.Context, rel *Release, asset string) (sum string, fromMirror bool, err error) {
	sumsURL, ok := rel.assets["SHA256SUMS.txt"]
	if !ok {
		return "", false, errors.New("release has no SHA256SUMS.txt, refusing to install an unverified binary")
	}
	body, _, used, err := u.getFirst(ctx, u.trustedOrder(sumsURL))
	if err != nil {
		return "", false, err
	}
	defer body.Close()
	fromMirror = used != sumsURL

	scanner := bufio.NewScanner(io.LimitReader(body, maxChecksumBytes))
	for scanner.Scan() {
		// Lines are "<hex>  <name>", the format sha256sum writes and reads.
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), fromMirror, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fromMirror, fmt.Errorf("read checksums: %w", err)
	}
	return "", fromMirror, fmt.Errorf("no checksum published for %s", asset)
}

// downloadVerified streams the asset to a temp file, hashing as it goes, and
// deletes it unless the digest matches.
func (u *Updater) downloadVerified(ctx context.Context, urls []string, want string, progress func(done, total int64)) (path, from string, err error) {
	body, total, from, err := u.getFirst(ctx, urls)
	if err != nil {
		return "", "", err
	}
	defer body.Close()

	tmp, err := os.CreateTemp("", "hypercraft-archive-*")
	if err != nil {
		return "", from, err
	}
	tmpName := tmp.Name()
	defer tmp.Close()

	hash := sha256.New()
	var done int64
	src := io.LimitReader(body, maxArchiveBytes+1)
	buf := make([]byte, 256<<10)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxArchiveBytes {
				os.Remove(tmpName)
				return "", from, fmt.Errorf("release archive is larger than %d bytes", int64(maxArchiveBytes))
			}
			if _, err := tmp.Write(buf[:n]); err != nil {
				os.Remove(tmpName)
				return "", from, err
			}
			hash.Write(buf[:n])
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			os.Remove(tmpName)
			return "", from, fmt.Errorf("download: %w", readErr)
		}
	}
	if err := tmp.Sync(); err != nil {
		os.Remove(tmpName)
		return "", from, err
	}

	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		os.Remove(tmpName)
		return "", from, fmt.Errorf("checksum mismatch: expected %s, got %s", want, got)
	}
	return tmpName, from, nil
}

// getFirst tries each URL in order and returns the first that answers, along
// with which one it was. Falling back like this is what makes a flaky mirror a
// slowdown rather than an outage.
func (u *Updater) getFirst(ctx context.Context, urls []string) (io.ReadCloser, int64, string, error) {
	var firstErr error
	for _, url := range urls {
		body, length, err := u.getWithLength(ctx, url)
		if err == nil {
			return body, length, url, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		// A cancelled context will fail every remaining URL the same way;
		// retrying just delays reporting it.
		if ctx.Err() != nil {
			break
		}
	}
	return nil, 0, "", firstErr
}

func (u *Updater) getWithLength(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("download %s: %s", path.Base(url), resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

// extractBinary copies the panel executable out of the release archive. Only
// the entry whose base name is the binary is read; the path recorded in the
// archive is otherwise ignored, so a crafted entry name cannot escape.
func extractBinary(archivePath, assetName string, dst io.Writer) error {
	binary := "hypercraft"
	if runtime.GOOS == "windows" {
		binary = "hypercraft.exe"
	}
	if strings.HasSuffix(assetName, ".zip") {
		return extractFromZip(archivePath, binary, dst)
	}
	return extractFromTarGz(archivePath, binary, dst)
}

func extractFromTarGz(archivePath, binary string, dst io.Writer) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != binary {
			continue
		}
		return copyCapped(dst, tr)
	}
	return fmt.Errorf("archive contains no %s", binary)
}

func extractFromZip(archivePath, binary string, dst io.Writer) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || path.Base(entry.Name) != binary {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return copyCapped(dst, rc)
	}
	return fmt.Errorf("archive contains no %s", binary)
}

// copyCapped refuses to write more than maxBinaryBytes, so a decompression bomb
// cannot fill the disk the servers live on.
func copyCapped(dst io.Writer, src io.Reader) error {
	n, err := io.Copy(dst, io.LimitReader(src, maxBinaryBytes+1))
	if err != nil {
		return err
	}
	if n > maxBinaryBytes {
		return fmt.Errorf("binary in archive is larger than %d bytes", int64(maxBinaryBytes))
	}
	if n == 0 {
		return errors.New("binary in archive is empty")
	}
	return nil
}

// Commit moves the staged binary over the running executable, keeping the old
// one as <exe>.old so a bad release can be rolled back by hand.
//
// Renaming the executable of a running process is allowed on both Unix and
// Windows: the running image is already mapped, and only the directory entry
// moves.
func (s *Staged) Commit() error {
	backup := s.exe + ".old"
	_ = os.Remove(backup)

	if err := os.Rename(s.exe, backup); err != nil {
		return fmt.Errorf("move the old binary aside: %w", err)
	}
	if err := os.Rename(s.path, s.exe); err != nil {
		// Put the working binary back before giving up, so the panel still
		// restarts into something that runs.
		if restoreErr := os.Rename(backup, s.exe); restoreErr != nil {
			return fmt.Errorf("install new binary: %w (and restoring the old one failed: %v; it is at %s)", err, restoreErr, backup)
		}
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

// Discard removes a staged binary that will not be installed.
func (s *Staged) Discard() { _ = os.Remove(s.path) }

// Path is the staged binary's location on disk.
func (s *Staged) Path() string { return s.path }

// Target is where the new binary is installed, and so what a restart must
// execute.
//
// It is resolved before Commit renames anything, which is the only time it can
// be resolved correctly: afterwards the running image's inode has been moved to
// <exe>.old, so os.Executable — which reads /proc/self/exe and therefore
// follows the inode — reports the backup's path. Re-deriving the path after
// the swap restarts the binary the update just replaced.
func (s *Staged) Target() string { return s.exe }

// currentExecutable resolves the running binary, following symlinks so the
// replacement lands on the real file rather than on a link to it.
func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}
