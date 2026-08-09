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

// DefaultMaxUploadMB caps a single uploaded file. Modpack server jars and
// world archives run to hundreds of megabytes, so a conservative limit would
// just make the file manager useless for the thing people upload most.
const DefaultMaxUploadMB = 2048

// Panel is the persisted panel configuration.
type Panel struct {
	Listen          string          `json:"listen"`
	SessionTTLHours int             `json:"sessionTtlHours"`
	MaxUploadMB     int             `json:"maxUploadMb"`
	Credential      auth.Credential `json:"credential"`
}

// Defaults returns a config with everything but the credential filled in.
func Defaults() Panel {
	return Panel{
		Listen:          DefaultListen,
		SessionTTLHours: 24 * 7,
		MaxUploadMB:     DefaultMaxUploadMB,
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
	if p.MaxUploadMB <= 0 {
		p.MaxUploadMB = DefaultMaxUploadMB
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

// ResumeFile records which servers were running when the panel restarted
// itself to install an update, so they can be brought back afterwards. It is
// written just before the restart and consumed on the next boot.
func (p Paths) ResumeFile() string { return filepath.Join(p.Root, "resume.json") }
