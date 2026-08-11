package gitlite

import (
	"bytes"
	"compress/zlib"
	// SHA-1 is not a security choice here: it is the name Git gives an object,
	// and a store that hashed differently would not be a Git repository.
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// maxObjectBytes caps what will be read into memory as one object. The config
// history has its own, much smaller, per-file gate; this is the backstop that
// keeps a mistake there from turning into an out-of-memory kill.
const maxObjectBytes = 128 << 20

// hashObject computes an object id without writing anything, which is how a
// scan can tell an unchanged file from a changed one for the price of a read.
func hashObject(kind string, data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d", kind, len(data))
	h.Write([]byte{0})
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// writeObject stores one object and returns its id. Writing an object that is
// already there is a no-op: object ids are content, so an identical file in a
// hundred commits is one file on disk. That deduplication is most of why a
// thousand commits of YAML stay small.
func (r *Repo) writeObject(kind string, data []byte) (string, error) {
	hash := hashObject(kind, data)
	path := r.objectPath(hash)
	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	fmt.Fprintf(zw, "%s %d", kind, len(data))
	if _, err := zw.Write([]byte{0}); err != nil {
		return "", err
	}
	if _, err := zw.Write(data); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}

	if err := writeFileAtomic(path, buf.Bytes(), 0o444); err != nil {
		return "", fmt.Errorf("write object %s: %w", hash, err)
	}
	return hash, nil
}

// readObject returns an object's kind and payload.
func (r *Repo) readObject(hash string) (string, []byte, error) {
	if !validHash(hash) {
		return "", nil, fmt.Errorf("%w: %q is not an object id", ErrNotFound, hash)
	}
	file, err := os.Open(r.objectPath(hash))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
		}
		return "", nil, err
	}
	defer file.Close()

	zr, err := zlib.NewReader(file)
	if err != nil {
		return "", nil, fmt.Errorf("object %s is corrupt: %w", hash, err)
	}
	defer zr.Close()

	raw, err := io.ReadAll(io.LimitReader(zr, maxObjectBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("read object %s: %w", hash, err)
	}
	if len(raw) > maxObjectBytes {
		return "", nil, fmt.Errorf("object %s is larger than %d bytes", hash, maxObjectBytes)
	}

	nul := bytes.IndexByte(raw, 0)
	if nul < 0 {
		return "", nil, fmt.Errorf("object %s has no header", hash)
	}
	kind, sizeText, ok := bytes.Cut(raw[:nul], []byte(" "))
	if !ok {
		return "", nil, fmt.Errorf("object %s has a malformed header", hash)
	}
	size, err := strconv.Atoi(string(sizeText))
	if err != nil {
		return "", nil, fmt.Errorf("object %s has a malformed size: %w", hash, err)
	}
	body := raw[nul+1:]
	if size != len(body) {
		return "", nil, fmt.Errorf("object %s says %d bytes but holds %d", hash, size, len(body))
	}
	return string(kind), body, nil
}

// WriteBlob stores file content and returns its object id.
func (r *Repo) WriteBlob(data []byte) (string, error) { return r.writeObject("blob", data) }

// ReadBlob returns the content of a blob.
func (r *Repo) ReadBlob(hash string) ([]byte, error) {
	kind, data, err := r.readObject(hash)
	if err != nil {
		return nil, err
	}
	if kind != "blob" {
		return nil, fmt.Errorf("%s is a %s, not a blob", hash, kind)
	}
	return data, nil
}

// HashBlob is WriteBlob without the write: the id a file's content would get.
func HashBlob(data []byte) string { return hashObject("blob", data) }

// Has reports whether an object is already stored.
func (r *Repo) Has(hash string) bool {
	if !validHash(hash) {
		return false
	}
	_, err := os.Stat(r.objectPath(hash))
	return err == nil
}

func (r *Repo) objectPath(hash string) string {
	return filepath.Join(r.gitDir, "objects", hash[:2], hash[2:])
}

// Size is the repository's footprint on disk, which is what the operator is
// shown and what the total-size gate is measured against.
func (r *Repo) Size() (int64, error) {
	var total int64
	root := filepath.Join(r.gitDir, "objects")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// Prune deletes every object not reachable from the branch.
//
// This is the loose-object store's answer to `git gc`. There is no packfile
// writer here, so nothing is repacked; what it reclaims is the debris of
// commits that were built and then abandoned — a gate that tripped after the
// blobs were written, a panel killed mid-commit. Objects are immutable and
// content-addressed, so deleting an unreachable one can never invalidate a
// reachable one.
func (r *Repo) Prune() (int, error) {
	head, err := r.Head()
	if err != nil {
		return 0, err
	}

	live := make(map[string]bool)
	for hash := head; hash != ""; {
		live[hash] = true
		commit, err := r.ReadCommit(hash)
		if err != nil {
			return 0, err
		}
		if err := r.markTree(commit.Tree, live); err != nil {
			return 0, err
		}
		if len(commit.Parents) == 0 {
			break
		}
		hash = commit.Parents[0]
		if live[hash] {
			break
		}
	}

	removed := 0
	root := filepath.Join(r.gitDir, "objects")
	shards, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	for _, shard := range shards {
		if !shard.IsDir() || len(shard.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, shard.Name()))
		if err != nil {
			return removed, err
		}
		for _, file := range files {
			hash := shard.Name() + file.Name()
			if !validHash(hash) || live[hash] {
				continue
			}
			if err := os.Remove(filepath.Join(root, shard.Name(), file.Name())); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

func (r *Repo) markTree(hash string, live map[string]bool) error {
	if hash == "" || live[hash] {
		return nil
	}
	live[hash] = true
	entries, err := r.ReadTree(hash)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Mode == ModeTree {
			if err := r.markTree(entry.Hash, live); err != nil {
				return err
			}
			continue
		}
		live[entry.Hash] = true
	}
	return nil
}
