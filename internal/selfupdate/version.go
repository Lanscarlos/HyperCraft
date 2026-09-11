package selfupdate

import (
	"strconv"
	"strings"
)

// NormalizeVersion strips the leading "v" that tags carry but release names and
// asset filenames do not, so "v0.3.0" and "0.3.0" compare equal.
func NormalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// snapshotPrefix starts the whole version of a snapshot build: "snapshot-1234",
// where the number is the commit count of main at that build.
const snapshotPrefix = "snapshot-"

// snapshotBuild returns a snapshot version's commit count, and whether v is a
// snapshot at all.
//
// A snapshot under this naming carries no semantic version. It is not heading
// for a particular release — it is whatever main was at that commit — so
// naming it after the next release means inventing a version nobody has
// decided to cut. The commit count is the only number a snapshot actually has.
//
// Reading this form shipped before the workflow started producing it, and that
// order was not optional. The binary that compares versions is the one already
// installed on the machine: a panel built without this code skips such a tag as
// uncomparable, finds nothing newer it can read, and reports "already up to
// date" forever. Publishing the naming first is exactly how the snapshot
// channel went dead once already.
func snapshotBuild(v string) (int, bool) {
	rest, ok := strings.CutPrefix(NormalizeVersion(v), snapshotPrefix)
	if !ok || rest == "" {
		return 0, false
	}
	// Digits only: strconv.Atoi would also take "+12" and "-3", and a version
	// that compares as a number has to look like one.
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

// IsReleaseVersion reports whether v looks like a version this package can
// reason about. A binary built outside the release workflow reports "dev", and
// offering to "update" that would overwrite someone's local build.
//
// Releases carry all three fields (0.3.0). Snapshots come in two forms, and
// both are accepted: the snapshot-1234 the workflow publishes now, and the
// vX.Y-snapshot.N it used to, whose two-field core compares as three with a
// trailing zero (see compareNumeric) and which panels installed before the
// rename are still running. A panel on either must be accepted here, or it
// would classify itself as a local build and turn panel updates off entirely.
func IsReleaseVersion(v string) bool {
	v = NormalizeVersion(v)
	if v == "" || v == "dev" {
		return false
	}
	if _, ok := snapshotBuild(v); ok {
		return true
	}
	core, _, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 2 && len(parts) != 3 {
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

// IsStableVersion reports whether v is a final release rather than something
// leading up to one — 0.3.0 rather than 0.3.0-rc.1 or snapshot-1234. The "-"
// every non-release form carries is what decides it.
func IsStableVersion(v string) bool {
	return IsReleaseVersion(v) && !strings.Contains(NormalizeVersion(v), "-")
}

// CompareVersions orders two versions, returning -1 if a sorts before b, 0 if
// they are equal, and 1 if a sorts after b. A pre-release sorts before the
// release it leads to, so 0.3.0-rc.1 < 0.3.0.
//
// Snapshots sit outside that order: "snapshot-1234" has no semantic version to
// compare, so two of them compare on their commit count and a snapshot outranks
// everything else. That is the rule the snapshot channel runs on — a snapshot is
// built from main, which already contains every release, so being pulled "up"
// onto a release would move the panel backwards in content. Leaving the
// snapshot track is therefore a deliberate act (switch the channel back to
// stable and take the downgrade Offer reports), not something a release does to
// a panel on its own.
//
// The rule also covers the naming this replaced: a 0.4-snapshot.86 compares as
// an ordinary pre-release and so loses to any snapshot-N, which is what carried
// panels across the rename.
func CompareVersions(a, b string) int {
	aBuild, aSnap := snapshotBuild(a)
	bBuild, bSnap := snapshotBuild(b)
	switch {
	case aSnap && bSnap:
		switch {
		case aBuild < bBuild:
			return -1
		case aBuild > bBuild:
			return 1
		}
		return 0
	case aSnap:
		return 1
	case bSnap:
		return -1
	}

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

// compareNumeric compares dot-separated numeric cores field by field. A missing
// field counts as 0, so a two-field snapshot core sorts exactly where the
// release it is named after will land: 0.4 == 0.4.0, and therefore
// 0.4-snapshot.86 sits between 0.3.0 and 0.4.0.
//
// A field that is not a number sorts as 0 rather than failing: this runs
// against a version already accepted by IsReleaseVersion, and a panic here
// would take the update check down over a malformed tag.
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
