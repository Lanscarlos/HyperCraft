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

// Config is the persisted description of one Minecraft server instance.
type Config struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Directory string    `json:"directory"`
	CreatedAt time.Time `json:"createdAt"`

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

	// Supervision settings.
	AutoStart      bool   `json:"autoStart"`      // start when the panel boots
	AutoRestart    bool   `json:"autoRestart"`    // restart after an unexpected exit
	StopCommand    string `json:"stopCommand"`    // console command for a graceful stop
	StopTimeoutSec int    `json:"stopTimeoutSec"` // how long to wait before signalling
}

// Defaults for fields the user left blank. Applied on create and on load, so
// configs written by older versions keep working.
func (c *Config) applyDefaults() {
	if c.Java == "" {
		c.Java = "java"
	}
	if c.StopCommand == "" {
		c.StopCommand = "stop"
	}
	if c.StopTimeoutSec <= 0 {
		c.StopTimeoutSec = 60
	}
	if c.MaxMemoryMB <= 0 {
		c.MaxMemoryMB = 2048
	}
	if c.MinMemoryMB < 0 {
		c.MinMemoryMB = 0
	}
	if c.JVMArgs == nil {
		c.JVMArgs = []string{}
	}
	if c.ServerArgs == nil {
		c.ServerArgs = []string{"--nogui"}
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
	return nil
}

// usesCustomCommand reports whether this instance bypasses the java/jar path.
func (c *Config) usesCustomCommand() bool { return len(c.Command) > 0 }

// commandLine builds the argv used to launch the server.
func (c *Config) commandLine() (string, []string, error) {
	if c.usesCustomCommand() {
		return c.Command[0], append([]string(nil), c.Command[1:]...), nil
	}
	if strings.TrimSpace(c.Jar) == "" {
		return "", nil, fmt.Errorf("%w: no server jar configured", ErrInvalidConfig)
	}

	args := make([]string, 0, len(c.JVMArgs)+len(c.ServerArgs)+4)
	if c.MinMemoryMB > 0 {
		args = append(args, fmt.Sprintf("-Xms%dM", c.MinMemoryMB))
	}
	if c.MaxMemoryMB > 0 {
		args = append(args, fmt.Sprintf("-Xmx%dM", c.MaxMemoryMB))
	}
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
}

// Status is an instance's config plus its live state, as returned by the API.
type Status struct {
	Config
	StateInfo
	LastSeq uint64 `json:"lastSeq"`
}
