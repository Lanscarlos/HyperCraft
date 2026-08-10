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
	// PluginMirror is the proxy plugin jars are downloaded through, by the id
	// of one of plugin.Mirrors() or as a custom URL prefix. Empty means the
	// automatic order, which is both the default and what a config written
	// before this setting existed carries — so no pointer is needed to tell
	// them apart.
	//
	// Separate from UpdateMirror on purpose, even though both proxy the same
	// GitHub CDN: the panel updates a few times a year and plugins download
	// weekly, so "the proxy that works for my plugins" is a choice worth making
	// on its own rather than inheriting from a page about panel updates.
	PluginMirror string `json:"pluginMirror,omitempty"`
	// GitHubToken is the single credential panels held before they could hold
	// several. Read on load and folded into GitHubTokens by ApplyDefaults, then
	// left empty — so an upgrade keeps working and a downgrade is the only thing
	// that loses it.
	GitHubToken string `json:"githubToken,omitempty"`
	// GitHubTokens authenticate the panel's plugin lookups and downloads, so a
	// plugin the operator publishes to a private repository can be tracked like
	// any other. Empty means anonymous requests, which is all a public
	// repository needs.
	//
	// A list rather than one token because a fine-grained access token is
	// scoped to one account's repositories: covering a personal repository and
	// an organisation's private fork with a single credential means a classic
	// token with blanket repo scope, which is a much larger key than a panel
	// that reads two repositories has any use for. Each plugin source names the
	// token it is read with; see plugin.Source.TokenID.
	//
	// Order is meaning: the first entry is the default, used by every source
	// that names none — which is every source added before this existed, and
	// every public repository, where a token buys rate limit rather than access.
	//
	// They live here, beside the password hash, because panel.json is written
	// 0600 and is already the file that must not be readable by anyone but the
	// panel. The secrets are never sent back out over the API — see
	// handlePluginTokens.
	GitHubTokens []GitHubToken `json:"githubTokens,omitempty"`
	// TrustedProxies are the CIDRs (or bare IPs, taken as /32 and /128) of
	// reverse proxies and accelerators allowed to speak for their clients. It
	// decides one thing: whether X-Forwarded-For is believed when deciding
	// which address the login rate limiter counts against.
	//
	// Empty — the default — means every request is counted against the address
	// it actually arrived from, which is correct for a panel reached directly.
	// It has to be opt-in: X-Forwarded-For is a header any client can write, so
	// believing it unconditionally would hand the rate limiter's key to the
	// attacker it exists to slow down.
	//
	// Behind something like Alibaba Cloud GA or a CDN the opposite failure
	// appears — every request arrives from a handful of back-to-origin
	// addresses, so one attacker would spend the whole budget and lock out
	// everybody else. Listing those addresses here restores per-client
	// counting.
	TrustedProxies []string `json:"trustedProxies,omitempty"`
	// Terminal configures the host shell terminal. Off unless the operator
	// turns it on — see Terminal.
	Terminal   Terminal        `json:"terminal"`
	Credential auth.Credential `json:"credential"`
	// Devices are the paired native clients. Unlike sessions, which are
	// deliberately in-memory, these survive a restart — a phone app should not
	// be signed out every time the panel updates itself.
	Devices []auth.DeviceToken `json:"devices,omitempty"`
}

// GitHubToken is one stored GitHub credential.
type GitHubToken struct {
	// ID is what a plugin source points at. Stable across renames and across
	// the secret being replaced: an id that changed when a token was rotated
	// would silently detach every plugin using it.
	ID string `json:"id"`
	// Name is the operator's own label — "我的私库", "公司 org" — and is what
	// error messages call this token. Never the secret.
	Name string `json:"name"`
	// Token is the credential. It leaves this file only as an Authorization
	// header to api.github.com.
	Token string `json:"token"`
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
	p.migrateGitHubToken()
}

// LegacyTokenID is the id the one token a panel used to hold becomes when it is
// folded into the list. Fixed rather than random so the migration is idempotent
// and so a hand-written panel.json has something to name.
const LegacyTokenID = "default"

// migrateGitHubToken folds the single stored token into the list.
//
// The panel used to hold exactly one, and every plugin added under that panel
// names no token at all — so the migrated entry has to land at the head of the
// list, where "names none" resolves to. Emptying the old field afterwards is
// what stops it being re-migrated into a second copy the next time the config
// is read and written.
func (p *Panel) migrateGitHubToken() {
	if p.GitHubToken == "" {
		return
	}
	legacy := GitHubToken{ID: LegacyTokenID, Name: "默认令牌", Token: p.GitHubToken}
	p.GitHubToken = ""
	for _, token := range p.GitHubTokens {
		// Either the same secret or the id it would take means this migration
		// already ran; the old field is a leftover copy rather than a token the
		// list is missing.
		if token.Token == legacy.Token || token.ID == legacy.ID {
			return
		}
	}
	p.GitHubTokens = append([]GitHubToken{legacy}, p.GitHubTokens...)
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

// DatabaseRoot is where the panel keeps the databases it set up: engines/ holds
// the downloaded binaries, which are shared, and services/ holds one directory
// per database — its data files and its log. They sit together because deleting
// this one directory is what "undo the database feature" means.
func (p Paths) DatabaseRoot() string { return filepath.Join(p.Root, "db") }

// DatabaseEnginesRoot is where downloaded database engines are unpacked.
// Anything dropped in here by hand is picked up too, like the Java shelf.
func (p Paths) DatabaseEnginesRoot() string { return filepath.Join(p.DatabaseRoot(), "engines") }

// DatabasesFile is the registry of databases the panel manages. It sits beside
// instances.json rather than inside the database root, so a service id can
// never collide with it — and, more to the point, it holds the passwords, so it
// is one of the files written 0600.
func (p Paths) DatabasesFile() string { return filepath.Join(p.Root, "databases.json") }

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

// PendingPluginsFile records plugin changes a running server has not seen yet,
// so the "N 项变更待重启生效" banner survives a page reload and a panel
// restart. Its own file rather than a field on the install records: it is
// cleared by the passage of a restart rather than by anything the operator
// does, and losing it costs a banner, not a change.
func (p Paths) PendingPluginsFile() string { return filepath.Join(p.Root, "pending-plugins.json") }

// ResumeFile records which servers were running when the panel restarted
// itself to install an update, so they can be brought back afterwards. It is
// written just before the restart and consumed on the next boot.
func (p Paths) ResumeFile() string { return filepath.Join(p.Root, "resume.json") }
