package schemlib

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// indexServer stands in for a published index: a manifest at /index.json and
// the files it points at beside it, so the relative-link resolution and the
// download are exercised the way a real source uses them.
func indexServer(t *testing.T, items []map[string]any) *httptest.Server {
	t.Helper()
	body := schem(t, "城堡")

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "测试索引", "items": items})
	})
	mux.HandleFunc("/castle.schem", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	return httptest.NewServer(mux)
}

func newMarket(t *testing.T, url string) *Market {
	t.Helper()
	market := NewMarket(t.TempDir(), "HyperCraft/test")
	// The builtin index would reach the real internet from a unit test.
	market.mu.Lock()
	market.loaded = true
	market.sources = []Source{{ID: "test", Name: "测试索引", Kind: SourceIndex, URL: url}}
	market.mu.Unlock()
	return market
}

func castleSum(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256(schem(t, "城堡"))
	return hex.EncodeToString(sum[:])
}

func TestIndexSourceIsRead(t *testing.T) {
	server := indexServer(t, []map[string]any{{
		"id":          "castle",
		"name":        "中世纪城堡",
		"author":      "somebody",
		"description": "带护城河",
		"tags":        []string{"中世纪", "城堡"},
		"gameVersion": "1.20+",
		// Relative on purpose: an index that sits beside its own files should
		// be able to say "castle.schem".
		"url":    "castle.schem",
		"width":  40,
		"height": 25,
		"length": 40,
	}})
	defer server.Close()

	market := newMarket(t, server.URL+"/index.json")
	catalogue := market.Read(context.Background(), false)

	if len(catalogue.Items) != 1 {
		t.Fatalf("items = %+v, notes = %v", catalogue.Items, catalogue.Notes)
	}
	item := catalogue.Items[0]
	if item.Name != "中世纪城堡" || item.Author != "somebody" || item.GameVersion != "1.20+" {
		t.Errorf("item = %+v", item)
	}
	if item.URL != server.URL+"/castle.schem" {
		t.Errorf("url = %q, want it resolved against the manifest", item.URL)
	}
	if item.FileName != "castle.schem" || item.Width != 40 {
		t.Errorf("item = %+v", item)
	}
	if len(catalogue.Notes) != 0 {
		t.Errorf("notes = %v, want none", catalogue.Notes)
	}
}

func TestInstallStoresTheBuildAndVerifiesItsDigest(t *testing.T) {
	server := indexServer(t, []map[string]any{{
		"id":          "castle",
		"name":        "中世纪城堡",
		"description": "带护城河",
		"tags":        []string{"中世纪"},
		"url":         "castle.schem",
		"sha256":      castleSum(t),
	}})
	defer server.Close()

	market := newMarket(t, server.URL+"/index.json")
	library := newLibrary(t)

	entry, item, err := market.Install(context.Background(), library, "test", "castle")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if item.Name != "中世纪城堡" {
		t.Errorf("item = %+v", item)
	}
	// Everything an upload gets: parsed, hashed, described.
	if entry.Facts.NonAir != 7 || entry.SHA256 != castleSum(t) {
		t.Errorf("entry = %+v", entry)
	}
	if entry.Origin.Kind != OriginMarket || entry.Origin.ItemID != "castle" {
		t.Errorf("origin = %+v", entry.Origin)
	}
	// The source's own account of the build is kept where the file says
	// nothing — a .schem carries no description and no tags.
	if entry.Note != "带护城河" || len(entry.Tags) != 1 {
		t.Errorf("note = %q, tags = %v", entry.Note, entry.Tags)
	}
	if len(library.List()) != 1 {
		t.Errorf("library holds %d entries, want 1", len(library.List()))
	}
}

func TestInstallRollsBackOnADigestMismatch(t *testing.T) {
	server := indexServer(t, []map[string]any{{
		"id":     "castle",
		"name":   "中世纪城堡",
		"url":    "castle.schem",
		"sha256": strings.Repeat("ab", 32),
	}})
	defer server.Close()

	market := newMarket(t, server.URL+"/index.json")
	library := newLibrary(t)

	_, _, err := market.Install(context.Background(), library, "test", "castle")
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Install = %v, want ErrDigestMismatch", err)
	}
	// Rolled back rather than kept with a warning: a file that does not match
	// the digest its own source published does not belong in the library.
	if got := library.List(); len(got) != 0 {
		t.Errorf("library holds %+v, want nothing", got)
	}
}

func TestAFailedSourceKeepsWhatItLastSaid(t *testing.T) {
	fail := false
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"id": "castle", "name": "城堡", "url": "castle.schem"},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	market := newMarket(t, server.URL+"/index.json")
	if got := market.Read(context.Background(), false); len(got.Items) != 1 {
		t.Fatalf("first read = %+v", got)
	}

	fail = true
	got := market.Read(context.Background(), true)
	// A source that times out once must not empty the page it was filling a
	// minute ago; the note is what says the list may be stale.
	if len(got.Items) != 1 {
		t.Errorf("items = %+v, want the previous read kept", got.Items)
	}
	if got.Notes["test"] == "" {
		t.Errorf("notes = %v, want one saying why", got.Notes)
	}
}

func TestSourcesAreNormalisedAndPersisted(t *testing.T) {
	root := t.TempDir()
	market := NewMarket(root, "HyperCraft/test")

	source, err := market.AddSource("", SourceGitHub, "https://github.com/someone/builds/tree/main/medieval", "")
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	// A pasted browser URL is reduced to what the API is asked for.
	if source.URL != "someone/builds@main:medieval" {
		t.Errorf("url = %q", source.URL)
	}
	if _, err := market.AddSource("", SourceGitHub, "someone/builds@main:medieval", ""); !errors.Is(err, ErrExists) {
		t.Errorf("second AddSource = %v, want ErrExists", err)
	}

	// The builtin source can be switched off but never removed: it would come
	// back on the next start and look like a bug.
	if err := market.RemoveSource(builtinSources[0].ID); !errors.Is(err, ErrBuiltinSource) {
		t.Errorf("RemoveSource(builtin) = %v, want ErrBuiltinSource", err)
	}
	off := true
	if _, err := market.UpdateSource(builtinSources[0].ID, "", &off); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}

	reopened := NewMarket(root, "HyperCraft/test")
	sources := reopened.Sources()
	if len(sources) != 2 {
		t.Fatalf("sources = %+v, want the builtin and the added one", sources)
	}
	if !sources[0].Builtin || !sources[0].Disabled {
		t.Errorf("builtin = %+v, want it kept and switched off", sources[0])
	}
	if sources[1].URL != "someone/builds@main:medieval" || sources[1].Builtin {
		t.Errorf("added = %+v", sources[1])
	}
}

func TestBadSourcesAreRefused(t *testing.T) {
	market := NewMarket(t.TempDir(), "HyperCraft/test")

	for _, raw := range []string{"", "not a repo", "ftp://example.com/index.json"} {
		if _, err := market.AddSource("", SourceIndex, raw, ""); !errors.Is(err, ErrBadSource) {
			t.Errorf("AddSource(index, %q) = %v, want ErrBadSource", raw, err)
		}
	}
	// A repository pasted into the index box is somebody who meant the other
	// kind, and the message says so.
	_, err := market.AddSource("", SourceIndex, "someone/builds", "")
	if err == nil || !strings.Contains(err.Error(), "GitHub 仓库") {
		t.Errorf("AddSource(index, repo) = %v, want a pointer at the other kind", err)
	}
}

func TestSplitRepoReadsEveryShape(t *testing.T) {
	cases := []struct{ in, repo, ref, dir string }{
		{"someone/builds", "someone/builds", "HEAD", ""},
		{"someone/builds@main", "someone/builds", "main", ""},
		{"someone/builds@main:medieval/keeps", "someone/builds", "main", "medieval/keeps"},
		{"someone/builds:medieval", "someone/builds", "HEAD", "medieval"},
		{"someone/builds/tree/v2/spawn", "someone/builds", "v2", "spawn"},
		{"someone", "", "", ""},
	}
	for _, want := range cases {
		repo, ref, dir := splitRepo(want.in)
		if repo != want.repo || ref != want.ref || dir != want.dir {
			t.Errorf("splitRepo(%q) = %q %q %q, want %q %q %q",
				want.in, repo, ref, dir, want.repo, want.ref, want.dir)
		}
	}
}

func TestFilterSearchesEveryFieldARowShows(t *testing.T) {
	items := []Item{
		{ID: "1", SourceID: "a", Name: "中世纪城堡", Tags: []string{"castle"}},
		{ID: "2", SourceID: "a", Name: "现代别墅", Description: "glass and concrete"},
		{ID: "3", SourceID: "b", Name: "农场", Author: "someone"},
	}

	if got := Filter(items, "castle", ""); len(got) != 1 || got[0].ID != "1" {
		t.Errorf("tag search = %+v", got)
	}
	if got := Filter(items, "GLASS", ""); len(got) != 1 || got[0].ID != "2" {
		t.Errorf("description search = %+v", got)
	}
	if got := Filter(items, "", "b"); len(got) != 1 || got[0].ID != "3" {
		t.Errorf("source filter = %+v", got)
	}
	if got := Filter(items, "", ""); len(got) != 3 {
		t.Errorf("empty query = %+v, want everything", got)
	}
}

func TestFolderTagsDropTheObviousOnes(t *testing.T) {
	got := folderTags("schematics/medieval/keeps/tower.schem")
	if len(got) != 2 || got[0] != "medieval" || got[1] != "keeps" {
		t.Errorf("folderTags = %v, want [medieval keeps]", got)
	}
	if got := folderTags("tower.schem"); len(got) != 0 {
		t.Errorf("folderTags(root) = %v, want none", got)
	}
}
