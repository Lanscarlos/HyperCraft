package confighist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Limits are the three hard gates of the design's §2.
//
// Hard, not advisory. A gitignore that misses something leaves the repository
// slightly dirty; one 2 GB world committed once leaves it permanently fat,
// because Git history cannot be trimmed without rewriting it. So a commit that
// trips a gate does not go in with a warning — it does not go in.
type Limits struct {
	// FileBytes caps one file. Config files are kilobytes; something in the
	// hundreds of kilobytes is usually a data file that slipped through.
	FileBytes int64 `json:"fileBytes"`
	// FileCount caps one commit. Tripping this almost always means the rules
	// missed a data directory rather than that the operator edited 500 files.
	FileCount int `json:"fileCount"`
	// RepoBytes caps the repository.
	RepoBytes int64 `json:"repoBytes"`
}

// DefaultLimits are the design's defaults.
var DefaultLimits = Limits{
	FileBytes: 512 << 10,
	FileCount: 500,
	RepoBytes: 200 << 20,
}

func (l Limits) withDefaults() Limits {
	if l.FileBytes <= 0 {
		l.FileBytes = DefaultLimits.FileBytes
	}
	if l.FileCount <= 0 {
		l.FileCount = DefaultLimits.FileCount
	}
	if l.RepoBytes <= 0 {
		l.RepoBytes = DefaultLimits.RepoBytes
	}
	return l
}

// InstanceSettings is what one instance's config history remembers between
// panel restarts, beyond the repository itself.
type InstanceSettings struct {
	// Disabled turns the module off for this instance. Set by the operator, and
	// set automatically for an instance sharing its directory with another —
	// see the design's §10.
	Disabled bool `json:"disabled,omitempty"`
	// Limits overrides the defaults. A WorldGuard regions.yml that runs to tens
	// of megabytes is a legitimate exception, and the answer to it is a raised
	// ceiling for that instance rather than a raised default for everyone.
	Limits Limits `json:"limits"`
	// Allow lists paths that may be recorded despite exceeding FileBytes. This
	// is the "确认收录" half of what a tripped gate asks the operator.
	Allow []string `json:"allow,omitempty"`
	// Exclude lists paths never to record. The "永久排除" half.
	Exclude []string `json:"exclude,omitempty"`

	CompactedAt time.Time `json:"compactedAt,omitempty"`
	// SincePrune counts commits since the last unreachable-object sweep, which
	// is this store's stand-in for `git gc --auto`. See Repo.Prune.
	SincePrune int `json:"sincePrune,omitempty"`
}

func (s InstanceSettings) limits() Limits { return s.Limits.withDefaults() }

func (s InstanceSettings) allows(path string) bool {
	for _, entry := range s.Allow {
		if normalisePath(entry) == path {
			return true
		}
	}
	return false
}

// settingsFile is the whole store: one JSON object keyed by instance id, beside
// the panel's other registries.
type settingsFile map[string]*InstanceSettings

func (s *Service) loadSettings() settingsFile {
	if s.settings != nil {
		return s.settings
	}
	s.settings = settingsFile{}

	data, err := os.ReadFile(s.settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			s.log.Warn("could not read the config history settings", "err", err)
		}
		return s.settings
	}
	if err := json.Unmarshal(data, &s.settings); err != nil {
		// A corrupt settings file must not take the history down with it: the
		// repositories are the data, this is preferences about them.
		s.log.Warn("config history settings are unreadable, falling back to defaults", "err", err)
		s.settings = settingsFile{}
	}
	return s.settings
}

// settingsFor returns the instance's settings, creating them if needed. The
// caller holds s.mu.
func (s *Service) settingsFor(instanceID string) *InstanceSettings {
	all := s.loadSettings()
	if entry := all[instanceID]; entry != nil {
		return entry
	}
	entry := &InstanceSettings{Limits: DefaultLimits}
	all[instanceID] = entry
	return entry
}

// Settings returns a copy of one instance's settings.
func (s *Service) Settings(instanceID string) InstanceSettings {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := *s.settingsFor(instanceID)
	entry.Limits = entry.limits()
	return entry
}

// UpdateSettings applies a change under the lock and persists the result.
func (s *Service) UpdateSettings(instanceID string, apply func(*InstanceSettings)) (InstanceSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.settingsFor(instanceID)
	apply(entry)
	entry.Allow = tidyPaths(entry.Allow)
	entry.Exclude = tidyPaths(entry.Exclude)

	if err := s.persistLocked(); err != nil {
		return InstanceSettings{}, err
	}
	out := *entry
	out.Limits = out.limits()
	return out, nil
}

// Forget drops an instance's settings. Called when the instance is deleted,
// along with the repository itself.
func (s *Service) Forget(instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.loadSettings()
	if _, ok := all[instanceID]; !ok {
		return s.removeRepo(instanceID)
	}
	delete(all, instanceID)
	if err := s.persistLocked(); err != nil {
		return err
	}
	return s.removeRepo(instanceID)
}

func (s *Service) removeRepo(instanceID string) error {
	dir := s.repoDir(instanceID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove config history: %w", err)
	}
	// The per-instance parent is ours too, and only holds the repository.
	_ = os.Remove(filepath.Dir(dir))
	return nil
}

func (s *Service) persistLocked() error {
	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config history settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.settingsPath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.settingsPath), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(name)
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.settingsPath)
}

func tidyPaths(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, entry := range in {
		clean := normalisePath(entry)
		if clean == "" || clean == "." || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
