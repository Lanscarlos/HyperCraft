package gitlite

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// TreeEntry is one member of a tree: a file, or a subtree.
type TreeEntry struct {
	Mode string
	Name string
	Hash string
}

// File is one tracked path, flattened out of the tree it lives in. Path is
// slash-separated and relative to the work tree, which is the form every layer
// above this package speaks.
type File struct {
	Path string
	Mode string
	Hash string
}

// WriteTree stores one tree from its immediate members.
func (r *Repo) WriteTree(entries []TreeEntry) (string, error) {
	sorted := make([]TreeEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(a, b int) bool {
		return treeLess(sorted[a], sorted[b])
	})

	var buf bytes.Buffer
	for i, entry := range sorted {
		if entry.Name == "" || strings.ContainsAny(entry.Name, "/\x00") {
			return "", fmt.Errorf("%q is not a usable tree entry name", entry.Name)
		}
		if i > 0 && sorted[i-1].Name == entry.Name {
			return "", fmt.Errorf("tree has two entries named %q", entry.Name)
		}
		raw, err := hex.DecodeString(entry.Hash)
		if err != nil || len(raw) != 20 {
			return "", fmt.Errorf("tree entry %q points at %q, which is not an object id", entry.Name, entry.Hash)
		}
		buf.WriteString(entry.Mode)
		buf.WriteByte(' ')
		buf.WriteString(entry.Name)
		buf.WriteByte(0)
		buf.Write(raw)
	}
	return r.writeObject("tree", buf.Bytes())
}

// treeLess is Git's entry ordering, and it is not plain lexicographic: a
// subtree sorts as though its name ended in a slash. Get this wrong and the
// repository still reads back fine here while `git fsck` calls every tree
// malformed — which is exactly the kind of breakage that only shows up on
// somebody else's machine.
func treeLess(a, b TreeEntry) bool {
	return sortKey(a) < sortKey(b)
}

func sortKey(e TreeEntry) string {
	if e.Mode == ModeTree {
		return e.Name + "/"
	}
	return e.Name
}

// ReadTree returns a tree's immediate members, in stored order.
func (r *Repo) ReadTree(hash string) ([]TreeEntry, error) {
	kind, data, err := r.readObject(hash)
	if err != nil {
		return nil, err
	}
	if kind != "tree" {
		return nil, fmt.Errorf("%s is a %s, not a tree", hash, kind)
	}

	var entries []TreeEntry
	for len(data) > 0 {
		nul := bytes.IndexByte(data, 0)
		if nul < 0 || len(data) < nul+21 {
			return nil, fmt.Errorf("tree %s is truncated", hash)
		}
		mode, name, ok := bytes.Cut(data[:nul], []byte(" "))
		if !ok {
			return nil, fmt.Errorf("tree %s has a malformed entry", hash)
		}
		entries = append(entries, TreeEntry{
			Mode: string(mode),
			Name: string(name),
			Hash: hex.EncodeToString(data[nul+1 : nul+21]),
		})
		data = data[nul+21:]
	}
	return entries, nil
}

// WriteTreeFromFiles builds the whole nested tree for a flat list of paths and
// returns the root. Intermediate directories are created as they are needed;
// an empty list gives the empty tree, which is what an instance with no
// configuration at all commits.
func (r *Repo) WriteTreeFromFiles(files []File) (string, error) {
	sorted := make([]File, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Path < sorted[b].Path })
	return r.writeSubtree(sorted, "")
}

// writeSubtree consumes the slice of files sharing one prefix. The input is
// sorted by path, so every group of files under the same subdirectory is
// contiguous and one pass is enough.
func (r *Repo) writeSubtree(files []File, prefix string) (string, error) {
	var entries []TreeEntry
	for i := 0; i < len(files); {
		rest := strings.TrimPrefix(files[i].Path, prefix)
		slash := strings.Index(rest, "/")
		if slash < 0 {
			entries = append(entries, TreeEntry{
				Mode: files[i].Mode,
				Name: rest,
				Hash: files[i].Hash,
			})
			i++
			continue
		}

		name := rest[:slash]
		sub := prefix + name + "/"
		end := i
		for end < len(files) && strings.HasPrefix(files[end].Path, sub) {
			end++
		}
		hash, err := r.writeSubtree(files[i:end], sub)
		if err != nil {
			return "", err
		}
		entries = append(entries, TreeEntry{Mode: ModeTree, Name: name, Hash: hash})
		i = end
	}
	return r.WriteTree(entries)
}

// ListTree flattens a tree into every file it holds, sorted by path.
func (r *Repo) ListTree(hash string) ([]File, error) {
	var out []File
	if err := r.walkTree(hash, "", &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, nil
}

func (r *Repo) walkTree(hash, prefix string, out *[]File) error {
	entries, err := r.ReadTree(hash)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := prefix + entry.Name
		if entry.Mode == ModeTree {
			if err := r.walkTree(entry.Hash, path+"/", out); err != nil {
				return err
			}
			continue
		}
		*out = append(*out, File{Path: path, Mode: entry.Mode, Hash: entry.Hash})
	}
	return nil
}

// FindInTree resolves one path inside a tree.
func (r *Repo) FindInTree(tree, path string) (File, bool, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	current := tree
	for depth, part := range parts {
		entries, err := r.ReadTree(current)
		if err != nil {
			return File{}, false, err
		}
		found := false
		for _, entry := range entries {
			if entry.Name != part {
				continue
			}
			last := depth == len(parts)-1
			if last {
				if entry.Mode == ModeTree {
					return File{}, false, nil
				}
				return File{Path: path, Mode: entry.Mode, Hash: entry.Hash}, true, nil
			}
			if entry.Mode != ModeTree {
				return File{}, false, nil
			}
			current = entry.Hash
			found = true
			break
		}
		if !found {
			return File{}, false, nil
		}
	}
	return File{}, false, nil
}

// Change is one path that differs between two trees. A nil side means the file
// is absent there, so Old == nil is an addition and New == nil a deletion.
type Change struct {
	Path string
	Old  *File
	New  *File
}

// DiffTrees reports the paths that differ, sorted by path.
//
// Subtrees with equal ids are skipped whole rather than walked and compared
// leaf by leaf. That is what keeps listing a timeline cheap on an instance
// whose plugins directory holds several hundred tracked files: a commit that
// touched one YAML reads two trees, not two hundred.
func (r *Repo) DiffTrees(a, b string) ([]Change, error) {
	// Keyed by path, because a path can arrive from both sides — once as gone
	// from the old tree, once as present in the new one — and those are one
	// modification rather than two changes.
	found := map[string]*Change{}
	if err := r.diffTrees(a, b, "", found); err != nil {
		return nil, err
	}

	out := make([]Change, 0, len(found))
	for _, change := range found {
		out = append(out, *change)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (r *Repo) diffTrees(a, b, prefix string, found map[string]*Change) error {
	if a == b {
		return nil
	}

	left, err := r.readTreeMap(a)
	if err != nil {
		return err
	}
	right, err := r.readTreeMap(b)
	if err != nil {
		return err
	}

	names := make(map[string]bool, len(left)+len(right))
	for name := range left {
		names[name] = true
	}
	for name := range right {
		names[name] = true
	}

	for name := range names {
		old, hadOld := left[name]
		next, hadNew := right[name]
		path := prefix + name

		if hadOld && hadNew {
			if old.Mode == ModeTree && next.Mode == ModeTree {
				if err := r.diffTrees(old.Hash, next.Hash, path+"/", found); err != nil {
					return err
				}
				continue
			}
			if old.Mode == next.Mode && old.Hash == next.Hash {
				continue
			}
		}

		// Anything left is a real change on at least one side. A directory
		// replaced by a file (or the reverse) lands here too, which is why each
		// side is expanded rather than reported as one path.
		if hadOld {
			if err := r.collect(old, path, found, false); err != nil {
				return err
			}
		}
		if hadNew {
			if err := r.collect(next, path, found, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// collect records every file at or below one tree entry as a change on the
// given side.
func (r *Repo) collect(entry TreeEntry, path string, found map[string]*Change, isNew bool) error {
	files := []File{{Path: path, Mode: entry.Mode, Hash: entry.Hash}}
	if entry.Mode == ModeTree {
		files = nil
		if err := r.walkTree(entry.Hash, path+"/", &files); err != nil {
			return err
		}
	}
	for _, file := range files {
		change := found[file.Path]
		if change == nil {
			change = &Change{Path: file.Path}
			found[file.Path] = change
		}
		side := &change.Old
		if isNew {
			side = &change.New
		}
		copied := file
		*side = &copied
	}
	return nil
}

func (r *Repo) readTreeMap(hash string) (map[string]TreeEntry, error) {
	entries, err := r.ReadTree(hash)
	if err != nil {
		return nil, err
	}
	out := make(map[string]TreeEntry, len(entries))
	for _, entry := range entries {
		out[entry.Name] = entry
	}
	return out, nil
}
