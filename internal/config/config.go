// Package config holds the panel's own settings (as opposed to the settings of
// the Minecraft servers it manages).
package config

import (
	"path/filepath"

	"github.com/lanscarlos/hypercraft/internal/auth"
)

// DefaultListen is the panel's bind address. It is loopback-only on purpose:
// the panel can run arbitrary console commands, so exposing it to the internet
// should be a conscious act (a reverse proxy with TLS, or an explicit --listen).
const DefaultListen = "127.0.0.1:8080"

// Panel is the persisted panel configuration.
type Panel struct {
	Listen          string          `json:"listen"`
	SessionTTLHours int             `json:"sessionTtlHours"`
	Credential      auth.Credential `json:"credential"`
}

// Defaults returns a config with everything but the credential filled in.
func Defaults() Panel {
	return Panel{
		Listen:          DefaultListen,
		SessionTTLHours: 24 * 7,
	}
}

// ApplyDefaults fills in blanks left by an older or hand-edited config file.
func (p *Panel) ApplyDefaults() {
	if p.Listen == "" {
		p.Listen = DefaultListen
	}
	if p.SessionTTLHours <= 0 {
		p.SessionTTLHours = 24 * 7
	}
}

// Paths resolves the on-disk layout below a data directory.
type Paths struct {
	Root string
}

func NewPaths(root string) Paths { return Paths{Root: root} }

// PanelFile is where panel settings and the credential live.
func (p Paths) PanelFile() string { return filepath.Join(p.Root, "panel.json") }

// InstancesFile is the registry of managed servers.
func (p Paths) InstancesFile() string { return filepath.Join(p.Root, "instances.json") }

// ServersRoot is the default parent directory for new instances.
func (p Paths) ServersRoot() string { return filepath.Join(p.Root, "servers") }
