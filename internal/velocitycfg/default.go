package velocitycfg

import (
	_ "embed"
	"strings"
)

// FileName is what Velocity reads on startup, in the proxy's own directory.
const FileName = "velocity.toml"

// DefaultSecretFile is where Velocity keeps the forwarding secret unless the
// config points somewhere else.
const DefaultSecretFile = "forwarding.secret"

// defaultConfig is Velocity 3.x's own velocity.toml, comments included, with
// the three example sub-servers and the example forced host taken out.
//
// The panel writes it when a proxy has never been started, so 代理配置 is a
// page you can fill in on the day you create the instance rather than one that
// needs a boot first. Every comment upstream ships is kept: this file is also
// read by hand, and the explanation of what "modern" forwarding means is worth
// more there than anywhere the panel could put it.
//
//go:embed default.toml
var defaultConfig string

// Default returns a fresh copy of Velocity's stock configuration.
func Default() *File {
	file, err := Parse(strings.NewReader(defaultConfig))
	if err != nil {
		// Parse only fails on a read error, and a string cannot have one.
		return &File{}
	}
	return file
}
