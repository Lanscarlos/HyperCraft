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

// DefaultUpdateMirror prefixes release downloads during a panel self-update.
// GitHub's release CDN is slow to unusable from parts of Asia, which is where
// most of this panel's operators are, so a proxy is the default rather than an
// opt-in. It only ever carries the release archive: the checksums it is
// verified against are fetched from GitHub itself, so a mirror cannot swap the
// binary for one of its own. See internal/selfupdate.
const DefaultUpdateMirror = "https://ghfast.top/"

// Panel is the persisted panel configuration.
type Panel struct {
	Listen          string `json:"listen"`
	SessionTTLHours int    `json:"sessionTtlHours"`
	MaxUploadMB     int    `json:"maxUploadMb"`
	// UpdateMirror is a pointer to tell "never configured" (nil, take the
	// default) apart from "deliberately turned off" (empty string, go straight
	// to GitHub). A plain string could not express the second.
	UpdateMirror *string         `json:"updateMirror,omitempty"`
	Credential   auth.Credential `json:"credential"`
}

// Defaults returns a config with everything but the credential filled in.
func Defaults() Panel {
	mirror := DefaultUpdateMirror
	return Panel{
		Listen:          DefaultListen,
		SessionTTLHours: 24 * 7,
		MaxUploadMB:     DefaultMaxUploadMB,
		UpdateMirror:    &mirror,
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
	if p.UpdateMirror == nil {
		mirror := DefaultUpdateMirror
		p.UpdateMirror = &mirror
	}
}

// Mirror is the configured update mirror, or "" for downloading straight from
// GitHub.
func (p Panel) Mirror() string {
	if p.UpdateMirror == nil {
		return DefaultUpdateMirror
	}
	return *p.UpdateMirror
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

// JavaRoot is where downloaded Java runtimes are unpacked. Anything dropped in
// here by hand is picked up too, so it doubles as "the panel's JDK shelf".
func (p Paths) JavaRoot() string { return filepath.Join(p.Root, "java") }

// CoresRoot is the panel-wide library of server jars. A core is downloaded once
// and copied into as many instances as needed, so a new server can be created
// offline; a jar dropped in here by hand is listed too.
func (p Paths) CoresRoot() string { return filepath.Join(p.Root, "cores") }

// ResumeFile records which servers were running when the panel restarted
// itself to install an update, so they can be brought back afterwards. It is
// written just before the restart and consumed on the next boot.
func (p Paths) ResumeFile() string { return filepath.Join(p.Root, "resume.json") }
