// Package schemlib keeps WorldEdit schematics as a panel-wide asset.
//
// The shape mirrors internal/serverjar rather than internal/plugin: a build is
// held once and copied into whichever server wants it, and there is no version
// history to keep, because WorldEdit does not publish releases — an edited
// build is saved as a new file, usually under a new name.
//
// What the library adds over "a folder of .schem files" is the thing the folder
// cannot answer: which of these thirty files is the castle. Every entry is
// parsed on the way in (internal/schematic) and the answer — dimensions, block
// count, the blocks it is mostly made of, the game version it was saved from —
// is stored beside it, so the list is readable without opening anything. The
// full preview is re-parsed on demand; see Preview.
//
// Nothing here writes a schematic and nothing places blocks in a world. A
// schematic reaches a server as a file in its schematics directory, which is
// where WorldEdit's own //load looks; pasting it stays an in-game act.
package schemlib

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lanscarlos/hypercraft/internal/schematic"
)

var (
	// ErrNotFound is returned for a schematic that is not in the library.
	ErrNotFound = errors.New("schematic not found")
	// ErrInvalidID rejects an id that does not name something inside the
	// library directory.
	ErrInvalidID = errors.New("invalid schematic id")
	// ErrExists is returned when the same file, or the same source, is already
	// held. The message names the one that has it, because "already in the
	// library" without saying where is a dead end — see existsError.
	ErrExists = errors.New("this schematic is already in the library")
	// ErrNotSchematic rejects a file that is not one of the formats the panel
	// reads. Kept apart from the parser's own errors so the HTTP layer can
	// answer 415 for "this is a zip" and 422 for "this is a schematic the
	// reader gave up on".
	ErrNotSchematic = errors.New("not a schematic file")
	// ErrTooLarge marks an upload past the size cap.
	ErrTooLarge = errors.New("schematic is too large")
)

// indexName is the library's index: what each file is, what the operator
// renamed it to, and what the parse found. Unlike the plugin registry, this is
// annotation rather than the list itself — the files on disk are the source of
// truth, so a lost index costs names and tags and never a build. That is also
// what makes dropping a .schem into the directory by hand a supported way in;
// see Rescan.
const indexName = "index.json"

// partSuffix marks a file still being written. Nothing with this suffix is
// listed, adopted or opened.
const partSuffix = ".hypercraft-part"

// Kinds of origin, for the badge on a row and for nothing else: no decision is
// ever made from where a schematic came from.
const (
	// OriginUpload is a file the operator uploaded through the panel.
	OriginUpload = "upload"
	// OriginInstance is a file taken out of a server's own schematics folder.
	OriginInstance = "instance"
	// OriginMarket is a download from one of the market's sources.
	OriginMarket = "market"
	// OriginFound is a file the panel discovered in the library directory
	// rather than put there. See Rescan.
	OriginFound = "found"
)

// Origin records where a schematic came from, for display.
type Origin struct {
	Kind string `json:"kind"`
	// From is the human account of it: an instance name, a market source name,
	// the path it was imported from.
	From string `json:"from,omitempty"`
	// URL is the market download link, kept so an operator can tell two builds
	// with the same name apart by where they came from.
	URL string `json:"url,omitempty"`
	// ItemID is the market item's id, so a second download of the same build
	// can be recognised as one it already has.
	ItemID string `json:"itemId,omitempty"`
}

// Block is one palette entry and how much of the build it accounts for.
type Block struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// topBlocks is how many of them the summary keeps. Enough to say what a build
// is made of — "spruce, stone bricks, glass" — without storing a palette of
// four hundred stair variants per row in a file that is read on every listing.
const topBlocks = 8

// Facts is what the parse found, stored so the list can describe a build
// without re-reading megabytes of gzipped NBT per page load.
//
// Cached rather than derived on demand because it is read for every row of a
// listing and would otherwise be a full parse per row. It is only ever
// decoration: nothing installs, deletes or refuses anything because of what is
// in here, so an index written by an older build that lacks a field costs a
// column and not a feature.
type Facts struct {
	Format      string `json:"format,omitempty"`
	Version     int    `json:"version,omitempty"`
	DataVersion int    `json:"dataVersion,omitempty"`

	Width  int `json:"width"`
	Height int `json:"height"`
	Length int `json:"length"`
	Volume int `json:"volume"`
	NonAir int `json:"nonAir"`
	// Kinds is how many distinct non-air block states the build uses, which is
	// the closest single number to "how detailed is this".
	Kinds         int `json:"kinds"`
	BlockEntities int `json:"blockEntities,omitempty"`
	Entities      int `json:"entities,omitempty"`

	// SavedName and Author are what the builder wrote into the file, which is
	// routinely not what the file is called — hence both, and hence Entry.Name
	// being a third thing the operator can set.
	SavedName string `json:"savedName,omitempty"`
	Author    string `json:"author,omitempty"`
	Created   string `json:"created,omitempty"`

	Top []Block `json:"top,omitempty"`

	// Unreadable is the parser's complaint about a file the library is holding
	// anyway. A schematic that this build cannot read is still a file somebody
	// wants to keep and hand to a server — WorldEdit may well read it — so it
	// is listed with the reason instead of being refused on the way in. Only
	// Rescan can produce one; an upload is checked and rejected.
	Unreadable string `json:"unreadable,omitempty"`
}

// Entry is one schematic in the library.
type Entry struct {
	// ID is a slug of the name, unique in the library, and what a URL says.
	ID string `json:"id"`
	// Name is what the panel calls this build. It starts as the file name
	// without its extension and the operator can change it; the file on disk is
	// never renamed with it, so a name is free to be "主城 spawn（v3）" without
	// putting that into a path.
	Name string `json:"name"`
	// FileName is the name on disk, inside the library root. Also what an
	// install writes into the server, because WorldEdit's //load takes the file
	// name and the operator has to be able to type it.
	FileName string `json:"fileName"`
	// Note is the operator's own description. Nothing reads it.
	Note string   `json:"note,omitempty"`
	Tags []string `json:"tags,omitempty"`

	Origin  Origin    `json:"origin"`
	SHA256  string    `json:"sha256,omitempty"`
	Size    int64     `json:"size"`
	AddedAt time.Time `json:"addedAt"`

	Facts Facts `json:"facts"`
}

// Library owns the schematics directory, normally <data>/schematics.
type Library struct {
	root string

	// mu serialises index reads and writes, and the file staging that goes with
	// them: two uploads of the same name must not race for the same free file
	// name. The parse itself is the expensive part and happens outside it.
	mu    sync.Mutex
	index map[string]Entry
	// loaded distinguishes "read the index and it was empty" from "have not
	// read it yet", which an empty map cannot.
	loaded bool
}

func NewLibrary(root string) *Library { return &Library{root: root} }

// Root is the directory schematics are stored in.
func (l *Library) Root() string { return l.root }

// List returns every schematic, newest first.
//
// Entries whose file has gone are dropped from the answer, the same way the
// plugin library drops a version whose jar is missing: the index is what the
// panel knows, but it cannot install a file that is not there, and a row that
// fails on click is worse than one that is honestly absent. The index is left
// alone — a directory that failed to mount is not a reason to forget what was
// in it.
func (l *Library) List() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	index := l.load()
	out := make([]Entry, 0, len(index))
	for _, entry := range index {
		described, ok := l.describe(entry)
		if !ok {
			continue
		}
		out = append(out, described)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].AddedAt.Equal(out[b].AddedAt) {
			return out[a].AddedAt.After(out[b].AddedAt)
		}
		return out[a].ID < out[b].ID
	})
	return out
}

// Get returns one schematic by id.
func (l *Library) Get(id string) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(id)
}

func (l *Library) get(id string) (Entry, error) {
	if err := validID(id); err != nil {
		return Entry{}, err
	}
	entry, ok := l.load()[id]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	described, ok := l.describe(entry)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s 的文件不在了", ErrNotFound, entry.Name)
	}
	return described, nil
}

// describe refreshes what the filesystem knows about an entry, and reports
// false when its file has gone.
func (l *Library) describe(entry Entry) (Entry, bool) {
	info, err := os.Stat(l.pathOf(entry.FileName))
	if err != nil || info.IsDir() {
		return Entry{}, false
	}
	entry.Size = info.Size()
	return entry, true
}

// Add stores a schematic and records what is in it.
//
// The reader is staged to a temporary file first, under the size cap, and only
// then parsed: the parser needs the whole file, the caller's reader is usually
// a network connection, and a 40 MB upload should not be held in memory to find
// out whether it is a schematic at all. A file that will not parse is refused
// and nothing is kept — the library is browsable precisely because everything
// in it has been read.
//
// A file the library already holds, byte for byte, is refused with ErrExists.
// The same build under two names is a thing operators do deliberately, but it
// is never a thing they do by uploading the same file twice.
func (l *Library) Add(name, fileName string, r io.Reader, origin Origin, limit int64) (Entry, error) {
	fileName = cleanFileName(fileName)
	if fileName == "" {
		return Entry{}, fmt.Errorf("%w: 文件名是空的", ErrInvalidID)
	}
	if !IsSchematic(fileName) {
		return Entry{}, fmt.Errorf("%w: %s 不是 .schem 或 .schematic", ErrNotSchematic, fileName)
	}
	if limit <= 0 || limit > schematic.MaxFileBytes {
		limit = schematic.MaxFileBytes
	}

	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return Entry{}, err
	}
	temp, err := os.CreateTemp(l.root, "upload-*"+partSuffix)
	if err != nil {
		return Entry{}, err
	}
	staged := temp.Name()
	// Removed on every path that does not rename it into place, including the
	// ones where the caller's connection died halfway through.
	defer func() {
		temp.Close()
		os.Remove(staged)
	}()

	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, digest), io.LimitReader(r, limit+1))
	if err != nil {
		return Entry{}, err
	}
	if written > limit {
		return Entry{}, fmt.Errorf("%w: 超过 %d 字节", ErrTooLarge, limit)
	}
	if written == 0 {
		return Entry{}, fmt.Errorf("%w: 文件是空的", ErrNotSchematic)
	}
	if err := temp.Close(); err != nil {
		return Entry{}, err
	}

	facts, err := readFacts(staged)
	if err != nil {
		return Entry{}, err
	}
	sum := hex.EncodeToString(digest.Sum(nil))

	l.mu.Lock()
	defer l.mu.Unlock()
	index := l.load()

	for _, existing := range index {
		if existing.SHA256 != "" && strings.EqualFold(existing.SHA256, sum) {
			return Entry{}, existsError{fmt.Sprintf("同样的文件已经在库里，叫「%s」。", existing.Name)}
		}
	}

	if name = strings.TrimSpace(name); name == "" {
		// The saved name is the builder's own, so it beats the file name — but
		// only when there is one; most schematics carry no metadata at all.
		name = strings.TrimSpace(facts.SavedName)
	}
	if name == "" {
		name = baseName(fileName)
	}

	entry := Entry{
		ID:       freeID(index, name, fileName),
		Name:     name,
		FileName: l.freeFileName(index, fileName),
		Origin:   origin,
		SHA256:   sum,
		Size:     written,
		AddedAt:  time.Now(),
		Facts:    facts,
	}
	if err := os.Rename(staged, l.pathOf(entry.FileName)); err != nil {
		return Entry{}, err
	}
	index[entry.ID] = entry
	if err := l.save(index); err != nil {
		// The file landed and the index did not. Left in place on purpose: it
		// is a valid schematic in the library directory, so the next Rescan
		// adopts it rather than it being lost with the error.
		return Entry{}, err
	}
	return entry, nil
}

// Edit changes what a schematic is called, what is written under it, and how it
// is tagged. The file is untouched — renaming an entry must not change the name
// a server's //load takes.
func (l *Library) Edit(id, name, note string, tags []string) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	index := l.load()
	entry, ok := index[id]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if name = strings.TrimSpace(name); name != "" {
		entry.Name = name
	}
	entry.Note = strings.TrimSpace(note)
	entry.Tags = cleanTags(tags)

	index[id] = entry
	if err := l.save(index); err != nil {
		return Entry{}, err
	}
	described, _ := l.describe(entry)
	if described.ID == "" {
		return entry, nil
	}
	return described, nil
}

// Remove deletes a schematic and forgets it. Copies already handed to servers
// are untouched: they are ordinary files in their own directories, exactly like
// a plugin jar or a core.
func (l *Library) Remove(id string) error {
	if err := validID(id); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	index := l.load()
	entry, ok := index[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(index, id)
	if err := l.save(index); err != nil {
		return err
	}
	// The index goes first: a file with no entry is adopted again by the next
	// rescan, which is recoverable, while an entry pointing at nothing is a row
	// that fails on click.
	if err := os.Remove(l.pathOf(entry.FileName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Open returns one schematic's file, for downloading or for copying into a
// server directory.
func (l *Library) Open(id string) (*os.File, Entry, error) {
	entry, err := l.Get(id)
	if err != nil {
		return nil, Entry{}, err
	}
	file, err := os.Open(l.pathOf(entry.FileName))
	if err != nil {
		return nil, Entry{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return file, entry, nil
}

// Preview parses one schematic in full, for the renderer.
//
// Not cached, unlike Facts: the payload is the whole region as run-length
// encoded palette indices and runs to megabytes, which is worth re-reading on
// the rare occasion somebody opens a preview and is not worth holding for every
// entry in the library on the off-chance.
func (l *Library) Preview(id string) (*schematic.Preview, Entry, error) {
	file, entry, err := l.Open(id)
	if err != nil {
		return nil, Entry{}, err
	}
	defer file.Close()

	preview, err := schematic.Parse(file)
	if err != nil {
		return nil, Entry{}, err
	}
	return preview, entry, nil
}

// Rescan adopts .schem files that are in the library directory but not in the
// index, and drops entries whose files have gone for good.
//
// This is what makes "drop your builds into the folder" a supported way in,
// which the core library has and which matters more here: an operator migrating
// from another panel has a directory of schematics and no interest in uploading
// them one at a time through a browser.
//
// A file that will not parse is adopted anyway, with the reason recorded in
// Facts.Unreadable. Refusing it would mean the panel silently ignoring a file
// sitting in its own directory, which is the failure mode that has people
// checking whether the daemon is running.
//
// Returns how many entries were added and how many were dropped.
func (l *Library) Rescan() (added, dropped int) {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		return 0, 0
	}

	l.mu.Lock()
	index := l.load()
	held := make(map[string]bool, len(index))
	for _, entry := range index {
		held[entry.FileName] = true
	}
	l.mu.Unlock()

	// Parsing happens outside the lock: a directory of a hundred builds on a
	// slow disk is long enough that holding the index through it would stall
	// every page that only wanted to list what is there.
	type found struct {
		name  string
		size  int64
		sum   string
		facts Facts
	}
	var loose []found
	for _, item := range entries {
		name := item.Name()
		if item.IsDir() || held[name] || !IsSchematic(name) || strings.HasSuffix(name, partSuffix) {
			continue
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		full := l.pathOf(name)
		facts, err := readFacts(full)
		if err != nil {
			facts = Facts{Unreadable: err.Error()}
		}
		loose = append(loose, found{name: name, size: info.Size(), sum: fileSum(full), facts: facts})
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	index = l.load()

	for _, item := range loose {
		// Re-checked under the lock: an upload may have claimed this name while
		// the parsing was going on.
		taken := false
		for _, entry := range index {
			if entry.FileName == item.name {
				taken = true
				break
			}
		}
		if taken {
			continue
		}
		entry := Entry{
			ID:       freeID(index, baseName(item.name), item.name),
			Name:     firstNonEmpty(strings.TrimSpace(item.facts.SavedName), baseName(item.name)),
			FileName: item.name,
			Origin:   Origin{Kind: OriginFound},
			SHA256:   item.sum,
			Size:     item.size,
			AddedAt:  time.Now(),
			Facts:    item.facts,
		}
		index[entry.ID] = entry
		added++
	}

	for id, entry := range index {
		if _, err := os.Stat(l.pathOf(entry.FileName)); err == nil || !os.IsNotExist(err) {
			continue
		}
		delete(index, id)
		dropped++
	}

	if added == 0 && dropped == 0 {
		return 0, 0
	}
	if err := l.save(index); err != nil {
		return 0, 0
	}
	return added, dropped
}

// TotalSize is what the library costs on disk.
func (l *Library) TotalSize() int64 {
	var total int64
	for _, entry := range l.List() {
		total += entry.Size
	}
	return total
}

// readFacts parses a staged file and folds it into the stored summary.
func readFacts(path string) (Facts, error) {
	file, err := os.Open(path)
	if err != nil {
		return Facts{}, err
	}
	defer file.Close()

	preview, err := schematic.Parse(file)
	if err != nil {
		return Facts{}, err
	}
	return FactsOf(preview), nil
}

// FactsOf reduces a full preview to what the library stores.
//
// Exported because the market stores the same summary for a build it has not
// downloaded yet, and the two have to agree on what a summary is or the same
// build reads differently on either side of 入库.
func FactsOf(preview *schematic.Preview) Facts {
	facts := Facts{
		Format:        preview.Format,
		Version:       preview.Version,
		DataVersion:   preview.DataVersion,
		Width:         preview.Width,
		Height:        preview.Height,
		Length:        preview.Length,
		Volume:        preview.Volume,
		NonAir:        preview.NonAir,
		BlockEntities: preview.BlockEntities,
		Entities:      preview.Entities,
		SavedName:     preview.Name,
		Author:        preview.Author,
		Created:       preview.Created,
	}

	blocks := make([]Block, 0, len(preview.Palette))
	for i, name := range preview.Palette {
		if i >= len(preview.Counts) || preview.Counts[i] == 0 || schematic.IsAir(name) {
			continue
		}
		blocks = append(blocks, Block{Name: name, Count: preview.Counts[i]})
	}
	facts.Kinds = len(blocks)
	sort.Slice(blocks, func(a, b int) bool {
		if blocks[a].Count != blocks[b].Count {
			return blocks[a].Count > blocks[b].Count
		}
		return blocks[a].Name < blocks[b].Name
	})
	if len(blocks) > topBlocks {
		blocks = blocks[:topBlocks]
	}
	facts.Top = blocks
	return facts
}

func fileSum(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(file, schematic.MaxFileBytes)); err != nil {
		return ""
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func (l *Library) pathOf(fileName string) string {
	return filepath.Join(l.root, fileName)
}

// load reads the index once and keeps it in memory. The panel is the only
// writer, so re-reading per call would buy nothing.
func (l *Library) load() map[string]Entry {
	if l.loaded {
		return l.index
	}
	l.loaded = true
	l.index = map[string]Entry{}

	data, err := os.ReadFile(filepath.Join(l.root, indexName))
	if err != nil {
		return l.index
	}
	var stored map[string]Entry
	if err := json.Unmarshal(data, &stored); err != nil || stored == nil {
		// A corrupt index is not worth refusing to boot over: the files are
		// still there and the next rescan adopts every one of them, which costs
		// the names and tags and keeps the builds.
		return l.index
	}
	for id, entry := range stored {
		if validID(id) != nil || cleanFileName(entry.FileName) != entry.FileName {
			// An index edited by hand into something that names a file outside
			// the library. Dropped rather than repaired: there is no honest
			// guess at what was meant.
			continue
		}
		entry.ID = id
		l.index[id] = entry
	}
	return l.index
}

func (l *Library) save(index map[string]Entry) error {
	l.index = index
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}

	// Written beside the file and renamed over it, so an interrupted save
	// cannot leave a half-written index behind.
	temp := filepath.Join(l.root, indexName+".tmp")
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(l.root, indexName)); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// freeID picks a slug no other entry holds.
func freeID(index map[string]Entry, name, fileName string) string {
	base := slug(name)
	if base == "" {
		base = slug(baseName(fileName))
	}
	if base == "" {
		base = "schematic"
	}
	if _, taken := index[base]; !taken {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if _, taken := index[candidate]; !taken {
			return candidate
		}
	}
}

// freeFileName picks the name the file takes on disk.
//
// Derived from the uploaded file name rather than from the entry's display
// name, because this is the name an operator types into //schem load: a file
// dragged in as castle.schem has to still be castle.schem on the server, even
// when the builder wrote 城堡 into the metadata and the library shows that.
// Renaming the entry later never touches it — see Edit.
//
// Collisions are resolved against both the index and the directory: a file
// dropped in by hand may be sitting on the name this upload would take, and the
// rescan that would adopt it has not run yet.
func (l *Library) freeFileName(index map[string]Entry, fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	base := slug(baseName(fileName))
	if base == "" {
		base = "schematic"
	}

	taken := make(map[string]bool, len(index))
	for _, entry := range index {
		taken[strings.ToLower(entry.FileName)] = true
	}
	free := func(candidate string) bool {
		if taken[strings.ToLower(candidate)] {
			return false
		}
		_, err := os.Stat(l.pathOf(candidate))
		return os.IsNotExist(err)
	}

	if candidate := base + ext; free(candidate) {
		return candidate
	}
	for n := 2; ; n++ {
		if candidate := fmt.Sprintf("%s-%d%s", base, n, ext); free(candidate) {
			return candidate
		}
	}
}

// IsSchematic reports whether a name is one of the two WorldEdit formats the
// panel reads. It agrees with api.IsSchematic and with the UI's own copy; all
// three are short lists of the same two extensions.
func IsSchematic(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".schem", ".schematic":
		return true
	default:
		return false
	}
}

// cleanFileName reduces an uploaded name to a plain file name in the library
// directory. A browser sends whatever the operating system called the file,
// which on some of them includes a path.
func cleanFileName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = path.Base(name)
	if name == "." || name == ".." || name == "/" || strings.ContainsAny(name, "\x00") {
		return ""
	}
	if strings.HasSuffix(name, partSuffix) || name == indexName {
		return ""
	}
	return name
}

func baseName(fileName string) string {
	return strings.TrimSuffix(fileName, filepath.Ext(fileName))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// cleanTags normalises the operator's tags: trimmed, de-duplicated, capped.
// The cap is there because tags are free text in a form and an index file is
// not the place for a paragraph pasted into the wrong box.
func cleanTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]rune(tag)) > 24 || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		out = append(out, tag)
		if len(out) == 12 {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// slug reduces a name to something safe as a file name and readable in a URL.
//
// CJK is kept: the operators this panel is written for name their builds in
// Chinese, and a library where every id is "schematic-7" because the names were
// dropped is a library nobody can find anything in. What is removed is what a
// path or a URL cannot carry.
func slug(in string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped
	for _, r := range strings.TrimSpace(in) {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_':
			b.WriteRune(r)
			lastDash = false
		case r > 0x7f && !isPunctuation(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len([]rune(out)) > 48 {
		out = strings.Trim(string([]rune(out)[:48]), "-.")
	}
	return out
}

// isPunctuation catches the full-width punctuation that comes with CJK text and
// that has no business in a file name. It separates rather than disappearing,
// the same way a space does: 「主城（旧）」 slugs to 主城-旧.
func isPunctuation(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303f: // CJK symbols and punctuation
		return true
	case r >= 0xff00 && r <= 0xff0f, r >= 0xff1a && r <= 0xff20:
		return true
	case r >= 0xff3b && r <= 0xff40, r >= 0xff5b && r <= 0xff65:
		return true
	}
	return false
}

// existsError is an ErrExists that carries the sentence the panel shows.
//
// Every sentinel in this tree is English, and deliberately so: they exist for
// errors.Is and for log lines. This one is different only because it is the
// one conflict an operator meets routinely — dragging in a file they already
// have — and a toast reading "this schematic is already in the library：同样的
// 文件已经在库里" is the seam between the two showing through.
type existsError struct{ msg string }

func (e existsError) Error() string { return e.msg }

// Is makes errors.Is(err, ErrExists) hold, which is what the HTTP layer maps
// onto 409 and what the tests assert.
func (e existsError) Is(target error) bool { return target == ErrExists }

// validID rejects anything that names something other than an entry of this
// library.
func validID(id string) error {
	if id == "" || id == "." || id == ".." ||
		strings.ContainsAny(id, `/\`+"\x00") || strings.HasSuffix(id, partSuffix) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}
