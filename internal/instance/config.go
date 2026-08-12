package instance

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// State is the lifecycle position of a managed server process.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateCrashed  State = "crashed"
)

// Running reports whether a process exists for this state.
func (s State) Running() bool {
	return s == StateStarting || s == StateRunning || s == StateStopping
}

// The two things an instance can be. A proxy is not a smaller server: it has
// no world, no server.properties and no EULA, it reads a config file of its own
// and it answers a different console command to shut down. Everything the panel
// shows for one is wrong for the other, so the difference is recorded on the
// instance rather than guessed from the jar's name every time it is needed.
//
// Only Velocity is supported as a proxy for now. BungeeCord and Waterfall are
// deliberately absent: their config is a different file with different keys,
// and pretending one setting page fits both would be worse than not offering
// them at all.
const (
	KindServer = "server"
	KindProxy  = "proxy"
)

// Config is the persisted description of one Minecraft server instance.
type Config struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Directory string    `json:"directory"`
	CreatedAt time.Time `json:"createdAt"`

	// Kind is "server" or "proxy". Blank means server, which is what every
	// instance written before this field existed is.
	Kind string `json:"kind"`

	// Launch settings. The usual case is a Java jar; Command overrides the
	// whole argv for servers that are not jars at all (Bedrock's
	// bedrock_server binary, a start.sh wrapper, BungeeCord launchers).
	Java        string   `json:"java"`        // java executable, "java" resolves via PATH
	Jar         string   `json:"jar"`         // server jar, relative to Directory
	MinMemoryMB int      `json:"minMemoryMB"` // -Xms, 0 to omit
	MaxMemoryMB int      `json:"maxMemoryMB"` // -Xmx, 0 to omit
	JVMArgs     []string `json:"jvmArgs"`     // extra flags before -jar
	ServerArgs  []string `json:"serverArgs"`  // args after the jar, e.g. --nogui
	Command     []string `json:"command"`     // full argv; when set, the fields above are ignored

	// Console settings.
	Encoding string `json:"encoding"` // console charset: auto (default), utf-8, gbk, …
	// TTY runs the server on a pseudo-terminal instead of pipes, which is the
	// default: it is what makes JLine offer the server's *own* tab completion,
	// what makes progress output appear as it is drawn instead of only once it
	// ends in a newline, and what makes colour work without being forced.
	// Turning it off restores the pipe console, which keeps stdout and stderr
	// apart and is the only mode Windows can run. Default true.
	TTY *bool `json:"tty"`
	// ForceColor keeps ANSI colours on through a pipe. It has no effect in TTY
	// mode, where the server can see a terminal and colours itself. Default true.
	ForceColor *bool `json:"forceColor"`

	// Supervision settings.
	AutoStart      bool   `json:"autoStart"`      // start when the panel boots
	AutoRestart    bool   `json:"autoRestart"`    // restart after an unexpected exit
	StopCommand    string `json:"stopCommand"`    // console command for a graceful stop
	StopTimeoutSec int    `json:"stopTimeoutSec"` // how long to wait before signalling
}

// Defaults for fields the user left blank. Applied on create and on load, so
// configs written by older versions keep working.
func (c *Config) applyDefaults() {
	if c.Kind == "" {
		c.Kind = KindServer
	}
	if c.Java == "" {
		c.Java = "java"
	}
	if c.StopCommand == "" {
		c.StopCommand = DefaultStopCommand(c.Kind)
	}
	if c.StopTimeoutSec <= 0 {
		c.StopTimeoutSec = 60
	}
	if c.MaxMemoryMB <= 0 {
		// A proxy holds connections, not chunks. Velocity's own docs put it at
		// half a gig and warn against giving it more, since a large heap only
		// makes its GC pauses longer — and a GC pause on the proxy is a pause
		// for everyone behind it.
		if c.IsProxy() {
			c.MaxMemoryMB = 512
		} else {
			c.MaxMemoryMB = 2048
		}
	}
	if c.MinMemoryMB < 0 {
		c.MinMemoryMB = 0
	}
	if c.JVMArgs == nil {
		c.JVMArgs = []string{}
	}
	if c.ServerArgs == nil {
		c.ServerArgs = DefaultServerArgs(c.Kind)
	}
	if canonical, ok := canonicalEncoding(c.Encoding); ok {
		c.Encoding = canonical
	}
	if c.ForceColor == nil {
		// Colours on by default: an instance saved before this option existed
		// should behave like a console, not like a log file.
		on := true
		c.ForceColor = &on
	}
	if c.TTY == nil {
		// On by default, including for instances saved before the option
		// existed: a console attached to a terminal is what the server software
		// itself expects, and every panel-visible behaviour that depends on it
		// (completion, progress output, colour) is better for it.
		on := true
		c.TTY = &on
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
}

// ErrInvalidConfig wraps every validation failure so the API can map it to 400.
var ErrInvalidConfig = errors.New("invalid instance config")

func (c *Config) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidConfig)
	}
	if c.Kind != KindServer && c.Kind != KindProxy {
		return fmt.Errorf("%w: unknown instance kind %q", ErrInvalidConfig, c.Kind)
	}
	if strings.TrimSpace(c.Directory) == "" {
		return fmt.Errorf("%w: directory is required", ErrInvalidConfig)
	}
	if !filepath.IsAbs(c.Directory) {
		return fmt.Errorf("%w: directory must be an absolute path", ErrInvalidConfig)
	}
	if c.MinMemoryMB > 0 && c.MaxMemoryMB > 0 && c.MinMemoryMB > c.MaxMemoryMB {
		return fmt.Errorf("%w: minMemoryMB cannot exceed maxMemoryMB", ErrInvalidConfig)
	}
	if c.Jar != "" {
		// The jar is resolved inside Directory; reject anything that escapes it.
		if filepath.IsAbs(c.Jar) || strings.Contains(filepath.ToSlash(c.Jar), "../") {
			return fmt.Errorf("%w: jar must be a path inside the instance directory", ErrInvalidConfig)
		}
	}
	if c.usesCustomCommand() && strings.TrimSpace(c.Command[0]) == "" {
		return fmt.Errorf("%w: command's first element must be the executable", ErrInvalidConfig)
	}
	if _, ok := canonicalEncoding(c.Encoding); !ok {
		return fmt.Errorf("%w: unknown console encoding %q", ErrInvalidConfig, c.Encoding)
	}
	return nil
}

// IsProxy reports whether this instance runs a proxy rather than a world.
func (c Config) IsProxy() bool { return c.Kind == KindProxy }

// DefaultStopCommand is the console command that asks this kind of software to
// shut down.
//
// Velocity has no "stop": typing it into a proxy console prints an
// unknown-command line, and the panel then waits out the whole stop timeout
// before signalling the process. "end" is the alias Velocity documents.
func DefaultStopCommand(kind string) string {
	if kind == KindProxy {
		return "end"
	}
	return "stop"
}

// DefaultServerArgs are the arguments a fresh instance of this kind launches
// with. --nogui is a Minecraft server flag; Velocity exits on an argument it
// does not know, so a proxy starts with none.
func DefaultServerArgs(kind string) []string {
	if kind == KindProxy {
		return []string{}
	}
	return []string{"--nogui"}
}

// colorForced reports whether the panel should make the server emit ANSI
// colour even though its stdout is a pipe.
func (c *Config) colorForced() bool { return c.ForceColor == nil || *c.ForceColor }

// ttyEnabled reports the operator's choice of console transport, which is only
// a request: whether it is honoured also depends on the platform.
func (c *Config) ttyEnabled() bool { return c.TTY == nil || *c.TTY }

// wantsTTY reports whether this instance will actually be given a terminal.
func (c *Config) wantsTTY() bool { return c.ttyEnabled() && ptySupported }

// consoleJVMArgs are the flags that make the JVM's console behave, given the
// transport it is about to be handed.
//
// They go before the operator's own JVM args, so anyone who disagrees can
// override any of them by repeating the property — the last -D wins.
func (c *Config) consoleJVMArgs(tty bool) []string {
	args := make([]string, 0, 9)

	switch {
	case tty:
		// Nothing to compensate for: the server can see a terminal, so JLine
		// drives it properly and TerminalConsoleAppender colours its output on
		// its own. Forcing either would be worse than leaving them alone —
		// terminal.jline=false is precisely what would throw away the
		// server-side completion this mode exists to get.
	case c.colorForced():
		// TerminalConsoleAppender — used by vanilla, Paper, Fabric and friends
		// — only colours its output when it can see a terminal, and there is
		// no terminal behind a pipe. terminal.ansi=true says "emit it anyway";
		// terminal.jline=false stops JLine from trying to drive a terminal
		// that does not exist and printing a warning about it.
		args = append(args, "-Dterminal.jline=false", "-Dterminal.ansi=true")
	}

	// Anything other than UTF-8 is a charset we decode ourselves, so leave the
	// JVM on its platform default in that case.
	if canonical, _ := canonicalEncoding(c.Encoding); canonical == EncodingAuto || canonical == EncodingUTF8 {
		// file.encoding is what Java 8-17 uses for the console streams; 18+
		// splits stdout/stderr/stdin out into their own properties (and still
		// answers to the older sun.* spellings), so set both generations.
		args = append(args,
			"-Dfile.encoding=UTF-8",
			"-Dstdout.encoding=UTF-8",
			"-Dstderr.encoding=UTF-8",
			"-Dstdin.encoding=UTF-8",
			"-Dsun.stdout.encoding=UTF-8",
			"-Dsun.stderr.encoding=UTF-8",
			"-Dsun.stdin.encoding=UTF-8",
		)
	}
	return args
}

// usesCustomCommand reports whether this instance bypasses the java/jar path.
func (c *Config) usesCustomCommand() bool { return len(c.Command) > 0 }

// commandLine builds the argv used to launch the server. tty says which
// console transport the process is about to get, since some of the JVM flags
// exist only to paper over not having a terminal.
func (c *Config) commandLine(tty bool) (string, []string, error) {
	if c.usesCustomCommand() {
		return c.Command[0], append([]string(nil), c.Command[1:]...), nil
	}
	if strings.TrimSpace(c.Jar) == "" {
		return "", nil, fmt.Errorf("%w: no server jar configured", ErrInvalidConfig)
	}

	console := c.consoleJVMArgs(tty)
	args := make([]string, 0, len(console)+len(c.JVMArgs)+len(c.ServerArgs)+4)
	if c.MinMemoryMB > 0 {
		args = append(args, fmt.Sprintf("-Xms%dM", c.MinMemoryMB))
	}
	if c.MaxMemoryMB > 0 {
		args = append(args, fmt.Sprintf("-Xmx%dM", c.MaxMemoryMB))
	}
	args = append(args, console...)
	args = append(args, c.JVMArgs...)
	args = append(args, "-jar", c.Jar)
	args = append(args, c.ServerArgs...)
	return c.Java, args, nil
}

// StateInfo is the observable runtime status of an instance.
//
// Rev increases on every state change. Clients receive state from two racing
// sources — the console websocket and HTTP responses that were snapshotted
// when the request arrived — so they need it to tell which one is newer.
type StateInfo struct {
	Rev       uint64     `json:"rev"`
	State     State      `json:"state"`
	PID       int        `json:"pid,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	Message   string     `json:"message,omitempty"`
	// TTYActive is whether the *running* process actually got a terminal, which
	// can differ from the config: the platform may not support one, or opening
	// it may have failed and the start fallen back to pipes. The name has to
	// differ from Config.TTY — Status embeds both, and two identically tagged
	// fields at the same depth make encoding/json drop them both.
	TTYActive bool `json:"ttyActive"`
}

// Status is an instance's config plus its live state, as returned by the API.
type Status struct {
	Config
	StateInfo
	LastSeq uint64 `json:"lastSeq"`
	// TTYSupported is whether this platform can give an instance a terminal at
	// all, so the UI can grey the switch out instead of letting it be set to
	// something that will silently fall back.
	TTYSupported bool `json:"ttySupported"`
}
