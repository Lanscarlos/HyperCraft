package selfupdate

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// publishedRelease is one entry in the fake repository below. Only the fields
// that decide which release gets offered are modelled; the download path is
// covered by the tests in selfupdate_test.go.
type publishedRelease struct {
	version    string
	prerelease bool
	draft      bool
}

// fakeRepo serves both release endpoints for a set of published releases: the
// /latest one GitHub filters down to the newest full release, and the listing
// the snapshot channel has to sort through itself.
type fakeRepo struct {
	server *httptest.Server
}

func newFakeRepo(t *testing.T, releases ...publishedRelease) *fakeRepo {
	t.Helper()
	repo := &fakeRepo{}

	body := func(r publishedRelease) map[string]any {
		return map[string]any{
			"tag_name":     "v" + r.version,
			"name":         "v" + r.version,
			"body":         "notes for " + r.version,
			"html_url":     "https://example.invalid/releases/v" + r.version,
			"published_at": "2026-08-08T00:00:00Z",
			"draft":        r.draft,
			"prerelease":   r.prerelease,
			"assets": []map[string]string{
				{"name": AssetName(r.version), "browser_download_url": "https://example.invalid/a"},
				{"name": "SHA256SUMS.txt", "browser_download_url": "https://example.invalid/s"},
			},
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases", func(w http.ResponseWriter, r *http.Request) {
		out := make([]map[string]any, 0, len(releases))
		for _, rel := range releases {
			out = append(out, body(rel))
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		// GitHub's own filter: the newest release that is neither a draft nor
		// a prerelease. The list is given newest-first, as the API returns it.
		for _, rel := range releases {
			if rel.draft || rel.prerelease {
				continue
			}
			_ = json.NewEncoder(w).Encode(body(rel))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	})

	repo.server = httptest.NewServer(mux)
	t.Cleanup(repo.server.Close)
	return repo
}

func (f *fakeRepo) updater(current string, channel Channel) *Updater {
	u := New("owner/repo", current)
	u.apiBase = f.server.URL
	u.SetChannel(channel)
	return u
}

// offered runs a check and reports what the panel would do with the result.
func offered(t *testing.T, u *Updater) (version string, available, downgrade bool) {
	t.Helper()
	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	available, downgrade = u.Offer(rel)
	return rel.Version, available, downgrade
}

func TestStableChannelNeverSeesASnapshot(t *testing.T) {
	repo := newFakeRepo(t,
		publishedRelease{version: "1.2.1-snapshot.431", prerelease: true},
		publishedRelease{version: "1.2.0"},
	)

	version, available, _ := offered(t, repo.updater("v1.2.0", ChannelStable))
	if version != "1.2.0" {
		t.Errorf("stable channel offered %q, want the release 1.2.0", version)
	}
	if available {
		t.Error("stable channel offered an update to a panel already on the latest release")
	}
}

func TestSnapshotChannelOffersTheNewestSnapshot(t *testing.T) {
	repo := newFakeRepo(t,
		publishedRelease{version: "1.2.1-snapshot.431", prerelease: true},
		publishedRelease{version: "1.2.1-snapshot.428", prerelease: true},
		publishedRelease{version: "1.2.0"},
	)

	version, available, downgrade := offered(t, repo.updater("v1.2.0", ChannelSnapshot))
	if version != "1.2.1-snapshot.431" {
		t.Errorf("offered %q, want the highest snapshot 1.2.1-snapshot.431", version)
	}
	if !available || downgrade {
		t.Errorf("offer of %q: available=%v downgrade=%v, want true/false", version, available, downgrade)
	}
}

func TestSnapshotChannelPrefersAReleaseThatOvertookTheSnapshots(t *testing.T) {
	// The release that the snapshots were leading up to has shipped. A panel on
	// the snapshot channel must move onto it rather than sitting on a snapshot
	// of code that is now released.
	repo := newFakeRepo(t,
		publishedRelease{version: "1.2.1"},
		publishedRelease{version: "1.2.1-snapshot.431", prerelease: true},
		publishedRelease{version: "1.2.0"},
	)

	version, available, _ := offered(t, repo.updater("v1.2.1-snapshot.431", ChannelSnapshot))
	if version != "1.2.1" {
		t.Errorf("offered %q, want the release 1.2.1", version)
	}
	if !available {
		t.Error("a panel on a superseded snapshot was not offered the release")
	}
}

func TestSnapshotChannelDoesNotGoBackwards(t *testing.T) {
	repo := newFakeRepo(t,
		publishedRelease{version: "1.2.1-snapshot.428", prerelease: true},
		publishedRelease{version: "1.2.0"},
	)

	_, available, _ := offered(t, repo.updater("v1.2.1-snapshot.431", ChannelSnapshot))
	if available {
		t.Error("offered an older snapshot to a panel running a newer one")
	}
}

func TestStableChannelOffersTheWayBackFromASnapshot(t *testing.T) {
	// Someone tried a snapshot and switched the channel back. The newest
	// release is older than what they are running, and offering it anyway is
	// the only way off the snapshot track short of replacing the binary.
	repo := newFakeRepo(t,
		publishedRelease{version: "1.2.1-snapshot.431", prerelease: true},
		publishedRelease{version: "1.2.0"},
	)

	version, available, downgrade := offered(t, repo.updater("v1.2.1-snapshot.431", ChannelStable))
	if version != "1.2.0" {
		t.Fatalf("offered %q, want the release 1.2.0", version)
	}
	if !available || !downgrade {
		t.Errorf("offer of 1.2.0: available=%v downgrade=%v, want true/true", available, downgrade)
	}
}

func TestSnapshotChannelSkipsDraftsAndUncomparableTags(t *testing.T) {
	repo := newFakeRepo(t,
		publishedRelease{version: "9.9.9", draft: true},
		publishedRelease{version: "nightly-20260808", prerelease: true},
		publishedRelease{version: "1.2.1-snapshot.431", prerelease: true},
		publishedRelease{version: "1.2.0"},
	)

	version, _, _ := offered(t, repo.updater("v1.2.0", ChannelSnapshot))
	if version != "1.2.1-snapshot.431" {
		t.Errorf("offered %q; a draft or an uncomparable tag was treated as publishable", version)
	}
}

func TestSwitchingChannelDiscardsTheCachedCheck(t *testing.T) {
	// The cached result describes the channel that was just left. Keeping it
	// would show a panel a snapshot as "available" immediately after it asked
	// to stop being offered them.
	repo := newFakeRepo(t,
		publishedRelease{version: "1.2.1-snapshot.431", prerelease: true},
		publishedRelease{version: "1.2.0"},
	)
	svc := NewService("owner/repo", "v1.2.0", "", ChannelSnapshot, Hooks{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.up.apiBase = repo.server.URL

	if _, err := svc.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st := svc.Status(); !st.UpdateAvailable || !st.LatestIsPrerelease {
		t.Fatalf("snapshot channel status = %+v, want an available prerelease", st)
	}

	if err := svc.SetChannel(ChannelStable); err != nil {
		t.Fatalf("SetChannel: %v", err)
	}
	st := svc.Status()
	if st.Channel != ChannelStable {
		t.Errorf("Status().Channel = %q, want stable", st.Channel)
	}
	if st.LatestVersion != "" || st.UpdateAvailable {
		t.Errorf("status after the switch still carries the snapshot: %+v", st)
	}

	if _, err := svc.Check(context.Background()); err != nil {
		t.Fatalf("Check after the switch: %v", err)
	}
	if st := svc.Status(); st.LatestVersion != "1.2.0" || st.UpdateAvailable {
		t.Errorf("stable status = %+v, want 1.2.0 with no update available", st)
	}
}

func TestStatusMarksASnapshotBuildAsOne(t *testing.T) {
	for _, c := range []struct {
		version string
		want    bool
	}{
		{"v1.2.0", false},
		{"v1.2.1-snapshot.431", true},
		{"v1.2.1-rc.1", true},
		// A dev build is not a snapshot; it has no version at all, and the UI
		// already reports that through Eligible.
		{"dev", false},
	} {
		svc := NewService("owner/repo", c.version, "", ChannelStable, Hooks{},
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		if got := svc.Status().CurrentIsSnapshot; got != c.want {
			t.Errorf("running %s: CurrentIsSnapshot = %v, want %v", c.version, got, c.want)
		}
	}
}

func TestParseChannelFallsBackToStable(t *testing.T) {
	for _, in := range []string{"", "stable", "beta", "SNAPSHOT", "nonsense"} {
		if got := ParseChannel(in); got != ChannelStable {
			t.Errorf("ParseChannel(%q) = %q, want stable", in, got)
		}
	}
	if got := ParseChannel(" snapshot "); got != ChannelSnapshot {
		t.Errorf("ParseChannel(\" snapshot \") = %q, want snapshot", got)
	}
}
