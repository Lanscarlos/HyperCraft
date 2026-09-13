package javaruntime

import "errors"

var (
	// ErrUnsupported is returned for a platform there is no Java build for.
	ErrUnsupported = errors.New("unsupported platform")
	// ErrUnknownRelease is returned when no build matches the request.
	ErrUnknownRelease = errors.New("no matching java build")
	// ErrUpstream wraps anything an upstream metadata API did that we cannot
	// act on.
	ErrUpstream = errors.New("java metadata api")
)

// ImageType selects how much of the JDK to install.
const (
	// ImageJRE is enough to run a server and is about a third smaller.
	ImageJRE = "jre"
	// ImageJDK adds the compiler and tools; some plugins and profilers want it.
	ImageJDK = "jdk"
)

func validImageType(kind string) bool { return kind == ImageJRE || kind == ImageJDK }

// Major is one Java feature release on offer.
type Major struct {
	Major int  `json:"major"`
	LTS   bool `json:"lts"`
}

// Release is a specific build of a major version, for one platform.
type Release struct {
	// Distribution is who built it. It rides along on the release so that
	// everything downstream — the source list, the install id, the error
	// messages — can ask the release instead of taking another parameter.
	Distribution string `json:"distribution"`
	Major        int    `json:"major"`
	Version      string `json:"version"`   // e.g. 21.0.12+8
	Name         string `json:"name"`      // upstream release name, e.g. jdk-21.0.12+8
	ImageType    string `json:"imageType"` // jre or jdk
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	FileName     string `json:"fileName"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}
