package javaruntime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// ErrBadArchive is returned for an archive the panel refuses to unpack.
var ErrBadArchive = errors.New("bad archive")

// Extraction limits. A JDK is roughly 350 MB across 25k entries, so these are
// generous — they exist so a hostile or corrupt archive cannot fill the disk
// or spin forever, not to constrain real runtimes.
const (
	maxExtractedBytes   = 4 << 30 // 4 GiB
	maxExtractedEntries = 200_000
)

// extractArchive unpacks a .tar.gz or .zip into dest, which is an os.Root
// handle on the install directory.
//
// Everything is written through that handle, so the kernel — not a string
// check — is what stops an entry called ../../etc/cron.d/x from landing
// outside the tree. Symlink targets are checked separately, because a symlink
// is only inert until something outside the Root follows it.
func extractArchive(ctx context.Context, name string, archive *os.File, dest *os.Root) error {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		return extractZip(ctx, archive, dest)
	}
	return extractTarGz(ctx, archive, dest)
}

func extractTarGz(ctx context.Context, archive *os.File, dest *os.Root) error {
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	var written, entries int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBadArchive, err)
		}

		entries++
		if entries > maxExtractedEntries {
			return fmt.Errorf("%w: more than %d entries", ErrBadArchive, maxExtractedEntries)
		}
		name, err := entryPath(header.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}

		mode := fs.FileMode(header.Mode).Perm()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := dest.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			n, err := writeFile(dest, name, reader, mode, maxExtractedBytes-written)
			if err != nil {
				return err
			}
			written += n
		case tar.TypeSymlink:
			if err := writeSymlink(dest, name, header.Linkname); err != nil {
				return err
			}
		case tar.TypeLink:
			// Hard links inside the archive; Temurin has none, but honouring
			// them is cheap and a missing file is worse than a duplicate.
			target, err := entryPath(header.Linkname)
			if err != nil || target == "" {
				continue
			}
			if err := dest.Link(target, name); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		default:
			// Devices, fifos and sockets have no business in a JDK tarball.
			continue
		}
	}
}

func extractZip(ctx context.Context, archive *os.File, dest *os.Root) error {
	info, err := archive.Stat()
	if err != nil {
		return err
	}
	reader, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	if len(reader.File) > maxExtractedEntries {
		return fmt.Errorf("%w: more than %d entries", ErrBadArchive, maxExtractedEntries)
	}

	var written int64
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, err := entryPath(entry.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}

		mode := entry.Mode()
		switch {
		case mode.IsDir():
			if err := dest.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			target, err := readZipEntry(entry, 4096)
			if err != nil {
				return err
			}
			if err := writeSymlink(dest, name, string(target)); err != nil {
				return err
			}
		case mode.IsRegular():
			body, err := entry.Open()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrBadArchive, err)
			}
			n, err := writeFile(dest, name, body, mode.Perm(), maxExtractedBytes-written)
			body.Close()
			if err != nil {
				return err
			}
			written += n
		default:
			continue
		}
	}
	return nil
}

func readZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	body, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, limit))
}

// writeFile creates one extracted file, making its parent directories first.
func writeFile(dest *os.Root, name string, body io.Reader, mode fs.FileMode, budget int64) (int64, error) {
	if budget <= 0 {
		return 0, fmt.Errorf("%w: extracted contents exceed %d bytes", ErrBadArchive, int64(maxExtractedBytes))
	}
	if parent := path.Dir(name); parent != "." {
		if err := dest.MkdirAll(parent, 0o755); err != nil {
			return 0, err
		}
	}

	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	file, err := dest.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}

	written, copyErr := io.Copy(file, io.LimitReader(body, budget+1))
	closeErr := file.Close()
	if copyErr != nil {
		return written, copyErr
	}
	if closeErr != nil {
		return written, closeErr
	}
	if written > budget {
		return written, fmt.Errorf("%w: extracted contents exceed %d bytes", ErrBadArchive, int64(maxExtractedBytes))
	}

	// The umask applies to OpenFile but not to Chmod. bin/java without its
	// execute bit is a runtime that looks installed and cannot start, so the
	// mode is set explicitly wherever the archive asked for one.
	if perm&0o111 != 0 {
		if err := dest.Chmod(name, perm); err != nil {
			return written, err
		}
	}
	return written, nil
}

// writeSymlink recreates a link, but only one that stays inside the extracted
// tree. Reads through os.Root would refuse to follow an escaping link anyway;
// the point is that the runtime directory is also walked and executed from
// outside the Root, where nothing would.
func writeSymlink(dest *os.Root, name, target string) error {
	if target == "" || path.IsAbs(target) || strings.HasPrefix(target, "/") ||
		strings.Contains(target, "\x00") || looksAbsWindows(target) {
		return fmt.Errorf("%w: symlink %q points outside the archive (%q)", ErrBadArchive, name, target)
	}
	resolved := path.Join(path.Dir(name), path.Clean(strings.ReplaceAll(target, "\\", "/")))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("%w: symlink %q escapes the archive (%q)", ErrBadArchive, name, target)
	}

	if parent := path.Dir(name); parent != "." {
		if err := dest.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	if err := dest.Symlink(target, name); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// entryPath normalises an archive member name, rejecting anything that is not
// a plain relative path. An empty result means "skip this entry".
func entryPath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	// tar and zip write directories with a trailing slash, so only a leading
	// one means "absolute". GNU tar would strip it and carry on; a JDK archive
	// has no business containing one, and this is an archive we are about to
	// give an execute bit to, so it is treated as a reason to stop.
	if strings.HasPrefix(name, "/") || looksAbsWindows(name) {
		return "", fmt.Errorf("%w: absolute entry name %q", ErrBadArchive, name)
	}
	name = strings.TrimRight(name, "/")
	if name == "" || name == "." {
		return "", nil
	}
	if strings.Contains(name, "\x00") {
		return "", fmt.Errorf("%w: entry name contains a null byte", ErrBadArchive)
	}

	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: entry %q escapes the archive", ErrBadArchive, name)
	}
	return cleaned, nil
}

// looksAbsWindows catches C:\... and \\server\share, which path.IsAbs does not
// treat as absolute but Windows does.
func looksAbsWindows(name string) bool {
	if strings.HasPrefix(name, "//") {
		return true
	}
	return len(name) >= 2 && name[1] == ':' &&
		((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z'))
}
