// Package config holds the panel's own settings (as opposed to the settings of
// the Minecraft servers it manages).
package config

import (
	"path/filepath"

	"github.com/lanscarlos/hypercraft/internal/auth"
)

// DefaultListen is the panel's bind address. It binds every interface so the
// panel is reachable from outside the host without extra configuration. That
// convenience has a price: the panel can run arbitrary console commands and
// speaks plain HTTP, so a publicly reachable instance should sit behind a
// reverse proxy with TLS. Operators who want the old loopback-only behaviour
// can pass --listen 127.0.0.1:19190.
const DefaultListen = "0.0.0.0:19190"

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

// DefaultUpdateChannel is the release channel a panel follows unless the
// operator picks the other one. Snapshots are built from every green commit on
// main and are not release-tested, so nothing opts a panel into them.
const DefaultUpdateChannel = "stable"

// Panel is the persisted panel configuration.
type Panel struct {
	Listen          string `json:"listen"`
	SessionTTLHours int    `json:"sessionTtlHours"`
	MaxUploadMB     int    `json:"maxUploadMb"`
	// UpdateMirror is a pointer to tell "never configured" (nil, take the
	// default) apart from "deliberately turned off" (empty string, go straight
	// to GitHub). A plain string could not express the second.
	UpdateMirror *string `json:"updateMirror,omitempty"`
	// UpdateChannel is "stable" or "snapshot"; empty means stable, which is
	// what a config written before channels existed carries. No pointer here:
	// there is nothing to express beyond the two channels, and stable is both
	// the default and the safe answer for anything unrecognised.
	UpdateChannel string `json:"updateChannel,omitempty"`
	// JavaSource is where Java runtime downloads are pulled from, by the id of
	// one of javaruntime's sources. Empty means the automatic choice, which is
	// both the default and what every config written before sources existed
	// carries — so no pointer is needed to tell them apart.
	//
	// It is remembered from the last install rather than set on a settings
	// page: "this machine's line to GitHub is bad" is true for every download,
	// and re-picking the mirror each time would be the annoyance.
	JavaSource string `json:"javaSource,omitempty"`
	// GitHubToken authenticates the panel's plugin lookups and downloads, so a
	// plugin the operator publishes to their own private repository can be
	// tracked like any other. Empty means anonymous requests, which is all a
	// public repository needs.
	//
	// It lives here, beside the password hash, because panel.json is written
	// 0600 and is already the file that must not be readable by anyone but the
	// panel. It is never sent back out over the API — see handlePluginToken.
	GitHubToken string `json:"githubToken,omitempty"`
	// Terminal configures the host shell terminal. Off unless the operator
	// turns it on — see Terminal.
	Terminal   Terminal        `json:"terminal"`
	Credential auth.Credential `json:"credential"`
	// Devices are the paired native clients. Unlike sessions, which are
	// deliberately in-memory, these survive a restart — a phone app should not
	// be signed out every time the panel updates itself.
	Devices []auth.DeviceToken `json:"devices,omitempty"`
}

// Terminal configures the in-panel shell.
//
// It is off by default and stays off through an upgrade, because turning it on
// changes what the panel password is worth: without it, someone who guesses it
// can manage Minecraft servers; with it, they have a shell as the user the
// panel runs as. That is a decision for the operator, not a default.
type Terminal struct {
	Enabled bool `json:"enabled"`
	// Shell overrides the program the terminal runs. Empty picks the login
	// shell from $SHELL, falling back to bash and then sh.
	Shell string `json:"shell,omitempty"`
}

// Defaults returns a config with everything but the credential filled in.
func Defaults() Panel {
	mirror := DefaultUpdateMirror
	return Panel{
		Listen:          DefaultListen,
		SessionTTLHours: 24 * 7,
		MaxUploadMB:     DefaultMaxUploadMB,
		UpdateMirror:    &mirror,
		UpdateChannel:   DefaultUpdateChannel,
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
	if p.UpdateChannel == "" {
		p.UpdateChannel = DefaultUpdateChannel
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

// Channel is the configured update channel, defaulting to stable.
func (p Panel) Channel() string {
	if p.UpdateChannel == "" {
		return DefaultUpdateChannel
	}
	return p.UpdateChannel
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

// PluginsRoot is the panel-wide plugin library. Unlike the core library, which
// holds one file per jar, a plugin keeps every version the panel downloaded —
// pinning and rolling back a plugin is routine, so the history is the point.
func (p Paths) PluginsRoot() string { return filepath.Join(p.Root, "plugins") }

// InstancePluginsFile records which library plugins each instance was given,
// and at which version. It sits beside instances.json rather than inside the
// plugin library, so a plugin id can never collide with it.
func (p Paths) InstancePluginsFile() string { return filepath.Join(p.Root, "instance-plugins.json") }

// ResumeFile records which servers were running when the panel restarted
// itself to install an update, so they can be brought back afterwards. It is
// written just before the restart and consumed on the next boot.
func (p Paths) ResumeFile() string { return filepath.Join(p.Root, "resume.json") }
