package javaruntime

import (
	"os"
	"os/exec"
	"sort"
	"time"
)

// Where an available Java came from.
//
// Named Origin rather than Source because this package already has a Source,
// and it means something else entirely: SourceAuto and SourceOfficial pick the
// mirror a download comes from. Two enums both called Source, one about CDNs
// and one about whether the panel unpacked this JDK itself, would be read as
// one enum by anybody who had not just written them.
const (
	OriginManaged  = "managed"  // unpacked under the runtimes root
	OriginExternal = "external" // a path the operator registered
)

// Available is one Java an instance is allowed to point at.
//
// The two origins are merged into one shape because every consumer — the
// dropdown, the whitelist check, the Java page — wants the same four facts
// (path, version, where it came from, is it still there) and none of them
// wants to branch on which list it was in.
type Available struct {
	ID       string `json:"id"`
	JavaPath string `json:"javaPath"`
	Vendor   string `json:"vendor"`
	Version  string `json:"version"`
	Major    int    `json:"major"`
	Origin   string `json:"origin"`
	// Valid is false for a path that is no longer there — a JDK uninstalled
	// behind the panel's back. Such an entry is kept and marked rather than
	// dropped: an instance still points at it, and a dropdown that silently
	// loses the selected option is worse than one that says why.
	Valid bool `json:"valid"`

	// Managed runtimes only; zero for a registered path.
	Path        string    `json:"path"`
	ImageType   string    `json:"imageType"`
	Size        int64     `json:"size"`
	InstalledAt time.Time `json:"installedAt"`
}

// Usable reports whether the launcher is still where the entry says it is.
// A bare "java" means PATH, so it is resolved the way the JVM would be.
func Usable(javaPath string) bool {
	if javaPath == "" {
		return false
	}
	if javaPath == javaBinary() || javaPath == "java" {
		_, err := exec.LookPath(javaPath)
		return err == nil
	}
	info, err := os.Stat(javaPath)
	return err == nil && !info.IsDir()
}

// AvailableList merges the runtimes directory with the registry.
//
// A launcher present in both is one Java, and the managed row wins: it is the
// one that carries a size, an image type and a delete button that does
// something.
func AvailableList(store *Store, reg *Registry) ([]Available, error) {
	runtimes, err := store.List()
	if err != nil {
		return nil, err
	}

	out := make([]Available, 0, len(runtimes))
	seen := make(map[string]bool, len(runtimes))
	for _, rt := range runtimes {
		seen[rt.JavaPath] = true
		out = append(out, Available{
			ID:          rt.ID,
			JavaPath:    rt.JavaPath,
			Vendor:      rt.Vendor,
			Version:     rt.Version,
			Major:       rt.Major,
			Origin:      OriginManaged,
			Valid:       Usable(rt.JavaPath),
			Path:        rt.Path,
			ImageType:   rt.ImageType,
			Size:        rt.Size,
			InstalledAt: rt.InstalledAt,
		})
	}

	if reg != nil {
		for _, entry := range reg.List() {
			if seen[entry.JavaPath] {
				continue
			}
			out = append(out, Available{
				ID:          entry.ID,
				JavaPath:    entry.JavaPath,
				Vendor:      entry.Vendor,
				Version:     entry.Version,
				Major:       entry.Major,
				Origin:      OriginExternal,
				Valid:       Usable(entry.JavaPath),
				InstalledAt: entry.AddedAt,
			})
		}
	}

	// Newest major first, matching Store.List, so the dropdown's first option
	// is the one most servers want.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Major != out[b].Major {
			return out[a].Major > out[b].Major
		}
		if out[a].Origin != out[b].Origin {
			return out[a].Origin == OriginManaged
		}
		return out[a].ID < out[b].ID
	})
	return out, nil
}
