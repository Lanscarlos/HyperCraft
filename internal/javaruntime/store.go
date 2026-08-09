package javaruntime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNotFound is returned for an unknown runtime ID.
	ErrNotFound = errors.New("java runtime not found")
	// ErrInvalidID is returned for an ID that does not name a directory in the
	// runtimes root.
	ErrInvalidID = errors.New("invalid runtime id")
)

// Runtime is one Java installation the panel can launch servers with.
type Runtime struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// JavaPath is what goes into an instance's launch config. Empty means the
	// directory is there but has no runnable java in it.
	JavaPath    string    `json:"javaPath"`
	Vendor      string    `json:"vendor"`
	Version     string    `json:"version"`
	Major       int       `json:"major"`
	ImageType   string    `json:"imageType"`
	Size        int64     `json:"size"`
	InstalledAt time.Time `json:"installedAt"`
}

// Store owns the runtimes directory, normally <data>/java.
//
// Anything found in there is listed, not just what the panel downloaded: an
// operator who unpacks their own JDK into it gets it in the dropdown for free,
// because every OpenJDK build ships the `release` file this reads.
type Store struct {
	root string
}

func NewStore(root string) *Store { return &Store{root: root} }

// Root is the directory runtimes are installed into.
func (s *Store) Root() string { return s.root }

// List returns every runtime, newest major first.
func (s *Store) List() ([]Runtime, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Runtime{}, nil
		}
		return nil, err
	}

	runtimes := make([]Runtime, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		runtime, err := s.inspect(entry.Name())
		if err != nil {
			continue
		}
		runtimes = append(runtimes, runtime)
	}

	sort.Slice(runtimes, func(a, b int) bool {
		if runtimes[a].Major != runtimes[b].Major {
			return runtimes[a].Major > runtimes[b].Major
		}
		return runtimes[a].ID < runtimes[b].ID
	})
	return runtimes, nil
}

// Get returns one runtime by ID.
func (s *Store) Get(id string) (Runtime, error) {
	if err := validID(id); err != nil {
		return Runtime{}, err
	}
	return s.inspect(id)
}

// Remove deletes a runtime from disk.
func (s *Store) Remove(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.root, id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return os.RemoveAll(dir)
}

func (s *Store) inspect(id string) (Runtime, error) {
	dir := filepath.Join(s.root, id)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Runtime{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	runtime := Runtime{
		ID:          id,
		Path:        dir,
		InstalledAt: info.ModTime(),
		JavaPath:    findJava(dir),
		Size:        directorySize(dir),
	}
	applyReleaseFile(&runtime, dir)

	if runtime.Version == "" && runtime.JavaPath != "" {
		// No release file (a stripped or hand-built runtime): ask the binary.
		if probed, ok := probe(context.Background(), runtime.JavaPath); ok {
			runtime.Version, runtime.Major, runtime.Vendor = probed.Version, probed.Major, probed.Vendor
		}
	}
	if runtime.Version == "" && runtime.JavaPath == "" {
		return Runtime{}, fmt.Errorf("%w: %s has no java in it", ErrNotFound, id)
	}
	return runtime, nil
}

// findJava locates the launcher inside an install directory. The layout is
// bin/java everywhere except macOS, where the bundle puts it two levels down.
func findJava(dir string) string {
	binary := javaBinary()
	for _, candidate := range []string{
		filepath.Join(dir, "bin", binary),
		filepath.Join(dir, "Contents", "Home", "bin", binary),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// applyReleaseFile fills in what the JDK says about itself. Every OpenJDK
// build ships this file, including the ones we did not download.
func applyReleaseFile(runtime *Runtime, dir string) {
	for _, candidate := range []string{
		filepath.Join(dir, "release"),
		filepath.Join(dir, "Contents", "Home", "release"),
	} {
		file, err := os.Open(candidate)
		if err != nil {
			continue
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			key, value, found := strings.Cut(scanner.Text(), "=")
			if !found {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch strings.TrimSpace(key) {
			case "JAVA_VERSION":
				runtime.Version = value
				runtime.Major = majorOf(value)
			case "IMPLEMENTOR":
				runtime.Vendor = value
			case "IMAGE_TYPE":
				runtime.ImageType = strings.ToLower(value)
			}
		}
		return
	}
}

func directorySize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a partially readable runtime still reports a useful size
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func validID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`+"\x00") {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}

// ---------------------------------------------------------- system runtime

// SystemJava is the java already on the machine, if there is one.
type SystemJava struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Vendor  string `json:"vendor"`
	// Source says where it was found: JAVA_HOME or PATH.
	Source string `json:"source"`
}

// DetectSystem finds the java an instance configured with a bare "java" would
// actually run, and asks it what version it is.
func DetectSystem(ctx context.Context) (SystemJava, bool) {
	binary := javaBinary()
	candidates := []struct{ path, source string }{}
	if home := strings.TrimSpace(os.Getenv("JAVA_HOME")); home != "" {
		candidates = append(candidates, struct{ path, source string }{filepath.Join(home, "bin", binary), "JAVA_HOME"})
	}
	if found, err := exec.LookPath(binary); err == nil {
		candidates = append(candidates, struct{ path, source string }{found, "PATH"})
	}

	for _, candidate := range candidates {
		probed, ok := probe(ctx, candidate.path)
		if !ok {
			continue
		}
		probed.Path = candidate.path
		probed.Source = candidate.source
		return probed, true
	}
	return SystemJava{}, false
}

// versionPattern matches the first line of `java -version`, which looks like
//
//	openjdk version "21.0.12" 2026-07-21
//	java version "1.8.0_502"
var versionPattern = regexp.MustCompile(`version "([^"]+)"`)

// probe runs a java binary to find out what it is. `java -version` writes to
// stderr, which is why both streams are captured.
func probe(ctx context.Context, javaPath string) (SystemJava, bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, javaPath, "-version").CombinedOutput()
	if err != nil && len(output) == 0 {
		return SystemJava{}, false
	}
	match := versionPattern.FindSubmatch(output)
	if match == nil {
		return SystemJava{}, false
	}

	version := string(match[1])
	result := SystemJava{Version: version, Major: majorOf(version)}
	// The second line names the build, e.g. "OpenJDK Runtime Environment
	// Temurin-21.0.12+8 (build ...)". Worth showing, not worth parsing hard.
	if lines := strings.Split(strings.TrimSpace(string(output)), "\n"); len(lines) > 1 {
		result.Vendor = strings.TrimSpace(lines[1])
	}
	return result, true
}

// majorOf turns a Java version string into its feature number. Java 8 and
// earlier report 1.8.0_502, where the major version is the second component.
func majorOf(version string) int {
	version = strings.TrimSpace(version)
	if version == "" {
		return 0
	}
	parts := strings.FieldsFunc(version, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == '+'
	})
	if len(parts) == 0 {
		return 0
	}
	first, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	if first == 1 && len(parts) > 1 {
		if second, err := strconv.Atoi(parts[1]); err == nil {
			return second
		}
	}
	return first
}
