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

	"github.com/lanscarlos/hypercraft/internal/download"
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

// Channel is the set of releases a panel is willing to be offered.
type Channel string

const (
	// ChannelStable offers only published releases — the versions cut from
	// CHANGELOG.md. This is what an unconfigured panel uses.
	ChannelStable Channel = "stable"

	// ChannelSnapshot also offers the prereleases the snapshot workflow
	// publishes for every green commit on main. They pass CI but nothing else,
	// so this is opt-in and stays opt-in: nothing switches a panel to it.
	ChannelSnapshot Channel = "snapshot"
)

// ParseChannel reads a stored or user-supplied channel name, falling back to
// stable for anything it does not recognise — including the empty string a
// config written before channels existed carries.
func ParseChannel(s string) Channel {
	if Channel(strings.TrimSpace(s)) == ChannelSnapshot {
		return ChannelSnapshot
	}
	return ChannelStable
}

// Release is the subset of a GitHub release this package needs.
type Release struct {
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
	// Prerelease marks a release GitHub does not consider final, which for
	// this repository means a snapshot or an rc.
	Prerelease bool `json:"prerelease"`

	// assets maps asset name to download URL.
	assets map[string]string
}

// routeSet is the set in internal/download release downloads are routed
// through. The same one plugin jars use: they come off the same CDN.
const routeSet = "github"

// Updater checks for and installs releases of a single GitHub repository.
type Updater struct {
	repo    string // "owner/name"
	current string
	client  *http.Client

	// channel decides whether prereleases are candidates. See Check.
	channel Channel

	// apiBase points at GitHub; tests redirect it at an httptest server.
	apiBase string

	// mirror is the chosen route id, or a custom "https://…/" prefix. See
	// SetMirror for what it may carry.
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
		channel:        ChannelStable,
		mirror:         download.RouteAuto,
		apiBase:        "https://api.github.com",
		downloadPrefix: "https://github.com/",
	}
}

// CurrentVersion is the version of the running binary.
func (u *Updater) CurrentVersion() string { return u.current }

// SetChannel picks which releases Check considers.
func (u *Updater) SetChannel(c Channel) { u.channel = ParseChannel(string(c)) }

// Channel is the release channel this updater follows.
func (u *Updater) Channel() Channel { return u.channel }

// SetMirror chooses the route release downloads go through, by the id of one of
// internal/download's "github" routes or as a custom "https://…/" prefix.
//
// The table used to be written out here as a single bare prefix string — no
// list, no automatic order, no fallback — while the plugin shelf held a full
// copy of the same four proxies. They are one table now; what stays here is the
// rule below about what a mirror is and is not allowed to decide.
//
// A mirror carries the release archive, which is the megabytes and therefore
// the slow part. It does not get to decide what that archive should contain:
// the checksums are fetched from GitHub first and only fall back to the mirror
// if GitHub is unreachable, so serving a doctored binary requires breaking
// GitHub's TLS too. Nor does it carry the update check — the mirrors people use
// for this do not proxy api.github.com at all.
func (u *Updater) SetMirror(id string) error {
	// Empty means direct here, not automatic. That is this API's long-standing
	// contract — PUT /api/update/mirror with "" is how an operator turns the
	// proxy off, and config.UpdateMirror stores it as a deliberate choice
	// distinct from "never configured" — and it predates the shared table,
	// where empty means automatic. The mapping happens at this boundary rather
	// than by changing either side.
	if strings.TrimSpace(id) == "" {
		u.mirror = ""
		return nil
	}
	resolved, err := download.ResolveRoute(routeSet, id)
	if err != nil {
		return err
	}
	u.mirror = resolved
	return nil
}

// Mirror is the configured route id.
func (u *Updater) Mirror() string { return u.mirror }

// Mirrors lists what an operator can pick, automatic first.
func Mirrors() []download.Route {
	routes := download.RouteSets[routeSet].Routes
	out := make([]download.Route, 0, len(routes)+1)
	out = append(out, download.Route{
		ID:   download.RouteAuto,
		Name: "自动",
		Note: "按上面的顺序挨个试，哪个通用哪个",
	})
	return append(out, routes...)
}

// proxied is the mirror URLs for one release asset, most preferred first and
// without the origin. Empty when no route applies.
func (u *Updater) proxied(raw string) []string {
	if !strings.HasPrefix(raw, u.downloadPrefix) {
		// Only github.com URLs are rewritten: the proxies are GitHub-specific,
		// and prefixing anything else would just produce a 404. The test
		// updater points downloadPrefix at its own origin, which is what makes
		// the mirror/direct split observable there.
		return nil
	}
	chosen := u.mirror
	if chosen == "" {
		// Off. See SetMirror.
		return nil
	}
	var out []string
	for _, route := range download.RouteOrder(routeSet, chosen, download.Origin(raw)) {
		if route.Kind == download.RouteDirect {
			continue
		}
		if link := route.Link(download.Origin(raw)); link != "" {
			out = append(out, link)
		}
	}
	return out
}

// bulkOrder is the URL preference for the release archive: mirrors first,
// because speed is the whole point, falling back to GitHub if they fail.
func (u *Updater) bulkOrder(raw string) []string {
	return append(u.proxied(raw), raw)
}

// trustedOrder is the URL preference for the checksums: GitHub first, because
// they are what stops a mirror substituting its own binary, and they are small
// enough that fetching them slowly costs nothing. The mirrors remain a fallback
// so a blocked GitHub still leaves the panel updatable — with the mirror
// trusted for that run, which the caller reports.
//
// This is why the route table is consulted but not obeyed: RouteOrder puts the
// proxies first, which is right for the archive and exactly wrong here.
func (u *Updater) trustedOrder(raw string) []string {
	return append([]string{raw}, u.proxied(raw)...)
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

// releasePayload is the GitHub release JSON, in the fields this package reads.
type releasePayload struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (p releasePayload) release() *Release {
	rel := &Release{
		Tag:         p.TagName,
		Version:     NormalizeVersion(p.TagName),
		Notes:       p.Body,
		URL:         p.HTMLURL,
		PublishedAt: p.PublishedAt,
		Prerelease:  p.Prerelease,
		assets:      make(map[string]string, len(p.Assets)),
	}
	for _, a := range p.Assets {
		rel.assets[a.Name] = a.URL
	}
	return rel
}

// Check asks GitHub what this panel's channel currently offers.
//
// On the stable channel that is /releases/latest, which excludes prereleases
// and drafts at the endpoint itself, so a snapshot or an rc tag can never be
// offered by accident. The snapshot channel has to list releases instead and
// pick the highest version itself — including stable ones, so a panel tracking
// snapshots still moves to a release the moment it is newer than the snapshot
// it is running.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	if u.channel == ChannelSnapshot {
		return u.checkNewest(ctx)
	}
	return u.checkLatestStable(ctx)
}

func (u *Updater) checkLatestStable(ctx context.Context) (*Release, error) {
	var payload releasePayload
	if err := u.getJSON(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", u.apiBase, u.repo), &payload); err != nil {
		return nil, err
	}
	if payload.TagName == "" {
		return nil, errors.New("release has no tag")
	}
	return payload.release(), nil
}

// checkNewest picks the highest version this channel offers. One page is
// plenty: snapshots are pruned to a handful and releases are rare, so the
// newest of either is always near the top.
//
// It is the head of the same list the update page shows — one filter, one
// ordering, so what the page offers and what this installs cannot drift.
func (u *Updater) checkNewest(ctx context.Context) (*Release, error) {
	offered, err := u.offered(ctx)
	if err != nil {
		return nil, err
	}
	if len(offered) == 0 {
		return nil, errors.New("no published release found")
	}
	return offered[0], nil
}

func (u *Updater) getJSON(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github returned %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(into); err != nil {
		return fmt.Errorf("parse release: %w", err)
	}
	return nil
}

// Offer reports whether rel should be presented to the operator, and whether
// installing it would move the panel backwards.
//
// Backwards is only ever offered in one situation: the panel is running a
// snapshot and its channel has been set back to stable. The newest release is
// then older than what is running, and installing it is precisely what the
// operator asked for — without it, leaving the snapshot track would mean
// waiting for the next release or replacing the binary by hand.
func (u *Updater) Offer(rel *Release) (available, downgrade bool) {
	if rel == nil || !IsReleaseVersion(u.current) || !IsReleaseVersion(rel.Version) {
		return false, false
	}
	switch cmp := CompareVersions(rel.Version, u.current); {
	case cmp > 0:
		return true, false
	case cmp < 0 && u.channel == ChannelStable && !IsStableVersion(u.current):
		return true, true
	}
	return false, false
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
