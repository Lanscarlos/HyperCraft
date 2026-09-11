package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Version is one release this panel could install, as the update page lists
// them.
//
// Current and Downgrade are decided here rather than in the browser: the UI has
// no way to compare two versions — that is CompareVersions' job, and a
// snapshot's ordering in particular is a rule, not a string comparison.
type Version struct {
	Version     string    `json:"version"`
	Tag         string    `json:"tag"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt,omitempty"`
	Prerelease  bool      `json:"prerelease"`
	// Installable is false when the release carries no build for this
	// platform — a release cut before the platform existed, or one whose
	// upload failed. Listed anyway, because "this version exists but not for
	// your machine" is worth seeing.
	Installable bool `json:"installable"`
	// Current marks the running build, so the list can say where you are.
	Current bool `json:"current"`
	// Downgrade marks a version older than the running one: installing it
	// moves backwards and takes a backup first.
	Downgrade bool `json:"downgrade"`
}

// List reports the versions the panel's channel offers, newest first.
//
// Same filter as Check: the stable channel sees only finished releases, the
// snapshot channel sees those and the snapshots. A list that ignored the
// channel would be a way around it — picking a snapshot from a list is still
// installing a snapshot.
func (u *Updater) List(ctx context.Context) ([]Version, error) {
	releases, err := u.offered(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Version, 0, len(releases))
	for _, rel := range releases {
		out = append(out, Version{
			Version:     rel.Version,
			Tag:         rel.Tag,
			URL:         rel.URL,
			PublishedAt: rel.PublishedAt,
			Prerelease:  rel.Prerelease,
			Installable: rel.HasAssetForPlatform(),
			Current:     CompareVersions(rel.Version, u.current) == 0,
			Downgrade:   CompareVersions(rel.Version, u.current) < 0,
		})
	}
	return out, nil
}

// Find returns the release for one version, provided this panel's channel
// offers it.
//
// Going through the same listing as List is what keeps the channel meaningful:
// the version arrives from a request, and fetching it by tag directly would
// install whatever tag was asked for.
func (u *Updater) Find(ctx context.Context, version string) (*Release, error) {
	releases, err := u.offered(ctx)
	if err != nil {
		return nil, err
	}
	want := NormalizeVersion(version)
	for _, rel := range releases {
		if NormalizeVersion(rel.Version) == want {
			return rel, nil
		}
	}
	return nil, fmt.Errorf("当前更新通道里没有 %s 这个版本", version)
}

// offered lists the releases this channel may install, newest first, dropping
// drafts and anything whose tag cannot be compared.
func (u *Updater) offered(ctx context.Context) ([]*Release, error) {
	var payload []releasePayload
	if err := u.getJSON(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=30", u.apiBase, u.repo), &payload); err != nil {
		return nil, err
	}

	var out []*Release
	for _, p := range payload {
		if p.Draft || !IsReleaseVersion(NormalizeVersion(p.TagName)) {
			continue
		}
		rel := p.release()
		// The stable channel is defined by what /releases/latest returns, and
		// this endpoint does not filter, so the same rule is applied here.
		if u.channel == ChannelStable && !IsStableVersion(rel.Version) {
			continue
		}
		out = append(out, rel)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return CompareVersions(out[i].Version, out[j].Version) > 0
	})
	return out, nil
}

// ApplyVersion installs one version chosen from List instead of whatever is
// newest, and is otherwise the ordinary update: the same download, the same
// verification, the same shutdown, and the same backup when it moves backwards.
func (s *Service) ApplyVersion(ctx context.Context, version string) error {
	s.mu.Lock()
	if s.phase != PhaseIdle {
		s.mu.Unlock()
		return ErrBusy
	}
	if !IsReleaseVersion(s.up.CurrentVersion()) {
		// A build the updater cannot place should not be replaced by one
		// picked from a list either — see Status.IneligibleWhy.
		why := s.statusLocked().IneligibleWhy
		s.mu.Unlock()
		return errors.New(why)
	}
	// Held only for the phase guard above. The lookup below reaches GitHub, and
	// the status the UI polls must not queue behind it.
	s.phase = PhaseChecking
	s.mu.Unlock()

	rel, err := s.up.Find(ctx, version)
	if err == nil && !rel.HasAssetForPlatform() {
		err = fmt.Errorf("%s 没有提供适用于这台机器的构建", rel.Version)
	}
	if err != nil {
		s.mu.Lock()
		s.phase = PhaseIdle
		s.mu.Unlock()
		return err
	}

	s.mu.Lock()
	s.phase = PhaseDownloading
	s.progress = 0
	s.lastErr = ""
	s.shutdown = nil
	s.backupDir = ""
	s.mu.Unlock()

	if err := s.apply(ctx, rel); err != nil {
		s.mu.Lock()
		s.phase = PhaseIdle
		s.progress = 0
		s.shutdown = nil
		s.lastErr = err.Error()
		s.mu.Unlock()
		s.log.Error("update to a chosen version failed", "version", rel.Version, "err", err)
		return err
	}
	return nil
}

// Versions is the update page's list, fetched on demand rather than cached:
// it is opened rarely, and a stale list is a list that offers a release that
// has since been pruned.
func (s *Service) Versions(ctx context.Context) ([]Version, error) {
	return s.up.List(ctx)
}
