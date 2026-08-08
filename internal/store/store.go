// Package store persists panel settings and the instance registry as JSON on
// disk. There is no database: the data is a handful of kilobytes, and a plain
// file the operator can read and edit is a feature for a self-hosted tool.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/instance"
)

// Store serialises access to the panel's JSON files.
type Store struct {
	paths config.Paths
	mu    sync.Mutex
}

func New(paths config.Paths) (*Store, error) {
	for _, dir := range []string{paths.Root, paths.ServersRoot()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return &Store{paths: paths}, nil
}

// LoadPanel reads panel.json, returning defaults if it does not exist yet.
func (s *Store) LoadPanel() (config.Panel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	panel := config.Defaults()
	data, err := os.ReadFile(s.paths.PanelFile())
	if err != nil {
		if os.IsNotExist(err) {
			return panel, nil
		}
		return panel, fmt.Errorf("read panel config: %w", err)
	}
	if err := json.Unmarshal(data, &panel); err != nil {
		return panel, fmt.Errorf("parse panel config: %w", err)
	}
	panel.ApplyDefaults()
	return panel, nil
}

// SavePanel writes panel.json. It holds the password hash, so the file is
// created 0600 rather than world-readable.
func (s *Store) SavePanel(panel config.Panel) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(panel, "", "  ")
	if err != nil {
		return fmt.Errorf("encode panel config: %w", err)
	}
	return writeFileAtomic(s.paths.PanelFile(), append(data, '\n'), 0o600)
}

// LoadInstances reads the instance registry.
func (s *Store) LoadInstances() ([]instance.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.paths.InstancesFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read instances: %w", err)
	}

	var configs []instance.Config
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("parse instances: %w", err)
	}
	return configs, nil
}

// SaveInstances writes the instance registry. It satisfies instance.Persister.
func (s *Store) SaveInstances(configs []instance.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if configs == nil {
		configs = []instance.Config{}
	}
	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instances: %w", err)
	}
	return writeFileAtomic(s.paths.InstancesFile(), append(data, '\n'), 0o600)
}

// writeFileAtomic writes via a temp file and a rename, so a crash or a full
// disk can never leave a half-written config behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
