package dbruntime

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Service is one database the panel set up and is responsible for.
//
// The distinction from Install matters: an Install is a copy of the engine's
// binaries, shared by everything on the machine, while a Service is a data
// directory, a port and a process. Several services can run on one install,
// which is the normal case for an operator giving each server its own database.
type Service struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Engine and Version are copied from the install rather than looked up
	// through it, so a service whose engine was deleted can still say what it
	// used to be instead of rendering as blanks.
	Engine    string `json:"engine"`
	Version   string `json:"version"`
	InstallID string `json:"installId"`
	// Dir is the service's own directory: data/, logs and nothing else.
	Dir  string `json:"dir"`
	Port int    `json:"port"`
	// Bind is the address the engine listens on. It defaults to loopback and
	// the page says plainly what changing it means — see validate.
	Bind string `json:"bind"`
	// Database and User are the application account plugins connect with.
	Database string `json:"database"`
	User     string `json:"user"`
	// Password is stored here in plain text, in a file written 0600 beside the
	// panel's own credentials.
	//
	// There is no alternative that is actually better: the panel has to hand
	// the password to the operator to paste into a plugin config, and every
	// plugin then stores the same string in its own world-readable YAML. A hash
	// would protect nothing and lose the one thing the page exists to provide.
	Password string `json:"password"`
	// RunAs is the system account the engine runs as on Unix. Empty means the
	// panel's own account, which is right whenever the panel is not root.
	RunAs string `json:"runAs,omitempty"`
	// AutoStart brings the service back when the panel restarts. A server that
	// starts on boot and cannot reach its database is worse than either.
	AutoStart bool      `json:"autoStart"`
	CreatedAt time.Time `json:"createdAt"`
}

// State is what a service is doing right now. It is not persisted: a process
// does not survive the panel, so every service is stopped at startup until
// something starts it.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
)

func (s State) Live() bool { return s == StateStarting || s == StateRunning || s == StateStopping }

// Status is a service plus what it is doing.
type Status struct {
	Service
	State State `json:"state"`
	PID   int   `json:"pid,omitempty"`
	// Since is when the current state began, for an uptime readout.
	Since time.Time `json:"since"`
	// Error is why a failed service failed.
	Error string `json:"error,omitempty"`
	// Missing is true when the engine this service runs on has been deleted.
	Missing bool `json:"missing"`
}

// ConnectionURI is what goes into a plugin's config file.
func (s Service) ConnectionURI() string {
	host := s.Bind
	if host == "" || host == "0.0.0.0" || host == "::" {
		// A wildcard bind is not an address anything connects to.
		host = "127.0.0.1"
	}
	engine, err := EngineByID(s.Engine)
	if err != nil {
		return ""
	}
	authority := net.JoinHostPort(host, strconv.Itoa(s.Port))
	if !engine.Password || s.User == "" {
		return fmt.Sprintf("%s://%s/%s", engine.Scheme, authority, s.Database)
	}
	return fmt.Sprintf("%s://%s:%s@%s/%s", engine.Scheme, s.User, s.Password, authority, s.Database)
}

// JDBCURL is the same thing in the shape a Bukkit plugin's config asks for.
// Empty when the engine has no JDBC driver plugins would use.
func (s Service) JDBCURL() string {
	engine, err := EngineByID(s.Engine)
	if err != nil || engine.JDBC == "" {
		return ""
	}
	host := s.Bind
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s%s/%s", engine.JDBC, net.JoinHostPort(host, strconv.Itoa(s.Port)), s.Database)
}

// DataDir is where the engine keeps its files.
func (s Service) DataDir() string { return filepath.Join(s.Dir, "data") }

// LogFile is where the engine's own output is kept.
func (s Service) LogFile() string { return filepath.Join(s.Dir, "server.log") }

// socketDir holds the Unix socket PostgreSQL insists on creating somewhere.
// The default is /tmp or /var/run/postgresql, neither of which a panel running
// as an unprivileged user can necessarily write to.
func (s Service) socketDir() string { return filepath.Join(s.Dir, "run") }

// ---------------------------------------------------------------- validation

// nameRule is what a service, database or user name may contain.
//
// Deliberately narrower than any of the three engines allow. These strings go
// into command lines, into SQL the panel generates for MySQL's --init-file, and
// into directory names, and there is no version of "quote it properly for all
// three" that is worth the risk when a database called `lp_survival` covers
// every real use.
var nameRule = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,30}$`)

// idRule is the same for a service id, which is also a directory name.
var idRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (s *Service) validate() error {
	if !idRule.MatchString(s.ID) {
		return fmt.Errorf("%w: 服务 id 只能用小写字母、数字和连字符", ErrInvalidConfig)
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: 名字不能为空", ErrInvalidConfig)
	}
	engine, err := EngineByID(s.Engine)
	if err != nil {
		return err
	}
	if !nameRule.MatchString(s.Database) {
		return fmt.Errorf("%w: 数据库名要以字母开头，只能用字母、数字和下划线，最长 31 位", ErrInvalidConfig)
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("%w: 端口要在 1-65535 之间", ErrInvalidConfig)
	}
	if s.Bind == "" {
		s.Bind = "127.0.0.1"
	}
	if net.ParseIP(s.Bind) == nil {
		return fmt.Errorf("%w: 监听地址 %q 不是一个 IP", ErrInvalidConfig, s.Bind)
	}

	if !engine.Password {
		// MongoDB: the panel cannot create a user, so it must not pretend to
		// have one. Binding such a service anywhere but loopback would put an
		// unauthenticated database on the network.
		s.User, s.Password = "", ""
		if !isLoopback(s.Bind) {
			return fmt.Errorf("%w: %s 没法由面板设置账号密码，只能监听回环地址；"+
				"要让别的机器连，请自己在前面加一层代理或者防火墙规则", ErrInvalidConfig, engine.Name)
		}
		return nil
	}
	if !nameRule.MatchString(s.User) {
		return fmt.Errorf("%w: 用户名要以字母开头，只能用字母、数字和下划线，最长 31 位", ErrInvalidConfig)
	}
	if err := validPassword(s.Password); err != nil {
		return err
	}
	return nil
}

// validPassword rejects what would break the command lines and the generated
// SQL the password travels through, and nothing else. Length is not policed:
// this is a database on the operator's own machine, usually on loopback, and a
// panel that lectures about password strength here would be posturing.
func validPassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("%w: 密码至少 8 位", ErrInvalidConfig)
	}
	if len(password) > 64 {
		return fmt.Errorf("%w: 密码最长 64 位", ErrInvalidConfig)
	}
	for _, r := range password {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("%w: 密码只能用可见的 ASCII 字符（不含空格）", ErrInvalidConfig)
		}
	}
	if strings.ContainsAny(password, `'"\`+"`") {
		return fmt.Errorf("%w: 密码不能包含引号和反斜杠", ErrInvalidConfig)
	}
	return nil
}

func isLoopback(address string) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}

// ------------------------------------------------------------------ helpers

// defaultPort finds a free port for a new service, starting from the engine's
// standard one. Two MySQL services on one machine cannot both have 3306, and
// making the operator work that out is a poor first experience.
func defaultPort(engine Engine, taken map[int]bool) int {
	for port := engine.DefaultPort; port < engine.DefaultPort+200; port++ {
		if taken[port] || !portFree(port) {
			continue
		}
		return port
	}
	return engine.DefaultPort
}

// portFree reports whether nothing is listening on loopback. It is advisory:
// the engine is what finds out for certain, and it says so in its own log.
func portFree(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// waitForPort blocks until the engine is accepting connections, which is the
// one readiness signal all three share. Parsing each engine's startup log for
// its own "ready" line would be three parsers to keep in step with three
// upstreams; a successful connect means the same thing in every case.
func waitForPort(address string, timeout time.Duration, alive func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive() {
			return errors.New("进程已经退出")
		}
		conn, err := net.DialTimeout("tcp", address, 2*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("等了 %s 端口还是没起来", timeout)
}

// settle waits out a grace period, failing if the process goes away during it.
func settle(grace time.Duration, alive func() bool) error {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive() {
			return errors.New("进程已经退出")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !alive() {
		return errors.New("进程已经退出")
	}
	return nil
}

// dialAddress is where to knock to see whether the service is up. A service
// bound to a wildcard is reached on loopback like anything else.
func (s Service) dialAddress() string {
	host := s.Bind
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(s.Port))
}

// sortServices orders services by name, so the list does not reshuffle between
// requests.
func sortServices(services []Service) {
	sort.Slice(services, func(a, b int) bool {
		if services[a].Name != services[b].Name {
			return services[a].Name < services[b].Name
		}
		return services[a].ID < services[b].ID
	})
}

// removeAllIfEmpty deletes a directory the panel created but never populated,
// so a failed creation does not leave litter behind.
func removeAllIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}
