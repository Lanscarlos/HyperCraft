package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// catalogEntry is one release in the fake repository below.
type catalogEntry struct {
	version    string
	prerelease bool
	draft      bool
	// noAsset publishes the release without a build for this platform, which
	// is what a release cut before a platform was added looks like.
	noAsset bool
	binary  []byte
}

// newFakeCatalog serves the releases listing with archives that can actually be
// downloaded and verified, so installing a chosen version can be tested through
// the real download path rather than around it.
func newFakeCatalog(t *testing.T, entries ...catalogEntry) *httptest.Server {
	t.Helper()

	archives := map[string][]byte{}
	for _, e := range entries {
		body := e.binary
		if body == nil {
			body = []byte("binary of " + e.version)
		}
		archives[e.version] = (&fakeRelease{t: t, version: e.version, binary: body}).archive()
	}

	mux := http.NewServeMux()
	var server *httptest.Server

	mux.HandleFunc("/repos/owner/repo/releases", func(w http.ResponseWriter, r *http.Request) {
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			assets := []map[string]string{
				{"name": "SHA256SUMS.txt", "browser_download_url": server.URL + "/dl/" + e.version + "/sums"},
			}
			if !e.noAsset {
				assets = append(assets, map[string]string{
					"name":                 AssetName(e.version),
					"browser_download_url": server.URL + "/dl/" + e.version + "/archive",
				})
			}
			out = append(out, map[string]any{
				"tag_name":     "v" + e.version,
				"name":         "v" + e.version,
				"body":         "notes for " + e.version,
				"html_url":     "https://example.invalid/releases/v" + e.version,
				"published_at": "2026-08-08T00:00:00Z",
				"draft":        e.draft,
				"prerelease":   e.prerelease,
				"assets":       assets,
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		// /dl/<version>/<archive|sums>
		version := filepath.Base(filepath.Dir(r.URL.Path))
		body, ok := archives[version]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if filepath.Base(r.URL.Path) == "sums" {
			sum := sha256.Sum256(body)
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), AssetName(version))
			return
		}
		_, _ = w.Write(body)
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func catalogUpdater(t *testing.T, server *httptest.Server, current string, channel Channel) *Updater {
	t.Helper()
	u := New("owner/repo", current)
	u.apiBase = server.URL
	u.downloadPrefix = server.URL
	u.SetChannel(channel)
	return u
}

func TestListOffersOnlyWhatTheChannelOffers(t *testing.T) {
	// The list is another way into the same decision the update button makes,
	// so it has to obey the channel: a panel on the stable channel must not be
	// able to reach a snapshot by picking it from a list.
	server := newFakeCatalog(t,
		catalogEntry{version: "0.4-snapshot.90", prerelease: true},
		catalogEntry{version: "0.3.0"},
		catalogEntry{version: "0.2.0"},
	)

	stable, err := catalogUpdater(t, server, "v0.3.0", ChannelStable).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, v := range stable {
		if v.Prerelease {
			t.Errorf("the stable channel listed a prerelease: %s", v.Version)
		}
	}
	if len(stable) != 2 {
		t.Errorf("stable listed %d versions, want 2: %+v", len(stable), stable)
	}

	snapshot, err := catalogUpdater(t, server, "v0.3.0", ChannelSnapshot).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snapshot) != 3 {
		t.Errorf("the snapshot channel listed %d versions, want all 3: %+v", len(snapshot), snapshot)
	}
}

func TestListIsNewestFirstAndMarksTheRunningBuild(t *testing.T) {
	// The UI cannot compare versions — it has no semver of its own — so the
	// ordering and every "is this backwards" judgement are made here.
	server := newFakeCatalog(t,
		catalogEntry{version: "0.2.0"},
		catalogEntry{version: "0.10.0"},
		catalogEntry{version: "0.9.0"},
	)

	list, err := catalogUpdater(t, server, "v0.9.0", ChannelStable).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var order []string
	for _, v := range list {
		order = append(order, v.Version)
	}
	want := []string{"0.10.0", "0.9.0", "0.2.0"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("listed %v, want newest first %v", order, want)
		}
	}

	for _, v := range list {
		switch v.Version {
		case "0.9.0":
			if !v.Current || v.Downgrade {
				t.Errorf("the running build is marked current=%v downgrade=%v", v.Current, v.Downgrade)
			}
		case "0.10.0":
			if v.Current || v.Downgrade {
				t.Errorf("a newer release is marked current=%v downgrade=%v", v.Current, v.Downgrade)
			}
		case "0.2.0":
			if !v.Downgrade {
				t.Error("an older release is not marked as going backwards")
			}
		}
	}
}

func TestListSkipsDraftsAndMarksMissingBuilds(t *testing.T) {
	server := newFakeCatalog(t,
		catalogEntry{version: "0.5.0", draft: true},
		catalogEntry{version: "0.4.0", noAsset: true},
		catalogEntry{version: "0.3.0"},
	)

	list, err := catalogUpdater(t, server, "v0.3.0", ChannelStable).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, v := range list {
		if v.Version == "0.5.0" {
			t.Error("a draft was listed as installable")
		}
		if v.Version == "0.4.0" && v.Installable {
			t.Error("a release with no build for this platform was offered as installable")
		}
		if v.Version == "0.3.0" && !v.Installable {
			t.Error("a release with a build for this platform was not installable")
		}
	}
}

func TestApplyVersionInstallsTheChosenRelease(t *testing.T) {
	// Not the newest one: picking an older build from the list is the whole
	// point of this path.
	want := []byte("binary of 0.2.0")
	server := newFakeCatalog(t,
		catalogEntry{version: "0.4.0"},
		catalogEntry{version: "0.3.0"},
		catalogEntry{version: "0.2.0", binary: want},
	)

	var backedUp bool
	svc := NewService("owner/repo", "v0.3.0", "", ChannelStable, Hooks{
		StopServers:    func(context.Context, func(Shutdown)) error { return nil },
		TriggerRestart: func(string) {},
		BackupState: func(string, string) (string, error) {
			backedUp = true
			return filepath.Join(t.TempDir(), "backup"), nil
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.up.apiBase = server.URL
	svc.up.downloadPrefix = server.URL

	exe := filepath.Join(t.TempDir(), "hypercraft")
	if err := os.WriteFile(exe, []byte("the running binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc.up.exePath = exe

	if err := svc.ApplyVersion(context.Background(), "0.2.0"); err != nil {
		t.Fatalf("ApplyVersion: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("installed %q, want the chosen build %q", got, want)
	}
	if !backedUp {
		t.Error("installing an older version took no backup")
	}
}

func TestApplyVersionRefusesWhatTheChannelDoesNotOffer(t *testing.T) {
	// Otherwise the list would be a way around the channel: a stable panel
	// could be pointed at a snapshot tag by anyone who can call the API.
	server := newFakeCatalog(t,
		catalogEntry{version: "0.4-snapshot.90", prerelease: true},
		catalogEntry{version: "0.3.0"},
	)

	svc := NewService("owner/repo", "v0.3.0", "", ChannelStable, Hooks{
		StopServers:    func(context.Context, func(Shutdown)) error { return nil },
		TriggerRestart: func(string) {},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.up.apiBase = server.URL
	svc.up.downloadPrefix = server.URL

	exe := filepath.Join(t.TempDir(), "hypercraft")
	if err := os.WriteFile(exe, []byte("the running binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc.up.exePath = exe

	if err := svc.ApplyVersion(context.Background(), "0.4-snapshot.90"); err == nil {
		t.Fatal("ApplyVersion installed a version the channel does not offer")
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "the running binary" {
		t.Errorf("the binary was touched: %q (%v)", got, err)
	}
	if phase := svc.Status().Phase; phase != PhaseIdle {
		t.Errorf("Phase = %q, want idle after a refusal", phase)
	}
}

func TestApplyVersionRefusesAnUnknownVersion(t *testing.T) {
	server := newFakeCatalog(t, catalogEntry{version: "0.3.0"})

	svc := NewService("owner/repo", "v0.3.0", "", ChannelStable, Hooks{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.up.apiBase = server.URL
	svc.up.downloadPrefix = server.URL

	if err := svc.ApplyVersion(context.Background(), "9.9.9"); err == nil {
		t.Fatal("ApplyVersion accepted a version that is not published")
	}
}
