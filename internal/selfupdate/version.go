package selfupdate

import (
	"strconv"
	"strings"
)

// NormalizeVersion strips the leading "v" that tags carry but release names and
// asset filenames do not, so "v1.2.0" and "1.2.0" compare equal.
func NormalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// IsReleaseVersion reports whether v looks like a version this package can
// reason about. A binary built outside the release workflow reports "dev", and
// offering to "update" that would overwrite someone's local build.
func IsReleaseVersion(v string) bool {
	v = NormalizeVersion(v)
	if v == "" || v == "dev" {
		return false
	}
	core, _, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// CompareVersions orders two semantic versions, returning -1 if a sorts before
// b, 0 if they are equal, and 1 if a sorts after b. A pre-release sorts before
// the release it leads to, so 1.2.0-rc.1 < 1.2.0.
func CompareVersions(a, b string) int {
	aCore, aPre, _ := strings.Cut(NormalizeVersion(a), "-")
	bCore, bPre, _ := strings.Cut(NormalizeVersion(b), "-")

	if c := compareNumeric(aCore, bCore); c != 0 {
		return c
	}
	// Equal cores: the one without a pre-release suffix is the later version.
	switch {
	case aPre == "" && bPre == "":
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	}
	return comparePreRelease(aPre, bPre)
}

// compareNumeric compares dot-separated numeric cores field by field. A field
// that is not a number sorts as 0 rather than failing: this runs against a
// version already accepted by IsReleaseVersion, and a panic here would take the
// update check down over a malformed tag.
func compareNumeric(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < max(len(aParts), len(bParts)); i++ {
		var x, y int
		if i < len(aParts) {
			x, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			y, _ = strconv.Atoi(bParts[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// comparePreRelease applies the semver rule for pre-release identifiers:
// numeric ones compare numerically, others lexically, and numeric sorts below
// alphanumeric. A longer identifier list wins when the shared prefix is equal.
func comparePreRelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < min(len(aParts), len(bParts)); i++ {
		x, xErr := strconv.Atoi(aParts[i])
		y, yErr := strconv.Atoi(bParts[i])
		switch {
		case xErr == nil && yErr == nil:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		case xErr == nil:
			return -1
		case yErr == nil:
			return 1
		default:
			if c := strings.Compare(aParts[i], bParts[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(aParts) < len(bParts):
		return -1
	case len(aParts) > len(bParts):
		return 1
	}
	return 0
}
