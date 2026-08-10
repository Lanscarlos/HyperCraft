package dbruntime

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Platform is the machine an engine has to be downloaded for.
//
// It carries more than javaruntime.Platform does because these downloads are
// not one portable build per OS. MongoDB publishes a separate tarball per Linux
// distribution and the PostgreSQL builds come in glibc and musl flavours, so
// "which Linux is this" is a question the resolvers actually have to answer.
type Platform struct {
	OS   string `json:"os"`   // linux, windows, darwin
	Arch string `json:"arch"` // amd64, arm64
	// Distro and DistroVersion come from /etc/os-release, e.g. ubuntu / 22.04.
	// Empty off Linux, and empty on a Linux with no os-release, where the
	// resolvers fall back to their most portable build.
	Distro        string `json:"distro,omitempty"`
	DistroVersion string `json:"distroVersion,omitempty"`
	// Musl is true on Alpine and friends.
	Musl bool `json:"musl,omitempty"`
	// Warning is a non-fatal note about this machine.
	Warning string `json:"warning,omitempty"`
}

var (
	platformOnce sync.Once
	platformVal  Platform
	platformErr  error
)

// CurrentPlatform describes the machine the panel is running on. The answer
// cannot change while the process lives, and reading os-release on every
// version listing would be pointless work, so it is worked out once.
func CurrentPlatform() (Platform, error) {
	platformOnce.Do(func() { platformVal, platformErr = detectPlatform() })
	return platformVal, platformErr
}

func detectPlatform() (Platform, error) {
	var platform Platform
	switch runtime.GOOS {
	case "linux", "windows", "darwin":
		platform.OS = runtime.GOOS
	default:
		return Platform{}, fmt.Errorf("%w: 没有 %s 的数据库构建", ErrUnsupported, runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
		platform.Arch = runtime.GOARCH
	default:
		return Platform{}, fmt.Errorf("%w: 没有 %s/%s 的数据库构建",
			ErrUnsupported, runtime.GOOS, runtime.GOARCH)
	}
	if platform.OS != "linux" {
		return platform, nil
	}

	platform.Musl = isMusl()
	platform.Distro, platform.DistroVersion = readOSRelease()
	if platform.Musl {
		// Only PostgreSQL has a musl build. Saying so once, here, is better than
		// three engines each discovering it separately at download time.
		platform.Warning = "这台机器用的是 musl（Alpine 之类）。" +
			"MySQL 和 MongoDB 官方只提供 glibc 构建，装上也起不来；PostgreSQL 有 musl 版本，可以正常使用。"
	}
	return platform, nil
}

// isMusl matches javaruntime's check, for the same reason: a glibc binary on a
// musl system installs perfectly and then fails at the dynamic linker.
func isMusl() bool {
	matches, err := filepath.Glob("/lib/ld-musl-*.so.1")
	return err == nil && len(matches) > 0
}

// readOSRelease returns the distribution id and version, e.g. ubuntu / 22.04.
// A machine without the file — a container built from scratch, an unusual
// distro — reports nothing, and the resolvers pick their fallback build.
func readOSRelease() (string, string) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "", ""
	}
	defer file.Close()

	var id, version string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "ID":
			id = strings.ToLower(value)
		case "VERSION_ID":
			version = value
		}
	}
	return id, version
}

// exeSuffix is what an executable is called on this OS.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
