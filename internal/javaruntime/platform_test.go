package javaruntime

import "testing"

// musl is only a problem for Temurin, which links against glibc. Zulu builds
// for musl, so on Alpine it is the answer rather than the casualty — and
// CurrentPlatform cannot know which distribution is selected, so the warning
// is computed per distribution instead of baked into the platform.
func TestPlatformWarningIsPerDistribution(t *testing.T) {
	musl := Platform{OS: "linux", Arch: "x64", LibC: "musl"}
	if got := PlatformWarning(DistZulu, musl); got != "" {
		t.Errorf("Zulu on musl should not warn, got %q", got)
	}
	if got := PlatformWarning(DistTemurin, musl); got == "" {
		t.Error("Temurin on musl should warn")
	}

	glibc := Platform{OS: "linux", Arch: "x64", LibC: "glibc"}
	for _, dist := range []string{DistZulu, DistTemurin} {
		if got := PlatformWarning(dist, glibc); got != "" {
			t.Errorf("%s on glibc should not warn, got %q", dist, got)
		}
	}
}

func TestCurrentPlatformReportsLibCAndNoWarning(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("no build for this platform: %v", err)
	}
	if platform.OS == "linux" && platform.LibC == "" {
		t.Error("a linux platform should say which libc it is")
	}
	// Whether this platform is a problem depends on the distribution, which
	// CurrentPlatform does not know, so the warning is not its to fill.
	if platform.Warning != "" {
		t.Errorf("CurrentPlatform should not fill Warning, got %q", platform.Warning)
	}
}
