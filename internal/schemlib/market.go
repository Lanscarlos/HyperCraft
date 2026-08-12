package schemlib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 建筑市场: where builds come from when they do not come from your own disk.
//
// There is no registry for schematics the way there is for plugins. Modrinth,
// Hangar and SpigotMC all publish jars and none of them publish builds; the
// sites that do — Planet Minecraft, minecraft-schematics.com — have no API to
// read and terms that do not invite one being invented. Scraping them would put
// the panel one page redesign away from broken, on somebody else's schedule.
//
// So the market is federated rather than central: a source is either a JSON
// index somebody publishes, or a GitHub repository with .schem files in it,
// and the panel reads both. That covers how builds are actually shared today
// (a repository, a link to a manifest in a community's docs) and it means a
// server group can point their panels at their own index and have their own
// builds in the market next to everything else.
//
// What a source never gets is trust. Every download is size-capped, checksum-
// verified when the source published one, and parsed before it is stored —
// exactly like an upload. A schematic is data rather than code, so the risk it
// carries is a file that eats memory, and that is what the caps are for.

var (
	// ErrSourceNotFound is returned for a market source that is not configured.
	ErrSourceNotFound = errors.New("market source not found")
	// ErrItemNotFound is returned for a build the source's catalogue does not
	// list — usually a stale page whose source has been re-published since.
	ErrItemNotFound = errors.New("this build is no longer listed by its source")
	// ErrBadSource rejects a source the panel cannot read at all.
	ErrBadSource = errors.New("invalid market source")
	// ErrDigestMismatch means the bytes did not match the checksum the source
	// published. The download is discarded.
	ErrDigestMismatch = errors.New("downloaded file does not match its checksum")
	// ErrBuiltinSource is returned when the operator tries to delete a source
	// that ships with the panel. It can be switched off; it cannot be removed,
	// because it would come back on the next start and look like a bug.
	ErrBuiltinSource = errors.New("this source ships with the panel")
)

// Source kinds.
const (
	// SourceIndex is a JSON manifest listing builds. See indexManifest.
	SourceIndex = "index"
	// SourceGitHub is a repository read through the GitHub API: every .schem
	// in its tree becomes an item. This is the one that needs no cooperation
	// from whoever published the builds, which is why it exists.
	SourceGitHub = "github"
)

const (
	// sourcesName holds the operator's market sources, beside the library they
	// download into.
	sourcesName = "sources.json"
	// catalogueTTL is how long a source's listing is reused before it is read
	// again. A schematic index changes when somebody publishes a build, which
	// is not something anybody is waiting on to the minute — and the GitHub API
	// allows 60 anonymous calls an hour, which one impatient page could spend.
	catalogueTTL = 30 * time.Minute
	// fetchTimeout bounds reading one source's catalogue.
	fetchTimeout = 30 * time.Second
	// maxManifestBytes caps an index manifest. A thousand builds with
	// descriptions is a few hundred kilobytes; the cap is there so a hostile or
	// broken response cannot exhaust memory.
	maxManifestBytes = 8 << 20
	// maxTreeItems caps how many files one GitHub repository contributes. A
	// repository with more .schem files than this is a world dump, and the
	// listing would be unreadable long before it got there.
	maxTreeItems = 500
)

// Source is one place the market reads builds from.
type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// URL is the manifest link for an index source. For a GitHub source it is
	// the repository, as "owner/name", optionally with "@ref" and ":path" —
	// "someone/builds@main:medieval" reads one folder of one branch.
	URL string `json:"url"`
	// Note is what the source says about itself, or what the operator wrote
	// when adding it.
	Note string `json:"note,omitempty"`
	// Builtin marks a source that ships with the panel. It can be switched off
	// but not deleted.
	Builtin  bool `json:"builtin,omitempty"`
	Disabled bool `json:"disabled,omitempty"`
}

// Item is one build a source offers. It is not in the library: everything here
// comes from the source's own claims, and none of it is verified until the file
// is downloaded and parsed.
type Item struct {
	ID       string `json:"id"`
	SourceID string `json:"sourceId"`
	// Source is the source's display name, carried so a row can say where it
	// came from without the UI joining two lists.
	Source      string   `json:"source"`
	Name        string   `json:"name"`
	Author      string   `json:"author,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// GameVersion is what the publisher says this build was made for. Free text
	// — "1.20+", "1.16.5" — because that is how people write it, and the panel
	// has nothing to check it against until the file is parsed.
	GameVersion string `json:"gameVersion,omitempty"`
	FileName    string `json:"fileName"`
	Size        int64  `json:"size,omitempty"`
	// Width, Height and Length are the publisher's claim about the region, for
	// sorting and for the card. Zero when unknown, which is the normal case for
	// a GitHub repository, where nothing but the file itself knows.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	Length int `json:"length,omitempty"`
	// URL is where the file is fetched from, SHA256 what it should hash to.
	URL    string `json:"-"`
	SHA256 string `json:"sha256,omitempty"`
	// Page is a human link — the repository, the build's own page — so an
	// operator can go and read about it before taking it.
	Page string `json:"page,omitempty"`
}

// Catalogue is one read of every enabled source.
type Catalogue struct {
	Sources []Source `json:"sources"`
	Items   []Item   `json:"items"`
	// Notes says, per source id, why it contributed nothing. Rendered under the
	// results rather than as an error: the other sources answered.
	Notes map[string]string `json:"notes,omitempty"`
	// FetchedAt is the oldest cache entry behind this answer, so the page can
	// say how fresh it is rather than pretending it is live.
	FetchedAt time.Time `json:"fetchedAt,omitempty"`
}

// builtinSources ship with the panel.
//
// One, and it is the project's own index. It is a real URL that may hold
// nothing yet — a 404 is reported as "this index has not published anything",
// not as an error, because a market whose only source is broken on first open
// reads as the feature being broken. Everything else an operator wants is one
// 添加索引源 away, and a GitHub repository of builds needs no cooperation from
// whoever published it.
var builtinSources = []Source{{
	ID:      "hypercraft",
	Name:    "HyperCraft 建筑索引",
	Kind:    SourceIndex,
	URL:     "https://raw.githubusercontent.com/Lanscarlos/hypercraft-schematics/HEAD/index.json",
	Note:    "面板自带的社区索引",
	Builtin: true,
}}

type cached struct {
	items     []Item
	note      string
	fetchedAt time.Time
}

// Market reads the configured sources and downloads from them.
type Market struct {
	root      string
	userAgent string
	client    *http.Client

	mu      sync.Mutex
	sources []Source
	loaded  bool
	cache   map[string]cached

	// token authenticates GitHub API reads, lifting the anonymous 60 calls an
	// hour and making a private repository of builds readable. Optional, and
	// only ever sent to api.github.com — never to a download mirror, which is a
	// third party the operator's credential has no business reaching.
	token string
	// mirrors are URL prefixes tried in order for a GitHub download, where ""
	// is the direct link. Supplied by the panel so this package does not need
	// its own opinion about which proxies are up this month.
	mirrors []string
}

func NewMarket(root, userAgent string) *Market {
	return &Market{
		root:      root,
		userAgent: userAgent,
		client:    &http.Client{Timeout: fetchTimeout},
		cache:     map[string]cached{},
	}
}

// SetToken sets the GitHub credential used for repository sources.
func (m *Market) SetToken(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = strings.TrimSpace(token)
}

// SetMirrors sets the URL prefixes a GitHub download is tried through, most
// preferred first. An empty string is the direct link, and one is always
// appended: a proxy that is down must not turn a working download into a
// failure, and there is nothing a proxy has that GitHub does not.
func (m *Market) SetMirrors(prefixes []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mirrors = append(append([]string{}, prefixes...), "")
}

// Sources lists what the market reads, builtin first.
func (m *Market) Sources() []Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Source{}, m.load()...)
}

// AddSource starts reading a new place. The URL is normalised first, so a
// pasted browser link to a repository becomes an owner/name pair before it is
// stored.
func (m *Market) AddSource(name, kind, raw, note string) (Source, error) {
	kind, raw, err := normaliseSource(kind, raw)
	if err != nil {
		return Source{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	sources := m.load()

	for _, existing := range sources {
		if existing.Kind == kind && strings.EqualFold(existing.URL, raw) {
			return Source{}, existsError{fmt.Sprintf("%s 已经添加过了，叫「%s」。", raw, existing.Name)}
		}
	}
	if name = strings.TrimSpace(name); name == "" {
		name = defaultSourceName(kind, raw)
	}

	source := Source{
		ID:   freeSourceID(sources, name, raw),
		Name: name,
		Kind: kind,
		URL:  raw,
		Note: strings.TrimSpace(note),
	}
	m.sources = append(sources, source)
	if err := m.saveSources(); err != nil {
		return Source{}, err
	}
	return source, nil
}

// UpdateSource renames a source or switches it off. A nil disabled leaves the
// switch alone, so renaming from one form and toggling from a row cannot revert
// each other.
func (m *Market) UpdateSource(id, name string, disabled *bool) (Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sources := m.load()

	for i := range sources {
		if sources[i].ID != id {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			sources[i].Name = name
		}
		if disabled != nil {
			sources[i].Disabled = *disabled
		}
		if err := m.saveSources(); err != nil {
			return Source{}, err
		}
		// A source that was just switched back on should be read rather than
		// answered from whatever it said before it was switched off.
		delete(m.cache, id)
		return sources[i], nil
	}
	return Source{}, fmt.Errorf("%w: %s", ErrSourceNotFound, id)
}

// RemoveSource stops reading a place. A builtin source can only be disabled.
func (m *Market) RemoveSource(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sources := m.load()

	for i, source := range sources {
		if source.ID != id {
			continue
		}
		if source.Builtin {
			return fmt.Errorf("%w: %s", ErrBuiltinSource, source.Name)
		}
		m.sources = append(sources[:i:i], sources[i+1:]...)
		delete(m.cache, id)
		return m.saveSources()
	}
	return fmt.Errorf("%w: %s", ErrSourceNotFound, id)
}

// Read returns everything the enabled sources offer.
//
// Sources are read one after another rather than in parallel: there are a
// handful of them, each is one request, and a page that answers in half a
// second either way is not worth the failure modes of a fan-out. A source that
// fails contributes a note and nothing else — the others answered, and losing
// the whole market because one index moved is the outcome this avoids.
func (m *Market) Read(ctx context.Context, refresh bool) Catalogue {
	m.mu.Lock()
	sources := append([]Source{}, m.load()...)
	m.mu.Unlock()

	out := Catalogue{Sources: sources, Notes: map[string]string{}}
	for _, source := range sources {
		if source.Disabled {
			continue
		}
		items, note, fetchedAt := m.readSource(ctx, source, refresh)
		if note != "" {
			out.Notes[source.ID] = note
		}
		out.Items = append(out.Items, items...)
		if !fetchedAt.IsZero() && (out.FetchedAt.IsZero() || fetchedAt.Before(out.FetchedAt)) {
			out.FetchedAt = fetchedAt
		}
	}

	sort.SliceStable(out.Items, func(a, b int) bool {
		if out.Items[a].SourceID != out.Items[b].SourceID {
			return out.Items[a].SourceID < out.Items[b].SourceID
		}
		return strings.ToLower(out.Items[a].Name) < strings.ToLower(out.Items[b].Name)
	})
	if len(out.Notes) == 0 {
		out.Notes = nil
	}
	return out
}

// readSource answers one source from cache, or reads it.
func (m *Market) readSource(ctx context.Context, source Source, refresh bool) ([]Item, string, time.Time) {
	m.mu.Lock()
	hit, ok := m.cache[source.ID]
	token, mirrors := m.token, append([]string{}, m.mirrors...)
	m.mu.Unlock()

	if ok && !refresh && time.Since(hit.fetchedAt) < catalogueTTL {
		return hit.items, hit.note, hit.fetchedAt
	}

	items, err := m.fetchSource(ctx, source, token, mirrors)
	note := ""
	if err != nil {
		note = err.Error()
		if ctx.Err() != nil {
			// The caller went away mid-read — a page closed, a navigation. Not
			// cached: the source said nothing about itself here, and holding
			// "请求被取消" for half an hour would make the next visit look like
			// a broken source.
			return hit.items, note, hit.fetchedAt
		}
		// The previous read is kept and served alongside the note: a source
		// that timed out once should not empty the page it was filling a minute
		// ago. The note is what says the list may be stale.
		if ok && len(hit.items) > 0 {
			m.mu.Lock()
			m.cache[source.ID] = cached{items: hit.items, note: note, fetchedAt: hit.fetchedAt}
			m.mu.Unlock()
			return hit.items, note, hit.fetchedAt
		}
	}

	now := time.Now()
	m.mu.Lock()
	m.cache[source.ID] = cached{items: items, note: note, fetchedAt: now}
	m.mu.Unlock()
	return items, note, now
}

func (m *Market) fetchSource(ctx context.Context, source Source, token string, mirrors []string) ([]Item, error) {
	switch source.Kind {
	case SourceGitHub:
		return m.fetchGitHub(ctx, source, token)
	default:
		return m.fetchIndex(ctx, source, mirrors)
	}
}

/* ------------------------------------------------------------- index source */

// indexManifest is the format an index source publishes.
//
// Deliberately small, and every field but the download link optional: the point
// is that publishing a shelf of builds should be writing one JSON file by hand,
// not running a service.
type indexManifest struct {
	Name  string `json:"name"`
	Note  string `json:"note"`
	Items []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Author      string   `json:"author"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		GameVersion string   `json:"gameVersion"`
		File        string   `json:"file"`
		URL         string   `json:"url"`
		SHA256      string   `json:"sha256"`
		Size        int64    `json:"size"`
		Width       int      `json:"width"`
		Height      int      `json:"height"`
		Length      int      `json:"length"`
		Page        string   `json:"page"`
	} `json:"items"`
}

func (m *Market) fetchIndex(ctx context.Context, source Source, mirrors []string) ([]Item, error) {
	body, status, err := m.get(ctx, source.URL, "", mirrors)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	if status == http.StatusNotFound {
		if source.Builtin {
			// The project's own index, before it has published anything. Said
			// plainly rather than as an error: nothing is broken, there is
			// simply nothing there yet.
			return nil, errors.New("这个索引还没有发布内容")
		}
		return nil, errors.New("索引地址返回 404，检查一下链接")
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("索引读取失败（HTTP %d）", status)
	}

	var manifest indexManifest
	if err := json.NewDecoder(io.LimitReader(body, maxManifestBytes)).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("索引不是能识别的 JSON：%v", err)
	}

	base, _ := url.Parse(source.URL)
	out := make([]Item, 0, len(manifest.Items))
	for i, raw := range manifest.Items {
		link := strings.TrimSpace(raw.URL)
		if link == "" {
			continue
		}
		// Relative links are resolved against the manifest, so an index can sit
		// beside its own files and say "castle.schem".
		if base != nil {
			if parsed, err := url.Parse(link); err == nil {
				link = base.ResolveReference(parsed).String()
			}
		}
		if !isHTTP(link) {
			continue
		}

		fileName := strings.TrimSpace(raw.File)
		if fileName == "" {
			fileName = path.Base(link)
		}
		if !IsSchematic(fileName) {
			fileName += ".schem"
		}
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = baseName(fileName)
		}
		id := strings.TrimSpace(raw.ID)
		if id == "" {
			id = fmt.Sprintf("%s-%d", slug(name), i)
		}

		out = append(out, Item{
			ID:          id,
			SourceID:    source.ID,
			Source:      source.Name,
			Name:        name,
			Author:      strings.TrimSpace(raw.Author),
			Description: strings.TrimSpace(raw.Description),
			Tags:        cleanTags(raw.Tags),
			GameVersion: strings.TrimSpace(raw.GameVersion),
			FileName:    cleanFileName(fileName),
			Size:        raw.Size,
			Width:       raw.Width,
			Height:      raw.Height,
			Length:      raw.Length,
			URL:         link,
			SHA256:      strings.ToLower(strings.TrimSpace(raw.SHA256)),
			Page:        strings.TrimSpace(raw.Page),
		})
		if len(out) >= maxTreeItems {
			break
		}
	}
	return out, nil
}

/* ------------------------------------------------------------ github source */

type githubTree struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"tree"`
	Truncated bool `json:"truncated"`
}

func (m *Market) fetchGitHub(ctx context.Context, source Source, token string) ([]Item, error) {
	repo, ref, dir := splitRepo(source.URL)
	if repo == "" {
		return nil, fmt.Errorf("%w: %q 不是 owner/name", ErrBadSource, source.URL)
	}

	// One call for the whole tree rather than a walk per directory: the API
	// allows 60 an hour without a token, and a repository of builds is one
	// listing, not a directory tree worth of them.
	api := fmt.Sprintf("https://api.github.com/repos/%s/git/trees/%s?recursive=1", repo, url.PathEscape(ref))
	body, status, err := m.get(ctx, api, token, nil)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errors.New("仓库或分支不存在；私有仓库需要在「GitHub 集成」里配置访问令牌")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub API 配额用完了，稍后再试；配置访问令牌可以把上限从每小时 60 次提到 5000 次")
	default:
		return nil, fmt.Errorf("读取仓库失败（HTTP %d）", status)
	}

	var tree githubTree
	if err := json.NewDecoder(io.LimitReader(body, maxManifestBytes)).Decode(&tree); err != nil {
		return nil, fmt.Errorf("GitHub 返回的内容读不懂：%v", err)
	}

	out := make([]Item, 0, 16)
	for _, node := range tree.Tree {
		if node.Type != "blob" || !IsSchematic(node.Path) {
			continue
		}
		if dir != "" && !strings.HasPrefix(node.Path, dir+"/") {
			continue
		}
		fileName := path.Base(node.Path)
		out = append(out, Item{
			ID:       node.Path,
			SourceID: source.ID,
			Source:   source.Name,
			Name:     baseName(fileName),
			// The folder a build sits in is the only thing a bare repository
			// says about it, and it is usually the category the author meant:
			// medieval/, spawn/, farms/.
			Tags:     cleanTags(folderTags(node.Path)),
			FileName: fileName,
			Size:     node.Size,
			URL: fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s",
				repo, url.PathEscape(ref), escapePath(node.Path)),
			Page: fmt.Sprintf("https://github.com/%s/blob/%s/%s", repo, url.PathEscape(ref), escapePath(node.Path)),
		})
		if len(out) >= maxTreeItems {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("这个仓库里没有 .schem 文件")
	}
	return out, nil
}

// folderTags turns the directories above a file into tags, skipping the ones
// that say nothing: every repository of builds has a "schematics" folder.
func folderTags(full string) []string {
	parts := strings.Split(path.Dir(full), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch strings.ToLower(part) {
		case "", ".", "schematics", "schematic", "schems", "build", "builds":
			continue
		}
		out = append(out, part)
	}
	return out
}

/* ---------------------------------------------------------------- download */

// Fetch opens one build for downloading into the library. The caller closes the
// reader; the item travels back so what is stored carries the source's account
// of the build rather than only its file name.
func (m *Market) Fetch(ctx context.Context, sourceID, itemID string) (io.ReadCloser, Item, error) {
	found, err := m.find(ctx, sourceID, itemID)
	if err != nil {
		return nil, Item{}, err
	}

	m.mu.Lock()
	mirrors := append([]string{}, m.mirrors...)
	m.mu.Unlock()

	// No credential on a download, ever. The link is a public one — a raw.
	// githubusercontent URL or whatever an index published — and the mirrors it
	// may travel through are third parties who have no business being handed
	// the operator's token. See the note on Market.token.
	body, status, err := m.get(ctx, found.URL, "", mirrors)
	if err != nil {
		return nil, Item{}, err
	}
	if status != http.StatusOK {
		body.Close()
		return nil, Item{}, fmt.Errorf("下载失败（HTTP %d）", status)
	}
	return body, found, nil
}

// find locates one item, reading the source from cache when it is fresh.
func (m *Market) find(ctx context.Context, sourceID, itemID string) (Item, error) {
	// Copied rather than pointed at: the slice is reallocated whenever a source
	// is added, and this outlives the lock.
	var source Source
	m.mu.Lock()
	for _, candidate := range m.load() {
		if candidate.ID == sourceID {
			source = candidate
			break
		}
	}
	m.mu.Unlock()
	if source.ID == "" {
		return Item{}, fmt.Errorf("%w: %s", ErrSourceNotFound, sourceID)
	}

	items, note, _ := m.readSource(ctx, source, false)
	for _, item := range items {
		if item.ID == itemID {
			return item, nil
		}
	}
	if note != "" {
		return Item{}, fmt.Errorf("%w：%s", ErrItemNotFound, note)
	}
	return Item{}, fmt.Errorf("%w: %s", ErrItemNotFound, itemID)
}

// Install downloads one build straight into the library.
//
// The verification is the same as an upload's, plus the checksum when the
// source published one: the bytes are staged, hashed, parsed, and only a file
// that is a readable schematic is kept. A source that publishes a digest gets
// it checked; one that does not is trusted exactly as far as an operator's own
// upload is, which is the honest position — see the note at the top of this
// file.
func (m *Market) Install(ctx context.Context, library *Library, sourceID, itemID string) (Entry, Item, error) {
	body, item, err := m.Fetch(ctx, sourceID, itemID)
	if err != nil {
		return Entry{}, Item{}, err
	}
	defer body.Close()

	entry, err := library.Add(item.Name, item.FileName, body, Origin{
		Kind:   OriginMarket,
		From:   item.Source,
		URL:    item.URL,
		ItemID: item.ID,
	}, 0)
	if err != nil {
		return Entry{}, Item{}, err
	}

	if item.SHA256 != "" && !strings.EqualFold(item.SHA256, entry.SHA256) {
		// Rolled back rather than kept with a warning: a file that does not
		// match the digest its own source published is either corrupt or is not
		// the file that was published, and neither belongs in the library.
		_ = library.Remove(entry.ID)
		return Entry{}, Item{}, fmt.Errorf("%w：%s 期望 %s，实际 %s",
			ErrDigestMismatch, item.Name, short(item.SHA256), short(entry.SHA256))
	}

	// The source's own account of the build is worth keeping where the file
	// says nothing: a repository's .schem carries no author and no description,
	// and the index that lists it usually does.
	if item.Description != "" || len(item.Tags) > 0 {
		if updated, err := library.Edit(entry.ID, "", item.Description, item.Tags); err == nil {
			entry = updated
		}
	}
	return entry, item, nil
}

func short(sum string) string {
	if len(sum) <= 12 {
		return sum
	}
	return sum[:12]
}

/* ------------------------------------------------------------------ http */

// get issues one request, trying each mirror prefix in turn. The status is
// returned rather than turned into an error so callers can say something useful
// about a 404, which for a builtin index is not a failure.
func (m *Market) get(ctx context.Context, raw, token string, mirrors []string) (io.ReadCloser, int, error) {
	attempts := []string{raw}
	if len(mirrors) > 0 && strings.HasPrefix(raw, "https://raw.githubusercontent.com/") {
		attempts = attempts[:0]
		for _, prefix := range mirrors {
			attempts = append(attempts, prefix+raw)
		}
	}

	var lastErr error
	for _, attempt := range attempts {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, attempt, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrBadSource, err)
		}
		req.Header.Set("User-Agent", m.userAgent)
		req.Header.Set("Accept", "application/json, application/octet-stream;q=0.9, */*;q=0.8")
		if token != "" && strings.HasPrefix(attempt, "https://api.github.com/") {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		}

		resp, err := m.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		// A proxy that is up but does not have the file is the case the
		// fall-through exists for; a 404 from the last attempt is the answer.
		if resp.StatusCode == http.StatusNotFound && attempt != attempts[len(attempts)-1] {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return resp.Body, resp.StatusCode, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的地址")
	}
	return nil, 0, fmt.Errorf("连接失败：%s", reason(lastErr))
}

// reason strips the request URL out of a transport error.
//
// net/http wraps every failure in a *url.Error, whose Error() repeats the whole
// link — which for a raw.githubusercontent URL behind a proxy prefix is two
// hundred characters of address in front of the four words that say what went
// wrong. The address is already on the page: it is the source the note is
// attached to.
func reason(err error) string {
	var wrapped *url.Error
	if errors.As(err, &wrapped) && wrapped.Err != nil {
		err = wrapped.Err
	}
	if errors.Is(err, context.Canceled) {
		return "请求被取消（多半是页面还没读完就切走了）"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "超时，这台机器可能连不上这个地址"
	}
	return err.Error()
}

/* --------------------------------------------------------------- storage */

func (m *Market) load() []Source {
	if m.loaded {
		return m.sources
	}
	m.loaded = true
	m.sources = append([]Source{}, builtinSources...)

	data, err := os.ReadFile(filepath.Join(m.root, sourcesName))
	if err != nil {
		return m.sources
	}
	var stored []Source
	if err := json.Unmarshal(data, &stored); err != nil {
		return m.sources
	}

	// The builtin list is the panel's, not the file's: a stored copy carries
	// the operator's switch and their rename, and the URL and the builtin flag
	// come from this build. That is what lets a builtin source be re-pointed in
	// a later release without every existing panel keeping the old address.
	byID := make(map[string]int, len(m.sources))
	for i, source := range m.sources {
		byID[source.ID] = i
	}
	for _, source := range stored {
		if i, ok := byID[source.ID]; ok {
			m.sources[i].Disabled = source.Disabled
			if strings.TrimSpace(source.Name) != "" {
				m.sources[i].Name = source.Name
			}
			continue
		}
		if source.ID == "" || source.URL == "" {
			continue
		}
		source.Builtin = false
		m.sources = append(m.sources, source)
	}
	return m.sources
}

func (m *Market) saveSources() error {
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.sources, "", "  ")
	if err != nil {
		return err
	}

	temp := filepath.Join(m.root, sourcesName+".tmp")
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(m.root, sourcesName)); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

/* ---------------------------------------------------------------- parsing */

// normaliseSource turns what was typed into what is stored. A GitHub source is
// reduced to owner/name[@ref][:path] from anything that names it, including a
// pasted browser URL; an index source has to be an http link.
func normaliseSource(kind, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("%w: 地址是空的", ErrBadSource)
	}

	switch kind {
	case SourceGitHub:
		repo, ref, dir := splitRepo(stripGitHubURL(raw))
		if repo == "" {
			return "", "", fmt.Errorf("%w: %q 不是 owner/name", ErrBadSource, raw)
		}
		out := repo
		if ref != "HEAD" {
			out += "@" + ref
		}
		if dir != "" {
			out += ":" + dir
		}
		return SourceGitHub, out, nil
	case SourceIndex, "":
		if !isHTTP(raw) {
			// A bare owner/name in the index box is somebody who meant the other
			// kind, which is worth saying rather than refusing as a bad URL.
			if strings.Count(raw, "/") == 1 && !strings.Contains(raw, " ") {
				return "", "", fmt.Errorf("%w: %q 看起来是个 GitHub 仓库，换成「GitHub 仓库」类型", ErrBadSource, raw)
			}
			return "", "", fmt.Errorf("%w: 索引地址要以 https:// 开头", ErrBadSource)
		}
		return SourceIndex, raw, nil
	default:
		return "", "", fmt.Errorf("%w: 未知的源类型 %q", ErrBadSource, kind)
	}
}

// stripGitHubURL reduces a pasted repository link to owner/name[/tree/ref/path].
func stripGitHubURL(raw string) string {
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(raw, prefix), "/"), ".git")
		}
	}
	return raw
}

// splitRepo reads owner/name[@ref][:path], and the /tree/<ref>/<path> shape a
// browser URL carries.
func splitRepo(raw string) (repo, ref, dir string) {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	ref = "HEAD"

	if at, rest, ok := strings.Cut(raw, "@"); ok {
		raw = at
		ref = rest
		if cut, sub, ok := strings.Cut(ref, ":"); ok {
			ref, dir = cut, strings.Trim(sub, "/")
		}
	} else if before, sub, ok := strings.Cut(raw, ":"); ok {
		raw, dir = before, strings.Trim(sub, "/")
	}

	parts := strings.Split(raw, "/")
	if len(parts) >= 4 && parts[2] == "tree" {
		ref = parts[3]
		if len(parts) > 4 {
			dir = strings.Join(parts[4:], "/")
		}
		parts = parts[:2]
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ""
	}
	if ref == "" {
		ref = "HEAD"
	}
	return parts[0] + "/" + parts[1], ref, dir
}

func escapePath(full string) string {
	parts := strings.Split(full, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func isHTTP(raw string) bool {
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")
}

func defaultSourceName(kind, raw string) string {
	if kind == SourceGitHub {
		return raw
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return raw
}

func freeSourceID(sources []Source, name, raw string) string {
	base := slug(name)
	if base == "" {
		base = slug(raw)
	}
	if base == "" {
		base = "source"
	}
	taken := func(candidate string) bool {
		for _, source := range sources {
			if source.ID == candidate {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !taken(candidate) {
			return candidate
		}
	}
}

// Filter narrows a catalogue to what was searched for. Done here rather than
// upstream because neither kind of source can be asked a question: an index is
// a file and a repository tree is a listing, so the whole catalogue is in hand
// either way.
func Filter(items []Item, query, sourceID string) []Item {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" && sourceID == "" {
		return items
	}
	out := make([]Item, 0, len(items))
	for _, item := range items {
		if sourceID != "" && item.SourceID != sourceID {
			continue
		}
		if query != "" && !matches(item, query) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func matches(item Item, query string) bool {
	if strings.Contains(strings.ToLower(item.Name), query) ||
		strings.Contains(strings.ToLower(item.Description), query) ||
		strings.Contains(strings.ToLower(item.Author), query) ||
		strings.Contains(strings.ToLower(item.FileName), query) {
		return true
	}
	for _, tag := range item.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
