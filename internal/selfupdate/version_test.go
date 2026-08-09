package selfupdate

import (
	"runtime"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.99.99", 1},
		// Double-digit fields must compare numerically; a lexical compare would
		// put 1.10.0 before 1.9.0 and hide the newer release.
		{"1.10.0", "1.9.0", 1},
		{"1.0.10", "1.0.9", 1},
		// A pre-release precedes the release it leads to.
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0", "1.2.0-rc.1", 1},
		{"1.2.0-rc.1", "1.2.0-rc.2", -1},
		{"1.2.0-rc.2", "1.2.0-rc.10", -1},
		{"1.2.0-alpha", "1.2.0-beta", -1},
		{"1.2.0-rc.1", "1.2.0-rc.1", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsReleaseVersion(t *testing.T) {
	valid := []string{"1.0.0", "v1.0.0", "0.0.1", "1.2.3-rc.1", "10.20.30"}
	for _, v := range valid {
		if !IsReleaseVersion(v) {
			t.Errorf("IsReleaseVersion(%q) = false, want true", v)
		}
	}
	// "dev" is what a local `go build` produces; offering to replace it would
	// overwrite somebody's own build with a release.
	invalid := []string{"", "dev", "1.0", "1", "v", "abc", "1.0.x", "1..0"}
	for _, v := range invalid {
		if IsReleaseVersion(v) {
			t.Errorf("IsReleaseVersion(%q) = true, want false", v)
		}
	}
}

func TestAssetNameMatchesReleaseWorkflow(t *testing.T) {
	// The workflow packages hypercraft-<version>-<goos>-<goarch>.<ext>; if the
	// two ever disagree, every update fails with "no build for your platform".
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	want := "hypercraft-1.2.3-" + runtime.GOOS + "-" + runtime.GOARCH + ext
	if got := AssetName("v1.2.3"); got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}
